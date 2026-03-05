package nextcloud

import (
	"github.com/triangles/polyon-core/internal/engine"
)

// Engine implements the PolyON engine interface for Nextcloud (HELIOS Drive).
type Engine struct {
	client *Client
}

func NewEngine(baseURL, adminUser, adminPass, container string) *Engine {
	return &Engine{
		client: NewClient(baseURL, adminUser, adminPass, container),
	}
}

func (e *Engine) Name() string { return "nextcloud" }

func (e *Engine) Health() engine.HealthStatus {
	s, err := e.client.Status()
	if err != nil {
		return engine.HealthStatus{Status: "down", Message: err.Error()}
	}
	if !s.Installed {
		return engine.HealthStatus{Status: "down", Version: s.VersionString, Message: "not installed"}
	}
	if s.Maintenance {
		return engine.HealthStatus{Status: "degraded", Version: s.VersionString, Message: "maintenance mode"}
	}
	return engine.HealthStatus{Status: "healthy", Version: s.VersionString}
}

// Client returns the underlying Nextcloud API client for direct access.
func (e *Engine) Client() *Client {
	return e.client
}
