package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAuthMgmt registers Keycloak Auth management API routes.
func RegisterAuthMgmt(r chi.Router, d *Deps) {
	r.Route("/auth", func(r chi.Router) {
		r.Get("/overview", authOverview(d))
		r.Get("/realms", authRealms(d))
		r.Get("/clients", authClients(d))
		r.Get("/users", authUsers(d))
		r.Get("/groups", authGroups(d))
		r.Get("/sessions", authSessions(d))
		r.Get("/federation", authFederation(d))
		r.Post("/federation/sync", authFederationSync(d))
	})
}

// ── Token cache ──────────────────────────────────────────────────────────────

type kcTokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

var globalKCTokenCache = &kcTokenCache{}

// kcGetToken obtains (or returns cached) master realm admin token.
func kcGetToken(d *Deps) (string, error) {
	globalKCTokenCache.mu.Lock()
	defer globalKCTokenCache.mu.Unlock()

	// Return cached token if still valid (keep 10s buffer)
	if globalKCTokenCache.token != "" && time.Now().Before(globalKCTokenCache.expiresAt.Add(-10*time.Second)) {
		return globalKCTokenCache.token, nil
	}

	kcURL := d.Cfg.KeycloakURL
	adminUser := d.Cfg.KCAdminUser
	adminPass := d.Cfg.KCAdminPassword

	kc, err := newKCClient(kcURL, adminUser, adminPass)
	if err != nil {
		return "", fmt.Errorf("kcGetToken: %w", err)
	}

	globalKCTokenCache.token = kc.token
	// KC default token TTL is 60s
	globalKCTokenCache.expiresAt = time.Now().Add(55 * time.Second)

	return kc.token, nil
}

// kcAdminDo performs a Keycloak Admin REST API call with auto token refresh.
func kcAdminDo(d *Deps, method, path string, body interface{}) (*http.Response, error) {
	token, err := kcGetToken(d)
	if err != nil {
		return nil, err
	}

	kc := &kcClient{
		baseURL: d.Cfg.KeycloakURL,
		token:   token,
		httpCli: &http.Client{Timeout: 15 * time.Second},
	}
	return kc.do(method, path, body)
}

// kcJSON performs an admin call and decodes the JSON response.
func kcJSON(d *Deps, method, path string, body interface{}, out interface{}) error {
	resp, err := kcAdminDo(d, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("KC %s %s → HTTP %d: %s", method, path, resp.StatusCode, string(data))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// ── Overview ─────────────────────────────────────────────────────────────────

type realmOverview struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Users          int    `json:"users"`
	Clients        int    `json:"clients"`
	Groups         int    `json:"groups"`
	ActiveSessions int    `json:"active_sessions"`
	Enabled        bool   `json:"enabled"`
}

func authOverview(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realms := []string{"admin", "polyon"}
		result := make([]realmOverview, 0, len(realms))

		for _, realm := range realms {
			ov := realmOverview{ID: realm, Name: realm, Enabled: true}

			// realm info
			var realmInfo map[string]interface{}
			if err := kcJSON(d, "GET", "/admin/realms/"+realm, nil, &realmInfo); err == nil {
				if en, ok := realmInfo["enabled"].(bool); ok {
					ov.Enabled = en
				}
			}

			// users count
			var users []json.RawMessage
			if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/users?max=1000", realm), nil, &users); err == nil {
				ov.Users = len(users)
			}

			// clients count
			var clients []json.RawMessage
			if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/clients", realm), nil, &clients); err == nil {
				ov.Clients = len(clients)
			}

			// groups count
			var groups []json.RawMessage
			if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/groups?briefRepresentation=true&max=1000", realm), nil, &groups); err == nil {
				ov.Groups = len(groups)
			}

			// active sessions
			var sessionStats map[string]interface{}
			if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/client-session-stats", realm), nil, &sessionStats); err == nil {
				// client-session-stats returns array; sum active
				var statsArr []map[string]interface{}
				if err2 := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/client-session-stats", realm), nil, &statsArr); err2 == nil {
					total := 0
					for _, s := range statsArr {
						if v, ok := s["active"].(float64); ok {
							total += int(v)
						}
					}
					ov.ActiveSessions = total
				}
			}

			result = append(result, ov)
		}

		httputil.RespondOK(w, map[string]interface{}{"realms": result})
	}
}

