// Package traefik manages the Traefik dynamic configuration file for
// site domain routing. It writes/reads a YAML file that Traefik watches
// via its file provider.
package traefik

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// defaultOutputDir is the default directory Core writes Traefik dynamic configs.
// Must match the file provider directory in traefik.yml.
const defaultOutputDir = "/traefik-dynamic"

// defaultDynamicPath is kept for backward compatibility with code that
// relied on the old single-file approach.
const defaultDynamicPath = "/runtime/traefik/dynamic-sites.yml"

// Manager manages Traefik dynamic configuration files.
// The new Regenerate* methods write the full set of numbered YAML files;
// the legacy single-file methods are preserved for backward compatibility.
type Manager struct {
	mu        sync.Mutex
	outputDir string // directory for numbered YAML files (Regenerate*)
	path      string // legacy single-file path (SetDomain etc.)
}

// NewManager creates a Manager.
//   - outputDir: directory for numbered config files (default: /traefik-dynamic)
//
// The legacy single-file path defaults to defaultDynamicPath and is only
// used by the backward-compatible Set* methods.
func NewManager(outputDir string) *Manager {
	if outputDir == "" {
		outputDir = defaultOutputDir
	}
	return &Manager{
		outputDir: outputDir,
		path:      defaultDynamicPath,
	}
}

// ── YAML structures ──────────────────────────────────────────────────────────

type dynamicConfig struct {
	HTTP httpConfig         `yaml:"http"`
	TLS  *tlsTopLevelConfig `yaml:"tls,omitempty"`
}

type tlsTopLevelConfig struct {
	Stores map[string]tlsStoreConfig `yaml:"stores,omitempty"`
}

type httpConfig struct {
	Routers     map[string]routerConfig     `yaml:"routers,omitempty"`
	Services    map[string]serviceConfig    `yaml:"services,omitempty"`
	Middlewares map[string]middlewareConfig `yaml:"middlewares,omitempty"`
}

type routerConfig struct {
	Rule        string     `yaml:"rule"`
	EntryPoints []string   `yaml:"entryPoints"`
	Service     string     `yaml:"service"`
	TLS         *tlsConfig `yaml:"tls,omitempty"`
	Priority    int        `yaml:"priority,omitempty"`
	Middlewares []string   `yaml:"middlewares,omitempty"`
}

type tlsConfig struct {
	CertResolver string   `yaml:"certResolver,omitempty"`
	Stores       []string `yaml:"stores,omitempty"`
}

// middlewareConfig holds Traefik middleware definitions.
type middlewareConfig struct {
	ForwardAuth *forwardAuthConfig `yaml:"forwardAuth,omitempty"`
	Headers     *headersConfig     `yaml:"headers,omitempty"`
}

type forwardAuthConfig struct {
	Address             string   `yaml:"address"`
	TrustForwardHeader  bool     `yaml:"trustForwardHeader"`
	AuthResponseHeaders []string `yaml:"authResponseHeaders,omitempty"`
}

// headersConfig represents the Traefik headers middleware.
type headersConfig struct {
	CustomRequestHeaders map[string]string `yaml:"customRequestHeaders,omitempty"`
}

type tlsStoreConfig struct {
	DefaultCertificate *tlsCertRef `yaml:"defaultCertificate,omitempty"`
}

type tlsCertRef struct {
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
}

type serviceConfig struct {
	LoadBalancer lbConfig `yaml:"loadBalancer"`
}

type lbConfig struct {
	Servers []serverConfig `yaml:"servers"`
}

type serverConfig struct {
	URL string `yaml:"url"`
}

// ── New Regenerate API ───────────────────────────────────────────────────────

// Regenerate regenerates all dynamic config files from cfg.
func (m *Manager) Regenerate(cfg GeneratorConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg.OutputDir = m.outputDir
	if err := Generate(cfg); err != nil {
		return fmt.Errorf("traefik regenerate: %w", err)
	}
	log.Info().Str("outputDir", m.outputDir).Msg("traefik: dynamic config regenerated")
	return nil
}

// RegenerateApps regenerates only 10-apps.yml using the provided app list.
// All other fields are taken from a minimal GeneratorConfig.
func (m *Manager) RegenerateApps(baseDomain string, apps []AppRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := GeneratorConfig{
		OutputDir:  m.outputDir,
		BaseDomain: baseDomain,
		Apps:       apps,
	}
	if err := generate10Apps(cfg); err != nil {
		return fmt.Errorf("traefik regenerate apps: %w", err)
	}
	log.Info().Int("count", len(apps)).Msg("traefik: 10-apps.yml regenerated")
	return nil
}

// RegenerateTLS regenerates only 50-tls.yml.
func (m *Manager) RegenerateTLS(certFile, keyFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := GeneratorConfig{
		OutputDir: m.outputDir,
		CertFile:  certFile,
		KeyFile:   keyFile,
	}
	if err := generate50TLS(cfg); err != nil {
		return fmt.Errorf("traefik regenerate tls: %w", err)
	}
	log.Info().Str("certFile", certFile).Msg("traefik: 50-tls.yml regenerated")
	return nil
}

// ── Legacy single-file API (backward compatibility) ──────────────────────────
// These methods continue to operate on m.path (the old single-file approach).
// New callers should use Regenerate / RegenerateApps / RegenerateTLS.

