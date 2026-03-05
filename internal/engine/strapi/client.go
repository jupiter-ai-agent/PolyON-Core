// Package strapi provides a client for the Strapi CMS admin API.
//
// Strapi v5 does not support LDAP natively. Authentication is handled externally:
//   - Traefik Forward Auth → Keycloak (SSO for web UI access)
//   - Strapi admin API token for backend operations
//
// This client provides:
//   - Health check (GET /admin/login — 200 means Strapi is up)
//   - SSO config verification (validates that admin API is reachable)
package strapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is an HTTP client for the Strapi CMS admin API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Strapi client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// SSOConfig — forward-auth based SSO configuration
// ──────────────────────────────────────────────────────────────────────────────

// SSOConfig describes the Strapi SSO / auth setup.
// Since Strapi v5 CE does not support LDAP or Keycloak natively without a plugin,
// authentication is handled via Traefik Forward Auth.
type SSOConfig struct {
	AdminEmail    string
	AdminPassword string
	// ForwardAuthEnabled signals that Traefik Forward Auth protects the Strapi UI.
	ForwardAuthEnabled bool
	// KeycloakIssuer is the Keycloak realm OIDC issuer (for reference).
	KeycloakIssuer string
}

// ──────────────────────────────────────────────────────────────────────────────
// Health
// ──────────────────────────────────────────────────────────────────────────────

// Health checks whether Strapi is reachable by calling GET /_health.
// Strapi v5 exposes this endpoint without authentication.
func (c *Client) Health() (bool, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/_health")
	if err != nil {
		return false, fmt.Errorf("strapi health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("strapi health returned HTTP %d", resp.StatusCode)
	}
	return true, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Admin login
// ──────────────────────────────────────────────────────────────────────────────

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Data *struct {
		Token string `json:"token"`
		User  struct {
			ID        int    `json:"id"`
			Email     string `json:"email"`
			Firstname string `json:"firstname"`
			Lastname  string `json:"lastname"`
		} `json:"user"`
	} `json:"data"`
	Error *struct {
		Status  int    `json:"status"`
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// Login authenticates with the Strapi admin API and returns the JWT token.
func (c *Client) Login(email, password string) (string, error) {
	body, err := json.Marshal(loginRequest{Email: email, Password: password})
	if err != nil {
		return "", fmt.Errorf("strapi login marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/admin/login", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("strapi login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = "localhost" // Strapi v5 host-based security filter

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("strapi login: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("strapi login read: %w", err)
	}

	var lr loginResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return "", fmt.Errorf("strapi login decode: %w", err)
	}

	if resp.StatusCode != http.StatusOK || lr.Data == nil {
		msg := "login failed"
		if lr.Error != nil {
			msg = lr.Error.Message
		}
		return "", fmt.Errorf("strapi login: %s (HTTP %d)", msg, resp.StatusCode)
	}
	return lr.Data.Token, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// SSO configuration verification
// ──────────────────────────────────────────────────────────────────────────────

// ConfigureSSO validates that Strapi is reachable and the admin API works.
// Returns a summary map suitable for API response.
func (c *Client) ConfigureSSO(cfg SSOConfig) (map[string]interface{}, error) {
	healthy, err := c.Health()
	if err != nil {
		return nil, fmt.Errorf("strapi not reachable: %w", err)
	}
	if !healthy {
		return nil, fmt.Errorf("strapi health check failed")
	}

	result := map[string]interface{}{
		"healthy":         true,
		"auth_method":     "forward_auth",
		"forward_auth":    cfg.ForwardAuthEnabled,
		"keycloak_issuer": cfg.KeycloakIssuer,
		"note":            "Strapi v5 CE does not support LDAP/Keycloak natively without a plugin. Authentication is handled by Keycloak via Traefik Forward Auth.",
	}

	// If admin credentials are provided, verify admin API login
	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		token, err := c.Login(cfg.AdminEmail, cfg.AdminPassword)
		if err != nil {
			result["admin_api"] = "login_failed"
			result["admin_api_error"] = err.Error()
		} else {
			result["admin_api"] = "ok"
			result["admin_token_preview"] = token[:min(len(token), 20)] + "..."
		}
	}

	return result, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
