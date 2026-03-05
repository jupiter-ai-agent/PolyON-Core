package onlyoffice

import (
	"github.com/triangles/polyon-core/internal/engine"
)

// OnlyOfficeEngine implements engine.Engine for OnlyOffice Document Server.
type OnlyOfficeEngine struct {
	client *Client
}

// NewEngine creates an OnlyOfficeEngine.
func NewEngine(client *Client) *OnlyOfficeEngine {
	return &OnlyOfficeEngine{client: client}
}

// Name returns the engine identifier.
func (e *OnlyOfficeEngine) Name() string {
	return "onlyoffice"
}

// Health checks OnlyOffice reachability.
func (e *OnlyOfficeEngine) Health() engine.HealthStatus {
	ok, err := e.client.Health()
	if err != nil || !ok {
		msg := "healthcheck failed"
		if err != nil {
			msg = err.Error()
		}
		return engine.HealthStatus{
			Status:  "down",
			Message: msg,
		}
	}
	return engine.HealthStatus{
		Status:  "healthy",
		Version: "8.x",
	}
}
