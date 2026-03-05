package traefik

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// defaultInfraServices returns the canonical InfraService list mirroring the DB seed.
func defaultInfraServices() []InfraService {
	return []InfraService{
		// ── infra ──
		{
			ID: "sentinel", Name: "Sentinel", Host: "polyon-sentinel", Port: 8080,
			Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
			PathRules: []PathRule{
				{Path: "/api/sentinel", Priority: 200},
				{Path: "/api/setup", Priority: 150},
				{Path: "/api/reset", Priority: 150},
			},
			Enabled: true,
		},
		{
			ID: "ui", Name: "Console UI", Host: "polyon-console", Port: 80,
			Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
			PathRules: []PathRule{
				{Path: "/", Priority: 1},
			},
			Enabled: true,
		},
		{
			ID: "core", Name: "Core API", Host: "polyon-core", Port: 8000,
			Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
			PathRules: []PathRule{
				{Path: "/api/v1", Priority: 120},
				{Path: "/health", Priority: 110},
				{Path: "/mail-proxy", Priority: 110},
				{Path: "/es-proxy", Priority: 110},
				{Path: "/api/alerts", Priority: 110},
			},
			Enabled: true,
		},
		{
			ID: "auth", Name: "Keycloak SSO", Host: "polyon-auth", Port: 8080,
			Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
			PathRules: []PathRule{
				{Path: "/auth", Priority: 100},
			},
			Enabled: true,
		},

		// ── monitoring ──
		{
			ID: "keycloak-admin", Name: "Keycloak Admin", Host: "polyon-auth", Port: 8080,
			Protocol: "http", Category: "monitoring", EntryPoint: "keycloak", Enabled: true,
		},
		{
			ID: "stalwart-admin", Name: "Stalwart Admin", Host: "polyon-mail", Port: 8080,
			Protocol: "http", Category: "monitoring", EntryPoint: "stalwart-admin", Enabled: true,
		},
		{
			ID: "grafana", Name: "Grafana", Host: "polyon-grafana", Port: 3000,
			Protocol: "http", Category: "monitoring", EntryPoint: "grafana", Enabled: true,
		},
		{
			ID: "rustfs", Name: "RustFS Console", Host: "polyon-rustfs", Port: 9001,
			Protocol: "http", Category: "monitoring", EntryPoint: "rustfs", Enabled: true,
		},
		{
			ID: "elasticvue", Name: "Elasticvue", Host: "polyon-elasticvue", Port: 8080,
			Protocol: "http", Category: "monitoring", EntryPoint: "elasticvue", Enabled: true,
		},
		{
			ID: "redisinsight", Name: "RedisInsight", Host: "polyon-redisinsight", Port: 5540,
			Protocol: "http", Category: "monitoring", EntryPoint: "redisinsight", Enabled: true,
		},

		// ── mail-tcp ──
		{
			ID: "mail-smtp", Name: "SMTP", Host: "polyon-mail", Port: 25,
			Protocol: "tcp", Category: "mail-tcp", EntryPoint: "smtp", Enabled: true,
		},
		{
			ID: "mail-submission", Name: "Submission", Host: "polyon-mail", Port: 587,
			Protocol: "tcp", Category: "mail-tcp", EntryPoint: "submission", Enabled: true,
		},
		{
			ID: "mail-imaps", Name: "IMAPS", Host: "polyon-mail", Port: 993,
			Protocol: "tcp", Category: "mail-tcp", EntryPoint: "imaps", Enabled: true,
		},
		{
			ID: "mail-managesieve", Name: "ManageSieve", Host: "polyon-mail", Port: 4190,
			Protocol: "tcp", Category: "mail-tcp", EntryPoint: "managesieve", Enabled: true,
		},
	}
}

// testConfig returns a fully-populated GeneratorConfig pointing at a
// temporary directory so tests never touch the real filesystem.
func testConfig(t *testing.T) GeneratorConfig {
	t.Helper()
	dir := t.TempDir()
	return GeneratorConfig{
		OutputDir:      dir,
		BaseDomain:     "cmars.com",
		ConsoleDomain:  "console.cmars.com",
		PortalDomain:   "portal.cmars.com",
		MailDomain:     "mail.cmars.kr",
		CertFile:       "/traefik-dynamic/certs/wildcard.crt",
		KeyFile:        "/traefik-dynamic/certs/wildcard.key",
		ForwardAuthURL: "http://polyon-core:8000/api/internal/auth/verify",
		Apps: []AppRoute{
			{ID: "chat", Subdomain: "chat", BackendURL: "http://polyon-chat:8065"},
			{ID: "wiki", Subdomain: "wiki", BackendURL: "http://app-affine:3010"},
		},
		InfraServices: defaultInfraServices(),
		MailEnabled:   true,
	}
}

