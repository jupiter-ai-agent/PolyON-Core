// Package auth — forward.go provides the Traefik Forward Auth endpoint.
// Traefik sends every request to /api/internal/auth/verify before forwarding.
// This handler validates the PolyON_TOKEN cookie (or Authorization header),
// and redirects unauthenticated users to the Keycloak login page.
package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
)

const polyonTokenCookie = "POLYON_TOKEN"

// forwardAuthClientID is the single Keycloak client used for all Forward Auth flows.
// All app subdomains share this client — Keycloak redirectUris must include the callback.
const forwardAuthClientID = "portal"

// BaseDomainResolver provides the service base domain from an external source (e.g. DB).
// If nil or returns "", falls back to config.Realm.
var BaseDomainResolver func() string

// RegisterForwardAuth registers the forward-auth endpoints on the given router.
// Expected: r is already scoped to /api/internal/auth
func RegisterForwardAuth(r chi.Router, cfg *config.Config) {
	r.Get("/verify", forwardAuthVerify(cfg))
	r.Get("/callback", forwardAuthCallback(cfg))
}

// forwardAuthVerify is the Forward Auth endpoint called by Traefik.
// It validates the JWT and either:
//   - Returns 200 + X-Auth-User header (success)
//   - Returns 302 redirect to Keycloak login (failure/unauthenticated)
func forwardAuthVerify(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)

		if token != "" {
			username, err := verifyJWTLocalWithRealm(cfg, token, "polyon")
			if err == nil {
				// Token valid — evaluate OPA policy
				groups := extractGroupsFromJWT(token)
				input := OPAInput{
					User:   username,
					Groups: groups,
					Method: r.Method,
					Path:   r.Header.Get("X-Forwarded-Uri"),
					IP:     r.Header.Get("X-Forwarded-For"),
				}
				if input.Path == "" {
					input.Path = r.URL.Path
				}

				allowed, _ := EvaluatePolicy(r.Context(), input)
				if !allowed {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"status":"error","code":"POLICY_DENIED","error":"접근이 거부되었습니다"}`))
					return
				}

				w.Header().Set("X-Auth-User", username)
				w.Header().Set("X-Auth-Realm", "polyon")
				w.WriteHeader(http.StatusOK)
				return
			}
			log.Debug().Err(err).Msg("forward-auth: token invalid, redirecting to login")
		}

		// Not authenticated — redirect to Keycloak login
		redirectToLogin(w, r, cfg)
	}
}

// forwardAuthCallback handles the OIDC authorization code callback from Keycloak.
// It exchanges the code for tokens, sets the PolyON_TOKEN cookie, and redirects
// the user back to the original URL.
func forwardAuthCallback(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state") // original URL, base64url-encoded
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		// Exchange code for tokens
		tokenResp, err := exchangeCode(cfg, code, callbackURL(r, cfg))
		if err != nil {
			log.Error().Err(err).Msg("forward-auth: token exchange failed")
			http.Error(w, "token exchange failed", http.StatusInternalServerError)
			return
		}

		// Set PolyON_TOKEN cookie (domain-wide, .baseDomain)
		baseDomain := baseDomainFromConfig(cfg)
		cookieDomain := "." + baseDomain
		if baseDomain == "" {
			cookieDomain = ""
		}

		http.SetCookie(w, &http.Cookie{
			Name:     polyonTokenCookie,
			Value:    tokenResp.AccessToken,
			Domain:   cookieDomain,
			Path:     "/",
			MaxAge:   86400,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})

		// Redirect to original URL (stored in state param)
		originalURL := "/"
		if state != "" {
			decoded, err := url.QueryUnescape(state)
			if err == nil && strings.HasPrefix(decoded, "https://") {
				originalURL = decoded
			}
		}

		http.Redirect(w, r, originalURL, http.StatusFound)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// extractToken pulls the JWT from the PolyON_TOKEN cookie or Authorization header.
func extractToken(r *http.Request) string {
	// 1. Cookie
	if c, err := r.Cookie(polyonTokenCookie); err == nil && c.Value != "" {
		return c.Value
	}
	// 2. Authorization: Bearer <token>
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// redirectToLogin redirects the user to the Keycloak login page.
// The original request URL is encoded as the state parameter.
func redirectToLogin(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	// Build the redirect_uri pointing to our callback
	cbURL := callbackURL(r, cfg)

	// The state carries the original URL so we can redirect back after login
	originalURL := originalRequestURL(r)

	// All apps use the same "portal" client — avoids client_id mismatch between
	// the authorization request and the token exchange.
	loginURL := fmt.Sprintf(
		"%s/realms/polyon/protocol/openid-connect/auth?client_id=%s&response_type=code&scope=openid&redirect_uri=%s&state=%s",
		cfg.KeycloakURL,
		url.QueryEscape(forwardAuthClientID),
		url.QueryEscape(cbURL),
		url.QueryEscape(originalURL),
	)

	http.Redirect(w, r, loginURL, http.StatusFound)
}

// exchangeCode exchanges an authorization code for tokens via Keycloak token endpoint.
func exchangeCode(cfg *config.Config, code, redirectURI string) (*oidcTokenResponse, error) {
	tokenURL := fmt.Sprintf("%s/realms/polyon/protocol/openid-connect/token", cfg.KeycloakURL)

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {"portal"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}

	resp, err := tlsClient.PostForm(tokenURL, form)
	if err != nil {
		return nil, fmt.Errorf("POST token endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tr oidcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &tr, nil
}

type oidcTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// baseDomainFromConfig derives the service base domain.
// Priority: BaseDomainResolver (DB) → config.Realm (AD domain) as fallback.
func baseDomainFromConfig(cfg *config.Config) string {
	if BaseDomainResolver != nil {
		if bd := BaseDomainResolver(); bd != "" {
			return bd
		}
	}
	if cfg.Realm != "" {
		return strings.ToLower(cfg.Realm)
	}
	return ""
}

// callbackURL builds the absolute callback URL for this request.
// Traefik forwards X-Forwarded-Host / X-Forwarded-Proto so we read those.
func callbackURL(r *http.Request, cfg *config.Config) string {
	scheme := "https"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	// Callback is always served from the core API
	// Use a fixed host derived from baseDomain so the redirect_uri is consistent
	baseDomain := baseDomainFromConfig(cfg)
	if baseDomain != "" {
		host = "console." + baseDomain
	}
	_ = scheme
	_ = host
	// Return a predictable absolute URL registered in Keycloak
	if baseDomain != "" {
		return fmt.Sprintf("https://console.%s/api/internal/auth/callback", baseDomain)
	}
	return fmt.Sprintf("http://%s/api/internal/auth/callback", r.Host)
}

// originalRequestURL reconstructs the original URL from Traefik forwarded headers.
func originalRequestURL(r *http.Request) string {
	scheme := "https"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	uri := r.RequestURI
	if fwdURI := r.Header.Get("X-Forwarded-Uri"); fwdURI != "" {
		uri = fwdURI
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, uri)
}

// extractGroupsFromJWT는 JWT payload에서 groups claim을 추출합니다.
func extractGroupsFromJWT(token string) []string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		Groups []string `json:"groups"`
	}
	json.Unmarshal(payload, &claims)
	return claims.Groups
}

// cookieDomainAge is a small helper for tests — not currently used in production.
var _ = time.Second // suppress unused import
