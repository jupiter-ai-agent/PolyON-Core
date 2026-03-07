package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ── Mattermost proxy helpers ──────────────────────────────────────────────────

var chatClient = &http.Client{Timeout: 15 * time.Second}

func mattermostURL(d *Deps) string {
	if d != nil && d.Cfg != nil && d.Cfg.MattermostURL != "" {
		return d.Cfg.MattermostURL
	}
	if u := os.Getenv("MATTERMOST_URL"); u != "" {
		return u
	}
	return "http://polyon-mattermost:8065"
}

func mattermostToken(d *Deps) string {
	// 1. Check environment variable
	if t := os.Getenv("MATTERMOST_TOKEN"); t != "" {
		return t
	}

	// 2. Check existing Mattermost client
	if d != nil && d.MattermostClient != nil {
		// Extract token via environment (the client stores it internally)
	}

	// 3. Lazy init: Get token from K8s Secret or create new one
	return chatEnsureToken(d)
}

// chatEnsureToken ensures that an admin token exists, creating one if needed.
var (
	cachedMMToken string
	mmTokenMu     sync.Mutex
)

// chatEnsureToken ensures a valid Mattermost admin session token.
// Creates admin user if needed, logs in, caches the token.
func chatEnsureToken(d *Deps) string {
	mmTokenMu.Lock()
	defer mmTokenMu.Unlock()

	// Return cached token if still valid
	if cachedMMToken != "" {
		if verifyMMToken(d, cachedMMToken) {
			return cachedMMToken
		}
		cachedMMToken = ""
	}

	// 1. Try to read from K8s Secret
	if d.Kube != nil {
		secret, err := d.Kube.GetSecret(context.Background(), "polyon-module-mattermost")
		if err == nil {
			if t, ok := secret["ADMIN_TOKEN"]; ok && t != "" && verifyMMToken(d, t) {
				cachedMMToken = t
				return t
			}
		}
	}

	mmURL := mattermostURL(d)
	adminPassword := "PolyON-Admin-2026!"
	adminEmail := "admin@" + d.Cfg.Realm

	// 2. Wait for Mattermost API to be ready (up to 90s)
	log.Info().Str("url", mmURL).Msg("Waiting for Mattermost API...")
	for i := 0; i < 45; i++ {
		resp, pingErr := mmClient.Get(mmURL + "/api/v4/system/ping")
		if pingErr == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				log.Info().Int("attempts", i+1).Msg("Mattermost API ready")
				break
			}
		}
		if i == 44 {
			log.Error().Msg("Mattermost API not ready after 90s")
			return ""
		}
		time.Sleep(2 * time.Second)
	}

	// 3. Try login first (admin may already exist)
	token, err := mmLogin(mmURL, "admin", adminPassword)
	if err != nil {
		// 4. Create admin user
		log.Info().Msg("Creating Mattermost admin user")
		if err := mmCreateAdmin(mmURL, adminEmail, adminPassword); err != nil {
			log.Error().Err(err).Msg("Failed to create Mattermost admin")
			return ""
		}
		// 4b. Login again (first user is auto system_admin in Mattermost)
		token, err = mmLogin(mmURL, "admin", adminPassword)
		if err != nil {
			log.Error().Err(err).Msg("Failed to login as Mattermost admin after creation")
			return ""
		}
	}

	// 4. Save token to K8s Secret
	if d.Kube != nil {
		_ = d.Kube.PatchSecret(context.Background(), "polyon-module-mattermost",
			map[string]string{"ADMIN_TOKEN": token})
	}

	cachedMMToken = token
	log.Info().Msg("Mattermost admin token acquired")
	return token
}

func mmLogin(baseURL, loginID, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"login_id": loginID, "password": password})
	resp, err := mmClient.Post(baseURL+"/api/v4/users/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login failed (%d): %s", resp.StatusCode, string(b))
	}
	return resp.Header.Get("Token"), nil
}