// readYAML unmarshals the named file from dir into v.
func readYAML(t *testing.T, dir, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("readYAML %s: %v", name, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		t.Fatalf("readYAML %s: unmarshal: %v", name, err)
	}
}

// ── Generate (all files) ─────────────────────────────────────────────────────

func TestGenerate_AllFilesExist(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := []string{
		"00-infra.yml",
		"10-apps.yml",
		"20-auth.yml",
		"30-monitoring.yml",
		"40-mail-tcp.yml",
		"50-tls.yml",
	}
	for _, f := range want {
		path := filepath.Join(cfg.OutputDir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s: %v", f, err)
		}
	}
}

func TestGenerate_HeaderComment(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range []string{"00-infra.yml", "10-apps.yml", "20-auth.yml", "30-monitoring.yml", "40-mail-tcp.yml", "50-tls.yml"} {
		data, err := os.ReadFile(filepath.Join(cfg.OutputDir, f))
		if err != nil {
			t.Fatalf("%s: read: %v", f, err)
		}
		if string(data[:len(fileHeader)]) != fileHeader {
			t.Errorf("%s: missing header comment", f)
		}
	}
}

// ── 00-infra.yml ─────────────────────────────────────────────────────────────

func TestGenerate00Infra_ParsesOK(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	type infraFile struct {
		HTTP struct {
			Middlewares map[string]any `yaml:"middlewares"`
			Routers     map[string]any `yaml:"routers"`
			Services    map[string]any `yaml:"services"`
		} `yaml:"http"`
	}
	var f infraFile
	readYAML(t, cfg.OutputDir, "00-infra.yml", &f)

	if _, ok := f.HTTP.Middlewares["https-headers"]; !ok {
		t.Error("00-infra: missing middleware https-headers")
	}

	// Console/Portal domain routers
	requiredRouters := []string{
		"console-domain", "console-domain-auth", "console-domain-api",
		"portal", "portal-auth", "portal-api",
	}
	for _, r := range requiredRouters {
		if _, ok := f.HTTP.Routers[r]; !ok {
			t.Errorf("00-infra: missing router %q", r)
		}
	}

	// Management port :1111 routers generated from PathRules
	// sentinel has 3 rules → "sentinel", "sentinel-1", "sentinel-2"
	mgmtRouters := []string{
		"sentinel",   // /api/sentinel
		"sentinel-1", // /api/setup
		"sentinel-2", // /api/reset
		"core",       // /api/v1
		"core-1",     // /health
		"core-2",     // /mail-proxy
		"core-3",     // /es-proxy
		"core-4",     // /api/alerts
		"auth",       // /auth
		"ui",         // /
	}
	for _, r := range mgmtRouters {
		if _, ok := f.HTTP.Routers[r]; !ok {
			t.Errorf("00-infra: missing management router %q", r)
		}
	}

	// Services derived from DB
	requiredServices := []string{"sentinel", "ui", "core", "auth"}
	for _, s := range requiredServices {
		if _, ok := f.HTTP.Services[s]; !ok {
			t.Errorf("00-infra: missing service %q", s)
		}
	}
}

func TestGenerate00Infra_NoHardcodedURLs(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Read generated file and check no hardcoded hostnames/ports exist
	// (all values should come from InfraServices)
	type infraFile struct {
		HTTP struct {
			Services map[string]serviceConfig `yaml:"services"`
		} `yaml:"http"`
	}
	var f infraFile
	readYAML(t, cfg.OutputDir, "00-infra.yml", &f)

	// Verify services use host/port from InfraServices, not hardcoded
	if svc, ok := f.HTTP.Services["core"]; ok {
		if len(svc.LoadBalancer.Servers) == 0 {
			t.Error("00-infra: core service has no servers")
		} else {
			wantURL := "http://polyon-core:8000"
			if svc.LoadBalancer.Servers[0].URL != wantURL {
				t.Errorf("00-infra: core service URL = %q, want %q", svc.LoadBalancer.Servers[0].URL, wantURL)
			}
		}
	}
}

func TestGenerate00Infra_EmptyInfraServices(t *testing.T) {
	cfg := testConfig(t)
	cfg.InfraServices = nil
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Should still produce a valid file (just with no dynamic services)
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "00-infra.yml")); err != nil {
		t.Errorf("00-infra.yml should exist even with no InfraServices: %v", err)
	}
}

// ── 10-apps.yml ──────────────────────────────────────────────────────────────

