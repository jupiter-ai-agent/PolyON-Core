package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/httputil"
)

type credService struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Port       *int   `json:"port"`
	EnvUser    string `json:"-"`
	DefaultUsr string `json:"-"`
	EnvEmail   string `json:"-"`
	EnvPW      string `json:"-"`
	ChangeCmd  string `json:"-"` // "api", "env", "sql", ""
	Note       string `json:"note,omitempty"`
}

func intPtr(i int) *int { return &i }

var credServices = []credService{
	{"keycloak", "Keycloak Admin", intPtr(1112), "KC_BOOTSTRAP_ADMIN_USERNAME", "keycloak", "", "KC_ADMIN_PASSWORD", "api", ""},
	{"stalwart", "Stalwart Mail Admin", intPtr(1113), "STALWART_ADMIN_USER", "admin", "", "STALWART_ADMIN_PASSWORD", "env", ""},
	{"grafana", "Grafana", intPtr(1114), "GF_SECURITY_ADMIN_USER", "grafana", "", "GF_SECURITY_ADMIN_PASSWORD", "env", ""},
	{"rustfs", "RustFS Console", intPtr(1115), "RUSTFS_ROOT_USER", "rustfs", "", "RUSTFS_ROOT_PASSWORD", "env", ""},
	{"elasticsearch", "Elasticsearch", nil, "", "elastic", "", "ELASTIC_PASSWORD", "api", ""},
	{"postgresql", "PostgreSQL", nil, "", "polyon", "", "DB_PASSWORD", "sql", ""},
	{"pgadmin", "pgAdmin", intPtr(1117), "", "", "PGADMIN_DEFAULT_EMAIL", "DB_PASSWORD", "env", ""},
	{"redisinsight", "RedisInsight", intPtr(1118), "", "", "", "", "", "인증 없음 (내부 접근 전용)"},
}

func RegisterCredentials(r chi.Router, d *Deps) {
	r.Route("/credentials", func(r chi.Router) {
		r.Get("/services", listCredentials(d))
		r.Get("/services/{id}/password", revealPassword(d))
		r.Put("/services/{id}/password", changeCredPassword(d))
	})
}

func findCredService(id string) *credService {
	for i := range credServices {
		if credServices[i].ID == id {
			return &credServices[i]
		}
	}
	return nil
}

func credUsername(svc *credService, env map[string]string) string {
	if svc.EnvUser != "" {
		if v, ok := env[svc.EnvUser]; ok && v != "" {
			return v
		}
		return svc.DefaultUsr
	}
	if svc.EnvEmail != "" {
		if v, ok := env[svc.EnvEmail]; ok && v != "" {
			return v
		}
	}
	return svc.DefaultUsr
}

func listCredentials(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		env := config.ReadEnvFile(d.Cfg.EnvFilePath())
		var services []map[string]interface{}
		for _, svc := range credServices {
			pw := env[svc.EnvPW]
			masked := ""
			if pw != "" {
				masked = "••••••••"
			}
			services = append(services, map[string]interface{}{
				"id": svc.ID, "name": svc.Name, "port": svc.Port,
				"username":        credUsername(&svc, env),
				"password_masked": masked,
				"has_password":    pw != "",
				"editable":        svc.ChangeCmd != "",
				"note":            svc.Note,
			})
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "services": services})
	}
}

func revealPassword(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		svc := findCredService(id)
		if svc == nil {
			httputil.RespondError(w, 404, "NOT_FOUND", "서비스를 찾을 수 없습니다")
			return
		}
		if svc.EnvPW == "" {
			httputil.RespondError(w, 400, "NO_PASSWORD", "비밀번호가 없는 서비스입니다")
			return
		}
		env := config.ReadEnvFile(d.Cfg.EnvFilePath())
		httputil.RespondOK(w, map[string]interface{}{"success": true, "password": env[svc.EnvPW]})
	}
}