// ── Realms ────────────────────────────────────────────────────────────────────

func authRealms(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allRealms []map[string]interface{}
		if err := kcJSON(d, "GET", "/admin/realms", nil, &allRealms); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", err.Error())
			return
		}

		// Filter: exclude master
		result := make([]map[string]interface{}, 0)
		for _, realm := range allRealms {
			id, _ := realm["id"].(string)
			if id == "master" {
				continue
			}
			result = append(result, map[string]interface{}{
				"id":          id,
				"displayName": realm["displayName"],
				"enabled":     realm["enabled"],
				"loginTheme":  realm["loginTheme"],
			})
		}

		httputil.RespondOK(w, map[string]interface{}{"realms": result})
	}
}

// ── Clients ───────────────────────────────────────────────────────────────────

func authClients(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realm := r.URL.Query().Get("realm")
		if realm == "" || realm == "master" {
			realm = "polyon"
		}

		var rawClients []map[string]interface{}
		if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/clients", realm), nil, &rawClients); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", err.Error())
			return
		}

		result := make([]map[string]interface{}, 0, len(rawClients))
		for _, c := range rawClients {
			result = append(result, map[string]interface{}{
				"id":           c["id"],
				"clientId":     c["clientId"],
				"name":         c["name"],
				"enabled":      c["enabled"],
				"protocol":     c["protocol"],
				"publicClient": c["publicClient"],
				"redirectUris": c["redirectUris"],
			})
		}

		httputil.RespondOK(w, map[string]interface{}{"clients": result})
	}
}

// ── Users ─────────────────────────────────────────────────────────────────────

func authUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realm := r.URL.Query().Get("realm")
		if realm == "" || realm == "master" {
			realm = "polyon"
		}
		maxParam := r.URL.Query().Get("max")
		if maxParam == "" {
			maxParam = "100"
		}

		var rawUsers []map[string]interface{}
		if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/users?max=%s", realm, maxParam), nil, &rawUsers); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", err.Error())
			return
		}

		result := make([]map[string]interface{}, 0, len(rawUsers))
		for _, u := range rawUsers {
			result = append(result, map[string]interface{}{
				"id":               u["id"],
				"username":         u["username"],
				"email":            u["email"],
				"enabled":          u["enabled"],
				"emailVerified":    u["emailVerified"],
				"firstName":        u["firstName"],
				"lastName":         u["lastName"],
				"createdTimestamp": u["createdTimestamp"],
			})
		}

		httputil.RespondOK(w, map[string]interface{}{"users": result})
	}
}

// ── Groups ────────────────────────────────────────────────────────────────────

func authGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realm := r.URL.Query().Get("realm")
		if realm == "" || realm == "master" {
			realm = "polyon"
		}

		var rawGroups []map[string]interface{}
		if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/groups?briefRepresentation=false&max=1000", realm), nil, &rawGroups); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", err.Error())
			return
		}

		result := make([]map[string]interface{}, 0, len(rawGroups))
		for _, g := range rawGroups {
			subGroups, _ := g["subGroups"].([]interface{})
			result = append(result, map[string]interface{}{
				"id":            g["id"],
				"name":          g["name"],
				"path":          g["path"],
				"subGroupCount": len(subGroups),
			})
		}

		httputil.RespondOK(w, map[string]interface{}{"groups": result})
	}
}

// ── Sessions ──────────────────────────────────────────────────────────────────

