package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAccount — 내 계정 관련 라우트
// admin realm의 service account (polyon-admin-api)를 통해 Keycloak Admin API 호출
func RegisterAccount(r chi.Router, d *Deps) {
	r.Route("/account", func(r chi.Router) {
		r.Get("/profile", getProfile(d))
		r.Put("/profile", updateProfile(d))
		r.Put("/password", changeAdminPassword(d))
	})
}

// kcInternalURL returns the Keycloak internal base URL (including /auth path).
// Uses config.Get() so it reflects DB-populated values after server init.
func kcInternalURL() string {
	if cfg := config.Get(); cfg != nil && cfg.KeycloakURL != "" {
		return cfg.KeycloakURL + "/auth"
	}
	return "http://polyon-auth:8080/auth"
}

// ── Service Account Token Cache ──

var (
	saTokenMu    sync.Mutex
	saToken      string
	saTokenExp   time.Time
)

// getServiceAccountToken — admin realm의 polyon-admin-api client_credentials로 토큰 발급
func getServiceAccountToken(envPath string) (string, error) {
	saTokenMu.Lock()
	defer saTokenMu.Unlock()

	// 캐시된 토큰이 아직 유효하면 재사용 (30초 여유)
	if saToken != "" && time.Now().Before(saTokenExp.Add(-30*time.Second)) {
		return saToken, nil
	}

	env := config.ReadEnvFile(envPath)
	secret := env["HELIOS_ADMIN_API_SECRET"]
	if secret == "" {
		return "", fmt.Errorf("HELIOS_ADMIN_API_SECRET이 설정되지 않았습니다")
	}

	resp, err := http.PostForm(
		kcInternalURL()+"/realms/admin/protocol/openid-connect/token",
		url.Values{
			"client_id":     {"polyon-admin-api"},
			"client_secret": {secret},
			"grant_type":    {"client_credentials"},
		},
	)
	if err != nil {
		return "", fmt.Errorf("Keycloak 연결 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("서비스 토큰 발급 실패 (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenData struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenData); err != nil {
		return "", fmt.Errorf("토큰 파싱 실패: %w", err)
	}

	saToken = tokenData.AccessToken
	saTokenExp = time.Now().Add(time.Duration(tokenData.ExpiresIn) * time.Second)
	return saToken, nil
}

// ── Admin API helpers ──

func kcAdminGet(token, path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", kcInternalURL()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	return http.DefaultClient.Do(req)
}

func findAdminUser(token string) (map[string]interface{}, error) {
	resp, err := kcAdminGet(token, "/admin/realms/admin/users?username=admin&exact=true")
	if err != nil {
		return nil, fmt.Errorf("사용자 조회 실패: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("사용자 조회 실패 (status %d): %s", resp.StatusCode, string(body))
	}

	var users []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("응답 파싱 실패: %w", err)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("admin 사용자를 찾을 수 없습니다")
	}
	return users[0], nil
}

// ── Handlers ──

func getProfile(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := getServiceAccountToken(d.Cfg.EnvFilePath())
		if err != nil {
			httputil.RespondError(w, 502, "KC_TOKEN_FAILED", err.Error())
			return
		}

		user, err := findAdminUser(token)
		if err != nil {
			httputil.RespondError(w, 502, "KC_USER_NOT_FOUND", err.Error())
			return
		}

		str := func(v interface{}) string {
			if s, ok := v.(string); ok {
				return s
			}
			return ""
		}
		boolVal := func(v interface{}) bool {
			if b, ok := v.(bool); ok {
				return b
			}
			return false
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"profile": map[string]interface{}{
				"username":      str(user["username"]),
				"email":         str(user["email"]),
				"firstName":     str(user["firstName"]),
				"lastName":      str(user["lastName"]),
				"emailVerified": boolVal(user["emailVerified"]),
			},
		})
	}
}

