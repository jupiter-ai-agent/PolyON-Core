// Package auth provides Keycloak OIDC JWT verification middleware.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
				r.URL.Path == "/api/v1/status" ||
				r.URL.Path == "/api/v1/system/auth-config" ||
				r.URL.Path == "/api/sentinel/state" ||
				r.URL.Path == "/api/v1/policy/status" ||
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
				// Provisioned → reject anonymous
				if cfg.IsProvisioned() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(401)
					w.Write([]byte(`{"status":"error","code":"AUTH_REQUIRED","error":"인증이 필요합니다"}`))
					return
				}
				// Not provisioned → allow through as setup
				ctx := context.WithValue(r.Context(), ActorKey, "setup")
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

			// JWT에서 roles + azp 추출 (fail-safe: 오류 시 빈 배열)
			rawRoles := extractRolesFromToken(token)

			// __azp: prefix 분리
			var roles []string
			var clientID string
			for _, r := range rawRoles {
				if strings.HasPrefix(r, "__azp:") {
					clientID = strings.TrimPrefix(r, "__azp:")
				} else {
					roles = append(roles, r)
				}
			}

			// OPA RBAC 판정 (fail-open)
			opaPath := strings.TrimPrefix(r.URL.Path, "/api/v1")
			input := OPAInput{
				User:   username,
				Roles:  roles,
				Client: clientID,
				Method: r.Method,
				Path:   opaPath,
				IP:     r.RemoteAddr,
			}
			allowed, opaErr := EvaluatePolicy(r.Context(), input)
			if opaErr != nil {
				// fail-open: OPA 오류 시 허용 (서비스 연속성)
				log.Debug().Err(opaErr).Str("user", username).Str("path", opaPath).Msg("OPA eval error — fail-open")
			} else if !allowed {
				// OPA가 명확히 거부한 경우에만 403
				log.Debug().Str("user", username).Strs("roles", roles).Str("path", opaPath).Str("method", r.Method).Msg("OPA denied request")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(403)
				w.Write([]byte(`{"status":"error","code":"FORBIDDEN","error":"권한이 없습니다"}`))
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

// extractRolesFromToken은 JWT에서 realm_access.roles 클레임을 추출합니다.
// 검증은 이미 verifyToken에서 완료된 상태이므로 파싱만 수행합니다.
// 추출 실패 시 빈 슬라이스를 반환합니다 (panic 방지).
func extractRolesFromToken(tokenStr string) []string {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return []string{}
	}

	// base64url 디코딩 (패딩 추가)
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return []string{}
	}

	// claims 파싱
	var claims struct {
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
		AZP string `json:"azp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return []string{}
	}

	roles := claims.RealmAccess.Roles
	// azp를 특수 역할로 포함 (OPA에서 client 식별에 사용)
	if claims.AZP != "" {
		roles = append(roles, "__azp:"+claims.AZP)
	}
	return roles
}
