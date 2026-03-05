package openclaw

import (
	"fmt"

	"github.com/triangles/polyon-core/internal/engine"
)

// OpenClawEngine implements engine.Engine for the PolyON Agent (OpenClaw gateway).
type OpenClawEngine struct {
	client *Client
}

// NewEngine creates an OpenClawEngine with the given client.
func NewEngine(client *Client) *OpenClawEngine {
	return &OpenClawEngine{client: client}
}

// Name returns the engine identifier.
func (e *OpenClawEngine) Name() string {
	return "openclaw"
}

// Health checks OpenClaw gateway reachability and returns a HealthStatus.
func (e *OpenClawEngine) Health() engine.HealthStatus {
	ok, err := e.client.Health()
	if err != nil || !ok {
		msg := "health endpoint unreachable"
		if err != nil {
			msg = err.Error()
		}
		return engine.HealthStatus{
			Status:  "down",
			Message: msg,
		}
	}

	version, err := e.client.Version()
	if err != nil {
		return engine.HealthStatus{
			Status:  "degraded",
			Message: fmt.Sprintf("version unavailable: %s", err.Error()),
		}
	}

	return engine.HealthStatus{
		Status:  "healthy",
		Version: version,
	}
}
