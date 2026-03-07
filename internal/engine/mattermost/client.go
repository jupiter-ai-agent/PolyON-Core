// Package mattermost provides a REST API client for Mattermost.
package mattermost

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Client is a Mattermost REST API client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new Mattermost REST API client.
func NewClient(url, token string) *Client {
	return &Client{
		baseURL: url,
		token:   token,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// NewClientFromEnv creates a Mattermost client using environment variables.
func NewClientFromEnv() *Client {
	url := os.Getenv("MATTERMOST_URL")
	token := os.Getenv("MATTERMOST_TOKEN")
	return NewClient(url, token)
}

// pingResponse represents the Mattermost /api/v4/system/ping response body.
type pingResponse struct {
	Status    string `json:"status"`
	VersionID string `json:"version_id"`
}

// User represents a Mattermost user.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Nickname    string `json:"nickname"`
	Position    string `json:"position"`
	AuthData    string `json:"auth_data"`
	AuthService string `json:"auth_service"`
	DeleteAt    int64  `json:"delete_at"`
}

// Team represents a Mattermost team.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// Channel represents a Mattermost channel.
type Channel struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"` // O=public, P=private
}

// get sends an authenticated GET request to the given path and returns the raw response.
func (c *Client) get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", path, err)
	}
	return resp, nil
}

// Health calls GET /api/v4/system/ping and returns true if status is "OK".
func (c *Client) Health() (bool, error) {
	resp, err := c.get("/api/v4/system/ping")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("ping returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read ping body: %w", err)
	}

	var p pingResponse
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, fmt.Errorf("decode ping response: %w", err)
	}

	if p.Status != "OK" {
		return false, fmt.Errorf("ping status: %s", p.Status)
	}
	return true, nil
}

// Version calls GET /api/v4/system/ping and returns the server version.
// It first checks the X-Version-Id response header; if absent it falls back
// to the version_id field in the response body.
func (c *Client) Version() (string, error) {
	resp, err := c.get("/api/v4/system/ping")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ping returned HTTP %d", resp.StatusCode)
	}

	// Prefer header
	if v := resp.Header.Get("X-Version-Id"); v != "" {
		// Drain body
		_, _ = io.Copy(io.Discard, resp.Body)
		return v, nil
	}

	// Fall back to body field
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read ping body: %w", err)
	}

	var p pingResponse
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("decode ping response: %w", err)
	}
	return p.VersionID, nil
}
