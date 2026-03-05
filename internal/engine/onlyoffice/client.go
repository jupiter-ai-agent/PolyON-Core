// Package onlyoffice provides a client for OnlyOffice Document Server.
//
// OnlyOffice Document Server is an API service — users do not access it directly.
// Nextcloud/AFFiNE use it via JWT-based requests. The authentication model is:
//   - JWT_ENABLED=true (already configured in polyon docker-compose)
//   - JWT_SECRET shared between OnlyOffice and the front-end app
//   - Users authenticate via the front-end app (Nextcloud/AFFiNE), not OnlyOffice
//
// Therefore, the correct auth approach is JWT_INTERNAL, not Forward Auth.
// This client provides health check and JWT configuration verification.
package onlyoffice

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to OnlyOffice Document Server.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates an OnlyOffice client.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Health returns true if /healthcheck responds "true".
func (c *Client) Health() (bool, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/healthcheck")
	if err != nil {
		return false, fmt.Errorf("healthcheck request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)) == "true", nil
}

// ──────────────────────────────────────────────────────────────────────────────
// JWT Configuration Verification
// ──────────────────────────────────────────────────────────────────────────────

// JWTConfig describes the JWT authentication configuration for OnlyOffice.
// These values are provided as environment variables to the OnlyOffice container.
type JWTConfig struct {
	// JWTEnabled mirrors the JWT_ENABLED env var.
	JWTEnabled bool
	// JWTSecret mirrors the JWT_SECRET env var (masked in output).
	JWTSecret string
	// JWTAlgorithm mirrors the JWT_ALGORITHM env var (default: HS256).
	JWTAlgorithm string
}

// VerifyJWT checks that OnlyOffice is healthy and reports the JWT configuration status.
// OnlyOffice Document Server does not expose an admin API to read JWT config at runtime.
// This function verifies health and documents the expected JWT setup.
//
// Returns a summary map describing the JWT configuration status.
func (c *Client) VerifyJWT(cfg JWTConfig) (map[string]interface{}, error) {
	healthy, err := c.Health()
	if err != nil {
		return nil, fmt.Errorf("onlyoffice not reachable: %w", err)
	}

	algorithm := cfg.JWTAlgorithm
	if algorithm == "" {
		algorithm = "HS256"
	}

	secretConfigured := cfg.JWTSecret != ""
	secretMasked := ""
	if secretConfigured {
		if len(cfg.JWTSecret) > 4 {
			secretMasked = cfg.JWTSecret[:4] + strings.Repeat("*", len(cfg.JWTSecret)-4)
		} else {
			secretMasked = "****"
		}
	}

	result := map[string]interface{}{
		"healthy":           healthy,
		"auth_method":       "jwt_internal",
		"jwt_enabled":       cfg.JWTEnabled,
		"jwt_algorithm":     algorithm,
		"jwt_secret_set":    secretConfigured,
		"jwt_secret_masked": secretMasked,
		"note":              "OnlyOffice Document Server uses JWT for internal API authentication. Users authenticate via Nextcloud/AFFiNE (front-end apps). Forward Auth is not applicable.",
	}

	if !healthy {
		result["health_error"] = "healthcheck returned non-true response"
	}

	if !cfg.JWTEnabled {
		result["warning"] = "JWT_ENABLED is false — document editing APIs are not protected. Set JWT_ENABLED=true and JWT_SECRET in docker-compose."
	}
	if !secretConfigured {
		result["warning"] = "JWT_SECRET is not set — document editing APIs are unprotected."
	}

	return result, nil
}
