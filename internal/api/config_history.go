package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterConfigHistory registers /api/v1/config/* routes.
func RegisterConfigHistory(r chi.Router, d *Deps) {
	r.Route("/config", func(r chi.Router) {
		r.Get("/history", getConfigHistory(d))
		r.Get("/history/{sha}/diff", getConfigDiff(d))
		r.Post("/track", postConfigTrack(d))
	})
}

// GET /api/v1/config/history
// Returns recent config change commits from the polyon-config repo.
func getConfigHistory(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.ConfigTracker == nil {
			httputil.RespondError(w, 503, "CONFIG_TRACK_UNAVAILABLE", "ConfigTracker가 초기화되지 않았습니다")
			return
		}

		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}

		commits, err := d.ConfigTracker.GetHistory(limit)
		if err != nil {
			log.Warn().Err(err).Msg("getConfigHistory: failed")
			httputil.RespondError(w, 500, "HISTORY_ERROR", "설정 이력 조회 실패: "+err.Error())
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"commits": commits,
			"count":   len(commits),
		})
	}
}

// GET /api/v1/config/history/{sha}/diff
// Returns the unified diff for a specific commit.
func getConfigDiff(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.ConfigTracker == nil {
			httputil.RespondError(w, 503, "CONFIG_TRACK_UNAVAILABLE", "ConfigTracker가 초기화되지 않았습니다")
			return
		}

		sha := chi.URLParam(r, "sha")
		if sha == "" {
			httputil.RespondError(w, 400, "MISSING_SHA", "SHA가 필요합니다")
			return
		}

		diff, err := d.ConfigTracker.GetFileDiff(sha)
		if err != nil {
			log.Warn().Err(err).Str("sha", sha).Msg("getConfigDiff: failed")
			httputil.RespondError(w, 500, "DIFF_ERROR", "diff 조회 실패: "+err.Error())
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"sha":     sha,
			"diff":    diff,
		})
	}
}

// POST /api/v1/config/track
// Manually snapshot a single config file into the polyon-config repo.
// Body: {"path": "settings/custom.txt", "content": "...", "message": "optional msg"}
func postConfigTrack(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.ConfigTracker == nil {
			httputil.RespondError(w, 503, "CONFIG_TRACK_UNAVAILABLE", "ConfigTracker가 초기화되지 않았습니다")
			return
		}

		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "INVALID_JSON", "요청 바디 파싱 실패: "+err.Error())
			return
		}
		if req.Path == "" {
			httputil.RespondError(w, 400, "MISSING_PATH", "path가 필요합니다")
			return
		}

		msg := req.Message
		if msg == "" {
			msg = "Manual config snapshot"
		}

		if err := d.ConfigTracker.CommitFile(req.Path, req.Content, msg, ""); err != nil {
			log.Warn().Err(err).Str("path", req.Path).Msg("postConfigTrack: commit failed")
			httputil.RespondError(w, 500, "TRACK_ERROR", "설정 커밋 실패: "+err.Error())
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"path":    req.Path,
			"message": msg,
		})
	}
}
