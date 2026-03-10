package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/triangles/polyon-core/internal/config"
	affineEngine "github.com/triangles/polyon-core/internal/engine/affine"
	litellmEngine "github.com/triangles/polyon-core/internal/engine/litellm"
	"github.com/triangles/polyon-core/internal/engine/mattermost"
	"github.com/triangles/polyon-core/internal/engine/n8n"
	"github.com/triangles/polyon-core/internal/engine/nextcloud"
	"github.com/triangles/polyon-core/internal/engine/odoo"
	onlyofficeEngine "github.com/triangles/polyon-core/internal/engine/onlyoffice"
	"github.com/triangles/polyon-core/internal/engine/operaton"
	strapiEngine "github.com/triangles/polyon-core/internal/engine/strapi"
	"github.com/triangles/polyon-core/internal/gitea"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/provision"
)

// RegisterProvision registers /api/v1/provision/* routes.
func RegisterProvision(r chi.Router, d *Deps) {
	r.Post("/provision/mattermost", handleProvisionMattermost(d))
	r.Post("/provision/nextcloud", handleProvisionNextcloud(d))
	r.Post("/provision/odoo/sync", handleProvisionOdooSync(d))
	r.Post("/provision/gitea", handleProvisionGitea(d))
	r.Post("/provision/operaton/verify", handleProvisionOperaton(d))
	r.Post("/provision/n8n/verify", handleProvisionN8n(d))
	r.Post("/provision/strapi/verify", handleProvisionStrapi(d))
	r.Post("/provision/affine/configure-oidc", handleProvisionAffineOIDC(d))
	r.Post("/provision/onlyoffice/verify", handleProvisionOnlyOffice(d))
	r.Post("/provision/litellm/verify", handleProvisionLiteLLM(d))
	r.Get("/provision/status", handleProvisionStatus(d))
	r.Post("/provision/organizations/sync", handleOrgSync(d))
	r.Get("/provision/organizations/status", handleOrgStatus(d))
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/mattermost
// ──────────────────────────────────────────────────────────────────────────────

// provisionMattermostRequest allows callers to override defaults.
// All fields are optional; the handler falls back to Config values.
type provisionMattermostRequest struct {
	LdapServer        string `json:"ldap_server"`
	OIDCClientID      string `json:"oidc_client_id"`
	OIDCSecret        string `json:"oidc_secret"`
	AuthEndpoint      string `json:"auth_endpoint"`
	TokenEndpoint     string `json:"token_endpoint"`
	UserInfoEndpoint  string `json:"userinfo_endpoint"`
	DiscoveryEndpoint string `json:"discovery_endpoint"`
	SyncAfter         bool   `json:"sync_after"`
}

func handleProvisionMattermost(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.MattermostClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "mattermost client not available")
			return
		}

		var req provisionMattermostRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httputil.RespondError(w, http.StatusBadRequest, "INVALID_JSON", "invalid JSON: "+err.Error())
				return
			}
		}

		cfg := d.Cfg

		// Build Keycloak endpoints
		realm := strings.ToLower(cfg.Realm)
		baseDomain := realm
		keycloakInternal := cfg.KeycloakURL

		authEndpoint := req.AuthEndpoint
		if authEndpoint == "" {
			authEndpoint = fmt.Sprintf("https://sso.%s/realms/polyon/protocol/openid-connect/auth", baseDomain)
		}
		tokenEndpoint := req.TokenEndpoint
		if tokenEndpoint == "" {
			tokenEndpoint = fmt.Sprintf("%s/realms/polyon/protocol/openid-connect/token", keycloakInternal)
		}
		userInfoEndpoint := req.UserInfoEndpoint
		if userInfoEndpoint == "" {
			userInfoEndpoint = fmt.Sprintf("%s/realms/polyon/protocol/openid-connect/userinfo", keycloakInternal)
		}
		discoveryEndpoint := req.DiscoveryEndpoint
		if discoveryEndpoint == "" {
			discoveryEndpoint = fmt.Sprintf("%s/realms/polyon/.well-known/openid-configuration", keycloakInternal)
		}

		oidcClientID := req.OIDCClientID
		if oidcClientID == "" {
			oidcClientID = "mattermost"
		}

		provCfg := mattermost.ProvisionConfig{
			BaseDN:            cfg.BaseDN(),
			BindDN:            cfg.AdminDN(),
			BindPassword:      cfg.DCAdminPassword,
			LdapServer:        req.LdapServer,
			OIDCClientID:      oidcClientID,
			OIDCSecret:        req.OIDCSecret,
			AuthEndpoint:      authEndpoint,
			TokenEndpoint:     tokenEndpoint,
			UserInfoEndpoint:  userInfoEndpoint,
			DiscoveryEndpoint: discoveryEndpoint,
		}

		if err := d.MattermostClient.ConfigureLDAP(provCfg); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "LDAP_CONFIG_ERROR", "configure LDAP: "+err.Error())
			return
		}

		if req.OIDCSecret != "" {
			if err := d.MattermostClient.ConfigureOIDC(provCfg); err != nil {
				httputil.RespondError(w, http.StatusInternalServerError, "OIDC_CONFIG_ERROR", "configure OIDC: "+err.Error())
				return
			}
		}

		if req.SyncAfter {
			if err := d.MattermostClient.SyncLDAP(); err != nil {
				// Non-fatal: LDAP is configured, sync will retry on next interval
				httputil.RespondOK(w, map[string]interface{}{
					"status": "partial",
					"note":   "LDAP configured but sync failed: " + err.Error(),
				})
				return
			}
		}

		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/nextcloud
