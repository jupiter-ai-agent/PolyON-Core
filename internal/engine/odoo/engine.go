package odoo

import (
	"fmt"

	"github.com/triangles/polyon-core/internal/engine"
)

// OdooEngine implements engine.Engine for Odoo.
type OdooEngine struct {
	client *Client
}

// NewEngine creates an OdooEngine with the given client.
func NewEngine(client *Client) *OdooEngine {
	return &OdooEngine{client: client}
}

// Name returns the engine identifier.
func (e *OdooEngine) Name() string {
	return "odoo"
}

// Health checks Odoo reachability and returns a HealthStatus.
// It combines /web/health (TCP/HTTP layer) with common.version (RPC layer).
func (e *OdooEngine) Health() engine.HealthStatus {
	// Layer 1: HTTP health check
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

	// Layer 2: JSON-RPC version check
	info, err := e.client.Version()
	if err != nil {
		return engine.HealthStatus{
			Status:  "degraded",
			Message: fmt.Sprintf("rpc unavailable: %s", err.Error()),
		}
	}

	version := ""
	if v, ok := info["server_version"]; ok {
		version = fmt.Sprintf("%v", v)
	}

	return engine.HealthStatus{
		Status:  "healthy",
		Version: version,
	}
}
