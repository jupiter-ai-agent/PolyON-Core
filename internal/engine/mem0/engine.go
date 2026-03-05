package mem0

import (
	"fmt"

	"github.com/triangles/polyon-core/internal/engine"
)

// Mem0Engine implements engine.Engine for the PolyON Memory (Mem0) server.
type Mem0Engine struct {
	client *Client
}

// NewEngine creates a Mem0Engine with the given client.
func NewEngine(client *Client) *Mem0Engine {
	return &Mem0Engine{client: client}
}

// Name returns the engine identifier.
func (e *Mem0Engine) Name() string {
	return "mem0"
}

// Health checks Mem0 reachability and returns a HealthStatus.
func (e *Mem0Engine) Health() engine.HealthStatus {
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
