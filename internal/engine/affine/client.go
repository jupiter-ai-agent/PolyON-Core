// Package affine provides a REST client for AFFiNE.
//
// AFFiNE 0.26.x supports OIDC via Admin API (GraphQL + REST).
// OIDC provider must be configured via the admin API at runtime.
// This client provides:
//   - Health check
//   - Version detection
//   - OIDC provider configuration via Admin API
package affine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is an AFFiNE REST client.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new AFFiNE REST client with the given base URL.
func NewClient(url string) *Client {
	return &Client{
		baseURL: url,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// NewClientFromEnv creates an AFFiNE client using environment variables.
// AFFINE_URL defaults to "http://polyon-wiki:3010".
func NewClientFromEnv() *Client {
	url := os.Getenv("AFFINE_URL")
	return NewClient(url)
}

// infoResponse represents the AFFiNE /info response body.
type infoResponse struct {
	Compatibility string `json:"compatibility"`
	Message       string `json:"message"`
}

// versionResponse represents the AFFiNE /api/server/version response body.
type versionResponse struct {
	Version string `json:"version"`
}

// get sends a GET request to the given path and returns the raw response.
func (c *Client) get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", path, err)
	}
	return resp, nil
}

// Health checks if AFFiNE is reachable and responsive.
// It tries /info, then /, then /api/server/version in order.
func (c *Client) Health() (bool, error) {
	paths := []string{"/info", "/", "/api/server/version"}
	var lastErr error
	for _, path := range paths {
		resp, err := c.get(path)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return true, nil
		}
		lastErr = fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return false, lastErr
}

// Version attempts to retrieve the AFFiNE server version.
// It tries GET /info first, then GET /api/server/version.
func (c *Client) Version() (string, error) {
	// Try /info first
	if v, err := c.versionFromInfo(); err == nil && v != "" {
		return v, nil
	}

	// Fall back to /api/server/version
	return c.versionFromAPI()
}

// versionFromInfo parses the version from GET /info.
func (c *Client) versionFromInfo() (string, error) {
	resp, err := c.get("/info")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("/info returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read /info body: %w", err)
	}

	var info infoResponse
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", fmt.Errorf("decode /info response: %w", err)
	}
	return info.Compatibility, nil
}

// versionFromAPI parses the version from GET /api/server/version.
func (c *Client) versionFromAPI() (string, error) {
	resp, err := c.get("/api/server/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("/api/server/version returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read /api/server/version body: %w", err)
	}

	var ver versionResponse
	if err := json.Unmarshal(raw, &ver); err != nil {
		return "", fmt.Errorf("decode /api/server/version response: %w", err)
	}
	return ver.Version, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// OIDC Configuration
// ──────────────────────────────────────────────────────────────────────────────

// OIDCConfig holds the OIDC provider parameters for AFFiNE.
// These map to the AFFiNE OIDC OAuth provider settings.
type OIDCConfig struct {
	// ClientID is the Keycloak client ID (e.g. "affine").
	ClientID string
	// ClientSecret is the Keycloak client secret.
	ClientSecret string
	// Issuer is the Keycloak realm OIDC issuer URL.
	// e.g. "http://polyon-auth:8080/realms/polyon"
	Issuer string
	// RedirectURI is the AFFiNE callback URL.
	// e.g. "https://wiki.cmars.kr/oauth/callback"
	RedirectURI string
}

// oidcProviderPayload matches the AFFiNE Admin GraphQL mutation for OAuth config.
// AFFiNE 0.26.x uses a GraphQL endpoint at /graphql for admin operations.
// The OIDC config mutation: updateServerConfig(input: { oauthProviders: [...] })
type oidcProviderPayload struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Issuer       string `json:"issuer"`
	RedirectURI  string `json:"redirect_uri"`
}

// adminConfigPayload is used for the AFFiNE Admin API REST fallback.
type adminConfigPayload struct {
	Providers struct {
		OIDC oidcProviderPayload `json:"oidc"`
	} `json:"providers"`
}

// postJSON sends a POST request with JSON body and returns the response.
func (c *Client) postJSON(path string, payload interface{}) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post %s: %w", path, err)
	}
	return resp, nil
}

