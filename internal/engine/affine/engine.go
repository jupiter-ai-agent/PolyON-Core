package affine

import (
	"fmt"

	"github.com/triangles/polyon-core/internal/engine"
)

// AffineEngine implements engine.Engine for AFFiNE.
type AffineEngine struct {
	client *Client
}

// NewEngine creates an AffineEngine with the given client.
func NewEngine(client *Client) *AffineEngine {
	return &AffineEngine{client: client}
}

// Name returns the engine identifier.
func (e *AffineEngine) Name() string {
	return "affine"
}

// Health checks AFFiNE reachability and returns a HealthStatus.
func (e *AffineEngine) Health() engine.HealthStatus {
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