func changeCredPassword(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		svc := findCredService(id)
		if svc == nil {
			httputil.RespondError(w, 404, "NOT_FOUND", "서비스를 찾을 수 없습니다")
			return
		}
		if svc.ChangeCmd == "" {
			httputil.RespondError(w, 400, "NOT_EDITABLE", "이 서비스의 비밀번호는 변경할 수 없습니다")
			return
		}

		var req struct {
			NewPassword string `json:"new_password"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.NewPassword) < 8 {
			httputil.RespondError(w, 400, "TOO_SHORT", "비밀번호는 최소 8자 이상이어야 합니다")
			return
		}

		env := config.ReadEnvFile(d.Cfg.EnvFilePath())
		oldPW := env[svc.EnvPW]
		newPW := req.NewPassword
		var restartNeeded []string

		// 1. Change in service
		var err error
		switch {
		case svc.ChangeCmd == "api" && id == "keycloak":
			err = changeKeycloakPassword(env, oldPW, newPW)
		case svc.ChangeCmd == "api" && id == "elasticsearch":
			err = changeESPassword(oldPW, newPW)
		case svc.ChangeCmd == "sql" && id == "postgresql":
			err = changePGPassword(oldPW, newPW)
			restartNeeded = []string{"polyon-core", "polyon-auth", "polyon-mail"}
		}
		if err != nil {
			httputil.RespondError(w, 500, "CHANGE_FAILED", err.Error())
			return
		}

		// 2. Update .env
		env[svc.EnvPW] = newPW
		config.WriteEnvFile(d.Cfg.EnvFilePath(), env)

		// 3. Determine restart needed (env-based)
		if svc.ChangeCmd == "env" {
			containerMap := map[string][]string{
				"stalwart": {"polyon-mail"},
				"grafana":  {"polyon-grafana"},
				"rustfs":   {"polyon-rustfs"},
			}
			if containers, ok := containerMap[id]; ok {
				restartNeeded = containers
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success":        true,
			"message":        svc.Name + " 비밀번호가 변경되었습니다.",
			"restart_needed": restartNeeded,
		})
	}
}

func changeKeycloakPassword(env map[string]string, oldPW, newPW string) error {
	kcUser := env["KC_BOOTSTRAP_ADMIN_USERNAME"]
	if kcUser == "" {
		kcUser = "keycloak"
	}
	kcBaseURL := "http://polyon-auth:8080" // fallback
	if cfg := config.Get(); cfg != nil && cfg.KeycloakURL != "" {
		kcBaseURL = cfg.KeycloakURL
	}
	kcBase := kcBaseURL + "/auth"

	// Get token
	resp, err := http.PostForm(kcBase+"/realms/master/protocol/openid-connect/token",
		map[string][]string{
			"client_id": {"admin-cli"}, "username": {kcUser},
			"password": {oldPW}, "grant_type": {"password"},
		})
	if err != nil {
		return fmt.Errorf("Keycloak 연결 실패: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Keycloak 인증 실패 (status %d)", resp.StatusCode)
	}
	var tokenData struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tokenData)

	// Find bootstrap admin user
	req, _ := http.NewRequest("GET",
		fmt.Sprintf("%s/admin/realms/master/users?username=%s&exact=true", kcBase, kcUser), nil)
	req.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	var users []struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp2.Body).Decode(&users)
	if len(users) == 0 {
		return fmt.Errorf("Keycloak admin '%s' not found", kcUser)
	}

	// Reset password
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "password", "value": newPW, "temporary": false,
	})
	req3, _ := http.NewRequest("PUT",
		fmt.Sprintf("%s/admin/realms/master/users/%s/reset-password", kcBase, users[0].ID),
		bytes.NewReader(payload))
	req3.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		return err
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 204 {
		body, _ := io.ReadAll(resp3.Body)
		return fmt.Errorf("password reset failed: %d %s", resp3.StatusCode, string(body))
	}
	return nil
}

func changeESPassword(oldPW, newPW string) error {
	esURL := "http://polyon-search:9200" // fallback
	if cfg := config.Get(); cfg != nil && cfg.ElasticURL != "" {
		esURL = cfg.ElasticURL
	}
	payload, _ := json.Marshal(map[string]string{"password": newPW})
	req, _ := http.NewRequest("PUT",
		esURL+"/_security/user/elastic/_password",
		bytes.NewReader(payload))
	req.SetBasicAuth("elastic", oldPW)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ES password change failed: %d %s", resp.StatusCode, string(body))
	}
	return nil
}

func changePGPassword(oldPW, newPW string) error {
	connStr := fmt.Sprintf("host=polyon-db dbname=polyon user=polyon password=%s", oldPW)
	conn, err := pgx.Connect(context.Background(), connStr)
	if err != nil {
		return fmt.Errorf("DB 연결 실패: %w", err)
	}
	defer conn.Close(context.Background())

	// Use string concatenation for ALTER USER (parameterized not supported for DDL password)
	// Password is validated to be 8+ chars above, no special chars by our generation rules
	escaped := strings.ReplaceAll(newPW, "'", "''")
	_, err = conn.Exec(context.Background(), fmt.Sprintf("ALTER USER polyon WITH PASSWORD '%s'", escaped))
	return err
}
