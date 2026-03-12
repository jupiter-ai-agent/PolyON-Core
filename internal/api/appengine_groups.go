package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineGroups registers AppEngine group sync routes.
func RegisterAppEngineGroups(r chi.Router, d *Deps) {
	// GET: Odoo [AD Group] 마커 그룹 목록 (사용자 수 포함)
	// /appengine/ad-groups: B안 표준 경로
	r.Get("/appengine/ad-groups", listADGroups(d))
	// 구 경로 alias (하위 호환)
	r.Get("/appengine/kc-groups", listADGroups(d))
	// POST: 전체 KC 그룹 재동기화 트리거 (Odoo internal 엔드포인트 호출)
	r.Post("/appengine/group-sync", triggerGroupSync(d))
}

// adGroup represents an Odoo group with the [AD Group] marker.
type adGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Comment   string `json:"comment"`
	UserCount int    `json:"user_count"`
}

// listADGroups returns all Odoo res.groups that have "[AD Group]" in their comment field.
func listADGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		domain := []interface{}{
			[]interface{}{"comment", "like", "[AD Group]"},
		}
		fields := []string{"id", "name", "comment", "user_ids"}

		records, err := d.OdooClient.SearchRead("res.groups", domain, fields, 0, 0)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("SearchRead failed: %v", err))
			return
		}

		groups := make([]adGroup, 0, len(records))
		for _, rec := range records {
			g := adGroup{}
			if v, ok := rec["id"]; ok {
				switch x := v.(type) {
				case float64:
					g.ID = int64(x)
				}
			}
			if v, ok := rec["name"].(string); ok {
				g.Name = v
			}
			if v, ok := rec["comment"].(string); ok {
				g.Comment = v
			}
			// user_ids 필드: list of user IDs (Odoo 19)
			if v, ok := rec["user_ids"]; ok {
				switch x := v.(type) {
				case []interface{}:
					g.UserCount = len(x)
				}
			}
			groups = append(groups, g)
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"groups":  groups,
			"count":   len(groups),
		})
	}
}

// triggerGroupSync calls the Odoo internal group-sync endpoint.
// polyon_oidc iterates all users, fetches their KC groups via KC Admin API,
// and re-syncs [AD Group] memberships in Odoo.
func triggerGroupSync(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		odooURL := os.Getenv("ODOO_INTERNAL_URL")
		if odooURL == "" {
			odooURL = "http://polyon-appengine.polyon.svc.cluster.local:8069"
		}

		syncURL := odooURL + "/polyon/oidc/internal/group-sync"

		req, err := http.NewRequest("POST", syncURL, strings.NewReader("{}"))
		if err != nil {
			httputil.RespondError(w, 500, "REQUEST_BUILD_FAILED", fmt.Sprintf("build request: %v", err))
			return
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Warn().Err(err).Msg("group-sync: Odoo call failed")
			httputil.RespondError(w, 502, "ODOO_UNREACHABLE", fmt.Sprintf("Odoo call failed: %v", err))
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			log.Warn().Int("status", resp.StatusCode).Str("body", string(body)).Msg("group-sync: bad status")
			httputil.RespondError(w, 502, "ODOO_ERROR", fmt.Sprintf("Odoo returned %d", resp.StatusCode))
			return
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			// Odoo가 유효한 JSON을 반환하지 않은 경우 — 그냥 성공 처리
			httputil.RespondOK(w, map[string]interface{}{
				"success": true,
				"message": "동기화가 완료되었습니다.",
			})
			return
		}

		result["success"] = true
		httputil.RespondOK(w, result)
	}
}
