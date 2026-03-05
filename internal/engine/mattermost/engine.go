package mattermost

import (
	"fmt"
	"strings"

	"github.com/triangles/polyon-core/internal/engine"
)

// cleanVersion extracts the semver portion from Mattermost's extended version string.
// e.g. "11.4.1.22133911175.a50f..." → "11.4.1"
func cleanVersion(v string) string {
	parts := strings.SplitN(v, ".", 4)
	if len(parts) >= 3 {
		// Verify first 3 parts look numeric-ish
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	return v
}

// MattermostEngine implements engine.Engine for Mattermost.
type MattermostEngine struct {
	client *Client
}

// NewEngine creates a MattermostEngine with the given client.
func NewEngine(client *Client) *MattermostEngine {
	return &MattermostEngine{client: client}
}

// Name returns the engine identifier.
func (e *MattermostEngine) Name() string {
	return "mattermost"
}

// Health checks Mattermost reachability and returns a HealthStatus.
// It combines /api/v4/system/ping (HTTP layer) with version extraction.
func (e *MattermostEngine) Health() engine.HealthStatus {
	// Layer 1: ping / status check
	ok, err := e.client.Health()
	if err != nil || !ok {
		msg := "ping endpoint unreachable"
		if err != nil {
			msg = err.Error()
		}
		return engine.HealthStatus{
			Status:  "down",
			Message: msg,
		}
	}

	// Layer 2: version extraction
	version, err := e.client.Version()
	if err != nil {
		return engine.HealthStatus{
			Status:  "degraded",
			Message: fmt.Sprintf("version unavailable: %s", err.Error()),
		}
	}

	return engine.HealthStatus{
		Status:  "healthy",
		Version: cleanVersion(version),
	}
}