func mmCreateAdmin(baseURL, email, password string) error {
	body, _ := json.Marshal(map[string]string{
		"email": email, "username": "admin", "password": password,
	})
	resp, err := mmClient.Post(baseURL+"/api/v4/users", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create user failed (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

func verifyMMToken(d *Deps, token string) bool {
	req, _ := http.NewRequest("GET", mattermostURL(d)+"/api/v4/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := mmClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// chatProxy forwards a request to Mattermost and writes the response back.
func chatProxy(d *Deps, method, path string, body io.Reader, w http.ResponseWriter, r *http.Request) {
	// Forward query string
	fullPath := path
	if q := r.URL.RawQuery; q != "" {
		fullPath += "?" + q
	}

	req, err := http.NewRequest(method, mattermostURL(d)+fullPath, body)
	if err != nil {
		httputil.RespondError(w, 500, "internal_error", "build request: "+err.Error())
		return
	}
	token := mattermostToken(d)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := chatClient.Do(req)
	if err != nil {
		log.Error().Err(err).Str("url", mattermostURL(d)+fullPath).Msg("Mattermost proxy error")
		httputil.RespondError(w, 502, "gateway_error", "mattermost unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// RegisterChat registers Mattermost management routes.
func RegisterChat(r chi.Router, d *Deps) {
	r.Route("/engines/chat", func(r chi.Router) {
		// Teams CRUD
		r.Get("/teams", chatListTeams(d))
		r.Post("/teams", chatCreateTeam(d))
		r.Put("/teams/{teamId}", chatUpdateTeam(d))
		r.Delete("/teams/{teamId}", chatDeleteTeam(d))
		r.Get("/teams/{teamId}/members", chatListTeamMembers(d))
		r.Post("/teams/{teamId}/members", chatAddTeamMember(d))
		r.Delete("/teams/{teamId}/members/{userId}", chatRemoveTeamMember(d))

		// Channels CRUD
		r.Get("/channels", chatListChannels(d))
		r.Post("/channels", chatCreateChannel(d))
		r.Put("/channels/{channelId}", chatUpdateChannel(d))
		r.Delete("/channels/{channelId}", chatDeleteChannel(d))
		r.Get("/channels/{channelId}/members", chatListChannelMembers(d))
		r.Post("/channels/{channelId}/members", chatAddChannelMember(d))
		r.Delete("/channels/{channelId}/members/{userId}", chatRemoveChannelMember(d))

		// Users Management
		r.Get("/users", chatListUsers(d))
		r.Get("/users/{userId}", chatGetUser(d))
		r.Put("/users/{userId}/roles", chatUpdateUserRoles(d))
		r.Put("/users/{userId}/active", chatUpdateUserActive(d))

		// Config
		r.Get("/config", chatGetConfig(d))
		r.Put("/config", chatPutConfig(d))

		// Stats
		r.Get("/stats", chatGetStats(d))

		// LDAP sync
		r.Post("/ldap-sync", chatLDAPSync(d))

		// System info
		r.Get("/ping", chatPing(d))
	})
}

// ── Teams CRUD ──

func chatListTeams(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/teams", nil, w, r)
	}
}

func chatCreateTeam(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "POST", "/api/v4/teams", r.Body, w, r)
	}
}

func chatUpdateTeam(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamId := chi.URLParam(r, "teamId")
		chatProxy(d, "PUT", "/api/v4/teams/"+teamId, r.Body, w, r)
	}
}

func chatDeleteTeam(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamId := chi.URLParam(r, "teamId")
		chatProxy(d, "DELETE", "/api/v4/teams/"+teamId, nil, w, r)
	}
}

func chatListTeamMembers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamId := chi.URLParam(r, "teamId")
		chatProxy(d, "GET", "/api/v4/teams/"+teamId+"/members", nil, w, r)
	}
}

func chatAddTeamMember(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamId := chi.URLParam(r, "teamId")
		chatProxy(d, "POST", "/api/v4/teams/"+teamId+"/members", r.Body, w, r)
	}
}

func chatRemoveTeamMember(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamId := chi.URLParam(r, "teamId")
		userId := chi.URLParam(r, "userId")
		chatProxy(d, "DELETE", "/api/v4/teams/"+teamId+"/members/"+userId, nil, w, r)
	}
}

// ── Channels CRUD ──

func chatListChannels(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Default to first team's channels or use ?team_id=...
		teamID := r.URL.Query().Get("team_id")
		if teamID == "" {
			// Return all public channels via system endpoint
			chatProxy(d, "GET", "/api/v4/channels", nil, w, r)
			return
		}
		chatProxy(d, "GET", "/api/v4/teams/"+teamID+"/channels", nil, w, r)
	}
}

func chatCreateChannel(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "POST", "/api/v4/channels", r.Body, w, r)
	}
}

func chatUpdateChannel(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelId := chi.URLParam(r, "channelId")
		chatProxy(d, "PUT", "/api/v4/channels/"+channelId, r.Body, w, r)
	}
}

func chatDeleteChannel(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelId := chi.URLParam(r, "channelId")
		chatProxy(d, "DELETE", "/api/v4/channels/"+channelId, nil, w, r)
	}
}

func chatListChannelMembers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelId := chi.URLParam(r, "channelId")
		chatProxy(d, "GET", "/api/v4/channels/"+channelId+"/members", nil, w, r)
	}
}

func chatAddChannelMember(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelId := chi.URLParam(r, "channelId")
		chatProxy(d, "POST", "/api/v4/channels/"+channelId+"/members", r.Body, w, r)
	}
}

func chatRemoveChannelMember(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channelId := chi.URLParam(r, "channelId")
		userId := chi.URLParam(r, "userId")
		chatProxy(d, "DELETE", "/api/v4/channels/"+channelId+"/members/"+userId, nil, w, r)
	}
}

// ── Users Management ──

func chatListUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/users", nil, w, r)
	}
}

func chatGetUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userId := chi.URLParam(r, "userId")
		chatProxy(d, "GET", "/api/v4/users/"+userId, nil, w, r)
	}
}

func chatUpdateUserRoles(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userId := chi.URLParam(r, "userId")
		chatProxy(d, "PUT", "/api/v4/users/"+userId+"/roles", r.Body, w, r)
	}
}

func chatUpdateUserActive(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userId := chi.URLParam(r, "userId")
		chatProxy(d, "PUT", "/api/v4/users/"+userId+"/active", r.Body, w, r)
	}
}

// ── Config ──

func chatGetConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/config", nil, w, r)
	}
}

func chatPutConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "PUT", "/api/v4/config", r.Body, w, r)
	}
}

// ── Stats ──

func chatGetStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/analytics/old", nil, w, r)
	}
}

// ── LDAP sync ──

func chatLDAPSync(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "POST", "/api/v4/ldap/sync", nil, w, r)
	}
}

// ── Ping ──

func chatPing(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/system/ping", nil, w, r)
	}
}