func updateProfile(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email     string `json:"email"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, 400, "INVALID_BODY", "요청 본문을 파싱할 수 없습니다")
			return
		}

		token, err := getServiceAccountToken(d.Cfg.EnvFilePath())
		if err != nil {
			httputil.RespondError(w, 502, "KC_TOKEN_FAILED", err.Error())
			return
		}

		user, err := findAdminUser(token)
		if err != nil {
			httputil.RespondError(w, 502, "KC_USER_NOT_FOUND", err.Error())
			return
		}

		userID, _ := user["id"].(string)
		if userID == "" {
			httputil.RespondError(w, 500, "KC_NO_USER_ID", "사용자 ID를 확인할 수 없습니다")
			return
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"email":     body.Email,
			"firstName": body.FirstName,
			"lastName":  body.LastName,
		})

		req, _ := http.NewRequest("PUT",
			fmt.Sprintf("%s/admin/realms/admin/users/%s", kcInternalURL(), userID),
			bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			httputil.RespondError(w, 502, "KC_UPDATE_FAILED", err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 204 {
			respBody, _ := io.ReadAll(resp.Body)
			httputil.RespondError(w, 500, "KC_UPDATE_ERROR",
				fmt.Sprintf("프로필 업데이트 실패 (status %d): %s", resp.StatusCode, string(respBody)))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "프로필이 업데이트되었습니다.",
		})
	}
}

func changeAdminPassword(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, 400, "INVALID_BODY", "요청 본문을 파싱할 수 없습니다")
			return
		}

		if len(body.NewPassword) < 8 {
			httputil.RespondError(w, 400, "TOO_SHORT", "비밀번호는 최소 8자 이상이어야 합니다")
			return
		}

		// 1. 현재 비밀번호 검증: admin realm에서 direct grant 시도
		verifyResp, err := http.PostForm(
			kcInternalURL()+"/realms/admin/protocol/openid-connect/token",
			url.Values{
				"client_id":  {"polyon-console"},
				"username":   {"admin"},
				"password":   {body.CurrentPassword},
				"grant_type": {"password"},
			},
		)
		if err != nil {
			httputil.RespondError(w, 502, "KC_VERIFY_FAILED", "현재 비밀번호 확인 중 오류가 발생했습니다")
			return
		}
		defer verifyResp.Body.Close()
		io.ReadAll(verifyResp.Body)
		if verifyResp.StatusCode != 200 {
			httputil.RespondError(w, 401, "WRONG_PASSWORD", "현재 비밀번호가 올바르지 않습니다")
			return
		}

		// 2. Service account token으로 비밀번호 변경
		token, err := getServiceAccountToken(d.Cfg.EnvFilePath())
		if err != nil {
			httputil.RespondError(w, 502, "KC_TOKEN_FAILED", err.Error())
			return
		}

		user, err := findAdminUser(token)
		if err != nil {
			httputil.RespondError(w, 502, "KC_USER_NOT_FOUND", err.Error())
			return
		}

		userID, _ := user["id"].(string)
		if userID == "" {
			httputil.RespondError(w, 500, "KC_NO_USER_ID", "사용자 ID를 확인할 수 없습니다")
			return
		}

		resetPayload, _ := json.Marshal(map[string]interface{}{
			"type":      "password",
			"value":     body.NewPassword,
			"temporary": false,
		})

		resetReq, _ := http.NewRequest("PUT",
			fmt.Sprintf("%s/admin/realms/admin/users/%s/reset-password", kcInternalURL(), userID),
			bytes.NewReader(resetPayload))
		resetReq.Header.Set("Authorization", "Bearer "+token)
		resetReq.Header.Set("Content-Type", "application/json")

		resetResp, err := http.DefaultClient.Do(resetReq)
		if err != nil {
			httputil.RespondError(w, 502, "KC_RESET_FAILED", err.Error())
			return
		}
		defer resetResp.Body.Close()

		if resetResp.StatusCode != 204 {
			respBody, _ := io.ReadAll(resetResp.Body)
			httputil.RespondError(w, 500, "KC_RESET_ERROR",
				fmt.Sprintf("비밀번호 변경 실패 (status %d): %s", resetResp.StatusCode, string(respBody)))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "비밀번호가 변경되었습니다.",
		})
	}
}

// ── unused import guard ──
var _ = strings.TrimSpace
