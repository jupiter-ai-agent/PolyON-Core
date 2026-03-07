package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/module"
)

// PostInstallProvisioning runs after module deployment is ready.
// AD/LDAP binding, OIDC setup, etc.
func PostInstallProvisioning(ctx context.Context, d *Deps, moduleID string, manifest *module.Manifest) {
	spec := manifest.Spec

	// LDAP 바인딩
	if spec.LDAP.Bind {
		log.Info().Str("module_id", moduleID).Str("engine", spec.LDAP.Engine).Msg("Starting LDAP provisioning")

		// Pod 준비 대기 (최대 3분)
		if err := d.Kube.WaitForDeploymentReady(ctx, moduleID, 3*time.Minute); err != nil {
			log.Error().Err(err).Str("module_id", moduleID).Msg("LDAP provisioning: deployment not ready")
			d.Store.CreateModuleEvent(ctx, moduleID, "provision", "warning",
				"LDAP binding skipped: deployment not ready", nil)
			return
		}

		switch spec.LDAP.Engine {
		case "mattermost":
			if err := provisionMattermostLDAP(ctx, d, moduleID, spec); err != nil {
				log.Error().Err(err).Str("module_id", moduleID).Msg("Mattermost LDAP provisioning failed")
				d.Store.CreateModuleEvent(ctx, moduleID, "provision", "warning",
					"LDAP binding failed: "+err.Error(), nil)
			} else {
				log.Info().Str("module_id", moduleID).Msg("Mattermost LDAP provisioning completed")
				d.Store.CreateModuleEvent(ctx, moduleID, "provision", "completed",
					"AD/LDAP binding configured and synced", nil)
			}
		default:
			log.Warn().Str("engine", spec.LDAP.Engine).Msg("Unknown LDAP engine, skipping")
		}
	}
}

// provisionMattermostLDAP configures Mattermost LDAP settings using the AD DC.
func provisionMattermostLDAP(ctx context.Context, d *Deps, moduleID string, spec module.Spec) error {
	mmURL := mattermostURL(d)
	token := mattermostToken(d)
	if token == "" {
		return fmt.Errorf("no Mattermost admin token available")
	}

	// 1. 현재 config 가져오기
	currentConfig, err := mmAPIGet(mmURL, token, "/api/v4/config")
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}

	// 2. LDAP 설정 구성
	ldapSettings := map[string]interface{}{
		"Enable":                    true,
		"EnableSync":                true,
		"LdapServer":               os.Getenv("SAMBA_HOST"),
		"LdapPort":                 389,
		"ConnectionSecurity":       "",
		"BaseDN":                   buildBaseDN(os.Getenv("POLYON_DOMAIN_UPPER")),
		"BindUsername":             fmt.Sprintf("CN=Administrator,CN=Users,%s", buildBaseDN(os.Getenv("POLYON_DOMAIN_UPPER"))),
		"BindPassword":             os.Getenv("ADMIN_PASSWORD"),
		"SkipCertificateVerification": true,
		"SyncIntervalMinutes":      60,
		"QueryTimeout":             60,
		"MaxPageSize":              1500,
	}

	// manifest의 settings 오버레이
	for k, v := range spec.LDAP.Settings {
		ldapSettings[k] = v
	}

	// 3. config에 머지
	configMap, ok := currentConfig.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected config format")
	}
	configMap["LdapSettings"] = ldapSettings

	// 4. PUT config
	if err := mmAPIPut(mmURL, token, "/api/v4/config", configMap); err != nil {
		return fmt.Errorf("put config: %w", err)
	}

	// 5. LDAP Sync 실행
	if err := mmAPIPost(mmURL, token, "/api/v4/ldap/sync"); err != nil {
		log.Warn().Err(err).Msg("LDAP sync trigger failed (may require license)")
	}

	log.Info().
		Str("server", os.Getenv("SAMBA_HOST")).
		Str("base_dn", buildBaseDN(os.Getenv("POLYON_DOMAIN_UPPER"))).
		Msg("Mattermost LDAP configured")

	return nil
}

// buildBaseDN converts "CMARS.COM" → "DC=CMARS,DC=COM"
func buildBaseDN(domain string) string {
	if domain == "" {
		return ""
	}
	result := ""
	for i, part := range splitDomain(domain) {
		if i > 0 {
			result += ","
		}
		result += "DC=" + part
	}
	return result
}

func splitDomain(domain string) []string {
	var parts []string
	current := ""
	for _, c := range domain {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// ── Mattermost API helpers ──

var mmClient = &http.Client{Timeout: 30 * time.Second}

func mmAPIGet(baseURL, token, path string) (interface{}, error) {
	req, err := http.NewRequest("GET", baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := mmClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func mmAPIPut(baseURL, token, path string, data interface{}) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := mmClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func mmAPIPost(baseURL, token, path string) error {
	req, err := http.NewRequest("POST", baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := mmClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
