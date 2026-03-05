package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterSSO registers SSO-related API routes.
func RegisterSSO(r chi.Router, d *Deps) {
	r.Route("/sso", func(r chi.Router) {
		r.Get("/status", ssoStatus(d))
		r.Post("/enable", ssoEnable(d))
	})
}

// ── Keycloak Admin Client ─────────────────────────────────────────────────────

type kcClient struct {
	baseURL  string
	token    string
	httpCli  *http.Client
}

func newKCClient(baseURL, adminUser, adminPass string) (*kcClient, error) {
	httpCli := &http.Client{Timeout: 15 * time.Second}

	// Detect /auth prefix — some Keycloak versions use it
	effectiveBase := baseURL
	testResp, err := httpCli.Get(baseURL + "/realms/master/.well-known/openid-configuration")
	if err != nil || testResp.StatusCode == 404 {
		if testResp != nil {
			testResp.Body.Close()
		}
		// Try /auth prefix
		testResp2, err2 := httpCli.Get(baseURL + "/auth/realms/master/.well-known/openid-configuration")
		if err2 == nil && testResp2.StatusCode == 200 {
			effectiveBase = baseURL + "/auth"
			testResp2.Body.Close()
		} else if testResp2 != nil {
			testResp2.Body.Close()
		}
	} else {
		testResp.Body.Close()
	}

	// Get admin token from master realm
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", effectiveBase)
	body := url.Values{
		"client_id":  {"admin-cli"},
		"grant_type": {"password"},
		"username":   {adminUser},
		"password":   {adminPass},
	}
	resp, err := httpCli.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("keycloak auth failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("keycloak auth HTTP %d: %s", resp.StatusCode, string(data))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("keycloak token decode: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("keycloak returned empty token")
	}

	return &kcClient{
		baseURL: effectiveBase,
		token:   tokenResp.AccessToken,
		httpCli: httpCli,
	}, nil
}

func (k *kcClient) do(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, k.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return k.httpCli.Do(req)
}

func (k *kcClient) realmExists(realm string) (bool, error) {
	resp, err := k.do("GET", "/admin/realms/"+realm, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return true, nil
	}
	if resp.StatusCode == 404 {
		return false, nil
	}
	return false, fmt.Errorf("realm check HTTP %d", resp.StatusCode)
}

func (k *kcClient) createRealm(realm string) error {
	body := map[string]interface{}{
		"realm":   realm,
		"enabled": true,
		"displayName": "HELIOS " + strings.ToUpper(realm[:1]) + realm[1:],
	}
	resp, err := k.do("POST", "/admin/realms", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 201 || resp.StatusCode == 409 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("create realm HTTP %d: %s", resp.StatusCode, string(data))
}

func (k *kcClient) clientExists(realm, clientID string) (bool, string, error) {
	resp, err := k.do("GET", fmt.Sprintf("/admin/realms/%s/clients?clientId=%s", realm, clientID), nil)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	var clients []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		return false, "", err
	}
	for _, c := range clients {
		if c["clientId"] == clientID {
			id, _ := c["id"].(string)
			return true, id, nil
		}
	}
	return false, "", nil
}

