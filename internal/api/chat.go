package api

import (
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
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
	return "http://polyon-chat:8065"
}

func mattermostToken(d *Deps) string {
	if d != nil && d.MattermostClient != nil {
		// Extract token via environment (the client stores it internally)
	}
	if t := os.Getenv("MATTERMOST_TOKEN"); t != "" {
		return t
	}
	return ""
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
		// Teams
		r.Get("/teams", chatListTeams(d))

		// Channels
		r.Get("/channels", chatListChannels(d))

		// Users
		r.Get("/users", chatListUsers(d))

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

// ── Teams ──

func chatListTeams(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/teams", nil, w, r)
	}
}

// ── Channels ──

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

// ── Users ──

func chatListUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatProxy(d, "GET", "/api/v4/users", nil, w, r)
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
