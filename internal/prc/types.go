// Package prc implements Platform Resource Claim — declarative resource
// provisioning for PolyON modules.
package prc

import (
	"context"
	"encoding/json"
)

// Credentials holds provisioning results as key-value pairs.
// Keys follow the PRC spec: url, endpoint, bucket, accessKey, secretKey, etc.
type Credentials map[string]string

// ResourceStatus represents the state of a provisioned resource.
type ResourceStatus string

const (
	StatusNotFound    ResourceStatus = "not_found"
	StatusProvisioned ResourceStatus = "provisioned"
	StatusDegraded    ResourceStatus = "degraded"
	StatusError       ResourceStatus = "error"
)

// Claim represents a single resource request from module.yaml spec.claims[].
type Claim struct {
	Type     string            `yaml:"type" json:"type"`
	Config   map[string]any    `yaml:"config" json:"config"`
	ModuleID string            `yaml:"-" json:"-"` // injected by engine
}

// ConfigString returns a string config value, or fallback if missing.
func (c Claim) ConfigString(key, fallback string) string {
	if v, ok := c.Config[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

// ConfigInt returns an int config value, or fallback if missing.
func (c Claim) ConfigInt(key string, fallback int) int {
	if v, ok := c.Config[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return int(i)
			}
		}
	}
	return fallback
}

// ResourceProvider is the interface that all Foundation providers implement.
type ResourceProvider interface {
	// Type returns the claim type identifier (e.g., "database", "objectStorage").
	Type() string

	// DependsOn returns claim types this provider depends on.
	DependsOn() []string

	// Provision creates resources and returns credentials.
	Provision(ctx context.Context, claim Claim) (Credentials, error)

	// Deprovision removes resources (saga compensation).
	Deprovision(ctx context.Context, claim Claim) error

	// Status checks the current state of provisioned resources (Phase 2).
	Status(ctx context.Context, claim Claim) (ResourceStatus, error)
}

// ProvisionResult holds the result of a single claim provisioning.
type ProvisionResult struct {
	ClaimType   string      `json:"claimType"`
	Credentials Credentials `json:"credentials"`
	Status      string      `json:"status"` // provisioned | failed
	Error       string      `json:"error,omitempty"`
	DurationMs  int64       `json:"durationMs"`
}
