// Package mem0 provides a REST API client for the Mem0 OSS memory server.
package mem0

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a Mem0 REST API client.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Mem0 REST API client.
func NewClient(url string) *Client {
	return &Client{
		baseURL: url,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// healthResponse represents the /health response from the Mem0 server.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// get sends a GET request to the given path and returns the response body.
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

// Health calls GET /health and returns true if the server is healthy.
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

	// Parse status field
	body, _ := io.ReadAll(resp.Body)
	var h healthResponse
	if err := json.Unmarshal(body, &h); err == nil && h.Status != "healthy" {
		return false, fmt.Errorf("mem0 status: %s", h.Status)
	}

	return true, nil
}

// Version returns the version string reported by the Mem0 server.
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
	if err := json.Unmarshal(body, &h); err != nil {
		return "unknown", nil
	}

	if h.Version != "" {
		return h.Version, nil
	}
	return "1.0.0", nil
}
