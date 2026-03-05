// Package auth provides Keycloak OIDC JWT verification middleware.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
)

type ctxKey string

const ActorKey ctxKey = "actor"

// Middleware returns an HTTP middleware that verifies Keycloak OIDC tokens.
// Before setup is complete, all requests are allowed through.
func Middleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health checks and internal endpoints
			if r.URL.Path == "/health" ||
				strings.HasPrefix(r.URL.Path, "/api/internal/") {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth if setup is not complete
			if !cfg.IsProvisioned() {
				ctx := context.WithValue(r.Context(), ActorKey, "setup")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Skip auth for setup/progress endpoints
			if strings.HasPrefix(r.URL.Path, "/api/setup/") {
				next.ServeHTTP(w, r)
				return
			}

			// Extract Bearer token
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				log.Debug().Str("path", r.URL.Path).Msg("No Authorization header — anonymous")
				ctx := context.WithValue(r.Context(), ActorKey, "anonymous")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			username, err := verifyToken(cfg, token)
			if err != nil {
				log.Debug().Err(err).Msg("Token verification failed")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(401)
				w.Write([]byte(`{"status":"error","code":"AUTH_FAILED","error":"인증 실패"}`))
				return
			}

			ctx := context.WithValue(r.Context(), ActorKey, username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetActor returns the authenticated username from context.
func GetActor(r *http.Request) string {
	if actor, ok := r.Context().Value(ActorKey).(string); ok {
		return actor
	}
	return "unknown"
}
