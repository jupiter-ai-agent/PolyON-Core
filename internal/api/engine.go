package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/engine"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterEngines registers engine-related routes.
func RegisterEngines(r chi.Router, d *Deps) {
	r.Get("/engines/status", getEnginesStatus(d))
}

func getEnginesStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := make(map[string]engine.HealthStatus)

		// 1. Collect engine health from registered adapters (HTTP-level checks)
		if d.Engines != nil {
			for k, v := range d.Engines.Health() {
				result[k] = v
			}
		}

		// 2. Enrich with Docker container status for ALL components
		if d.Docker != nil && d.Store != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			// Get all components from DB
			components, err := d.Store.ListComponents(ctx, "", "")
			if err == nil {
				// Get all Docker containers
				containers, cerr := d.Docker.ContainerList(ctx)
				if cerr == nil {
					// Build container name → state/status map
					cmap := make(map[string]struct{ State, Status string })
					for _, c := range containers {
						cmap[c.Name] = struct{ State, Status string }{c.State, c.Status}
					}

					for _, comp := range components {
						// Skip if already has engine-level health
						if _, exists := result[comp.ID]; exists {
							continue
						}
						cname := comp.ContainerName
						if cname == "" {
							continue
						}
						if info, ok := cmap[cname]; ok {
							hs := engine.HealthStatus{}
							state := strings.ToLower(info.State)
							if state == "running" {
								if strings.Contains(strings.ToLower(info.Status), "healthy") {
									hs.Status = "healthy"
								} else if strings.Contains(strings.ToLower(info.Status), "unhealthy") {
									hs.Status = "degraded"
								} else {
									hs.Status = "healthy" // running but no healthcheck
								}
							} else {
								hs.Status = "down"
								hs.Message = "container " + state
							}
							result[comp.ID] = hs
						} else {
							result[comp.ID] = engine.HealthStatus{
								Status:  "down",
								Message: "container not found",
							}
						}
					}
				}
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"engines": result,
		})
	}
}
