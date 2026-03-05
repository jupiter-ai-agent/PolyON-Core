// Package openclaw provides a REST API client for the OpenClaw AI Agent gateway.
package openclaw

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is an OpenClaw gateway REST API client.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new OpenClaw gateway REST API client.
func NewClient(url string) *Client {
	return &Client{
		baseURL: url,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// healthResponse represents the /health response from OpenClaw gateway.
type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version,omitempty"`
}

// get sends a GET request to the given path.
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

// Health calls GET /health and returns true if the OpenClaw gateway is running.
func (c *Client) Health() (bool, error) {
	resp, err := c.get("/health")
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

// Version returns the version string reported by OpenClaw gateway.
// OpenClaw does not expose a dedicated version endpoint; the version is
// parsed from the /health response if available, otherwise returns a
// static fallback matching the installed package version.
func (c *Client) Version() (string, error) {
	resp, err := c.get("/health")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	var h healthResponse
	if err := json.Unmarshal(body, &h); err == nil && h.Version != "" {
		return h.Version, nil
	}

	// Fallback: version is determined by npm install at image build time
	return "latest", nil
}
