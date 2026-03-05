package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterInfraServices registers infra service management API routes.
func RegisterInfraServices(r chi.Router, d *Deps) {
	r.Route("/infra/services", func(r chi.Router) {
		r.Get("/", listInfraServices(d))
		r.Put("/{id}", updateInfraService(d))
	})
}

// listInfraServices returns all enabled infra services from the DB.
func listInfraServices(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		svcs, err := d.Store.ListInfraServices(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("listInfraServices: DB query failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "인프라 서비스 목록 조회 실패: "+err.Error())
			return
		}

		httputil.RespondOK(w, svcs)
	}
}

// updateInfraServiceRequest is the request body for PUT /api/v1/infra/services/{id}.
type updateInfraServiceRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// updateInfraService updates host/port for an infra service and triggers regeneration.
func updateInfraService(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")
		if id == "" {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_ID", "서비스 ID가 필요합니다")
			return
		}

		var req updateInfraServiceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_BODY", "잘못된 요청 본문입니다")
			return
		}

		if req.Host == "" {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_HOST", "호스트가 필요합니다")
			return
		}
		if req.Port <= 0 || req.Port > 65535 {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_PORT", "포트는 1~65535 사이여야 합니다")
			return
		}

		if err := d.Store.UpdateInfraService(r.Context(), id, req.Host, req.Port); err != nil {
			log.Error().Err(err).Str("id", id).Msg("updateInfraService: DB update failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "인프라 서비스 업데이트 실패: "+err.Error())
			return
		}

		log.Info().Str("id", id).Str("host", req.Host).Int("port", req.Port).Msg("infra service updated, triggering traefik regen")

		// Trigger Traefik regeneration in background
		go triggerTraefikRegenerate(d, context.Background())

		// Return the updated service
		svc, err := d.Store.GetInfraService(r.Context(), id)
		if err != nil {
			// Update succeeded but fetch failed — return success without body
			httputil.RespondOK(w, map[string]string{"status": "updated"})
			return
		}

		httputil.RespondOK(w, svc)
	}
}
