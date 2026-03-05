package litellm

import (
	"fmt"

	"github.com/triangles/polyon-core/internal/engine"
)

// LiteLLMEngine implements engine.Engine for LiteLLM proxy.
type LiteLLMEngine struct {
	client *Client
}

// NewEngine creates a LiteLLMEngine with the given client.
func NewEngine(client *Client) *LiteLLMEngine {
	return &LiteLLMEngine{client: client}
}

// Name returns the engine identifier.
func (e *LiteLLMEngine) Name() string {
	return "litellm"
}

// Health checks LiteLLM reachability and returns a HealthStatus.
func (e *LiteLLMEngine) Health() engine.HealthStatus {
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