func TestGenerate10Apps_PerApp(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	type appsFile struct {
		HTTP struct {
			Routers  map[string]routerConfig  `yaml:"routers"`
			Services map[string]serviceConfig `yaml:"services"`
		} `yaml:"http"`
	}
	var f appsFile
	readYAML(t, cfg.OutputDir, "10-apps.yml", &f)

	for _, app := range cfg.Apps {
		rKey := "app-" + sanitizeKey(app.ID)
		sKey := rKey + "-svc"
		r, ok := f.HTTP.Routers[rKey]
		if !ok {
			t.Errorf("10-apps: missing router %q", rKey)
			continue
		}
		if r.Service != sKey {
			t.Errorf("10-apps router %q: service = %q, want %q", rKey, r.Service, sKey)
		}
		if r.TLS == nil {
			t.Errorf("10-apps router %q: TLS should not be nil", rKey)
		}
		svc, ok := f.HTTP.Services[sKey]
		if !ok {
			t.Errorf("10-apps: missing service %q", sKey)
			continue
		}
		if len(svc.LoadBalancer.Servers) == 0 || svc.LoadBalancer.Servers[0].URL != app.BackendURL {
			t.Errorf("10-apps service %q: url = %q, want %q", sKey,
				svc.LoadBalancer.Servers[0].URL, app.BackendURL)
		}
	}
}

func TestGenerate10Apps_EmptyApps(t *testing.T) {
	cfg := testConfig(t)
	cfg.Apps = nil
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// File should still exist but routers/services may be empty
	if _, err := os.Stat(filepath.Join(cfg.OutputDir, "10-apps.yml")); err != nil {
		t.Errorf("10-apps.yml should exist even with no apps: %v", err)
	}
}

// ── 20-auth.yml ──────────────────────────────────────────────────────────────

func TestGenerate20Auth_ForwardAuthAndWildcard(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	type authFile struct {
		HTTP struct {
			Middlewares map[string]middlewareConfig `yaml:"middlewares"`
			Routers     map[string]routerConfig     `yaml:"routers"`
		} `yaml:"http"`
	}
	var f authFile
	readYAML(t, cfg.OutputDir, "20-auth.yml", &f)

	mw, ok := f.HTTP.Middlewares["forward-auth"]
	if !ok {
		t.Fatal("20-auth: missing middleware forward-auth")
	}
	if mw.ForwardAuth == nil {
		t.Fatal("20-auth: forwardAuth config is nil")
	}
	if mw.ForwardAuth.Address != cfg.ForwardAuthURL {
		t.Errorf("20-auth: forwardAuth.address = %q, want %q", mw.ForwardAuth.Address, cfg.ForwardAuthURL)
	}

	wc, ok := f.HTTP.Routers["wildcard-catchall"]
	if !ok {
		t.Fatal("20-auth: missing router wildcard-catchall")
	}
	if wc.Priority != 1 {
		t.Errorf("20-auth: wildcard priority = %d, want 1", wc.Priority)
	}
}

// ── 30-monitoring.yml ────────────────────────────────────────────────────────

func TestGenerate30Monitoring_RequiredRouters(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	type monFile struct {
		HTTP struct {
			Routers  map[string]any `yaml:"routers"`
			Services map[string]any `yaml:"services"`
		} `yaml:"http"`
	}
	var f monFile
	readYAML(t, cfg.OutputDir, "30-monitoring.yml", &f)

	routers := []string{"keycloak-admin", "stalwart-admin", "grafana", "rustfs", "elasticvue", "redisinsight", "traefik-dashboard", "traefik-api-internal"}
	for _, r := range routers {
		if _, ok := f.HTTP.Routers[r]; !ok {
			t.Errorf("30-monitoring: missing router %q", r)
		}
	}

	services := []string{"keycloak-admin-svc", "stalwart-admin-svc", "grafana-svc", "rustfs-svc", "elasticvue-svc", "redisinsight-svc"}
	for _, s := range services {
		if _, ok := f.HTTP.Services[s]; !ok {
			t.Errorf("30-monitoring: missing service %q", s)
		}
	}
}

// ── 40-mail-tcp.yml ──────────────────────────────────────────────────────────

func TestGenerate40MailTCP_WhenEnabled(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	type mailFile struct {
		TCP tcpConfig `yaml:"tcp"`
	}
	var f mailFile
	readYAML(t, cfg.OutputDir, "40-mail-tcp.yml", &f)

	required := []string{"smtp", "submission", "imaps", "managesieve"}
	for _, r := range required {
		if _, ok := f.TCP.Routers[r]; !ok {
			t.Errorf("40-mail: missing router %q", r)
		}
		if _, ok := f.TCP.Services[r]; !ok {
			t.Errorf("40-mail: missing service %q", r)
		}
	}

	imaps := f.TCP.Routers["imaps"]
	if imaps.TLS == nil || !imaps.TLS.Passthrough {
		t.Error("40-mail: imaps should have tls.passthrough=true")
	}
}

func TestGenerate40MailTCP_WhenDisabled_FileAbsent(t *testing.T) {
	cfg := testConfig(t)
	cfg.MailEnabled = false
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(cfg.OutputDir, "40-mail-tcp.yml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("40-mail-tcp.yml should not exist when MailEnabled=false")
	}
}