// ──────────────────────────────────────────────────────────────────────────────

type provisionNextcloudRequest struct {
	Container string `json:"container"` // defaults to "polyon-drive"
}

func handleProvisionNextcloud(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Docker == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "docker client not available")
			return
		}

		var req provisionNextcloudRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httputil.RespondError(w, http.StatusBadRequest, "INVALID_JSON", "invalid JSON: "+err.Error())
				return
			}
		}

		container := req.Container
		if container == "" {
			container = "polyon-drive"
		}

		cfg := d.Cfg
		ldapCfg := nextcloud.LDAPConfig{
			Host:          "polyon-dc",
			Port:          389,
			BaseDN:        cfg.BaseDN(),
			AdminDN:       cfg.AdminDN(),
			AdminPassword: cfg.DCAdminPassword,
			Container:     container,
		}

		if err := nextcloud.ConfigureLDAP(d.Docker, ldapCfg); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "LDAP_CONFIG_ERROR", "configure LDAP: "+err.Error())
			return
		}

		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/odoo/sync
// ──────────────────────────────────────────────────────────────────────────────

type provisionOdooSyncRequest struct {
	Users []odoo.ADUser `json:"users"`
}

func handleProvisionOdooSync(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "odoo client not available")
			return
		}

		var req provisionOdooSyncRequest
		_ = json.NewDecoder(r.Body).Decode(&req) // body may be empty

		// If no users provided, auto-fetch from AD
		if len(req.Users) == 0 && d.Samba != nil {
			adUsers, err := d.Samba.ListUsers()
			if err != nil {
				httputil.RespondError(w, http.StatusInternalServerError, "AD_ERROR", "list AD users: "+err.Error())
				return
			}
			for _, u := range adUsers {
				if !u.Enabled {
					continue
				}
				req.Users = append(req.Users, odoo.ADUser{
					Username:    u.Username,
					Email:       u.Mail,
					DisplayName: u.CN,
					FirstName:   u.GivenName,
					LastName:    u.Surname,
					Enabled:     u.Enabled,
				})
			}
		}

		if len(req.Users) == 0 {
			httputil.RespondError(w, http.StatusBadRequest, "EMPTY_USERS", "no users found in AD or request body")
			return
		}

		// OAuth Provider ID 조회 (polyon_oidc addon이 등록한 Keycloak provider)
		oauthProviderID := 0
		if pid, err := d.OdooClient.GetOAuthProviderID("odoo"); err == nil {
			oauthProviderID = pid
		}

		result, err := d.OdooClient.SyncUsers(req.Users, oauthProviderID)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "SYNC_ERROR", "sync users: "+err.Error())
			return
		}

		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// GET /api/v1/provision/status
