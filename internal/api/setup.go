package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"math/big"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/jackc/pgx/v5"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/httputil"
)

// Setup state machine
type setupState struct {
	mu       sync.RWMutex
	Phase    string `json:"phase"`
	Step     string `json:"step"`
	Progress int    `json:"progress"`
}

var currentSetup = &setupState{Phase: "waiting", Step: "설정 대기 중", Progress: 0}

func (s *setupState) set(phase, step string, progress int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = phase
	s.Step = step
	s.Progress = progress
	// Also persist to shared dir
	data, _ := json.Marshal(s)
	cfg := config.Get()
	if cfg != nil {
		os.WriteFile(filepath.Join(cfg.SharedDir, "setup-progress.json"), data, 0644)
	}
}

func (s *setupState) get() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]interface{}{
		"phase": s.Phase, "step": s.Step, "progress": s.Progress,
	}
}

// Service credential definitions
type serviceCredDef struct {
	ID          string
	Name        string
	EnvPassword string
	Username    string
	EnvUser     string
	UserTpl     string
	EnvEmail    string
	Port        *int
}

var serviceCredentials = []serviceCredDef{
	{"postgresql", "PostgreSQL", "DB_PASSWORD", "polyon", "", "", "", nil},
	{"keycloak", "Keycloak Admin", "KC_ADMIN_PASSWORD", "keycloak", "KC_BOOTSTRAP_ADMIN_USERNAME", "", "", intP(1112)},
	{"stalwart", "Stalwart Mail", "STALWART_ADMIN_PASSWORD", "admin", "", "", "", intP(1113)},
	{"grafana", "Grafana", "GF_SECURITY_ADMIN_PASSWORD", "grafana", "GF_SECURITY_ADMIN_USER", "", "", intP(1114)},
	{"rustfs", "RustFS Console", "RUSTFS_ROOT_PASSWORD", "rustfs", "RUSTFS_ROOT_USER", "", "", intP(1115)},
	{"elasticsearch", "Elasticsearch", "ELASTIC_PASSWORD", "elastic", "", "", "", nil},
	{"pgadmin", "pgAdmin", "DB_PASSWORD", "", "", "pgadmin@{realm_lower}", "PGADMIN_DEFAULT_EMAIL", intP(1117)},
}

func intP(i int) *int { return &i }

var portMap = map[string]int{
	"keycloak": 1112, "stalwart": 1113, "grafana": 1114,
	"rustfs": 1115, "pgadmin": 1117,
}

// Service containers for progress tracking
type serviceContainer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Icon     string `json:"icon"`
	Tier     int    `json:"tier"`
	TierName string `json:"tier_name"`
}

var serviceContainers = []serviceContainer{
	// Tier 0 — Basecamp
	{"polyon-db", "PostgreSQL", "database", 0, "Basecamp"},
	{"polyon-proxy", "Traefik Proxy", "network", 0, "Basecamp"},
	{"polyon-auth", "HELIOS Auth", "security", 0, "Basecamp"},
	{"polyon-core", "HELIOS Core", "server", 0, "Basecamp"},
	{"polyon-console", "PolyON Console", "ui", 0, "Basecamp"},
	// Tier 1 — 기반 인프라
	{"polyon-redis", "Redis", "cache", 1, "기반 인프라"},
	{"polyon-search", "Elasticsearch", "search", 1, "기반 인프라"},
	{"polyon-rustfs", "RustFS", "storage", 1, "기반 인프라"},
	// Tier 2 — 핵심 서비스
	{"polyon-dc", "HELIOS AD DC", "directory", 2, "핵심 서비스"},
	{"polyon-mail", "Stalwart Mail", "mail", 2, "핵심 서비스"},
	// Tier 3 — 모니터링
	{"polyon-prometheus", "Prometheus", "monitoring", 3, "모니터링"},
	{"polyon-pg-exporter", "PG Exporter", "monitoring", 3, "모니터링"},
	{"polyon-redis-exporter", "Redis Exporter", "monitoring", 3, "모니터링"},
	{"polyon-search-exporter", "ES Exporter", "monitoring", 3, "모니터링"},
	{"polyon-grafana", "Grafana", "dashboard", 3, "모니터링"},
	// Tier 4 — 관리 도구
	{"polyon-pgadmin", "pgAdmin", "tool", 4, "관리 도구"},
	{"polyon-redisinsight", "RedisInsight", "tool", 4, "관리 도구"},
	{"polyon-elasticvue", "Elasticvue", "tool", 4, "관리 도구"},
	{"polyon-sentinel", "Sentinel", "agent", 4, "관리 도구"},
}