func TestGenerate40MailTCP_EmptyMailServices_FileAbsent(t *testing.T) {
	cfg := testConfig(t)
	cfg.MailEnabled = true
	// Remove all mail-tcp services
	var filtered []InfraService
	for _, s := range cfg.InfraServices {
		if s.Category != "mail-tcp" {
			filtered = append(filtered, s)
		}
	}
	cfg.InfraServices = filtered
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(cfg.OutputDir, "40-mail-tcp.yml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("40-mail-tcp.yml should not exist when no mail-tcp services in InfraServices")
	}
}

// ── 50-tls.yml ───────────────────────────────────────────────────────────────

func TestGenerate50TLS_CertStore(t *testing.T) {
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var f tlsFileConfig
	readYAML(t, cfg.OutputDir, "50-tls.yml", &f)

	store, ok := f.TLS.Stores["default"]
	if !ok {
		t.Fatal("50-tls: missing TLS store 'default'")
	}
	if store.DefaultCertificate == nil {
		t.Fatal("50-tls: defaultCertificate is nil")
	}
	if store.DefaultCertificate.CertFile != cfg.CertFile {
		t.Errorf("50-tls: certFile = %q, want %q", store.DefaultCertificate.CertFile, cfg.CertFile)
	}
	if store.DefaultCertificate.KeyFile != cfg.KeyFile {
		t.Errorf("50-tls: keyFile = %q, want %q", store.DefaultCertificate.KeyFile, cfg.KeyFile)
	}
}

func TestGenerate50TLS_NoCert_FileAbsent(t *testing.T) {
	cfg := testConfig(t)
	cfg.CertFile = ""
	cfg.KeyFile = ""
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(cfg.OutputDir, "50-tls.yml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("50-tls.yml should not exist when CertFile/KeyFile are empty")
	}
}

// ── Manager.Regenerate ───────────────────────────────────────────────────────

func TestManager_Regenerate(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	cfg := testConfig(t)
	cfg.OutputDir = dir // Manager overrides this anyway

	if err := mgr.Regenerate(cfg); err != nil {
		t.Fatalf("Manager.Regenerate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "00-infra.yml")); err != nil {
		t.Errorf("00-infra.yml should exist after Regenerate")
	}
}

func TestManager_RegenerateApps(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	apps := []AppRoute{
		{ID: "vault", Subdomain: "vault", BackendURL: "http://app-vaultwarden:80"},
	}
	if err := mgr.RegenerateApps("cmars.com", apps); err != nil {
		t.Fatalf("Manager.RegenerateApps: %v", err)
	}

	type appsFile struct {
		HTTP struct {
			Routers map[string]routerConfig `yaml:"routers"`
		} `yaml:"http"`
	}
	var f appsFile
	readYAML(t, dir, "10-apps.yml", &f)
	if _, ok := f.HTTP.Routers["app-vault"]; !ok {
		t.Error("RegenerateApps: missing router app-vault")
	}
}

func TestManager_RegenerateTLS(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	cert := "/certs/wc.crt"
	key := "/certs/wc.key"
	if err := mgr.RegenerateTLS(cert, key); err != nil {
		t.Fatalf("Manager.RegenerateTLS: %v", err)
	}

	var f tlsFileConfig
	readYAML(t, dir, "50-tls.yml", &f)
	store := f.TLS.Stores["default"]
	if store.DefaultCertificate == nil {
		t.Fatal("RegenerateTLS: defaultCertificate is nil")
	}
	if store.DefaultCertificate.CertFile != cert {
		t.Errorf("RegenerateTLS: certFile = %q, want %q", store.DefaultCertificate.CertFile, cert)
	}
}

// ── Atomic write (temp→rename) ───────────────────────────────────────────────

func TestGenerate_AtomicWrite(t *testing.T) {
	// Verify no .tmp files are left behind after a successful Generate.
	cfg := testConfig(t)
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	entries, err := os.ReadDir(cfg.OutputDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("stale tmp file found: %s", e.Name())
		}
	}
}

// ── filterByCategory helper ──────────────────────────────────────────────────

func TestFilterByCategory(t *testing.T) {
	svcs := defaultInfraServices()

	infra := filterByCategory(svcs, "infra")
	if len(infra) != 4 {
		t.Errorf("filterByCategory(infra): got %d, want 4", len(infra))
	}

	monitoring := filterByCategory(svcs, "monitoring")
	if len(monitoring) != 6 {
		t.Errorf("filterByCategory(monitoring): got %d, want 6", len(monitoring))
	}

	mailTCP := filterByCategory(svcs, "mail-tcp")
	if len(mailTCP) != 4 {
		t.Errorf("filterByCategory(mail-tcp): got %d, want 4", len(mailTCP))
	}
}