// ──────────────────────────────────────────────────────────────────────────────

type provisionStatusResponse struct {
	Mattermost  provisionComponentStatus `json:"mattermost"`
	Nextcloud   provisionComponentStatus `json:"nextcloud"`
	Odoo        provisionComponentStatus `json:"odoo"`
	Gitea       provisionComponentStatus `json:"gitea"`
	Operaton    provisionComponentStatus `json:"operaton"`
	N8n         provisionComponentStatus `json:"n8n"`
	Strapi      provisionComponentStatus `json:"strapi"`
	AFFiNE      provisionComponentStatus `json:"affine"`
	OnlyOffice  provisionComponentStatus `json:"onlyoffice"`
	LiteLLM     provisionComponentStatus `json:"litellm"`
}

type provisionComponentStatus struct {
	Available bool   `json:"available"`
	Healthy   bool   `json:"healthy"`
	Error     string `json:"error,omitempty"`
}

func handleProvisionStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := provisionStatusResponse{}

		// Mattermost
		if d.MattermostClient != nil {
			resp.Mattermost.Available = true
			ok, err := d.MattermostClient.Health()
			if err != nil {
				resp.Mattermost.Error = err.Error()
			} else {
				resp.Mattermost.Healthy = ok
			}
		}

		// Nextcloud — check via Drive provisioner
		if d.Drive != nil {
			resp.Nextcloud.Available = true
			status, err := d.Drive.Client().Status()
			if err != nil {
				resp.Nextcloud.Error = err.Error()
			} else {
				resp.Nextcloud.Healthy = status.Installed && !status.Maintenance
			}
		}

		// Odoo
		if d.OdooClient != nil {
			resp.Odoo.Available = true
			ok, err := d.OdooClient.Health()
			if err != nil {
				resp.Odoo.Error = err.Error()
			} else {
				resp.Odoo.Healthy = ok
			}
		}

		// Gitea
		if d.Gitea != nil {
			resp.Gitea.Available = true
			// Gitea health: list auth sources succeeds ↔ reachable
			_, err := d.Gitea.ListAuthSources()
			if err != nil {
				resp.Gitea.Error = err.Error()
			} else {
				resp.Gitea.Healthy = true
			}
		}

		// Operaton
		if d.OperatonClient != nil {
			resp.Operaton.Available = true
			ok, err := d.OperatonClient.Health()
			if err != nil {
				resp.Operaton.Error = err.Error()
			} else {
				resp.Operaton.Healthy = ok
			}
		}

		// n8n
		if d.N8nClient != nil {
			resp.N8n.Available = true
			ok, err := d.N8nClient.Health()
			if err != nil {
				resp.N8n.Error = err.Error()
			} else {
				resp.N8n.Healthy = ok
			}
		}

		// Strapi
		if d.StrapiClient != nil {
			resp.Strapi.Available = true
			ok, err := d.StrapiClient.Health()
			if err != nil {
				resp.Strapi.Error = err.Error()
			} else {
				resp.Strapi.Healthy = ok
			}
		}

		// AFFiNE (Wiki) — Keycloak OIDC
		if d.AffineClient != nil {
			resp.AFFiNE.Available = true
			ok, err := d.AffineClient.Health()
			if err != nil {
				resp.AFFiNE.Error = err.Error()
			} else {
				resp.AFFiNE.Healthy = ok
			}
		}

		// OnlyOffice (Office) — JWT internal
		if d.OnlyOfficeClient != nil {
			resp.OnlyOffice.Available = true
			ok, err := d.OnlyOfficeClient.Health()
			if err != nil {
				resp.OnlyOffice.Error = err.Error()
			} else {
				resp.OnlyOffice.Healthy = ok
			}
		}

		// LiteLLM (AI Gateway) — Forward Auth
		if d.LiteLLMClient != nil {
			resp.LiteLLM.Available = true
			ok, err := d.LiteLLMClient.Health()
			if err != nil {
				resp.LiteLLM.Error = err.Error()
			} else {
				resp.LiteLLM.Healthy = ok
			}
		}

		httputil.RespondOK(w, resp)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/gitea
