package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// domainConfigKeys are the keys managed by this settings API.
var domainConfigKeys = []string{
	"dc_domain",
	"console_domain",
	"mail_domain",
	"portal_domain",
	"service_base_domain",
	"cert_mode",
	"external_access",
}

// RegisterSettings registers /api/v1/settings/* routes.
func RegisterSettings(r chi.Router, d *Deps) {
	r.Route("/settings", func(r chi.Router) {
		r.Get("/domain", getDomainSettings(d))
		r.Put("/domain", putDomainSettings(d))
	})
}

// GET /api/v1/settings/domain
// Returns current global domain configuration from polyon_config.
func getDomainSettings(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		configs, err := d.Store.GetConfigs(r.Context(), domainConfigKeys)
		if err != nil {
			log.Error().Err(err).Msg("getDomainSettings: DB query failed")
			httputil.RespondError(w, 500, "DB_ERROR", "도메인 설정 조회 실패: "+err.Error())
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success":             true,
			"base_domain":         configs["service_base_domain"],
			"dc_domain":           configs["dc_domain"],
			"console_domain":      configs["console_domain"],
			"mail_domain":         configs["mail_domain"],
			"portal_domain":       configs["portal_domain"],
			"service_base_domain": configs["service_base_domain"],
			"cert_mode":           configs["cert_mode"],
			"external_access":     configs["external_access"],
		})
	}
}

