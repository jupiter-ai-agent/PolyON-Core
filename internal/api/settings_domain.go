package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterSettingsDomain registers app-level domain management routes.
// Note: Global /settings/domain GET and PUT are handled by RegisterSettings in settings.go.
func RegisterSettingsDomain(r chi.Router, d *Deps) {
	r.Put("/apps/{appID}/domain", updateAppDomain(d))
}

// PUT /api/v1/apps/{appID}/domain
// Updates the subdomain and domain_status for a specific app, then regenerates Traefik config.
func updateAppDomain(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		appID := chi.URLParam(r, "appID")
		if appID == "" {
			httputil.RespondError(w, 400, "MISSING_APP_ID", "앱 ID가 필요합니다")
			return
		}

		var req struct {
			Subdomain string `json:"subdomain"`
			Status    string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "INVALID_JSON", "요청 바디 파싱 실패: "+err.Error())
			return
		}

		if err := d.Store.UpdateAppDomain(r.Context(), appID, req.Subdomain, req.Status); err != nil {
			log.Error().Err(err).Str("appID", appID).Msg("updateAppDomain: DB update failed")
			httputil.RespondError(w, 500, "DB_ERROR", "앱 도메인 업데이트 실패: "+err.Error())
			return
		}

		// Regenerate Traefik config from DB state
		go triggerTraefikRegenerate(d, r.Context())

		// ConfigTrack — commit app domain change (non-fatal)
		if d.ConfigTracker != nil {
			content := fmt.Sprintf("app: %s\nsubdomain: %s\nstatus: %s\n", appID, req.Subdomain, req.Status)
			if err := d.ConfigTracker.CommitFile(
				fmt.Sprintf("apps/%s/domain.txt", appID),
				content,
				fmt.Sprintf("앱 도메인 변경: %s → %s", appID, req.Subdomain),
				"",
			); err != nil {
				log.Warn().Err(err).Str("appID", appID).Msg("updateAppDomain: configtrack commit failed (non-fatal)")
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success":   true,
			"app_id":    appID,
			"subdomain": req.Subdomain,
			"status":    req.Status,
		})
	}
}
