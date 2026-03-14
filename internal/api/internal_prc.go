package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/module"
	"github.com/triangles/polyon-core/internal/prc"
)

// RegisterInternalPRC registers internal (no-auth) PRC provisioning endpoints.
// Used for re-provisioning modules that were installed outside of the PRC flow.
func RegisterInternalPRC(r chi.Router, d *Deps) {
	// POST /api/internal/prc/provision/{id}
	r.Post("/provision/{id}", internalProvisionModule(d))
	// GET /api/internal/prc/status/{id}
	r.Get("/status/{id}", internalPRCStatus(d))
}

// internalProvisionModule runs PRC for a module that is already deployed
// but missing claim provisioning (e.g. installed via Wizard before PRC existed).
func internalProvisionModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			http.Error(w, "module id required", 400)
			return
		}
		if d.PRC == nil {
			http.Error(w, "PRC engine not available", 503)
			return
		}

		// catalog에서 manifest 로드
		catalogManifest := module.FindCatalogManifestByID(id)
		if catalogManifest == nil {
			http.Error(w, fmt.Sprintf("module %s not found in catalog", id), 404)
			return
		}

		claims := make([]prc.Claim, len(catalogManifest.Spec.Claims))
		for i, cs := range catalogManifest.Spec.Claims {
			claims[i] = prc.Claim{
				ModuleID: id,
				Type:     cs.Type,
				Config:   cs.Config,
			}
		}

		envTemplates := catalogManifest.Spec.Env
		if envTemplates == nil {
			envTemplates = map[string]string{}
		}

		resolved, err := d.PRC.Provision(r.Context(), id, claims, envTemplates)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"error":   err.Error(),
				"module":  id,
			})
			return
		}

		// 민감 정보 마스킹
		masked := make(map[string]string)
		for k, v := range resolved {
			if len(v) > 8 {
				masked[k] = v[:4] + "****"
			} else {
				masked[k] = "****"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success":  true,
			"module":   id,
			"resolved": masked,
			"count":    len(resolved),
		})
	}
}

// internalPRCStatus returns current claim status for a module.
func internalPRCStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if d.Store == nil {
			http.Error(w, "store not available", 503)
			return
		}

		claims, err := d.PRC.ListClaims(r.Context(), "", "", id)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"module": id,
			"claims": claims,
			"total":  len(claims),
		})
	}
}
