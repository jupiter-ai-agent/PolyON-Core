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
func chatEnsureToken(d *Deps) string {
	// TODO: Implement K8s Secret-based token management
	// For now, this is a placeholder that will be enhanced in future iterations
	// when the K8s client interface is properly exposed.
	
	// Currently relies on MATTERMOST_TOKEN environment variable
	// which should be set during module installation
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
