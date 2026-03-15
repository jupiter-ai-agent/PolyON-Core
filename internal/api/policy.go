package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/httputil"
)

var opaBaseURL = "http://polyon-opa:8181"

func RegisterPolicy(r chi.Router, d *Deps) {
	r.Route("/policy", func(r chi.Router) {
		r.Get("/status", getPolicyStatus(d))
		r.Get("/roles", getPolicyRoles(d))
		r.Put("/roles", putPolicyRoles(d))
		r.Post("/test", testPolicy(d))
		r.Get("/decisions", getPolicyDecisions(d))
	})
}

// getPolicyStatus — OPA 연결 상태 + 정책 로드 여부
func getPolicyStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client := &http.Client{Timeout: 3 * time.Second}

		// OPA health check
		resp, err := client.Get(opaBaseURL + "/health")
		opaHealthy := err == nil && resp != nil && resp.StatusCode == 200
		if resp != nil {
			resp.Body.Close()
		}

		// 정책 로드 확인
		policyLoaded := false
		resp2, err2 := client.Get(opaBaseURL + "/v1/policies")
		if err2 == nil && resp2 != nil {
			defer resp2.Body.Close()
			var policies struct {
				Result []interface{} `json:"result"`
			}
			json.NewDecoder(resp2.Body).Decode(&policies)
			policyLoaded = len(policies.Result) > 0
		}

		httputil.RespondOK(w, map[string]interface{}{
			"healthy":       opaHealthy,
			"policy_loaded": policyLoaded,
			"mode":          "fail-open",
		})
	}
}

// getPolicyRoles — 현재 역할 매핑 조회 (OPA 데이터에서)
func getPolicyRoles(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(opaBaseURL + "/v1/data/polyon/authz/role_map")
		if err != nil {
			httputil.RespondError(w, 503, "OPA_UNAVAILABLE", "OPA 연결 실패")
			return
		}
		defer resp.Body.Close()

		var result struct {
			Result map[string]string `json:"result"`
		}
		json.NewDecoder(resp.Body).Decode(&result)

		// 역할 설명 추가
		roleDescriptions := map[string]string{
			"admin":   "전체 시스템 관리 권한",
			"manager": "운영 관리 (삭제 제외)",
			"member":  "일반 업무 사용",
			"viewer":  "읽기 전용",
		}

		httputil.RespondOK(w, map[string]interface{}{
			"role_map":     result.Result,
			"descriptions": roleDescriptions,
		})
	}
}

// putPolicyRoles — 역할 매핑 수정 (OPA 데이터 업데이트)
func putPolicyRoles(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var roleMap map[string]string
		if err := json.NewDecoder(r.Body).Decode(&roleMap); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "잘못된 요청")
			return
		}

		body, _ := json.Marshal(roleMap)
		client := &http.Client{Timeout: 3 * time.Second}
		req, _ := http.NewRequest("PUT", opaBaseURL+"/v1/data/polyon/authz/role_map", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			httputil.RespondError(w, 503, "OPA_UNAVAILABLE", "OPA 연결 실패")
			return
		}
		defer resp.Body.Close()

		d.Store.LogAction("UPDATE", "policy_roles", "role_map",
			fmt.Sprintf("%v", roleMap), auth.GetActor(r), httputil.ClientIP(r))

		// ConfigTrack — commit role mapping change (non-fatal).
		if d.ConfigTracker != nil {
			contentBytes, _ := json.MarshalIndent(roleMap, "", "  ")
			if err := d.ConfigTracker.CommitFile("policy/roles.json", string(contentBytes),
				"Update OPA role mapping", auth.GetActor(r)); err != nil {
				log.Warn().Err(err).Msg("putPolicyRoles: configtrack commit failed (non-fatal)")
			}
		}

		httputil.RespondOK(w, map[string]interface{}{"success": true})
	}
}

// testPolicy — 정책 테스트 (dry-run)
func testPolicy(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			User   string   `json:"user"`
			Roles  []string `json:"roles"`
			Groups []string `json:"groups"`
			Method string   `json:"method"`
			Path   string   `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "잘못된 요청")
			return
		}

		body, _ := json.Marshal(map[string]interface{}{"input": input})
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Post(opaBaseURL+"/v1/data/polyon/authz", "application/json", bytes.NewReader(body))
		if err != nil {
			httputil.RespondError(w, 503, "OPA_UNAVAILABLE", "OPA 연결 실패")
			return
		}
		defer resp.Body.Close()

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		httputil.RespondOK(w, map[string]interface{}{
			"input":  input,
			"result": result["result"],
		})
	}
}

// getPolicyDecisions — 최근 정책 판정 로그 (감사 로그에서)
func getPolicyDecisions(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, total, _ := d.Store.GetAuditLogs(50, 0, "policy", "")
		httputil.RespondOK(w, map[string]interface{}{
			"entries": entries,
			"total":   total,
		})
	}
}
