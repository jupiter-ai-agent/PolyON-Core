package auth

// OPA 클라이언트 — polyon-opa:8181에 정책 질의

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OPAInput은 OPA에 보내는 질의 입력
type OPAInput struct {
	User   string   `json:"user"`
	Roles  []string `json:"roles"`
	Groups []string `json:"groups"`
	Method string   `json:"method"`
	Path   string   `json:"path"`
	IP     string   `json:"ip"`
}

// OPAResult는 OPA 응답
type OPAResult struct {
	Allow bool `json:"allow"`
}

var opaClient = &http.Client{Timeout: 3 * time.Second}
var opaURL = "http://polyon-opa:8181/v1/data/polyon/authz"

// EvaluatePolicy는 OPA에 정책 평가를 요청합니다.
// OPA가 불가용하면 (연결 실패 등) allow=true를 반환합니다 (fail-open).
func EvaluatePolicy(ctx context.Context, input OPAInput) (bool, error) {
	body, _ := json.Marshal(map[string]interface{}{"input": input})

	req, err := http.NewRequestWithContext(ctx, "POST", opaURL, bytes.NewReader(body))
	if err != nil {
		return true, err // fail-open
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := opaClient.Do(req)
	if err != nil {
		return true, err // fail-open: OPA 불가용 시 허용
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return true, fmt.Errorf("OPA returned %d", resp.StatusCode)
	}

	var result struct {
		Result OPAResult `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return true, err
	}

	return result.Result.Allow, nil
}