// ──────────────────────────────────────────────────────────────────────────────

func handleProvisionGitea(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Gitea == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "gitea client not available (GITEA_TOKEN not set?)")
			return
		}

		cfg := d.Cfg
		ldapCfg := gitea.LDAPConfig{
			Host:           "polyon-dc",
			Port:           389,
			BindDN:         cfg.AdminDN(),
			BindPassword:   cfg.DCAdminPassword,
			UserSearchBase: cfg.BaseDN(),
			AdminDN:        cfg.AdminDN(),
		}

		if err := d.Gitea.ConfigureLDAP(ldapCfg); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "LDAP_CONFIG_ERROR", "configure Gitea LDAP: "+err.Error())
			return
		}

		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/operaton/verify
// ──────────────────────────────────────────────────────────────────────────────

func handleProvisionOperaton(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OperatonClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "operaton client not available")
			return
		}

		cfg := d.Cfg
		provCfg := operaton.ProvisionConfig{
			BaseDN:          cfg.BaseDN(),
			AdminDN:         cfg.AdminDN(),
			DCAdminPassword: cfg.DCAdminPassword,
		}

		// Health check
		healthy, err := d.OperatonClient.Health()
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "HEALTH_ERROR", "operaton health: "+err.Error())
			return
		}
		if !healthy {
			httputil.RespondError(w, http.StatusBadGateway, "NOT_HEALTHY", "operaton is not healthy")
			return
		}

		// LDAP connection test
		users, ldapErr := d.OperatonClient.VerifyLDAP()

		result := map[string]interface{}{
			"status":      "ok",
			"healthy":     healthy,
			"ldap_config": provCfg.GenerateConfig(),
		}
		if ldapErr != nil {
			result["ldap_verify"] = "failed"
			result["ldap_error"] = ldapErr.Error()
			result["note"] = "Operaton is healthy but LDAP may not be configured yet. Apply the docker env vars and restart the container."
		} else {
			result["ldap_verify"] = "ok"
			result["ldap_user_count"] = len(users)
		}

		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/n8n/verify
// ──────────────────────────────────────────────────────────────────────────────

func handleProvisionN8n(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.N8nClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "n8n client not available")
			return
		}

		cfg := d.Cfg
		realm := strings.ToLower(cfg.Realm)

		authCfg := n8n.AuthConfig{
			ForwardAuthEnabled: true,
			KeycloakIssuer:     fmt.Sprintf("%s/realms/polyon", cfg.KeycloakURL),
			InternalURL:        cfg.N8nURL,
		}

		result, err := d.N8nClient.ConfigureAuth(authCfg)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "N8N_AUTH_ERROR", "n8n auth verify: "+err.Error())
			return
		}

		result["realm"] = realm
		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/strapi/verify
// ──────────────────────────────────────────────────────────────────────────────

func handleProvisionStrapi(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.StrapiClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "strapi client not available")
			return
		}

		cfg := d.Cfg
		realm := strings.ToLower(cfg.Realm)

		ssoCfg := strapiSSOConfig(cfg, realm)

		result, err := d.StrapiClient.ConfigureSSO(ssoCfg)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "STRAPI_SSO_ERROR", "strapi SSO verify: "+err.Error())
			return
		}

		result["realm"] = realm
		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/organizations/sync
// GET  /api/v1/provision/organizations/status
// ──────────────────────────────────────────────────────────────────────────────

