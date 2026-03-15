// Package api - Agent on-behalf-of endpoints (/api/v1/agent/*).
//
// AI Agent(ZeroClaw)가 Bot service account JWT + X-Agent-User 헤더로 호출하면,
// auth.Middleware가 ActorKey를 사원 username으로 교체한 뒤 여기 핸들러가 동작.
// 각 핸들러는 context에서 ActorKey를 꺼내 해당 사원 기준으로 처리.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAgent registers /agent/* routes onto the given router.
// Expected to be mounted at /api/v1/agent prefix.
func RegisterAgent(r chi.Router, d *Deps) {
	r.Route("/agent", func(r chi.Router) {
		r.Post("/mail/send", agentMailSend(d))
		r.Get("/mail/inbox", agentMailInbox(d))
		r.Get("/drive/search", agentDriveSearch(d))
		r.Get("/search/semantic", agentSemanticSearch(d))
	})
}

// POST /api/v1/agent/mail/send
// 사원(ActorKey) 명의로 메일 발송.
func agentMailSend(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.GetActor(r)

		var req struct {
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Body    string   `json:"body"`
			HTML    bool     `json:"html"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if len(req.To) == 0 || req.Subject == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "to, subject은 필수입니다")
			return
		}

		// TODO: 실제 Stalwart JMAP/SMTP 발송 연동
		// 현재는 stub — actor(사원) 명의로 발송됨을 로그로 확인
		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"from":    actor,
			"to":      req.To,
			"subject": req.Subject,
			"note":    "stub: 실제 SMTP 발송 미구현 — actor=" + actor,
		})
	}
}

// GET /api/v1/agent/mail/inbox
// 사원(ActorKey) 수신함 조회.
func agentMailInbox(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.GetActor(r)

		// TODO: Stalwart JMAP API로 actor 수신함 조회
		// 현재는 stub
		httputil.RespondOK(w, map[string]interface{}{
			"actor":    actor,
			"messages": []interface{}{},
			"total":    0,
			"note":     "stub: Stalwart JMAP 연동 미구현 — actor=" + actor,
		})
	}
}

// GET /api/v1/agent/drive/search
// 사원(ActorKey) 기준 Drive 파일 검색.
// Query params: q (검색어), limit
func agentDriveSearch(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.GetActor(r)
		query := r.URL.Query().Get("q")
		if query == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "q 파라미터가 필요합니다")
			return
		}

		// TODO: Nextcloud WebDAV/OCS search API로 actor 파일 검색
		// 사원의 Nextcloud 토큰 또는 app-password 필요 (향후 Keycloak token exchange)
		httputil.RespondOK(w, map[string]interface{}{
			"actor":   actor,
			"query":   query,
			"results": []interface{}{},
			"total":   0,
			"note":    "stub: Nextcloud OCS 검색 연동 미구현 — actor=" + actor,
		})
	}
}

// GET /api/v1/agent/search/semantic
// 사내 문서 시맨틱 검색 (OpenSearch).
// Query params: q (검색어), index, size
func agentSemanticSearch(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := auth.GetActor(r)
		query := r.URL.Query().Get("q")
		if query == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "q 파라미터가 필요합니다")
			return
		}
		index := r.URL.Query().Get("index")
		if index == "" {
			index = "polyon-docs"
		}

		// TODO: OpenSearch knn_vector / semantic search 연동
		// d.Cfg.ElasticURL + index + actor 기반 필터
		httputil.RespondOK(w, map[string]interface{}{
			"actor":   actor,
			"query":   query,
			"index":   index,
			"hits":    []interface{}{},
			"total":   0,
			"note":    "stub: OpenSearch 시맨틱 검색 연동 미구현 — actor=" + actor,
		})
	}
}