func authSessions(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realm := r.URL.Query().Get("realm")
		if realm == "" || realm == "master" {
			realm = "polyon"
		}

		var statsArr []map[string]interface{}
		if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/client-session-stats", realm), nil, &statsArr); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", err.Error())
			return
		}

		totalActive := 0
		totalOffline := 0
		for _, s := range statsArr {
			if v, ok := s["active"].(float64); ok {
				totalActive += int(v)
			}
			if v, ok := s["offline"].(float64); ok {
				totalOffline += int(v)
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"realm":          realm,
			"active":         totalActive,
			"offline":        totalOffline,
			"client_details": statsArr,
		})
	}
}

// ── Federation ────────────────────────────────────────────────────────────────

func authFederation(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realm := r.URL.Query().Get("realm")
		if realm == "" || realm == "master" {
			realm = "polyon"
		}

		var rawProviders []map[string]interface{}
		if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/components?type=org.keycloak.storage.UserStorageProvider", realm), nil, &rawProviders); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", err.Error())
			return
		}

		result := make([]map[string]interface{}, 0, len(rawProviders))
		for _, p := range rawProviders {
			rawCfg, _ := p["config"].(map[string]interface{})
			flatCfg := map[string]interface{}{}
			for k, v := range rawCfg {
				// KC config values are []string — unwrap single value
				if arr, ok := v.([]interface{}); ok && len(arr) == 1 {
					flatCfg[k] = arr[0]
				} else {
					flatCfg[k] = v
				}
			}
			result = append(result, map[string]interface{}{
				"id":         p["id"],
				"name":       p["name"],
				"providerId": p["providerId"],
				"enabled":    flatCfg["enabled"],
				"config": map[string]interface{}{
					"connectionUrl":     flatCfg["connectionUrl"],
					"usersDn":           flatCfg["usersDn"],
					"bindDn":            flatCfg["bindDn"],
					"syncRegistrations": flatCfg["syncRegistrations"],
					"fullSyncPeriod":    flatCfg["fullSyncPeriod"],
				},
			})
		}

		httputil.RespondOK(w, map[string]interface{}{"providers": result})
	}
}

// ── Federation Sync ───────────────────────────────────────────────────────────

func authFederationSync(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		realm := r.URL.Query().Get("realm")
		if realm == "" || realm == "master" {
			realm = "polyon"
		}

		// Get all user storage providers
		var rawProviders []map[string]interface{}
		if err := kcJSON(d, "GET", fmt.Sprintf("/admin/realms/%s/components?type=org.keycloak.storage.UserStorageProvider", realm), nil, &rawProviders); err != nil {
			httputil.RespondError(w, 503, "KC_ERROR", "federation 목록 조회 실패: "+err.Error())
			return
		}

		if len(rawProviders) == 0 {
			httputil.RespondError(w, 404, "NO_PROVIDERS", "federation provider가 없습니다")
			return
		}

		results := make([]map[string]interface{}, 0)
		for _, p := range rawProviders {
			id, _ := p["id"].(string)
			name, _ := p["name"].(string)
			if id == "" {
				continue
			}

			syncPath := fmt.Sprintf("/admin/realms/%s/user-storage/%s/sync?action=triggerFullSync", realm, id)
			resp, err := kcAdminDo(d, "POST", syncPath, nil)
			if err != nil {
				results = append(results, map[string]interface{}{
					"id":      id,
					"name":    name,
					"success": false,
					"error":   err.Error(),
				})
				continue
			}
			defer resp.Body.Close()

			var syncResult map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&syncResult)

			results = append(results, map[string]interface{}{
				"id":      id,
				"name":    name,
				"success": resp.StatusCode >= 200 && resp.StatusCode < 300,
				"status":  resp.StatusCode,
				"result":  syncResult,
			})
		}

		httputil.RespondOK(w, map[string]interface{}{
			"realm":   realm,
			"synced":  len(results),
			"results": results,
		})
	}
}