// PUT /api/v1/settings/domain
// Updates console_domain, mail_domain, portal_domain.
// dc_domain is immutable (returns error if sent).
// service_base_domain is derived from portal_domain automatically.
func putDomainSettings(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		var req struct {
			BaseDomain     string `json:"base_domain"`
			DCDomain       string `json:"dc_domain"`
			ConsoleDomain  string `json:"console_domain"`
			MailDomain     string `json:"mail_domain"`
			PortalDomain   string `json:"portal_domain"`
			CertMode       string `json:"cert_mode"`
			ExternalAccess string `json:"external_access"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "INVALID_JSON", "요청 바디 파싱 실패: "+err.Error())
			return
		}

		// dc_domain is immutable — reject any attempt to change it
		if req.DCDomain != "" {
			httputil.RespondError(w, 400, "DC_DOMAIN_IMMUTABLE",
				"dc_domain은 Setup Wizard에서만 설정 가능하며 이후 변경할 수 없습니다")
			return
		}
		// mail_domain is immutable — MX/DKIM/SPF + Stalwart + existing addresses break
		if req.MailDomain != "" {
			httputil.RespondError(w, 400, "MAIL_DOMAIN_IMMUTABLE",
				"mail_domain은 Setup Wizard에서만 설정 가능하며 이후 변경할 수 없습니다 (MX/DKIM/SPF, 기존 메일 주소 영향)")
			return
		}

		kvs := map[string]string{}

		// base_domain is stored as service_base_domain (the root for all subdomains)
		if req.BaseDomain != "" {
			kvs["service_base_domain"] = strings.ToLower(strings.TrimSpace(req.BaseDomain))
		}
		if req.ConsoleDomain != "" {
			kvs["console_domain"] = strings.ToLower(strings.TrimSpace(req.ConsoleDomain))
		}
		if req.PortalDomain != "" {
			portal := strings.ToLower(strings.TrimSpace(req.PortalDomain))
			kvs["portal_domain"] = portal
			// Also derive service_base_domain if not explicitly set
			if kvs["service_base_domain"] == "" {
				parts := strings.SplitN(portal, ".", 2)
				if len(parts) == 2 {
					kvs["service_base_domain"] = parts[1]
				} else {
					kvs["service_base_domain"] = portal
				}
			}
		}
		if req.CertMode != "" {
			kvs["cert_mode"] = req.CertMode
		}
		if req.ExternalAccess != "" {
			kvs["external_access"] = req.ExternalAccess
		}

		if len(kvs) == 0 {
			httputil.RespondError(w, 400, "NO_FIELDS", "변경할 필드가 없습니다")
			return
		}

		// Step 1: DB — save to polyon_config
		if err := d.Store.SetConfigs(r.Context(), kvs); err != nil {
			log.Error().Err(err).Msg("putDomainSettings: DB write failed")
			httputil.RespondError(w, 500, "DB_ERROR", "도메인 설정 저장 실패: "+err.Error())
			return
		}

		cfg := d.Cfg
		stepResults := []map[string]string{{"step": "db", "status": "ok"}}

		// Resolve the base domain we're operating on
		baseDomain := ""
		if v, ok := kvs["service_base_domain"]; ok {
			baseDomain = v
		} else if cfg.Realm != "" {
			baseDomain = strings.ToLower(cfg.Realm)
		}

		// Step 2: DNS — wildcard A record
		dnsStatus := "ok"
		if d.Samba != nil && baseDomain != "" && cfg.Realm != "" && cfg.MailServerIP != "" {
			result := d.Samba.AddDNSRecord(cfg.Realm, "*", "A", cfg.MailServerIP)
			if !result.Success && !strings.Contains(result.Error, "already exists") {
				log.Warn().Str("error", result.Error).Msg("putDomainSettings: wildcard DNS add warning")
				dnsStatus = "warn:" + result.Error
			}
		} else {
			dnsStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "dns", "status": dnsStatus})

		// Step 3: Keycloak — update polyon realm clients
		// - "portal" client: Forward Auth callback + portal redirect URIs (wildcard for all subdomains)
		// - "polyon-console" client: Console redirect URIs
		kcStatus := "ok"
		if baseDomain != "" && cfg.KeycloakURL != "" {
			kc, kcErr := newKCClient(cfg.KeycloakURL, cfg.KCAdminUser, cfg.KCAdminPassword)
			if kcErr != nil {
				log.Warn().Err(kcErr).Msg("putDomainSettings: KC client init failed")
				kcStatus = "warn:" + kcErr.Error()
			} else {
				// Portal client — accepts callback from any subdomain of baseDomain
				// Forward Auth redirects all apps to console.{baseDomain}/api/internal/auth/callback
				consoleBase := fmt.Sprintf("https://console.%s", baseDomain)
				portalBase := fmt.Sprintf("https://portal.%s", baseDomain)
				wildcardBase := fmt.Sprintf("https://*.%s", baseDomain)
				portalRedirects := []string{
					consoleBase + "/api/internal/auth/callback",
					portalBase + "/*",
					wildcardBase + "/*",
				}
				portalBody := map[string]interface{}{
					"clientId":                  "portal",
					"enabled":                   true,
					"publicClient":              true,
					"standardFlowEnabled":       true,
					"directAccessGrantsEnabled": false,
					"redirectUris":              portalRedirects,
					"webOrigins":                []string{consoleBase, portalBase, wildcardBase},
					"attributes": map[string]string{
						"pkce.code.challenge.method": "S256",
					},
				}
				exists, clientUID, _ := kc.clientExists("polyon", "portal")
				if exists && clientUID != "" {
					if _, kcErr = kc.do("PUT", fmt.Sprintf("/admin/realms/polyon/clients/%s", clientUID), portalBody); kcErr != nil {
						log.Warn().Err(kcErr).Msg("putDomainSettings: KC portal client update failed")
						kcStatus = "warn:" + kcErr.Error()
					}
				}

				// polyon-console client — Console
				if kcErr = kc.createOrUpdateAppClient("polyon", "polyon-console", consoleBase); kcErr != nil {
					log.Warn().Err(kcErr).Msg("putDomainSettings: KC polyon-console update failed")
					if kcStatus == "ok" {
						kcStatus = "warn:" + kcErr.Error()
					}
				}
			}
		} else {
			kcStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "keycloak", "status": kcStatus})

		// Step 4: Traefik — forward-auth middleware + wildcard router
		traefikStatus := "ok"
		if d.Traefik != nil && baseDomain != "" && cfg.Realm != "" {
			// Determine the Forward Auth URL (this Core service)
			authURL := cfg.CoreURL + "/api/internal/auth/verify"
			if err := d.Traefik.SetForwardAuthMiddleware(authURL); err != nil {
				log.Warn().Err(err).Msg("putDomainSettings: Traefik SetForwardAuthMiddleware failed")
				traefikStatus = "warn:" + err.Error()
			} else if err := d.Traefik.SetWildcardRouter(baseDomain); err != nil {
				log.Warn().Err(err).Msg("putDomainSettings: Traefik SetWildcardRouter failed")
				traefikStatus = "warn:" + err.Error()
			}
		} else {
			traefikStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "traefik", "status": traefikStatus})

		// Step 5: Cert — generate wildcard self-signed cert for *.baseDomain
		certStatus := "ok"
		if baseDomain != "" {
			certStatus = generateWildcardCert(d, baseDomain)
		} else {
			certStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "cert", "status": certStatus})

		// Regenerate full Traefik config from DB state
		go triggerTraefikRegenerate(d, r.Context())

		// ConfigTrack — commit domain settings change to polyon-config repo (non-fatal).
		if d.ConfigTracker != nil {
			snapshot := "# Domain settings snapshot\n"
			for k, v := range kvs {
				snapshot += fmt.Sprintf("%s=%s\n", k, v)
			}
			if err := d.ConfigTracker.CommitFile("settings/domain.conf", snapshot,
				"Update domain settings", ""); err != nil {
				log.Warn().Err(err).Msg("putDomainSettings: configtrack commit failed (non-fatal)")
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"steps":   stepResults,
		})
	}
}

// generateWildcardCert generates a self-signed wildcard TLS certificate for *.baseDomain
// using openssl, saves it to /runtime/certs/, and configures the Traefik TLS store.
// Returns "ok", "skipped", or "warn:<reason>".
func generateWildcardCert(d *Deps, baseDomain string) string {
	// Write certs to Traefik shared directory so 50-tls.yml can reference them
	outputDir := os.Getenv("TRAEFIK_DYNAMIC_DIR")
	if outputDir == "" {
		outputDir = "/traefik-dynamic"
	}
	certDir := filepath.Join(outputDir, "certs")
	certFile := filepath.Join(certDir, "wildcard.crt")
	keyFile := filepath.Join(certDir, "wildcard.key")

	if err := os.MkdirAll(certDir, 0o755); err != nil {
		log.Warn().Err(err).Msg("generateWildcardCert: mkdir failed")
		return "warn:mkdir:" + err.Error()
	}

	// Generate self-signed wildcard cert with openssl
	subject := fmt.Sprintf("/CN=*.%s/O=HELIOS/C=KR", baseDomain)
	san := fmt.Sprintf("DNS:*.%s,DNS:%s", baseDomain, baseDomain)

	cmd := exec.Command("openssl", "req", "-x509", "-nodes",
		"-newkey", "rsa:2048",
		"-keyout", keyFile,
		"-out", certFile,
		"-days", "3650",
		"-subj", subject,
		"-addext", "subjectAltName="+san,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Warn().Err(err).Str("output", string(out)).Msg("generateWildcardCert: openssl failed")
		return "warn:openssl:" + err.Error()
	}

	log.Info().Str("cert", certFile).Str("domain", "*."+baseDomain).Msg("Wildcard cert generated")

	// TLS store is now handled by 50-tls.yml via traefik.Generate()
	return "ok"
}
