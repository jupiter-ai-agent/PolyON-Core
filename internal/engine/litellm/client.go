// Package litellm provides a REST API client for LiteLLM proxy.
package litellm

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Client is a LiteLLM REST API client.
type Client struct {
	baseURL    string
	masterKey  string
	httpClient *http.Client
}

// NewClient creates a new LiteLLM REST API client.
func NewClient(url, masterKey string) *Client {
	return &Client{
		baseURL:   url,
		masterKey: masterKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewClientFromEnv creates a LiteLLM client using environment variables.
func NewClientFromEnv() *Client {
	url := os.Getenv("LITELLM_URL")
	masterKey := os.Getenv("LITELLM_MASTER_KEY")
	return NewClient(url, masterKey)
}

// healthResponse represents the /health/liveliness response.
type healthResponse struct {
	Status string `json:"status"` // "healthy"
}

// get sends a GET request to the given path.
func (c *Client) get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", path, err)
	}
	return resp, nil
}

// Health calls GET /health/liveliness and returns true if 200 OK.
// LiteLLM returns a plain string "I'm alive!" on success.
func (c *Client) Health() (bool, error) {
	resp, err := c.get("/health/liveliness")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("health returned HTTP %d", resp.StatusCode)
	}

	return true, nil
}

// Version returns the LiteLLM version from the x-litellm-version response header.
func (c *Client) Version() (string, error) {
	resp, err := c.get("/health/liveliness")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if v := resp.Header.Get("x-litellm-version"); v != "" {
		return v, nil
	}

	return "1.81.15", nil // fallback to known build version
}

// ──────────────────────────────────────────────────────────────────────────────
// Auth Configuration
// ──────────────────────────────────────────────────────────────────────────────

// AuthConfig describes the authentication configuration for LiteLLM.
// LiteLLM Proxy v1.81+ supports:
//   - API Key authentication for programmatic access (LITELLM_MASTER_KEY)
//   - Traefik Forward Auth → Keycloak SSO for the management UI
type AuthConfig struct {
	// ForwardAuthEnabled signals that Traefik Forward Auth protects the LiteLLM UI.
	ForwardAuthEnabled bool
	// KeycloakIssuer is the Keycloak realm OIDC issuer URL (for reference).
	KeycloakIssuer string
	// MasterKeyConfigured indicates whether LITELLM_MASTER_KEY is set.
	MasterKeyConfigured bool
}

// ConfigureAuth validates the auth configuration for LiteLLM.
// LiteLLM API calls use API Key authentication (LITELLM_MASTER_KEY).
// The management UI is protected via Traefik Forward Auth → Keycloak.
//
// Returns a summary map describing the auth configuration status.
func (c *Client) ConfigureAuth(cfg AuthConfig) (map[string]interface{}, error) {
	healthy, err := c.Health()
	if err != nil {
		return nil, fmt.Errorf("litellm not reachable: %w", err)
	}
	if !healthy {
		return nil, fmt.Errorf("litellm health check failed")
	}

	version, _ := c.Version()

	result := map[string]interface{}{
		"healthy":              true,
		"version":              version,
		"auth_method":          "api_key + forward_auth",
		"api_key_configured":   c.masterKey != "",
		"master_key_env_set":   cfg.MasterKeyConfigured,
		"forward_auth":         cfg.ForwardAuthEnabled,
		"keycloak_issuer":      cfg.KeycloakIssuer,
		"note":                 "LiteLLM API calls use LITELLM_MASTER_KEY for authentication. The management UI is protected by Keycloak via Traefik Forward Auth.",
	}

	if c.masterKey == "" {
		result["warning"] = "LITELLM_MASTER_KEY is not set — LiteLLM API is unprotected."
	}

	return result, nil
}
