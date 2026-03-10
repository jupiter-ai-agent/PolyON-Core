package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// RegisterPRC registers /api/v1/prc/* routes for PRC Dashboard.
func RegisterPRC(r chi.Router, d *Deps) {
	r.Route("/prc", func(r chi.Router) {
		r.Get("/providers", listProviders(d))
		r.Get("/providers/{type}", getProvider(d))
		r.Get("/claims", listClaims(d))
		r.Get("/saga-log", listSagaLog(d))
	})
}

// GET /api/v1/prc/providers
func listProviders(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providers, err := d.PRC.ListProviders(r.Context())
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(providers)
	}
}

// GET /api/v1/prc/providers/{type}
func getProvider(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerType := chi.URLParam(r, "type")
		resources, err := d.PRC.GetProviderResources(r.Context(), providerType)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		result := map[string]any{
			"type":      providerType,
			"status":    "healthy",
			"resources": resources,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// GET /api/v1/prc/claims?status=bound&type=auth&moduleId=odoo
func listClaims(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		items, err := d.PRC.ListClaims(r.Context(), q.Get("status"), q.Get("type"), q.Get("moduleId"))
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items": items,
			"total": len(items),
		})
	}
}

// GET /api/v1/prc/saga-log?moduleId=odoo&limit=50
func listSagaLog(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 100
		if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
			limit = l
		}
		items, err := d.PRC.ListSagaLog(r.Context(), q.Get("moduleId"), limit)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items": items,
			"total": len(items),
		})
	}
}
