// setup-runner is a standalone binary that runs the PolyON setup process
// outside of the core container. It is launched by polyon-core as a separate
// Docker container, so that Core can safely restart itself without killing
// the setup goroutine.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
)

// --- Progress helpers ---

type progress struct {
	Phase    string `json:"phase"`
	Step     string `json:"step"`
	Progress int    `json:"progress"`
}

func writeProgress(p progress) {
	data, _ := json.Marshal(p)
	os.WriteFile("/shared/setup-progress.json", data, 0644)
	log.Info().Str("phase", p.Phase).Str("step", p.Step).Int("progress", p.Progress).Msg("progress")
}

func fail(step string, err error) {
	log.Error().Err(err).Str("step", step).Msg("setup failed")
	writeProgress(progress{Phase: "error", Step: step + ": " + err.Error(), Progress: 0})
	os.Exit(1)
}

// --- Docker helpers ---

// dockerCompose runs `docker compose` with the standard polyon flags.
func dockerCompose(hostDir string, args ...string) error {
	base := []string{
		"compose",
		"-f", "/polyon/docker-compose.yml",
		"--project-directory", hostDir,
		"--env-file", "/polyon/.env",
	}
	base = append(base, args...)
	cmd := exec.Command("docker", base...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// dockerComposeServices runs `docker compose` with the services override file.
func dockerComposeServices(hostDir string, args ...string) error {
	base := []string{
		"compose",
		"-f", "/polyon/docker-compose.yml",
		"-f", "/polyon/docker-compose.services.yml",
		"--project-directory", hostDir,
		"--env-file", "/polyon/.env",
	}
	base = append(base, args...)
	cmd := exec.Command("docker", base...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// containerStatusMap returns a map[name]status for all polyon- containers.
func containerStatusMap() map[string]string {
	out, err := exec.Command("docker", "ps", "-a",
		"--format", "{{.Names}}\t{{.Status}}",
		"--filter", "name=polyon-",
	).Output()
	if err != nil {
		return map[string]string{}
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m
}

func isHealthy(status string) bool {
	return strings.Contains(status, "Up") && strings.Contains(status, "(healthy)")
}

func isUp(status string) bool {
	return strings.Contains(status, "Up")
}

// waitHealthy waits up to maxWait for all named containers to report healthy.
func waitHealthy(containers []string, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		m := containerStatusMap()
		ready := 0
		for _, name := range containers {
			if isHealthy(m[name]) || isUp(m[name]) {
				ready++
			}
		}
		if ready == len(containers) {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

// waitHealthyStrict waits for all containers to have (healthy) state.
func waitHealthyStrict(containers []string, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		m := containerStatusMap()
		ready := 0
		for _, name := range containers {
			if isHealthy(m[name]) {
				ready++
			}
		}
		if ready == len(containers) {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

// --- HTTP helper ---

type kcClient struct {
	base   string
	token  string
	client *http.Client
}

func newKCClient(base string) *kcClient {
	return &kcClient{
		base:   base,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (k *kcClient) login(user, pw string) error {
	resp, err := k.client.PostForm(
		k.base+"/realms/master/protocol/openid-connect/token",
		url.Values{
			"grant_type": {"password"},
			"client_id":  {"admin-cli"},
			"username":   {user},
			"password":   {pw},
		},
	)
	if err != nil {
		return fmt.Errorf("kcClient.login POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kcClient.login status %d: %s", resp.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok)
	k.token = tok.AccessToken
	return nil
}

func (k *kcClient) do(method, path string, body interface{}) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, k.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return k.client.Do(req)
}

func drainClose(resp *http.Response) {
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// --- Setup steps ---

// step1_readConfig reads setup.json and .env file.
func step1_readConfig() (map[string]interface{}, map[string]string) {
	log.Info().Msg("Step 1: Reading setup.json and .env")

	setupData, err := os.ReadFile("/shared/setup.json")
	if err != nil {
		fail("step1: read setup.json", err)
	}
	var setupJSON map[string]interface{}
	if err := json.Unmarshal(setupData, &setupJSON); err != nil {
		fail("step1: parse setup.json", err)
	}

	env := config.ReadEnvFile("/polyon/.env")
	log.Info().Msg("Config loaded")
	return setupJSON, env
}

// step2_recreateBasecamp force-recreates polyon-auth and polyon-core.
func step2_recreateBasecamp(hostDir string) {
	writeProgress(progress{Phase: "basecamp", Step: "Basecamp 재시작 중...", Progress: 5})
	log.Info().Msg("Step 2: Recreating polyon-auth and polyon-core")

	if err := dockerCompose(hostDir,
		"up", "-d", "--force-recreate",
		"polyon-auth", "polyon-core",
	); err != nil {
		// Non-fatal — containers may restart themselves
		log.Warn().Err(err).Msg("Basecamp recreate returned error (may be ok)")
	}
}

// step3_waitCore waits for polyon-core to be healthy (max 90s).
func step3_waitCore() {
	writeProgress(progress{Phase: "basecamp", Step: "HELIOS Core 준비 대기 중...", Progress: 8})
	log.Info().Msg("Step 3: Waiting for polyon-core to be healthy")

	deadline := time.Now().Add(90 * time.Second)
	client := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://polyon-core:8000/api/setup/status")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			log.Info().Msg("polyon-core is ready")
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(3 * time.Second)
	}
	log.Warn().Msg("polyon-core not ready after 90s — continuing anyway")
}

// step4_provisionKeycloak provisions Keycloak SSO.
func step4_provisionKeycloak(setupJSON map[string]interface{}, env map[string]string) {
	writeProgress(progress{Phase: "keycloak-provision", Step: "HELIOS Auth 프로비저닝 중...", Progress: 10})
	log.Info().Msg("Step 4: Provisioning Keycloak")

	kcUser := env["KC_BOOTSTRAP_ADMIN_USERNAME"]
	if kcUser == "" {
		kcUser = "keycloak"
	}
	kcPW := env["KC_ADMIN_PASSWORD"]
	kcBase := "http://polyon-auth:8080/auth"

	kc := newKCClient(kcBase)

	// Wait for Keycloak to be ready
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := kc.client.Get(kcBase + "/realms/master")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			log.Info().Msg("Keycloak is ready")
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(3 * time.Second)
	}

	// Login — try new PW first, then temp (bootstrap admin PW only applies on first boot)
	passwords := []string{kcPW, "temp-basecamp-init_1234"}
	loggedIn := false
	for attempt := 0; attempt < 5 && !loggedIn; attempt++ {
		for _, pw := range passwords {
			if err := kc.login(kcUser, pw); err == nil {
				kcPW = pw // remember which one worked
				loggedIn = true
				log.Info().Str("attempt", fmt.Sprintf("%d", attempt)).Msg("Keycloak login successful")
				break
			}
		}
		if !loggedIn {
			time.Sleep(5 * time.Second)
		}
	}
	if !loggedIn {
		fail("step4: keycloak login", fmt.Errorf("all password attempts failed"))
	}

	// Disable SSL on master realm
	resp, _ := kc.do("PUT", "/admin/realms/master", map[string]interface{}{
		"realm": "master", "sslRequired": "NONE",
	})
	drainClose(resp)

	// Create realm 'polyon'
	// frontendUrl: tells Keycloak what URL to use in token issuers (must match browser-facing URL)
	externalURL := os.Getenv("HELIOS_EXTERNAL_URL")
	if externalURL == "" {
		externalURL = "https://192.168.55.200:1111"
	}
	realmFrontendURL := externalURL + "/auth"
	resp, _ = kc.do("POST", "/admin/realms", map[string]interface{}{
		"realm": "polyon", "enabled": true, "displayName": "PolyON",
		"sslRequired": "NONE", "registrationAllowed": false,
		"loginWithEmailAllowed": true, "bruteForceProtected": true,
		"attributes": map[string]string{
			"frontendUrl": realmFrontendURL,
		},
	})
	drainClose(resp)

	// Set frontendUrl on master realm as well (for admin console links)
	resp, _ = kc.do("PUT", "/admin/realms/master", map[string]interface{}{
		"realm": "master", "sslRequired": "NONE",
		"attributes": map[string]string{
			"frontendUrl": realmFrontendURL,
		},
	})
	drainClose(resp)

	// Create client 'polyon-console'
	resp, _ = kc.do("POST", "/admin/realms/polyon/clients", map[string]interface{}{
		"clientId": "polyon-console", "name": "PolyON Console",
		"enabled": true, "publicClient": true,
		"standardFlowEnabled": true, "redirectUris": []string{"*"},
		"webOrigins": []string{"+"}, "protocol": "openid-connect",
		"attributes": map[string]string{"pkce.code.challenge.method": "S256"},
	})
	drainClose(resp)

	// Create admin role
	resp, _ = kc.do("POST", "/admin/realms/polyon/roles", map[string]interface{}{
		"name": "admin", "description": "HELIOS Administrator",
	})
	drainClose(resp)

	// Create admin user
	realm, _ := setupJSON["realm"].(string)
	polyonPW, _ := setupJSON["polyon_admin_password"].(string)
	resp, _ = kc.do("POST", "/admin/realms/polyon/users", map[string]interface{}{
		"username": "admin", "enabled": true,
		"email":         fmt.Sprintf("admin@%s", strings.ToLower(realm)),
		"emailVerified": true, "firstName": "PolyON", "lastName": "Admin",
		"credentials": []map[string]interface{}{
			{"type": "password", "value": polyonPW, "temporary": false},
		},
	})
	drainClose(resp)

	// Get user ID for role assignment
	resp, err := kc.do("GET", "/admin/realms/polyon/users?username=admin&exact=true", nil)
	if err == nil && resp.StatusCode == 200 {
		var users []struct {
			ID string `json:"id"`
		}
		json.NewDecoder(resp.Body).Decode(&users)
		resp.Body.Close()

		if len(users) > 0 {
			userID := users[0].ID

			// Get role ID
			roleResp, err := kc.do("GET", "/admin/realms/polyon/roles/admin", nil)
			if err == nil && roleResp.StatusCode == 200 {
				var role struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				json.NewDecoder(roleResp.Body).Decode(&role)
				roleResp.Body.Close()

				// Assign role to user
				assignResp, _ := kc.do("POST",
					fmt.Sprintf("/admin/realms/polyon/users/%s/role-mappings/realm", userID),
					[]map[string]interface{}{{"id": role.ID, "name": role.Name}},
				)
				drainClose(assignResp)
				log.Info().Str("userID", userID).Msg("Admin role assigned to admin user")
			}
		}
	} else if resp != nil {
		resp.Body.Close()
	}

	// Sync bootstrap admin password to match KC_ADMIN_PASSWORD
	// (so that after core restart, the password is consistent)
	syncResp, err := kc.do("PUT",
		fmt.Sprintf("/admin/realms/master/users/%s/reset-password", kcUser),
		map[string]interface{}{"type": "password", "value": kcPW, "temporary": false},
	)
	if err == nil {
		drainClose(syncResp)
		log.Info().Msg("Bootstrap admin password synced")
	}

	log.Info().Msg("Keycloak provisioning complete")
}

// step5_phase1 starts Phase 1 services: redis, es, rustfs.
func step5_phase1(hostDir string) {
	writeProgress(progress{Phase: "starting-services", Step: "Phase 1 — 기반 인프라 시작 중...", Progress: 20})
	log.Info().Msg("Step 5: Phase 1 — redis, es, rustfs")

	if err := dockerComposeServices(hostDir,
		"up", "-d", "--no-recreate",
		"polyon-redis", "polyon-search", "polyon-rustfs",
	); err != nil {
		log.Warn().Err(err).Msg("Phase 1 compose up error (may be ok)")
	}

	writeProgress(progress{Phase: "starting-services", Step: "Phase 1 — 기반 인프라 대기 중...", Progress: 25})
	waitHealthy([]string{"polyon-redis", "polyon-search", "polyon-rustfs"}, 120*time.Second)
	log.Info().Msg("Phase 1 done")
}

// step6_phase2 starts Phase 2 services: dc, mail, and waits for DC provisioning.
func step6_phase2(hostDir string) {
	writeProgress(progress{Phase: "starting-services", Step: "Phase 2 — 핵심 서비스 시작 중...", Progress: 35})
	log.Info().Msg("Step 6: Phase 2 — dc, mail")

	if err := dockerComposeServices(hostDir,
		"up", "-d", "--no-recreate",
		"polyon-dc", "polyon-mail",
	); err != nil {
		log.Warn().Err(err).Msg("Phase 2 compose up error (may be ok)")
	}

	// Wait for DC provisioning flag
	writeProgress(progress{Phase: "provisioning", Step: "AD 도메인 프로비저닝 중...", Progress: 40})
	log.Info().Msg("Waiting for DC provisioning...")
	dcFlag := "/shared/.polyon-provisioned"
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dcFlag); err == nil {
			log.Info().Msg("DC provisioning complete")
			break
		}
		time.Sleep(3 * time.Second)
	}

	writeProgress(progress{Phase: "starting-services", Step: "Phase 2 — 핵심 서비스 대기 중...", Progress: 50})
	waitHealthy([]string{"polyon-dc", "polyon-mail"}, 120*time.Second)

	// Sync DC admin password: entrypoint uses setup.json["admin_password"] as fallback,
	// but the user may have set a different dc_admin_password. Ensure they match.
	step6_syncDCPassword()

	log.Info().Msg("Phase 2 done")
}

// step6_syncDCPassword ensures the DC Administrator password matches setup.json["dc_admin_password"].
func step6_syncDCPassword() {
	setupJSON := "/shared/setup.json"
	data, err := os.ReadFile(setupJSON)
	if err != nil {
		log.Warn().Err(err).Msg("DC password sync: cannot read setup.json")
		return
	}
	var cfg struct {
		DCAdminPassword string `json:"dc_admin_password"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.DCAdminPassword == "" {
		log.Info().Msg("DC password sync: no dc_admin_password in setup.json, skipping")
		return
	}

	log.Info().Msg("DC password sync: setting Administrator password via samba-tool")
	cmd := exec.Command("docker", "exec", "polyon-dc",
		"samba-tool", "user", "setpassword", "Administrator",
		"--newpassword="+cfg.DCAdminPassword,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Warn().Err(err).Msg("DC password sync: samba-tool setpassword failed (may already be correct)")
	} else {
		log.Info().Msg("DC password sync: Administrator password updated successfully")
	}
}

// step7_initDB initializes the PostgreSQL database.
func step7_initDB(env map[string]string) {
	writeProgress(progress{Phase: "starting-services", Step: "DB 초기화 중...", Progress: 55})
	log.Info().Msg("Step 7: Initializing DB")

	dbUser := "polyon"
	dbPW := env["DB_PASSWORD"]
	dbHost := "polyon-db"
	dbName := "polyon"
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", dbUser, dbPW, dbHost, dbName)

	// Wait for DB to be ready
	var conn *pgx.Conn
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err = pgx.Connect(ctx, dbURL)
		cancel()
		if err == nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		fail("step7: db connect", err)
	}
	defer conn.Close(context.Background())

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS audit_log (
			id SERIAL PRIMARY KEY,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			action VARCHAR(50) NOT NULL,
			object_type VARCHAR(30) NOT NULL,
			object_name VARCHAR(255) NOT NULL,
			actor VARCHAR(100) DEFAULT 'Administrator',
			details TEXT DEFAULT '',
			ip_address VARCHAR(45) DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS sentinel_alerts (
			id SERIAL PRIMARY KEY,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			level VARCHAR(20) NOT NULL,
			source VARCHAR(50) DEFAULT 'sentinel',
			service VARCHAR(100) NOT NULL,
			message TEXT NOT NULL,
			details JSONB,
			acknowledged BOOLEAN DEFAULT FALSE,
			ack_note TEXT,
			ack_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_object_type ON audit_log(object_type)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_timestamp ON sentinel_alerts(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_level ON sentinel_alerts(level)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_service ON sentinel_alerts(service)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_acked ON sentinel_alerts(acknowledged)`,
	}

	for _, m := range migrations {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := conn.Exec(ctx, m)
		cancel()
		if err != nil {
			log.Warn().Err(err).Msg("Migration failed (may be ok if already exists)")
		}
	}

	log.Info().Msg("DB initialization complete")
}

// step8_provisionMail calls the mail provision API on polyon-core.
func step8_provisionMail() {
	writeProgress(progress{Phase: "starting-services", Step: "Mail 서버 프로비저닝 중...", Progress: 60})
	log.Info().Msg("Step 8: Provisioning mail")

	client := &http.Client{Timeout: 30 * time.Second}

	// Wait a bit for core to be ready
	time.Sleep(5 * time.Second)

	for i := 0; i < 10; i++ {
		resp, err := client.Post(
			"http://polyon-core:8000/api/v1/mail/provision",
			"application/json",
			strings.NewReader("{}"),
		)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Info().Str("response", string(body)).Msg("Mail provision response")
			break
		}
		log.Warn().Err(err).Int("attempt", i+1).Msg("Mail provision attempt failed")
		time.Sleep(5 * time.Second)
	}
}

// step9_phase3 starts Phase 3 services: prometheus, exporters, grafana.
func step9_phase3(hostDir string) {
	writeProgress(progress{Phase: "starting-services", Step: "Phase 3 — 모니터링 시작 중...", Progress: 65})
	log.Info().Msg("Step 9: Phase 3 — prometheus, exporters, grafana")

	if err := dockerComposeServices(hostDir,
		"up", "-d", "--no-recreate",
		"polyon-prometheus", "polyon-pg-exporter", "polyon-redis-exporter",
		"polyon-search-exporter", "polyon-grafana",
	); err != nil {
		log.Warn().Err(err).Msg("Phase 3 compose up error (may be ok)")
	}

	writeProgress(progress{Phase: "starting-services", Step: "Phase 3 — 모니터링 대기 중...", Progress: 75})
	waitHealthy([]string{
		"polyon-prometheus", "polyon-pg-exporter", "polyon-redis-exporter",
		"polyon-search-exporter", "polyon-grafana",
	}, 120*time.Second)
	log.Info().Msg("Phase 3 done")
}

// step10_phase4 starts Phase 4 services: pgadmin, redisinsight, elasticvue, sentinel.
func step10_phase4(hostDir string) {
	writeProgress(progress{Phase: "starting-services", Step: "Phase 4 — 관리 도구 시작 중...", Progress: 80})
	log.Info().Msg("Step 10: Phase 4 — pgadmin, redisinsight, elasticvue, sentinel")

	if err := dockerComposeServices(hostDir,
		"up", "-d", "--no-recreate",
		"polyon-pgadmin", "polyon-redisinsight", "polyon-elasticvue", "polyon-sentinel",
	); err != nil {
		log.Warn().Err(err).Msg("Phase 4 compose up error (may be ok)")
	}

	writeProgress(progress{Phase: "starting-services", Step: "Phase 4 — 관리 도구 대기 중...", Progress: 88})
	waitHealthy([]string{
		"polyon-pgadmin", "polyon-redisinsight", "polyon-elasticvue", "polyon-sentinel",
	}, 120*time.Second)
	log.Info().Msg("Phase 4 done")
}

// step11_final writes final progress and marks setup complete.
func step11_final() {
	log.Info().Msg("Step 11: Final verification")

	allContainers := []string{
		"polyon-db", "polyon-proxy", "polyon-auth", "polyon-core", "polyon-console",
		"polyon-redis", "polyon-search", "polyon-rustfs",
		"polyon-dc", "polyon-mail",
		"polyon-prometheus", "polyon-pg-exporter", "polyon-redis-exporter",
		"polyon-search-exporter", "polyon-grafana",
		"polyon-pgadmin", "polyon-redisinsight", "polyon-elasticvue", "polyon-sentinel",
	}

	m := containerStatusMap()
	ready := 0
	for _, name := range allContainers {
		if isHealthy(m[name]) || isUp(m[name]) {
			ready++
		}
	}
	total := len(allContainers)

	if ready == total {
		writeProgress(progress{Phase: "complete", Step: "모든 서비스가 준비되었습니다", Progress: 100})
		log.Info().Msg("Setup complete!")
	} else {
		writeProgress(progress{
			Phase:    "complete",
			Step:     fmt.Sprintf("Setup 완료 (%d/%d 서비스 준비됨)", ready, total),
			Progress: 95 + ready*5/total,
		})
		log.Info().Int("ready", ready).Int("total", total).Msg("Setup complete with some services not yet ready")
	}

	// Write provisioned flag
	provisionedFlag := "/shared/.polyon-provisioned"
	if _, err := os.Stat(provisionedFlag); os.IsNotExist(err) {
		os.WriteFile(provisionedFlag, []byte(time.Now().Format(time.RFC3339)), 0644)
	}
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Caller().Logger()

	log.Info().Msg("=== PolyON Setup Runner starting ===")

	hostDir := os.Getenv("HOST_PROJECT_DIR")
	if hostDir == "" {
		hostDir = "/Users/jupiter/.openclaw/workspace/polyon"
	}
	log.Info().Str("hostDir", hostDir).Msg("Host project dir")

	// Ensure shared dir exists
	os.MkdirAll("/shared", 0755)

	// Create migrations dir placeholder if needed
	os.MkdirAll(filepath.Join("/app", "migrations"), 0755)

	writeProgress(progress{Phase: "starting", Step: "Setup Runner 시작 중...", Progress: 1})

	// Step 1: Read config
	setupJSON, env := step1_readConfig()

	// Step 2: Recreate Basecamp
	step2_recreateBasecamp(hostDir)

	// Step 3: Wait for Core
	step3_waitCore()

	// Step 4: Provision Keycloak
	step4_provisionKeycloak(setupJSON, env)

	// Step 5: Phase 1
	step5_phase1(hostDir)

	// Step 6: Phase 2
	step6_phase2(hostDir)

	// Step 7: Init DB
	step7_initDB(env)

	// Step 8: Provision Mail
	step8_provisionMail()

	// Step 9: Phase 3
	step9_phase3(hostDir)

	// Step 10: Phase 4
	step10_phase4(hostDir)

	// Step 11: Final
	step11_final()

	log.Info().Msg("=== PolyON Setup Runner complete ===")
}
