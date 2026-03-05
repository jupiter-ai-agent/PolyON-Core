package mattermost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ProvisionConfig holds all parameters needed to configure Mattermost LDAP and OIDC.
type ProvisionConfig struct {
	BaseDN            string
	BindDN            string
	BindPassword      string
	LdapServer        string
	OIDCClientID      string
	OIDCSecret        string
	AuthEndpoint      string // external Keycloak URL (public)
	TokenEndpoint     string // internal Keycloak URL
	UserInfoEndpoint  string
	DiscoveryEndpoint string
}

// patchConfig merges partial config into the existing config via GET+PUT /api/v4/config.
// Mattermost v11 removed PATCH /api/v4/config/patch, so we do read-modify-write.
func (c *Client) patchConfig(partial map[string]interface{}) error {
	if c.token == "" {
		return fmt.Errorf("mattermost: admin token required but not set")
	}

	// 1. GET current config
	current, err := c.GetConfig()
	if err != nil {
		return fmt.Errorf("get current config: %w", err)
	}

	// 2. Deep merge partial into current
	for section, val := range partial {
		sectionMap, ok := val.(map[string]interface{})
		if !ok {
			current[section] = val
			continue
		}
		existing, _ := current[section].(map[string]interface{})
		if existing == nil {
			existing = map[string]interface{}{}
		}
		for k, v := range sectionMap {
			existing[k] = v
		}
		current[section] = existing
	}

	// 3. PUT merged config
	body, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("marshal merged config: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, c.baseURL+"/api/v4/config", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build put request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http put config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put config returned HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// post sends an authenticated POST request.
func (c *Client) post(path string, payload interface{}) error {
	if c.token == "" {
		return fmt.Errorf("mattermost: admin token required but not set")
	}

	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post %s returned HTTP %d: %s", path, resp.StatusCode, string(raw))
	}
	return nil
}

// GetConfig returns the current Mattermost configuration as a raw map.
// Requires admin token.
func (c *Client) GetConfig() (map[string]interface{}, error) {
	resp, err := c.get("/api/v4/config")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get config returned HTTP %d: %s", resp.StatusCode, string(raw))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read config body: %w", err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

// ConfigureLDAP patches Mattermost LdapSettings via PATCH /api/v4/config/patch.
// Requires admin token.
func (c *Client) ConfigureLDAP(cfg ProvisionConfig) error {
	ldapServer := cfg.LdapServer
	if ldapServer == "" {
		ldapServer = "polyon-dc"
	}

	payload := map[string]interface{}{
		"LdapSettings": map[string]interface{}{
			"Enable":                    true,
			"LdapServer":                ldapServer,
			"LdapPort":                  389,
			"ConnectionSecurity":        "",
			"BaseDN":                    cfg.BaseDN,
			"BindUsername":              cfg.BindDN,
			"BindPassword":              cfg.BindPassword,
			"UserFilter":                "(&(objectClass=user)(!(objectClass=computer))(!(userAccountControl:1.2.840.113556.1.4.803:=2)))",
			"GroupFilter":               "(&(objectClass=group))",
			"EmailAttribute":            "mail",
			"UsernameAttribute":         "sAMAccountName",
			"IdAttribute":               "objectGUID",
			"LoginIdAttribute":          "sAMAccountName",
			"FirstNameAttribute":        "givenName",
			"LastNameAttribute":         "sn",
			"NicknameAttribute":         "displayName",
			"PositionAttribute":         "title",
			"GroupIdAttribute":          "objectGUID",
			"GroupDisplayNameAttribute": "cn",
			"SyncIntervalMinutes":       60,
			"MaxPageSize":               1500,
		},
	}

	return c.patchConfig(payload)
}

// ConfigureOIDC patches Mattermost GitLabSettings (generic OIDC) via PATCH /api/v4/config/patch.
// Mattermost uses "GitLabSettings" as its generic OIDC connector — unrelated to actual GitLab.
// Requires admin token.
func (c *Client) ConfigureOIDC(cfg ProvisionConfig) error {
	clientID := cfg.OIDCClientID
	if clientID == "" {
		clientID = "mattermost"
	}

	payload := map[string]interface{}{
		"GitLabSettings": map[string]interface{}{
			"Enable":            true,
			"Secret":            cfg.OIDCSecret,
			"Id":                clientID,
			"Scope":             "openid profile email",
			"AuthEndpoint":      cfg.AuthEndpoint,
			"TokenEndpoint":     cfg.TokenEndpoint,
			"UserApiEndpoint":   cfg.UserInfoEndpoint,
			"DiscoveryEndpoint": cfg.DiscoveryEndpoint,
		},
	}

	return c.patchConfig(payload)
}

// SyncLDAP triggers an immediate LDAP synchronization via POST /api/v4/ldap/sync.
// Requires admin token.
func (c *Client) SyncLDAP() error {
	return c.post("/api/v4/ldap/sync", nil)
}

// ──────────────────────────────────────────────────────────────────────────────
// Team & Channel provisioning
// ──────────────────────────────────────────────────────────────────────────────

// postAndDecode sends a POST and decodes the JSON response into result.
func (c *Client) postAndDecode(path string, payload interface{}, result interface{}) error {
	if c.token == "" {
		return fmt.Errorf("mattermost: admin token required but not set")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("post %s returned HTTP %d: %s", path, resp.StatusCode, string(raw))
	}
	if result != nil {
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// getAndDecode sends a GET and decodes the JSON response.
func (c *Client) getAndDecode(path string, result interface{}) error {
	resp, err := c.get(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("get %s returned HTTP %d: %s", path, resp.StatusCode, string(raw))
	}
	return json.Unmarshal(raw, result)
}

// TeamNameFromDisplay converts a display name to a valid Mattermost team name.
// Mattermost team names must be lowercase alphanumeric + hyphen, 2-64 chars.
// For non-ASCII names (e.g. Korean), generates a hex-based slug to avoid collisions.
func TeamNameFromDisplay(displayName string) string {
	name := strings.ToLower(displayName)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	// If no ASCII chars were extracted (e.g. pure Korean name), use hex encoding
	if len(result) < 2 {
		var hex strings.Builder
		hex.WriteString("team-")
		for _, r := range displayName {
			fmt.Fprintf(&hex, "%04x", r)
			if hex.Len() > 60 {
				break
			}
		}
		result = hex.String()
	}
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

// teamNameFromDisplay is the internal alias for TeamNameFromDisplay.
func teamNameFromDisplay(displayName string) string {
	return TeamNameFromDisplay(displayName)
}

// channelNameFromDisplay converts a display name to a valid Mattermost channel name.
func channelNameFromDisplay(displayName string) string {
	// Same rules as team name
	return TeamNameFromDisplay(displayName)
}

// ListTeams returns all teams.
// GET /api/v4/teams
func (c *Client) ListTeams() ([]map[string]interface{}, error) {
	var teams []map[string]interface{}
	if err := c.getAndDecode("/api/v4/teams?per_page=200", &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// CreateTeam creates a Mattermost team and returns the team ID.
// POST /api/v4/teams
func (c *Client) CreateTeam(name, displayName string) (string, error) {
	if name == "" {
		name = teamNameFromDisplay(displayName)
	}
	payload := map[string]interface{}{
		"name":         name,
		"display_name": displayName,
		"type":         "I", // Invite-only
	}
	var result map[string]interface{}
	if err := c.postAndDecode("/api/v4/teams", payload, &result); err != nil {
		return "", err
	}
	id, _ := result["id"].(string)
	return id, nil
}

// CreateChannel creates a channel in a team and returns the channel ID.
// POST /api/v4/channels
// channelType: "O" (public), "P" (private)
func (c *Client) CreateChannel(teamID, name, displayName, channelType string) (string, error) {
	if name == "" {
		name = channelNameFromDisplay(displayName)
	}
	if channelType == "" {
		channelType = "O"
	}
	payload := map[string]interface{}{
		"team_id":      teamID,
		"name":         name,
		"display_name": displayName,
		"type":         channelType,
	}
	var result map[string]interface{}
	if err := c.postAndDecode("/api/v4/channels", payload, &result); err != nil {
		return "", err
	}
	id, _ := result["id"].(string)
	return id, nil
}

// AddUserToTeam adds a user to a team.
// POST /api/v4/teams/{team_id}/members
func (c *Client) AddUserToTeam(teamID, userID string) error {
	payload := map[string]string{
		"team_id": teamID,
		"user_id": userID,
	}
	return c.postAndDecode(fmt.Sprintf("/api/v4/teams/%s/members", teamID), payload, nil)
}

// AddUserToChannel adds a user to a channel.
// POST /api/v4/channels/{channel_id}/members
func (c *Client) AddUserToChannel(channelID, userID string) error {
	payload := map[string]string{
		"user_id": userID,
	}
	return c.postAndDecode(fmt.Sprintf("/api/v4/channels/%s/members", channelID), payload, nil)
}

// GetUserByUsername looks up a user by login name.
// GET /api/v4/users/username/{username}
func (c *Client) GetUserByUsername(username string) (map[string]interface{}, error) {
	var user map[string]interface{}
	if err := c.getAndDecode(fmt.Sprintf("/api/v4/users/username/%s", username), &user); err != nil {
		return nil, err
	}
	return user, nil
}

// GetTeamByName looks up a team by name (URL slug).
// GET /api/v4/teams/name/{name}
func (c *Client) GetTeamByName(name string) (map[string]interface{}, error) {
	var team map[string]interface{}
	if err := c.getAndDecode(fmt.Sprintf("/api/v4/teams/name/%s", name), &team); err != nil {
		return nil, err
	}
	return team, nil
}
