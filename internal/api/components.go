package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterComponents registers the System Manifest API routes.
func RegisterComponents(r chi.Router, d *Deps) {
	r.Route("/system/components", func(r chi.Router) {
		r.Get("/", listComponents(d))
		r.Get("/topology", componentTopology(d))
		r.Get("/{id}", getComponent(d))
		r.Put("/{id}", updateComponent(d))
		r.Patch("/{id}/status", patchComponentStatus(d))
	})
}

// listComponents returns all components, optionally filtered by ?category= and ?status=.
func listComponents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		category := r.URL.Query().Get("category")
		status := r.URL.Query().Get("status")

		list, err := d.Store.ListComponents(r.Context(), category, status)
		if err != nil {
			log.Error().Err(err).Msg("listComponents: DB query failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "컴포넌트 목록 조회 실패")
			return
		}

		// Group by category for convenience
		grouped := make(map[string]any)
		categories := []string{}
		catMap := make(map[string][]any)

		for _, c := range list {
			if _, ok := catMap[c.Category]; !ok {
				categories = append(categories, c.Category)
			}
			catMap[c.Category] = append(catMap[c.Category], c)
		}

		for _, cat := range categories {
			grouped[cat] = catMap[cat]
		}

		httputil.RespondOK(w, map[string]any{
			"total":      len(list),
			"categories": categories,
			"grouped":    grouped,
			"components": list,
		})
	}
}

// getComponent returns a single component by ID.
func getComponent(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")
		c, err := d.Store.GetComponent(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "NOT_FOUND", "컴포넌트를 찾을 수 없습니다: "+id)
			return
		}
		httputil.RespondOK(w, c)
	}
}

// updateComponent updates mutable fields of a component.
func updateComponent(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_BODY", "잘못된 요청 본문입니다")
			return
		}

		if err := d.Store.UpdateComponent(r.Context(), id, updates); err != nil {
			log.Error().Err(err).Str("id", id).Msg("updateComponent: DB update failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "컴포넌트 업데이트 실패")
			return
		}

		c, err := d.Store.GetComponent(r.Context(), id)
		if err != nil {
			httputil.RespondOK(w, map[string]string{"status": "updated"})
			return
		}
		httputil.RespondOK(w, c)
	}
}

// patchComponentStatus updates only the status of a component.
func patchComponentStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_BODY", "잘못된 요청 본문입니다")
			return
		}

		valid := map[string]bool{"planned": true, "deployed": true, "active": true, "disabled": true}
		if !valid[body.Status] {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_STATUS", "유효한 상태: planned, deployed, active, disabled")
			return
		}

		if err := d.Store.UpdateComponentStatus(r.Context(), id, body.Status); err != nil {
			log.Error().Err(err).Str("id", id).Msg("patchComponentStatus failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "상태 업데이트 실패")
			return
		}

		log.Info().Str("id", id).Str("status", body.Status).Msg("component status updated")
		httputil.RespondOK(w, map[string]string{"id": id, "status": body.Status})
	}
}

// componentTopology returns the dependency graph for visualization.
func componentTopology(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		nodes, err := d.Store.ComponentTopology(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("componentTopology: DB query failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "토폴로지 조회 실패")
			return
		}
		httputil.RespondOK(w, map[string]any{"nodes": nodes})
	}
}
