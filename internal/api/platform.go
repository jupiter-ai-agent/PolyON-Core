package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ServiceStatus represents the status of a platform service
type ServiceStatus string

const (
	StatusRunning      ServiceStatus = "running"
	StatusNotInstalled ServiceStatus = "not_installed"
	StatusStopped      ServiceStatus = "stopped"
	StatusError        ServiceStatus = "error"
)

// PlatformService represents a service in the PolyON platform
type PlatformService struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Category string        `json:"category"`
	Status   ServiceStatus `json:"status"`
}

// PlatformServicesResponse represents the response for /api/v1/platform/services
type PlatformServicesResponse struct {
	Services []PlatformService `json:"services"`
}

// RegisterPlatform registers platform-related API routes
func RegisterPlatform(r chi.Router, d *Deps) {
	r.Route("/platform", func(r chi.Router) {
		r.Get("/services", listPlatformServices(d))
	})
}

// listPlatformServices returns the list of available services in the platform
// Currently returns a static list, future versions will query K8s API
func listPlatformServices(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Static service list for Phase 2 (K8s transition)
		// TODO: Replace with K8s API calls when kubectl/client-go integration is ready
		services := []PlatformService{
			// Base services (always installed in K8s)
			{ID: "directory", Name: "Directory", Category: "base", Status: StatusRunning},
			{ID: "mail", Name: "Mail", Category: "base", Status: StatusRunning},
			{ID: "auth", Name: "Keycloak", Category: "base", Status: StatusRunning},
			{ID: "database", Name: "Database", Category: "base", Status: StatusRunning},
			{ID: "search", Name: "OpenSearch", Category: "base", Status: StatusRunning},
			{ID: "storage", Name: "RustFS", Category: "base", Status: StatusRunning},
			{ID: "cache", Name: "Redis", Category: "base", Status: StatusRunning},
			{ID: "monitoring", Name: "Monitoring", Category: "base", Status: StatusRunning},

			// Application services (conditionally installed)
			{ID: "chat", Name: "Mattermost", Category: "app", Status: StatusNotInstalled},
			{ID: "ai", Name: "AI Platform", Category: "app", Status: StatusNotInstalled},
			{ID: "automation", Name: "Automation", Category: "app", Status: StatusNotInstalled},
			{ID: "bpmn", Name: "BPMN", Category: "app", Status: StatusNotInstalled},
			{ID: "wiki", Name: "Wiki", Category: "app", Status: StatusNotInstalled},
			{ID: "erp", Name: "ERP", Category: "app", Status: StatusNotInstalled},
			{ID: "git", Name: "Gitea", Category: "app", Status: StatusNotInstalled},
		}

		response := PlatformServicesResponse{
			Services: services,
		}

		httputil.RespondOK(w, response)
	}
}