// SetDomain adds (or updates) a Traefik router for the given site domain.
func (m *Manager) SetDomain(siteID, domain, slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	routerKey := "site-" + sanitizeKey(siteID)
	cfg.HTTP.Routers[routerKey] = routerConfig{
		Rule:        fmt.Sprintf("Host(`%s`)", domain),
		EntryPoints: []string{"websecure"},
		Service:     "polyon-console",
		Priority:    50,
		Middlewares: []string{"https-headers"},
		TLS: &tlsConfig{
			CertResolver: "letsencrypt",
		},
	}

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("site", siteID).Str("domain", domain).Msg("traefik: domain route added")
	return nil
}

// RemoveDomain removes the Traefik router for the given site.
func (m *Manager) RemoveDomain(siteID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	routerKey := "site-" + sanitizeKey(siteID)
	delete(cfg.HTTP.Routers, routerKey)

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("site", siteID).Msg("traefik: domain route removed")
	return nil
}

// SetAppDomain adds (or updates) a Traefik router+service for an app subdomain.
func (m *Manager) SetAppDomain(appID, subdomain, baseDomain, backendURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	routerKey := "app-" + sanitizeKey(appID)
	serviceKey := "app-" + sanitizeKey(appID) + "-svc"

	cfg.HTTP.Routers[routerKey] = routerConfig{
		Rule:        fmt.Sprintf("Host(`%s.%s`)", subdomain, baseDomain),
		EntryPoints: []string{"websecure"},
		Service:     serviceKey,
		Middlewares: []string{"forward-auth"},
		TLS:         &tlsConfig{},
	}

	cfg.HTTP.Services[serviceKey] = serviceConfig{
		LoadBalancer: lbConfig{
			Servers: []serverConfig{{URL: backendURL}},
		},
	}

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("app", appID).Str("subdomain", subdomain).Str("baseDomain", baseDomain).Msg("traefik: app domain route added")
	return nil
}

// RemoveAppDomain removes the Traefik router and service for an app.
func (m *Manager) RemoveAppDomain(appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	routerKey := "app-" + sanitizeKey(appID)
	serviceKey := "app-" + sanitizeKey(appID) + "-svc"
	delete(cfg.HTTP.Routers, routerKey)
	delete(cfg.HTTP.Services, serviceKey)

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("app", appID).Msg("traefik: app domain route removed")
	return nil
}

// SetWildcardRouter adds a low-priority catch-all router for all subdomains of baseDomain.
func (m *Manager) SetWildcardRouter(baseDomain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	cfg.HTTP.Routers["wildcard-catchall"] = routerConfig{
		Rule:        fmt.Sprintf("HostRegexp(`{host:.+}.%s`)", baseDomain),
		EntryPoints: []string{"websecure"},
		Service:     "polyon-console",
		Priority:    1,
		Middlewares: []string{"forward-auth"},
		TLS:         &tlsConfig{},
	}

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("baseDomain", baseDomain).Msg("traefik: wildcard router added")
	return nil
}

// SetForwardAuthMiddleware configures the forward-auth middleware pointing at the given authURL.
func (m *Manager) SetForwardAuthMiddleware(authURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	cfg.HTTP.Middlewares["forward-auth"] = middlewareConfig{
		ForwardAuth: &forwardAuthConfig{
			Address:             authURL,
			TrustForwardHeader:  true,
			AuthResponseHeaders: []string{"X-Auth-User", "X-Auth-Realm"},
		},
	}

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("authURL", authURL).Msg("traefik: forward-auth middleware configured")
	return nil
}

// SetTLSStore configures Traefik's default TLS store with a certificate file pair.
func (m *Manager) SetTLSStore(certFile, keyFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.load()
	if err != nil {
		return fmt.Errorf("traefik load: %w", err)
	}

	if cfg.TLS == nil {
		cfg.TLS = &tlsTopLevelConfig{}
	}
	if cfg.TLS.Stores == nil {
		cfg.TLS.Stores = make(map[string]tlsStoreConfig)
	}

	cfg.TLS.Stores["default"] = tlsStoreConfig{
		DefaultCertificate: &tlsCertRef{
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	if err := m.save(cfg); err != nil {
		return fmt.Errorf("traefik save: %w", err)
	}
	log.Info().Str("certFile", certFile).Msg("traefik: TLS store configured")
	return nil
}

// ── Internal helpers (legacy single-file) ────────────────────────────────────

func (m *Manager) load() (*dynamicConfig, error) {
	cfg := &dynamicConfig{}
	cfg.HTTP.Routers = make(map[string]routerConfig)
	cfg.HTTP.Services = make(map[string]serviceConfig)
	cfg.HTTP.Middlewares = make(map[string]middlewareConfig)

	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // start fresh
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if cfg.HTTP.Routers == nil {
		cfg.HTTP.Routers = make(map[string]routerConfig)
	}
	if cfg.HTTP.Services == nil {
		cfg.HTTP.Services = make(map[string]serviceConfig)
	}
	if cfg.HTTP.Middlewares == nil {
		cfg.HTTP.Middlewares = make(map[string]middlewareConfig)
	}
	return cfg, nil
}

func (m *Manager) save(cfg *dynamicConfig) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(m.path, []byte(fileHeader+string(data)), 0o644)
}

// sanitizeKey makes a string safe for use as a YAML map key.
func sanitizeKey(s string) string {
	return strings.ReplaceAll(s, ".", "-")
}
