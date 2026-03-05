// Package operaton provides a client for Operaton (Camunda 7 CE fork) REST API.
//
// Operaton does not expose an HTTP API to configure LDAP directly.
// LDAP integration is configured via Docker environment variables at startup.
// This client provides:
//   - Health check (GET /engine-rest/engine)
//   - LDAP verification (GET /engine-rest/user?maxResults=5 — only returns data when LDAP works)
//   - GenerateConfig() to produce the required Docker env-var map for documentation/reference
package operaton

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a REST client for the Operaton BPMN engine.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Operaton client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ProvisionConfig — LDAP settings (mirrors docker-compose env vars)
// ──────────────────────────────────────────────────────────────────────────────

// ProvisionConfig holds all LDAP parameters that must be set as Docker env vars
// for the Operaton container.
type ProvisionConfig struct {
	BaseDN          string // e.g. DC=cmars,DC=kr
	AdminDN         string // e.g. CN=Administrator,CN=Users,DC=cmars,DC=kr
	DCAdminPassword string
}

// GenerateConfig returns the Docker environment variable map that must be injected
// into the Operaton container to enable LDAP/AD authentication.
// These vars cannot be applied at runtime — the container must be restarted with them.
func (cfg ProvisionConfig) GenerateConfig() map[string]string {
	return map[string]string{
		"CAMUNDA_LDAP_ENABLED":                  "true",
		"CAMUNDA_LDAP_SERVER_URL":               "ldap://polyon-dc:389",
		"CAMUNDA_LDAP_BASE_DN":                  cfg.BaseDN,
		"CAMUNDA_LDAP_MANAGER_DN":               cfg.AdminDN,
		"CAMUNDA_LDAP_MANAGER_PASSWORD":         cfg.DCAdminPassword,
		"CAMUNDA_LDAP_USER_SEARCH_BASE":         "CN=Users",
		"CAMUNDA_LDAP_USER_SEARCH_FILTER":       "(objectClass=user)",
		"CAMUNDA_LDAP_USER_ID_ATTRIBUTE":        "sAMAccountName",
		"CAMUNDA_LDAP_USER_FIRST_NAME_ATTRIBUTE": "givenName",
		"CAMUNDA_LDAP_USER_LAST_NAME_ATTRIBUTE": "sn",
		"CAMUNDA_LDAP_USER_EMAIL_ATTRIBUTE":     "mail",
		"CAMUNDA_LDAP_GROUP_SEARCH_BASE":        "OU=Organizations",
		"CAMUNDA_LDAP_GROUP_SEARCH_FILTER":      "(objectClass=group)",
		"CAMUNDA_LDAP_GROUP_ID_ATTRIBUTE":       "cn",
		"CAMUNDA_LDAP_GROUP_NAME_ATTRIBUTE":     "name",
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Health
// ──────────────────────────────────────────────────────────────────────────────

// engineEntry is a single engine entry from GET /engine-rest/engine.
type engineEntry struct {
	Name string `json:"name"`
}

// Health calls GET /engine-rest/engine and returns true if Operaton is up.
func (c *Client) Health() (bool, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/engine-rest/engine")
	if err != nil {
		return false, fmt.Errorf("operaton health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("operaton health returned HTTP %d", resp.StatusCode)
	}

	// Verify it's a valid JSON array of engines
	var engines []engineEntry
	if err := json.NewDecoder(resp.Body).Decode(&engines); err != nil {
		return false, fmt.Errorf("operaton health decode: %w", err)
	}
	return len(engines) > 0, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// LDAP verification
// ──────────────────────────────────────────────────────────────────────────────

// VerifyLDAP calls GET /engine-rest/user?maxResults=5 and returns the users.
// If LDAP is correctly configured, this will return AD users.
// If LDAP is NOT configured, Operaton returns an empty list or 401.
func (c *Client) VerifyLDAP() ([]map[string]interface{}, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/engine-rest/user?maxResults=5")
	if err != nil {
		return nil, fmt.Errorf("operaton verify ldap: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("operaton verify ldap read: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("operaton returned 401 — LDAP may not be configured or service requires authentication")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("operaton verify ldap: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var users []map[string]interface{}
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("operaton verify ldap decode: %w", err)
	}
	return users, nil
}
