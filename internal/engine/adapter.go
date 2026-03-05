// Package engine defines the Engine interface and shared types for external system adapters.
package engine

// HealthStatus represents the health state of an external engine.
type HealthStatus struct {
	Status  string `json:"status"`            // healthy, degraded, down
	Version string `json:"version,omitempty"`
	Message string `json:"message,omitempty"`
}

// Engine is the common interface that all external system adapters must implement.
type Engine interface {
	Name() string
	Health() HealthStatus
}
