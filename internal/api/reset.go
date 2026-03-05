package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/httputil"
)

var resetProgress = struct {
	mu       sync.RWMutex
	Phase    string `json:"phase"`
	Step     string `json:"step"`
	Progress int    `json:"progress"`
	Target   string `json:"target"`
	Error    string `json:"error"`
}{Phase: "idle"}

func RegisterReset(r chi.Router, d *Deps) {
	r.Get("/status", resetStatus(d))
	r.Get("/progress", resetProgressHandler(d))
	r.Post("/", executeReset(d))
}

func resetStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		status := map[string]interface{}{
			"identity":   map[string]interface{}{"users": 0, "groups": 0, "ous": 0},
			"auth":       map[string]interface{}{"realms": 0, "clients": 0},
			"mail":       map[string]interface{}{"mailboxes": 0},
			"storage":    map[string]interface{}{"objects": 0},
			"monitoring": map[string]interface{}{"alerts": 0},
			"search":     map[string]interface{}{"indices": 0, "docs": 0},
		}

		// Identity
		if users, err := d.Samba.ListUsers(); err == nil {
			if groups, err := d.Samba.ListGroups(); err == nil {
				if ous, err := d.Samba.ListOUs(); err == nil {
					status["identity"] = map[string]interface{}{
						"users": len(users), "groups": len(groups), "ous": len(ous),
					}
				}
			}
		}

		httputil.RespondOK(w, status)
	}
}

func resetProgressHandler(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resetProgress.mu.RLock()
		result := map[string]interface{}{
			"phase": resetProgress.Phase, "step": resetProgress.Step,
			"progress": resetProgress.Progress,
		}
		resetProgress.mu.RUnlock()
		httputil.RespondOK(w, result)
	}
}

func executeReset(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Targets       []string `json:"targets"`
			AdminPassword string   `json:"admin_password"`
			Confirm       string   `json:"confirm"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.Confirm != "RESET" {
			httputil.RespondError(w, 400, "VALIDATION", "confirm must be exactly 'RESET'")
			return
		}

		// Verify admin password via LDAP bind
		adminDN := d.Cfg.AdminDN()
		if err := d.LDAP.VerifyBind(adminDN, req.AdminPassword); err != nil {
			httputil.RespondError(w, 401, "AUTH_FAILED", "Invalid administrator password")
			return
		}

		targets := req.Targets
		if contains(targets, "full") {
			targets = []string{"full"}
		}

		go runReset(d, targets)

		httputil.RespondOK(w, map[string]interface{}{
			"status": "ok", "message": "Reset initiated", "targets": targets,
		})
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func setResetProgress(phase, step string, progress int) {
	resetProgress.mu.Lock()
	defer resetProgress.mu.Unlock()
	resetProgress.Phase = phase
	resetProgress.Step = step
	resetProgress.Progress = progress
}

func runReset(d *Deps, targets []string) {
	if contains(targets, "full") {
		runFullReset(d)
		return
	}

	total := len(targets)
	for i, target := range targets {
		pct := i * 100 / total
		switch target {
		case "identity":
			setResetProgress("running", "ID/디렉토리 초기화 중...", pct)
			resetIdentity(d)
		case "monitoring":
			setResetProgress("running", "모니터링 초기화 중...", pct)
			d.Store.ClearAlerts("")
		}
	}
	setResetProgress("complete", "초기화 완료", 100)
}

var fullResetVolumes = []string{
	"polyon_pg-data", "polyon_samba-data", "polyon_samba-conf",
	"polyon_es-data", "polyon_grafana-data",
	"polyon_rustfs-data", "polyon_rustfs-logs",
	"polyon_stalwart-data", "polyon_stalwart-config",
	"polyon_pgadmin-data", "polyon_redis-data",
	"polyon_prometheus-data", "polyon_sentinel-data",
	"polyon_keycloak-data", "polyon_polyon-shared",
}

const defaultEnv = `# ── PostgreSQL ──
DB_PASSWORD=polyon-default-changeme

# ── RustFS ──
RUSTFS_ROOT_USER=rustfs
RUSTFS_ROOT_PASSWORD=polyon-default-changeme

# ── Elasticsearch ──
ELASTIC_PASSWORD=polyon-default-changeme

# ── Keycloak ──
KC_BOOTSTRAP_ADMIN_USERNAME=keycloak
KC_ADMIN_PASSWORD=polyon-default-changeme

# ── Grafana ──
GF_SECURITY_ADMIN_USER=grafana
GF_SECURITY_ADMIN_PASSWORD=polyon-default-changeme

# ── Stalwart Mail ──
STALWART_ADMIN_USER=stalwart
STALWART_ADMIN_PASSWORD=polyon-default-changeme

# ── pgAdmin ──
PGADMIN_DEFAULT_EMAIL=admin@polyon.local
`

func runFullReset(d *Deps) {
	setResetProgress("running", ".env 초기화 중...", 20)

	// Reset .env
	envPath := d.Cfg.EnvFilePath()
	env := config.ReadEnvFile(envPath)
	preserved := map[string]string{}
	if v, ok := env["OPENROUTER_API_KEY"]; ok {
		preserved["OPENROUTER_API_KEY"] = v
	}

	content := defaultEnv
	for k, v := range preserved {
		content += fmt.Sprintf("\n%s=%s\n", k, v)
	}
	os.WriteFile(envPath, []byte(content), 0644)

	// Reset admin password secret
	os.WriteFile(filepath.Join(d.Cfg.PolyonDir, "secrets", "admin_password.txt"), []byte(""), 0644)

	// Run factory reset script
	setResetProgress("running", "전체 초기화 진행 중...", 40)
	hostDir := getHostProjectDir(d)

	var volRmCmds []string
	for _, v := range fullResetVolumes {
		volRmCmds = append(volRmCmds, fmt.Sprintf("docker volume rm -f %s 2>/dev/null || true", v))
	}

	resetScript := fmt.Sprintf(
		"sleep 3 && "+
			"docker compose -f /polyon/docker-compose.yml -f /polyon/docker-compose.services.yml "+
			"--project-directory %s --project-name polyon down -v --remove-orphans && "+
			"%s && "+
			"docker compose -f /polyon/docker-compose.yml "+
			"--project-directory %s --project-name polyon --env-file /polyon/.env up -d",
		hostDir, strings.Join(volRmCmds, " && "), hostDir)

	cmd := exec.Command("docker", "run", "-d", "--rm",
		"--name", "polyon-factory-reset",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", hostDir+":/polyon:ro",
		"docker:cli",
		"sh", "-c", resetScript)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Str("output", string(out)).Msg("Factory reset failed")
	} else {
		log.Info().Msg("Factory reset container started")
	}
}

var defaultUsers = map[string]bool{
	"Administrator": true, "Guest": true, "krbtgt": true,
}

func resetIdentity(d *Deps) {
	users, _ := d.Samba.ListUsers()
	for _, u := range users {
		if !defaultUsers[u.Username] {
			d.Samba.DeleteUser(u.Username)
		}
	}
	// Groups and OUs would follow similar pattern
	_ = time.Now()
}