// orgSyncState holds the last sync result and tracks in-progress syncs.
var orgSyncState = struct {
	mu        sync.Mutex
	running   bool
	lastRun   time.Time
	lastResult *provision.SyncResult
	lastError  string
}{}

func handleOrgSync(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Samba == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "samba service not available")
			return
		}

		orgSyncState.mu.Lock()
		if orgSyncState.running {
			orgSyncState.mu.Unlock()
			httputil.RespondError(w, http.StatusConflict, "SYNC_RUNNING", "organization sync already in progress")
			return
		}
		orgSyncState.running = true
		orgSyncState.mu.Unlock()

		defer func() {
			orgSyncState.mu.Lock()
			orgSyncState.running = false
			orgSyncState.mu.Unlock()
		}()

		// Build MailSyncer if Stalwart is configured
		var mailSyncer *provision.MailSyncer
		if d.Cfg.StalwartURL != "" && d.Cfg.StalwartAdminUser != "" {
			mailSyncer = provision.NewMailSyncer(
				d.Cfg.StalwartURL,
				d.Cfg.StalwartAdminUser,
				d.Cfg.StalwartAdminPassword,
				d.Cfg.MailHostname,
			)
		}

		syncer := provision.NewOrgSyncer(
			d.Samba,
			d.Cfg,
			d.MattermostClient,
			d.Drive,
			mailSyncer,
			log.Default(),
		)

		result, err := syncer.SyncOrganizations(context.Background())

		orgSyncState.mu.Lock()
		orgSyncState.lastRun = time.Now()
		orgSyncState.lastResult = result
		if err != nil {
			orgSyncState.lastError = err.Error()
		} else {
			orgSyncState.lastError = ""
		}
		orgSyncState.mu.Unlock()

		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "SYNC_ERROR", "sync organizations: "+err.Error())
			return
		}

		httputil.RespondOK(w, result)
	}
}

type orgStatusResponse struct {
	Running    bool                    `json:"running"`
	LastRun    *time.Time              `json:"last_run,omitempty"`
	LastError  string                  `json:"last_error,omitempty"`
	LastResult *provision.SyncResult   `json:"last_result,omitempty"`
}

func handleOrgStatus(_ *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgSyncState.mu.Lock()
		resp := orgStatusResponse{
			Running:    orgSyncState.running,
			LastResult: orgSyncState.lastResult,
			LastError:  orgSyncState.lastError,
		}
		if !orgSyncState.lastRun.IsZero() {
			t := orgSyncState.lastRun
			resp.LastRun = &t
		}
		orgSyncState.mu.Unlock()

		httputil.RespondOK(w, resp)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/affine/configure-oidc
// ──────────────────────────────────────────────────────────────────────────────

type provisionAffineOIDCRequest struct {
	// ClientID is the Keycloak OIDC client ID (defaults to "affine").
	ClientID string `json:"client_id"`
	// ClientSecret is the Keycloak OIDC client secret.
	ClientSecret string `json:"client_secret"`
	// RedirectURI is the AFFiNE OAuth callback URL.
	// Defaults to https://wiki.<realm>/oauth/callback
	RedirectURI string `json:"redirect_uri"`
}

func handleProvisionAffineOIDC(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.AffineClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "affine client not available")
			return
		}

		var req provisionAffineOIDCRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httputil.RespondError(w, http.StatusBadRequest, "INVALID_JSON", "invalid JSON: "+err.Error())
				return
			}
		}

		cfg := d.Cfg
		realm := strings.ToLower(cfg.Realm)

		clientID := req.ClientID
		if clientID == "" {
			clientID = "affine"
		}
		redirectURI := req.RedirectURI
		if redirectURI == "" {
			redirectURI = fmt.Sprintf("https://wiki.%s/oauth/callback", realm)
		}

		// Step 1: Create/update Keycloak client for AFFiNE
		var kcErr string
		if cfg.KCAdminUser != "" && cfg.KCAdminPassword != "" {
			kc, err := newKCClient(cfg.KeycloakURL, cfg.KCAdminUser, cfg.KCAdminPassword)
			if err != nil {
				kcErr = "keycloak auth failed: " + err.Error()
			} else {
				appBase := fmt.Sprintf("https://wiki.%s", realm)
				if err := kc.createOrUpdateAppClient("polyon", clientID, appBase); err != nil {
					kcErr = "keycloak client create/update failed: " + err.Error()
				}
			}
		} else {
			kcErr = "keycloak admin credentials not configured"
		}

		// Step 2: Configure OIDC in AFFiNE
		oidcCfg := affineEngine.OIDCConfig{
			ClientID:     clientID,
			ClientSecret: req.ClientSecret,
			Issuer:       fmt.Sprintf("%s/realms/polyon", cfg.KeycloakURL),
			RedirectURI:  redirectURI,
		}

		result, err := d.AffineClient.ConfigureOIDC(oidcCfg)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "AFFINE_OIDC_ERROR", "affine OIDC configure: "+err.Error())
			return
		}

		result["realm"] = realm
		result["keycloak_client_id"] = clientID
		if kcErr != "" {
			result["keycloak_error"] = kcErr
		} else {
			result["keycloak_client"] = "created_or_updated"
		}

		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/onlyoffice/verify
