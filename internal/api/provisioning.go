package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

		// PP Directory API에서 AD DC 접속 정보 획득
		dirInfo := getDirectoryInfo(d)

		switch spec.LDAP.Engine {
		case "mattermost":
			if err := provisionMattermostLDAP(ctx, d, moduleID, spec, dirInfo); err != nil {
				log.Error().Err(err).Str("module_id", moduleID).Msg("Mattermost LDAP provisioning failed")
				d.Store.CreateModuleEvent(ctx, moduleID, "provision", "warning",
					"LDAP binding failed: "+err.Error(), nil)
			} else {
				log.Info().Str("module_id", moduleID).Msg("Mattermost LDAP provisioning completed")
				d.Store.CreateModuleEvent(ctx, moduleID, "provision", "completed",
					"AD/LDAP binding configured and synced", nil)
			}
		case "nextcloud":
			if err := provisionNextcloudLDAP(ctx, d, moduleID, spec, dirInfo); err != nil {
				log.Error().Err(err).Str("module_id", moduleID).Msg("Nextcloud LDAP provisioning failed")
				d.Store.CreateModuleEvent(ctx, moduleID, "provision", "warning",
					"LDAP binding failed: "+err.Error(), nil)
			} else {
				log.Info().Str("module_id", moduleID).Msg("Nextcloud LDAP provisioning completed")
				d.Store.CreateModuleEvent(ctx, moduleID, "provision", "completed",
					"AD/LDAP user sync completed", nil)
			}
		default:
			log.Warn().Str("engine", spec.LDAP.Engine).Msg("Unknown LDAP engine, skipping")
		}
	}
}

// getDirectoryInfo builds DirectoryConnectInfo from Config.
// Same source as GET /api/v1/directory/connect-info.
func getDirectoryInfo(d *Deps) DirectoryConnectInfo {
	cfg := d.Cfg
	return DirectoryConnectInfo{
		Host:    cfg.SambaHost,
		FQDN:    cfg.SambaHost + "." + cfg.Namespace + ".svc.cluster.local",
		Port:    389,
		TLSPort: 636,
		BaseDN:  cfg.BaseDN(),
		AdminDN: cfg.AdminDN(),
		UsersDN: "CN=Users," + cfg.BaseDN(),
		Realm:   cfg.Realm,
		Domain:  cfg.Domain,
		LDAPURL: cfg.LDAPURL,
		BindCredential: BindCredentialRef{
			SecretName: "polyon-dc-admin",
			SecretKey:  "password",
		},
	}
}

// provisionMattermostLDAP configures Mattermost LDAP settings using the AD DC.
func provisionMattermostLDAP(ctx context.Context, d *Deps, moduleID string, spec module.Spec, dir DirectoryConnectInfo) error {
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

	// 2. LDAP 설정 — Directory API 정보 기반으로 동적 구성
	ldapSettings := map[string]interface{}{
		"Enable":                       true,
		"EnableSync":                   true,
		"LdapServer":                   dir.FQDN,
		"LdapPort":                     dir.Port,
		"ConnectionSecurity":           "",
		"BaseDN":                       dir.BaseDN,
		"BindUsername":                  dir.AdminDN,
		"BindPassword":                 d.Cfg.DCAdminPassword,
		"SkipCertificateVerification":  true,
		"SyncIntervalMinutes":          60,
		"QueryTimeout":                 60,
		"MaxPageSize":                  1500,
	}

	// manifest의 settings 오버레이 (UserFilter, attribute mappings 등)
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

	// 5. Custom LDAP Sync 실행 (Team Edition 호환)
	result, err := ChatSyncLDAPUsers(d)
	if err != nil {
		log.Warn().Err(err).Msg("Custom LDAP sync failed")
	} else {
		log.Info().
			Int("created", result.Created).
			Int("updated", result.Updated).
			Int("skipped", result.Skipped).
			Int("errors", len(result.Errors)).
			Msg("Custom LDAP sync completed")
	}

	log.Info().
		Str("server", dir.FQDN).
		Str("base_dn", dir.BaseDN).
		Msg("Mattermost LDAP configured via Directory API")

	return nil
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

// provisionNextcloudLDAP configures Nextcloud LDAP backend and syncs users.
func provisionNextcloudLDAP(ctx context.Context, d *Deps, moduleID string, spec module.Spec, dir DirectoryConnectInfo) error {
	// TODO: K8s 환경에서 occ 명령으로 LDAP 백엔드 설정 (선택적)
	// GetPodsByLabel 메서드가 kube.Client에 없어서 구현하지 않음
	// 필요 시 kubectl exec를 통해 수동으로 LDAP 백엔드 설정
	/*
	if d.Kube != nil {
		pods, _ := d.Kube.GetPodsByLabel(ctx, "app=polyon-drive")
		if len(pods) > 0 {
			podName := pods[0]
			occCmds := [][]string{
				{"php", "occ", "ldap:create-empty-config"},
				{"php", "occ", "ldap:set-config", "s01", "ldapHost", dir.FQDN},
				{"php", "occ", "ldap:set-config", "s01", "ldapPort", fmt.Sprintf("%d", dir.Port)},
				{"php", "occ", "ldap:set-config", "s01", "ldapBase", dir.BaseDN},
				{"php", "occ", "ldap:set-config", "s01", "ldapAgentName", dir.AdminDN},
				{"php", "occ", "ldap:set-config", "s01", "ldapAgentPassword", d.Cfg.DCAdminPassword},
				{"php", "occ", "ldap:set-config", "s01", "ldapUserFilter", "(&(objectClass=user)(!(objectClass=computer))(!(userAccountControl:1.2.840.113556.1.4.803:=2)))"},
				{"php", "occ", "ldap:set-config", "s01", "ldapLoginFilter", "(&(objectClass=user)(|(sAMAccountName=%uid)(mail=%uid)))"},
				{"php", "occ", "ldap:set-config", "s01", "ldapUserName", "sAMAccountName"},
				{"php", "occ", "ldap:set-config", "s01", "ldapUserDisplayName", "displayName"},
				{"php", "occ", "ldap:set-config", "s01", "ldapEmailAttribute", "mail"},
				{"php", "occ", "ldap:set-config", "s01", "ldapConfigurationActive", "1"},
			}
			for _, cmd := range occCmds {
				_ = d.Kube.ExecInPod(ctx, podName, cmd)
			}
			log.Info().Str("pod", podName).Msg("Nextcloud LDAP backend configured via occ")
		}
	}
	*/

	// OCS API로 사용자 동기화
	result, err := DriveSyncLDAPUsers(d)
	if err != nil {
		return err
	}
	log.Info().Int("created", result.Created).Int("updated", result.Updated).Msg("Nextcloud user sync completed")
	return nil
}
