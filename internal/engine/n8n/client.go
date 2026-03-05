// Package n8n provides a client for the n8n workflow automation engine.
//
// n8n Community Edition does not support LDAP or SAML natively (Enterprise feature).
// Authentication is handled externally via Traefik Forward Auth → Keycloak (which
// federates with AD/LDAP). This client provides:
//   - Health check (GET /healthz)
//   - Auth config verification (validates environment-based auth settings)
package n8n

import (
	"fmt"
	"net/http"
	"time"
)

// Client is an HTTP client for the n8n automation engine.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new n8n client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AuthConfig — environment-based auth settings
// ──────────────────────────────────────────────────────────────────────────────

// AuthConfig describes the authentication configuration for n8n.
// Since n8n CE does not support LDAP/SAML directly, authentication is
// delegated to Keycloak via Traefik Forward Auth.
// These values are provided for reference/documentation.
type AuthConfig struct {
	// ForwardAuthEnabled signals that Traefik Forward Auth is in front of n8n.
	ForwardAuthEnabled bool
	// KeycloakIssuer is the Keycloak realm OIDC issuer URL (for reference).
	KeycloakIssuer string
	// InternalURL is the n8n internal URL used for health checks.
	InternalURL string
}

// ──────────────────────────────────────────────────────────────────────────────
// Health
// ──────────────────────────────────────────────────────────────────────────────

// Health calls GET /healthz and returns true if n8n is up.
func (c *Client) Health() (bool, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/healthz")
	if err != nil {
		return false, fmt.Errorf("n8n health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("n8n health returned HTTP %d", resp.StatusCode)
	}
	return true, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Auth configuration (verification only)
// ──────────────────────────────────────────────────────────────────────────────

// ConfigureAuth validates the auth configuration for n8n.
// Since LDAP is not natively supported in CE, this confirms:
//  1. n8n is healthy
//  2. Forward Auth (Keycloak) is in place (ForwardAuthEnabled flag)
//
// Returns a summary map suitable for API response.
func (c *Client) ConfigureAuth(cfg AuthConfig) (map[string]interface{}, error) {
	healthy, err := c.Health()
	if err != nil {
		return nil, fmt.Errorf("n8n not reachable: %w", err)
	}
	if !healthy {
		return nil, fmt.Errorf("n8n health check failed")
	}

	result := map[string]interface{}{
		"healthy":             true,
		"auth_method":         "forward_auth",
		"forward_auth":        cfg.ForwardAuthEnabled,
		"keycloak_issuer":     cfg.KeycloakIssuer,
		"note":                "n8n CE does not support LDAP/SAML natively. Authentication is handled by Keycloak via Traefik Forward Auth.",
	}
	return result, nil
}