func RegisterSetup(r chi.Router, d *Deps) {
	r.Get("/status", setupStatus(d))
	r.Post("/prepare", prepareCredentials(d))
	r.Post("/", startSetup(d))
	r.Get("/progress", setupProgress(d))
	r.Get("/credentials", getCredentials(d))
	r.Delete("/credentials", deleteCredentials(d))
}

func setupStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		provisioned := d.Cfg.IsProvisioned()
		result := map[string]interface{}{"provisioned": provisioned}
		if provisioned {
			result["realm"] = d.Cfg.Realm
			result["domain"] = d.Cfg.Domain
			result["org_name"] = d.Cfg.OrgName
		}
		httputil.RespondOK(w, result)
	}
}

func prepareCredentials(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Realm string `json:"realm"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		realmLower := strings.ToLower(strings.TrimSpace(req.Realm))
		// Ensure realm has a dot for email-like fields (e.g. pgadmin@realm)
		if !strings.Contains(realmLower, ".") {
			realmLower = realmLower + ".dev"
		}

		env := config.ReadEnvFile(d.Cfg.EnvFilePath())
		oldDBPW := env["DB_PASSWORD"] // Save before overwriting
		if oldDBPW == "" {
			oldDBPW = "temp-basecamp-init_1234"
		}
		var credentials []map[string]interface{}

		for _, svc := range serviceCredentials {
			pw := generatePassword(24)

			username := svc.Username
			if svc.UserTpl != "" && realmLower != "" {
				username = strings.ReplaceAll(svc.UserTpl, "{realm_lower}", realmLower)
			}

			if svc.EnvUser != "" {
				env[svc.EnvUser] = username
			}

			// pgAdmin shares DB_PASSWORD
			if svc.ID == "pgadmin" {
				pw = env["DB_PASSWORD"]
				if pw == "" {
					pw = generatePassword(24)
				}
			} else {
				env[svc.EnvPassword] = pw
			}

			if svc.EnvEmail != "" && realmLower != "" {
				env[svc.EnvEmail] = username
			}

			cred := map[string]interface{}{
				"id": svc.ID, "name": svc.Name,
				"username": username, "password": pw,
			}
			if p, ok := portMap[svc.ID]; ok {
				cred["port"] = p
			}
			credentials = append(credentials, cred)
		}

		// Write .env
		config.WriteEnvFile(d.Cfg.EnvFilePath(), env)

		// Sync DB password via ALTER USER (PG still has old password)
		newDBPW := env["DB_PASSWORD"]
		if newDBPW != "" && newDBPW != oldDBPW {
			connStr := fmt.Sprintf("host=polyon-db dbname=polyon user=polyon password=%s", oldDBPW)
			if conn, err := pgx.Connect(context.Background(), connStr); err == nil {
				escaped := strings.ReplaceAll(newDBPW, "'", "''")
				if _, err := conn.Exec(context.Background(), fmt.Sprintf("ALTER USER polyon WITH PASSWORD '%s'", escaped)); err != nil {
					log.Warn().Err(err).Msg("ALTER USER polyon failed")
				} else {
					log.Info().Msg("PostgreSQL password synced via ALTER USER")
				}
				conn.Close(context.Background())
			} else {
				log.Warn().Err(err).Msg("Cannot connect to PG for ALTER USER")
			}
		}

		// Save credentials JSON
		result := map[string]interface{}{
			"success": true, "credentials": credentials, "realm": realmLower,
		}
		credJSON, _ := json.MarshalIndent(result, "", "  ")
		credPath := filepath.Join(d.Cfg.SharedDir, "generated-credentials.json")
		os.MkdirAll(d.Cfg.SharedDir, 0755)
		os.WriteFile(credPath, credJSON, 0644)

		// Force-recreate polyon-auth with new password (synchronous)
		// Runner will later recreate everything, but we need auth up with the new PW.
		hostDir := getHostProjectDir(d)
		authCmd := exec.Command("docker", "compose",
			"-f", "/polyon/docker-compose.yml",
			"--project-directory", hostDir,
			"--env-file", "/polyon/.env",
			"up", "-d", "--force-recreate", "polyon-auth",
		)
		if out, err := authCmd.CombinedOutput(); err != nil {
			log.Warn().Err(err).Str("output", string(out)).Msg("polyon-auth recreate failed (non-fatal)")
		} else {
			log.Info().Msg("polyon-auth force-recreated with new password")
		}

		httputil.RespondOK(w, result)
	}
}

func startSetup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Cfg.IsProvisioned() {
			httputil.RespondError(w, 403, "ALREADY_PROVISIONED", "Domain is already provisioned.")
			return
		}

		setupJSONPath := d.Cfg.SetupJSONPath()
		if fileExists(setupJSONPath) {
			httputil.RespondError(w, 409, "IN_PROGRESS", "Setup already in progress.")
			return
		}

		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		// Validate required fields
		realm, _ := req["realm"].(string)
		if realm == "" {
			httputil.RespondError(w, 400, "VALIDATION", "Realm is required")
			return
		}

		// Validate realm format
		realm = strings.ToUpper(strings.TrimSpace(realm))
		if matched, _ := regexp.MatchString(`^[A-Z0-9]([A-Z0-9-]*[A-Z0-9])?(\.[A-Z0-9]([A-Z0-9-]*[A-Z0-9])?)+$`, realm); !matched {
			httputil.RespondError(w, 400, "VALIDATION", "Realm must be a valid FQDN (e.g. EXAMPLE.COM)")
			return
		}
		req["realm"] = realm

		// Write setup.json
		os.MkdirAll(d.Cfg.SharedDir, 0755)
		data, _ := json.MarshalIndent(req, "", "  ")
		os.WriteFile(setupJSONPath, data, 0644)

		// Write DC_REALM and DC_DOMAIN to .env for PolyON DC
		env := config.ReadEnvFile(d.Cfg.EnvFilePath())
		env["DC_REALM"] = realm // e.g. POLYON.DEV
		// NetBIOS domain = first label of realm (max 15 chars)
		parts := strings.SplitN(realm, ".", 2)
		env["DC_DOMAIN"] = parts[0] // e.g. POLYON
		config.WriteEnvFile(d.Cfg.EnvFilePath(), env)

		// Persist domain settings to polyon_config if store is available
		if d.Store != nil {
			realmLower := strings.ToLower(realm) // e.g. "cmars.com"
			kvs := map[string]string{
				"dc_domain": realmLower,
			}

			// service_base_domain: from setup.json field or fall back to dc_domain
			if sbd, ok := req["service_base_domain"].(string); ok && sbd != "" {
				kvs["service_base_domain"] = strings.ToLower(strings.TrimSpace(sbd))
			} else {
				kvs["service_base_domain"] = realmLower
			}

			// console_domain
			if v, ok := req["console_domain"].(string); ok && v != "" {
				kvs["console_domain"] = strings.ToLower(strings.TrimSpace(v))
			} else {
				kvs["console_domain"] = "console." + kvs["service_base_domain"]
			}

			// mail_domain
			if v, ok := req["mail_domain"].(string); ok && v != "" {
				kvs["mail_domain"] = strings.ToLower(strings.TrimSpace(v))
			} else {
				kvs["mail_domain"] = "mail." + kvs["service_base_domain"]
			}

			// portal_domain
			if v, ok := req["portal_domain"].(string); ok && v != "" {
				kvs["portal_domain"] = strings.ToLower(strings.TrimSpace(v))
			} else {
				kvs["portal_domain"] = "portal." + kvs["service_base_domain"]
			}

			// cert_mode and external_access defaults
			kvs["cert_mode"] = "self-signed"
			kvs["external_access"] = "internal"
			if v, ok := req["cert_mode"].(string); ok && v != "" {
				kvs["cert_mode"] = v
			}
			if v, ok := req["external_access"].(string); ok && v != "" {
				kvs["external_access"] = v
			}

			if err := d.Store.SetConfigs(context.Background(), kvs); err != nil {
				log.Warn().Err(err).Msg("startSetup: failed to persist domain config to polyon_config")
			} else {
				log.Info().Interface("kvs", kvs).Msg("startSetup: domain config saved to polyon_config")
			}
		}

		// Write admin_password.txt
		if dcPW, ok := req["dc_admin_password"].(string); ok {
			secretsDir := filepath.Join(d.Cfg.PolyonDir, "secrets")
			os.MkdirAll(secretsDir, 0755)
			os.WriteFile(filepath.Join(secretsDir, "admin_password.txt"), []byte(dcPW), 0644)
		}

		// Reload config
		d.Cfg.Reload()

		// Determine image tag (same as what core is running)
		hostDir := getHostProjectDir(d)
		imageTag := "jupitertriangles/polyon-core:202602"
		if tag := os.Getenv("HELIOS_IMAGE_TAG"); tag != "" {
			imageTag = tag
		}

		// Determine external URL for Keycloak frontendUrl
		externalURL := os.Getenv("HELIOS_EXTERNAL_URL")
		if externalURL == "" {
			externalURL = "https://192.168.55.200:1111"
		}

		// Launch setup-runner as a separate container
		cmd := exec.Command("docker", "run", "-d", "--rm",
			"--name", "polyon-setup-runner",
			"--network", "polyon_polyon-net",
			"--entrypoint", "/app/setup-runner",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-v", hostDir+":/polyon",
			"-v", "polyon_polyon-shared:/shared",
			"-e", "HOST_PROJECT_DIR="+hostDir,
			"-e", "HELIOS_EXTERNAL_URL="+externalURL,
			imageTag,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Error().Err(err).Str("output", string(out)).Msg("Failed to launch setup-runner")
			httputil.RespondError(w, 500, "RUNNER_LAUNCH_FAILED", "Failed to launch setup runner: "+string(out))
			return
		}
		log.Info().Str("containerID", strings.TrimSpace(string(out))).Msg("setup-runner container launched")

		currentSetup.set("starting", "Setup Runner 시작 중...", 1)

		httputil.RespondOK(w, map[string]interface{}{
			"status": "ok", "message": "Setup initiated.",
		})
	}
}

func setupProgress(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Prefer progress.json written by setup-runner
		var result map[string]interface{}
		progressPath := filepath.Join(d.Cfg.SharedDir, "setup-progress.json")
		if data, err := os.ReadFile(progressPath); err == nil {
			json.Unmarshal(data, &result)
		}
		if result == nil {
			result = currentSetup.get()
		}

		// Attach container status
		if d.Docker != nil {
			statusMap := d.Docker.ContainerStatusMap(context.Background())
			var containers []map[string]interface{}
			for _, svc := range serviceContainers {
				raw := statusMap[svc.ID]
				state := "waiting"
				if strings.Contains(raw, "Up") {
					if strings.Contains(raw, "(healthy)") {
						state = "healthy"
					} else if strings.Contains(raw, "health: starting") {
						state = "starting"
					} else {
						state = "running"
					}
				} else if strings.Contains(raw, "Restarting") {
					state = "restarting"
				} else if strings.Contains(raw, "Exited") {
					state = "error"
				}
				containers = append(containers, map[string]interface{}{
					"id": svc.ID, "name": svc.Name, "icon": svc.Icon,
					"tier": svc.Tier, "tier_name": svc.TierName,
					"state": state, "detail": raw,
				})
			}
			result["containers"] = containers
		}

		// If complete, verify all containers
		if result["phase"] == "complete" || d.Cfg.IsProvisioned() {
			if containers, ok := result["containers"].([]map[string]interface{}); ok {
				allUp := true
				running := 0
				for _, c := range containers {
					if c["state"] == "healthy" || c["state"] == "running" {
						running++
					} else {
						allUp = false
					}
				}
				if allUp {
					result["phase"] = "complete"
					result["step"] = "모든 서비스가 준비되었습니다"
					result["progress"] = 100
				} else {
					result["phase"] = "starting-services"
					result["step"] = fmt.Sprintf("서비스 준비 중... (%d/%d)", running, len(containers))
					result["progress"] = min(85+running*14/len(containers), 99)
				}
			}
		}

		httputil.RespondOK(w, result)
	}
}

func getCredentials(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		path := filepath.Join(d.Cfg.SharedDir, "generated-credentials.json")
		data, err := os.ReadFile(path)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"success": false, "error": "No generated credentials found."})
			return
		}
		var result map[string]interface{}
		json.Unmarshal(data, &result)
		httputil.RespondOK(w, result)
	}
}

func deleteCredentials(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		path := filepath.Join(d.Cfg.SharedDir, "generated-credentials.json")
		os.Remove(path)
		httputil.RespondOK(w, map[string]interface{}{"success": true})
	}
}

func getHostProjectDir(d *Deps) string {
	if d.Docker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		info, err := d.Docker.ContainerInspect(ctx, "polyon-core")
		if err == nil && info != nil {
			for _, m := range info.Mounts {
				if m.Destination == "/polyon" {
					path := m.Source
					path = strings.TrimPrefix(path, "/host_mnt")
					return path
				}
			}
		}
	}
	return "/Users/jupiter/.openclaw/workspace/polyon"
}

// Password generation — shell-safe, URL-safe
func generatePassword(length int) string {
	alphabet := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for {
		pw := make([]byte, length)
		for i := range pw {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			pw[i] = alphabet[n.Int64()]
		}
		s := string(pw)
		hasLower := strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz")
		hasUpper := strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		hasDigit := strings.ContainsAny(s, "0123456789")
		hasSpecial := strings.ContainsAny(s, "-_")
		if hasLower && hasUpper && hasDigit && hasSpecial {
			return s
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