// ──────────────────────────────────────────────────────────────────────────────

type provisionOnlyOfficeRequest struct {
	// JWTEnabled mirrors the JWT_ENABLED env var (default: true).
	JWTEnabled *bool `json:"jwt_enabled"`
	// JWTSecret is the JWT secret (masked in response).
	JWTSecret string `json:"jwt_secret"`
	// JWTAlgorithm is the JWT algorithm (default: HS256).
	JWTAlgorithm string `json:"jwt_algorithm"`
}

func handleProvisionOnlyOffice(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OnlyOfficeClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "onlyoffice client not available")
			return
		}

		var req provisionOnlyOfficeRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				httputil.RespondError(w, http.StatusBadRequest, "INVALID_JSON", "invalid JSON: "+err.Error())
				return
			}
		}

		// Default JWT enabled = true (PolyON default)
		jwtEnabled := true
		if req.JWTEnabled != nil {
			jwtEnabled = *req.JWTEnabled
		}

		jwtCfg := onlyofficeEngine.JWTConfig{
			JWTEnabled:   jwtEnabled,
			JWTSecret:    req.JWTSecret,
			JWTAlgorithm: req.JWTAlgorithm,
		}

		result, err := d.OnlyOfficeClient.VerifyJWT(jwtCfg)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "ONLYOFFICE_VERIFY_ERROR", "onlyoffice verify: "+err.Error())
			return
		}

		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /api/v1/provision/litellm/verify
// ──────────────────────────────────────────────────────────────────────────────

func handleProvisionLiteLLM(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.LiteLLMClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "litellm client not available")
			return
		}

		cfg := d.Cfg
		realm := strings.ToLower(cfg.Realm)

		authCfg := litellmEngine.AuthConfig{
			ForwardAuthEnabled:  true,
			KeycloakIssuer:      fmt.Sprintf("%s/realms/polyon", cfg.KeycloakURL),
			MasterKeyConfigured: cfg.LiteLLMURL != "",
		}

		result, err := d.LiteLLMClient.ConfigureAuth(authCfg)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "LITELLM_AUTH_ERROR", "litellm auth verify: "+err.Error())
			return
		}

		result["realm"] = realm
		httputil.RespondOK(w, result)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// strapiSSOConfig builds a Strapi SSO config from the current PolyON config.
func strapiSSOConfig(cfg *config.Config, realm string) strapiEngine.SSOConfig {
	return strapiEngine.SSOConfig{
		ForwardAuthEnabled: true,
		KeycloakIssuer:     fmt.Sprintf("%s/realms/polyon", cfg.KeycloakURL),
	}
}