func (k *kcClient) createClient(realm, clientID string) error {
	body := map[string]interface{}{
		"clientId":                  clientID,
		"enabled":                   true,
		"publicClient":              true,
		"standardFlowEnabled":       true,
		"directAccessGrantsEnabled": false,
		"redirectUris":              []string{"*"},
		"webOrigins":                []string{"*"},
	}
	resp, err := k.do("POST", fmt.Sprintf("/admin/realms/%s/clients", realm), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 201 || resp.StatusCode == 409 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("create client HTTP %d: %s", resp.StatusCode, string(data))
}

// createOrUpdateAppClient creates (or updates) an OIDC client for an app in the given realm.
// redirectBase is the base URL of the app, e.g. "https://chat.cmars.com".
func (k *kcClient) createOrUpdateAppClient(realm, clientID, redirectBase string) error {
	body := map[string]interface{}{
		"clientId":                  clientID,
		"enabled":                   true,
		"publicClient":              true,
		"standardFlowEnabled":       true,
		"directAccessGrantsEnabled": false,
		"redirectUris":              []string{redirectBase + "/*"},
		"webOrigins":                []string{redirectBase},
		// PKCE
		"attributes": map[string]string{
			"pkce.code.challenge.method": "S256",
		},
	}

	// Check if client already exists
	exists, clientUID, err := k.clientExists(realm, clientID)
	if err != nil {
		return err
	}

	if exists && clientUID != "" {
		// Update existing client via PUT
		resp, err := k.do("PUT", fmt.Sprintf("/admin/realms/%s/clients/%s", realm, clientUID), body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == 204 {
			return nil
		}
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update app client HTTP %d: %s", resp.StatusCode, string(data))
	}

	// Create new client
	resp, err := k.do("POST", fmt.Sprintf("/admin/realms/%s/clients", realm), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 201 || resp.StatusCode == 409 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("create app client HTTP %d: %s", resp.StatusCode, string(data))
}

// deleteClient removes a Keycloak client by its internal UUID.
func (k *kcClient) deleteClient(realm, clientUID string) error {
	resp, err := k.do("DELETE", fmt.Sprintf("/admin/realms/%s/clients/%s", realm, clientUID), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 || resp.StatusCode == 404 {
		return nil
	}
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("delete client HTTP %d: %s", resp.StatusCode, string(data))
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// ssoStatus checks Keycloak connectivity and realm/client existence.
func ssoStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := d.Cfg
		kcURL := cfg.KeycloakURL
		adminUser := cfg.KCAdminUser
		adminPass := cfg.KCAdminPassword

		result := map[string]interface{}{
			"keycloak_url":    kcURL,
			"connected":       false,
			"polyon_realm":    false,
			"polyon_ui_client": false,
			"error":           nil,
		}

		kc, err := newKCClient(kcURL, adminUser, adminPass)
		if err != nil {
			result["error"] = err.Error()
			httputil.RespondOK(w, result)
			return
		}
		result["connected"] = true

		// Check polyon realm
		realmExists, err := kc.realmExists("polyon")
		if err != nil {
			result["error"] = err.Error()
			httputil.RespondOK(w, result)
			return
		}
		result["polyon_realm"] = realmExists

		if realmExists {
			// Check polyon-console client
			clientExists, _, err := kc.clientExists("polyon", "polyon-console")
			if err != nil {
				result["error"] = err.Error()
			} else {
				result["polyon_ui_client"] = clientExists
			}
		}

		httputil.RespondOK(w, result)
	}
}

// ssoEnable provisions Keycloak realm 'polyon' + client 'polyon-console'.
func ssoEnable(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := d.Cfg
		kcURL := cfg.KeycloakURL
		adminUser := cfg.KCAdminUser
		adminPass := cfg.KCAdminPassword

		log.Info().Str("keycloak_url", kcURL).Msg("SSO enable requested")

		kc, err := newKCClient(kcURL, adminUser, adminPass)
		if err != nil {
			httputil.RespondError(w, 503, "KC_AUTH_FAILED", "Keycloak 인증 실패: "+err.Error())
			return
		}

		// Provision realm 'polyon'
		realmExists, err := kc.realmExists("polyon")
		if err != nil {
			httputil.RespondError(w, 503, "KC_REALM_CHECK_FAILED", err.Error())
			return
		}

		realmCreated := false
		if !realmExists {
			if err := kc.createRealm("polyon"); err != nil {
				httputil.RespondError(w, 503, "KC_REALM_CREATE_FAILED", err.Error())
				return
			}
			realmCreated = true
			log.Info().Msg("Keycloak realm 'polyon' created")
		} else {
			log.Info().Msg("Keycloak realm 'polyon' already exists")
		}

		// Provision client 'polyon-console'
		clientExists, _, err := kc.clientExists("polyon", "polyon-console")
		if err != nil {
			httputil.RespondError(w, 503, "KC_CLIENT_CHECK_FAILED", err.Error())
			return
		}

		clientCreated := false
		if !clientExists {
			if err := kc.createClient("polyon", "polyon-console"); err != nil {
				httputil.RespondError(w, 503, "KC_CLIENT_CREATE_FAILED", err.Error())
				return
			}
			clientCreated = true
			log.Info().Msg("Keycloak client 'polyon-console' created in realm 'polyon'")
		} else {
			log.Info().Msg("Keycloak client 'polyon-console' already exists")
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success":        true,
			"realm":          "polyon",
			"client":         "polyon-console",
			"realm_created":  realmCreated,
			"client_created": clientCreated,
			"message":        "SSO가 활성화되었습니다",
		})
	}
}