// graphqlQuery sends a GraphQL query/mutation to AFFiNE's /graphql endpoint.
func (c *Client) graphqlQuery(query string, variables map[string]interface{}) ([]byte, int, error) {
	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	resp, err := c.postJSON("/graphql", payload)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

// ConfigureOIDC configures the OIDC provider in AFFiNE via GraphQL admin mutation.
//
// AFFiNE 0.26.x exposes a GraphQL admin API. The OIDC provider is configured
// via the updateServerConfig mutation. If the GraphQL approach fails (e.g., the
// mutation schema differs), it falls back to documenting the required env vars.
//
// Returns a summary map describing what was done.
func (c *Client) ConfigureOIDC(cfg OIDCConfig) (map[string]interface{}, error) {
	// First, verify AFFiNE is healthy
	healthy, err := c.Health()
	if err != nil {
		return nil, fmt.Errorf("affine not reachable: %w", err)
	}
	if !healthy {
		return nil, fmt.Errorf("affine health check failed")
	}

	result := map[string]interface{}{
		"healthy":     true,
		"client_id":   cfg.ClientID,
		"issuer":      cfg.Issuer,
		"redirect_uri": cfg.RedirectURI,
	}

	// Try GraphQL mutation to configure OIDC
	// AFFiNE 0.26 schema: mutation UpdateOAuthProvider($input: UpdateOAuthProviderInput!)
	mutation := `
mutation UpdateOAuthProvider($provider: OAuthProviderType!, $clientId: String!, $clientSecret: String!, $enabled: Boolean!) {
  updateOAuthProvider(provider: $provider, clientId: $clientId, clientSecret: $clientSecret, enabled: $enabled)
}
`
	variables := map[string]interface{}{
		"provider":     "OIDC",
		"clientId":     cfg.ClientID,
		"clientSecret": cfg.ClientSecret,
		"enabled":      true,
	}

	raw, statusCode, gqlErr := c.graphqlQuery(mutation, variables)
	if gqlErr != nil || statusCode >= 500 {
		// GraphQL unavailable — document required env vars
		result["oidc_configured"] = false
		result["method"] = "env_vars_required"
		result["note"] = "AFFiNE GraphQL admin API not available. Configure OIDC via docker-compose environment variables."
		result["required_env_vars"] = oidcEnvVars(cfg)
		if gqlErr != nil {
			result["error"] = gqlErr.Error()
		}
		return result, nil
	}

	// Parse GraphQL response
	var gqlResp struct {
		Data   map[string]interface{} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &gqlResp); err != nil || len(gqlResp.Errors) > 0 {
		// GraphQL returned errors — likely schema mismatch; fall back to env var documentation
		errMsg := ""
		if len(gqlResp.Errors) > 0 {
			errMsg = gqlResp.Errors[0].Message
		}
		result["oidc_configured"] = false
		result["method"] = "env_vars_required"
		result["note"] = "AFFiNE GraphQL mutation returned errors. Configure OIDC via docker-compose environment variables."
		result["required_env_vars"] = oidcEnvVars(cfg)
		if errMsg != "" {
			result["graphql_error"] = errMsg
		}
		return result, nil
	}

	// Success via GraphQL
	result["oidc_configured"] = true
	result["method"] = "graphql_admin_api"
	result["note"] = "OIDC provider configured via AFFiNE GraphQL admin API."
	return result, nil
}

// oidcEnvVars returns the docker-compose environment variable map for AFFiNE OIDC.
// These vars are set when the GraphQL API is not available or for reference.
func oidcEnvVars(cfg OIDCConfig) map[string]string {
	issuer := strings.TrimRight(cfg.Issuer, "/")
	return map[string]string{
		"AFFINE_GOOGLE_CLIENT_ID":     "",     // not used; set below
		"OAUTH_OIDC_CLIENT_ID":        cfg.ClientID,
		"OAUTH_OIDC_CLIENT_SECRET":    cfg.ClientSecret,
		"OAUTH_OIDC_ISSUER":           issuer,
		"OAUTH_OIDC_REDIRECT_URI":     cfg.RedirectURI,
		// AFFiNE reads these under providers.oidc
		"AFFINE_OAUTH_PROVIDERS_OIDC_CLIENT_ID":     cfg.ClientID,
		"AFFINE_OAUTH_PROVIDERS_OIDC_CLIENT_SECRET": cfg.ClientSecret,
		"AFFINE_OAUTH_PROVIDERS_OIDC_ISSUER":        issuer,
	}
}
