// Package config provides unified configuration from env + JSON files.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// InfraServiceEntry is a lightweight representation of a DB-backed infra service.
// Used by ApplyInfraServices to overwrite URL fields after DB connection is established.
type InfraServiceEntry struct {
	ID   string
	Host string
	Port int
}

// Config is the single source of truth for all PolyON settings.
type Config struct {
	// Server
	Port int `json:"port"`

	// Paths
	SharedDir  string `json:"shared_dir"`
	PolyonDir  string `json:"polyon_dir"`
	SecretsDir string `json:"secrets_dir"`

	// AD/LDAP (from setup.json)
	Realm         string `json:"realm"`
	Domain        string `json:"domain"` // NetBIOS
	DNSForwarder  string `json:"dns_forwarder"`
	OrgName       string `json:"org_name"`
	FunctionLevel string `json:"function_level"`
	EnableMail    bool   `json:"enable_mail"`
	MailHostname  string `json:"mail_hostname"`
	MailServerIP  string `json:"mail_server_ip"`

	// Passwords (from env or setup.json)
	PolyonAdminPassword string `json:"-"`
	DCAdminPassword     string `json:"-"`

	// LDAP
	SambaHost string `json:"samba_host"`
	LDAPURL   string `json:"ldap_url"`

	// Database
	DatabaseURL string `json:"-"`
	DBUser      string `json:"-"`
	DBPassword  string `json:"-"`
	DBHost      string `json:"-"`
	DBName      string `json:"-"`

	// Keycloak
	KeycloakURL          string `json:"-"`
	KCAdminUser          string `json:"-"`
	KCAdminPassword      string `json:"-"`

	// Stalwart
	StalwartURL          string `json:"-"`
	StalwartAdminUser     string `json:"-"`
	StalwartAdminPassword string `json:"-"`

	// RustFS / S3
	RustFSEndpoint  string `json:"-"`
	RustFSAccessKey string `json:"-"`
	RustFSSecretKey string `json:"-"`

	// Elasticsearch
	ElasticURL      string `json:"-"`
	ElasticPassword string `json:"-"`

	// Grafana
	GrafanaUser     string `json:"-"`
	GrafanaPassword string `json:"-"`

	// SMTP (from smtp.json)
	SMTP SMTPConfig `json:"smtp"`

	// Nextcloud (HELIOS Drive)
	NextcloudURL           string `json:"-"`
	NextcloudAdminUser     string `json:"-"`
	NextcloudAdminPassword string `json:"-"`

	// Core (self — used for ForwardAuth URL construction)
	CoreURL string `json:"-"`

	// Prometheus
	PrometheusURL string `json:"-"`

	// Gitea
	GiteaURL string `json:"-"`

	// Odoo
	OdooURL string `json:"-"`

	// AFFiNE (Wiki)
	AffineURL string `json:"-"`

	// LiteLLM (AI Gateway)
	LiteLLMURL string `json:"-"`

	// OnlyOffice
	OnlyOfficeURL string `json:"-"`

	// Strapi CMS
	StrapiURL string `json:"-"`

	// Operaton BPMN
	OperatonURL string `json:"-"`

	// n8n Automation
	N8nURL string `json:"-"`

	// Mattermost
	MattermostURL string `json:"-"`

	// Mem0 (AI Memory Layer)
	Mem0URL string `json:"-"`

	// OpenClaw (AI Agent Runtime)
	OpenClawURL string `json:"-"`

	// Docker
	DCContainer string `json:"dc_container"`
}

type SMTPConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"` // none | starttls | ssl
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"from_address"`
	FromName    string `json:"from_name"`
	AlertTo     string `json:"alert_to"`
	Enabled     bool   `json:"enabled"`
}

var (
	global *Config
	mu     sync.RWMutex
)

// Get returns the current global config.
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return global
}

// Load reads configuration from environment and JSON files.
func Load() (*Config, error) {
	c := &Config{
		Port:        8000,
		SharedDir:   envOr("SHARED_DIR", "/shared"),
		PolyonDir:   envOr("POLYON_DIR", "/polyon"),
		SecretsDir:  envOr("SECRETS_DIR", "/polyon/secrets"),
		Realm:       envFirst("POLYON_DOMAIN_UPPER", "SAMBA_REALM"),
		Domain:      envFirst("POLYON_DOMAIN_NETBIOS", "SAMBA_DOMAIN"),
		SambaHost:   envOr("SAMBA_HOST", "samba-dc"),
		DCContainer: envOr("DC_CONTAINER", "polyon-dc"),
		DBUser:      envOr("DB_USER", "polyon"),
		DBHost:      envOr("DB_HOST", "polyon-db"),
		DBName:      envOr("DB_NAME", "polyon"),
	}

	c.LDAPURL = envFirst("POLYON_LDAP_URL", "LDAP_URL")
	if c.LDAPURL == "" {
		c.LDAPURL = fmt.Sprintf("ldap://%s:389", c.SambaHost)
	}
	c.KeycloakURL = envFirst("POLYON_AUTH_URL", "KEYCLOAK_URL")
	if c.KeycloakURL == "" {
		c.KeycloakURL = "http://polyon-auth:8080"
	}
	c.StalwartURL = envFirst("POLYON_STALWART_URL", "STALWART_URL")
	if c.StalwartURL == "" {
		c.StalwartURL = "http://polyon-mail:8080"
	}
	c.ElasticURL = envFirst("POLYON_SEARCH_URL", "ELASTICSEARCH_URL")
	if c.ElasticURL == "" {
		c.ElasticURL = "http://polyon-search:9200"
	}
	c.RustFSEndpoint = envFirst("POLYON_RUSTFS_URL", "RUSTFS_ENDPOINT")
	if c.RustFSEndpoint == "" {
		c.RustFSEndpoint = "http://polyon-rustfs:9000"
	}
	c.CoreURL = envOr("CORE_URL", "http://polyon-core:8000")
	c.PrometheusURL = envOr("PROMETHEUS_URL", "http://polyon-prometheus:9090")
	c.GiteaURL = envOr("GITEA_URL", "http://polyon-gitea:3000")
	c.OdooURL = envOr("ODOO_URL", "http://polyon-odoo:8069")
	c.AffineURL = envOr("AFFINE_URL", "http://polyon-wiki:3010")
	c.LiteLLMURL = envOr("LITELLM_URL", "http://polyon-ai:4000")
	c.OnlyOfficeURL = envOr("ONLYOFFICE_URL", "http://polyon-office:80")
	c.StrapiURL = envOr("STRAPI_URL", "http://polyon-strapi:1337")
	c.OperatonURL = envOr("OPERATON_URL", "http://polyon-operaton:8080")
	c.N8nURL = envOr("N8N_URL", "http://polyon-n8n:5678")
	c.MattermostURL = envOr("MATTERMOST_URL", "http://polyon-mattermost:8065")
	c.Mem0URL = envOr("MEM0_URL", "http://polyon-mem0:8888")
	c.OpenClawURL = envOr("OPENCLAW_URL", "http://polyon-agent:18789")

	// Read .env file
	envMap := readEnvFile(filepath.Join(c.PolyonDir, ".env"))

	c.DBPassword = envFirst("POLYON_DB_PASSWORD", "DB_PASSWORD")
	if c.DBPassword == "" {
		if v, ok := envMap["POLYON_DB_PASSWORD"]; ok {
			c.DBPassword = v
		} else if v, ok := envMap["DB_PASSWORD"]; ok {
			c.DBPassword = v
		}
	}
	c.KCAdminUser = envMapOr(envMap, "KC_BOOTSTRAP_ADMIN_USERNAME", "keycloak")
	c.KCAdminPassword = envFirst("POLYON_KC_ADMIN_PASSWORD", "KC_ADMIN_PASSWORD")
	if c.KCAdminPassword == "" {
		if v, ok := envMap["POLYON_KC_ADMIN_PASSWORD"]; ok {
			c.KCAdminPassword = v
		} else if v, ok := envMap["KC_ADMIN_PASSWORD"]; ok {
			c.KCAdminPassword = v
		}
	}
	c.StalwartAdminUser = envMapOr(envMap, "STALWART_ADMIN_USER", "stalwart")
	c.StalwartAdminPassword = envFirst("POLYON_STALWART_ADMIN_PASSWORD", "STALWART_ADMIN_PASSWORD")
	if c.StalwartAdminPassword == "" {
		if v, ok := envMap["POLYON_STALWART_ADMIN_PASSWORD"]; ok {
			c.StalwartAdminPassword = v
		} else if v, ok := envMap["STALWART_ADMIN_PASSWORD"]; ok {
			c.StalwartAdminPassword = v
		}
	}
	c.ElasticPassword = envFirst("POLYON_SEARCH_PASSWORD", "ELASTIC_PASSWORD")
	if c.ElasticPassword == "" {
		if v, ok := envMap["POLYON_SEARCH_PASSWORD"]; ok {
			c.ElasticPassword = v
		} else if v, ok := envMap["ELASTIC_PASSWORD"]; ok {
			c.ElasticPassword = v
		}
	}
	c.RustFSAccessKey = envFirst("POLYON_RUSTFS_ACCESS_KEY", "RUSTFS_ROOT_USER")
	if c.RustFSAccessKey == "" {
		if v, ok := envMap["POLYON_RUSTFS_ACCESS_KEY"]; ok {
			c.RustFSAccessKey = v
		} else if v, ok := envMap["RUSTFS_ROOT_USER"]; ok {
			c.RustFSAccessKey = v
		} else {
			c.RustFSAccessKey = "rustfs"
		}
	}
	c.RustFSSecretKey = envFirst("POLYON_RUSTFS_SECRET_KEY", "RUSTFS_ROOT_PASSWORD")
	if c.RustFSSecretKey == "" {
		if v, ok := envMap["POLYON_RUSTFS_SECRET_KEY"]; ok {
			c.RustFSSecretKey = v
		} else if v, ok := envMap["RUSTFS_ROOT_PASSWORD"]; ok {
			c.RustFSSecretKey = v
		}
	}
	c.GrafanaUser = envMapOr(envMap, "GF_SECURITY_ADMIN_USER", "grafana")
	c.GrafanaPassword = envMapOr(envMap, "GF_SECURITY_ADMIN_PASSWORD", "")

	// Nextcloud (HELIOS Drive)
	c.NextcloudURL = envOr("NEXTCLOUD_URL", "http://polyon-drive")
	c.NextcloudAdminUser = envMapOr(envMap, "NEXTCLOUD_ADMIN_USER", "admin")
	c.NextcloudAdminPassword = envMapOr(envMap, "NEXTCLOUD_ADMIN_PASSWORD", "")

	// Database URL
	c.DatabaseURL = envFirst("POLYON_DATABASE_URL", "DATABASE_URL")
	if c.DatabaseURL == "" {
		c.DatabaseURL = fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable",
			c.DBUser, c.DBPassword, c.DBHost, c.DBName)
	}

	// Admin password from secrets file
	c.DCAdminPassword = readFileStr(filepath.Join(c.SecretsDir, "admin_password.txt"))
	if c.DCAdminPassword == "" {
		c.DCAdminPassword = envFirst("POLYON_DC_ADMIN_PASSWORD", "ADMIN_PASSWORD")
	}

	// Read setup.json if exists
	c.loadSetupJSON()

	// Read SMTP config
	c.loadSMTPJSON()

	mu.Lock()
	global = c
	mu.Unlock()

	return c, nil
}

// ApplyInfraServices overwrites URL fields with DB-backed infra service values.
// Called after DB connection is established in server.go.
// Only overwrites fields that have a matching service ID in the DB.
func (c *Config) ApplyInfraServices(services []InfraServiceEntry) {
	urlMap := make(map[string]string, len(services))
	for _, s := range services {
		urlMap[s.ID] = fmt.Sprintf("http://%s:%d", s.Host, s.Port)
	}

	if v, ok := urlMap["auth"]; ok {
		c.KeycloakURL = v
	}
	if v, ok := urlMap["stalwart-admin"]; ok {
		c.StalwartURL = v
	}
	if v, ok := urlMap["core"]; ok {
		c.CoreURL = v
	}
	if v, ok := urlMap["elasticsearch"]; ok {
		c.ElasticURL = v
	}
	if v, ok := urlMap["rustfs-api"]; ok {
		c.RustFSEndpoint = v
	}
	if v, ok := urlMap["prometheus"]; ok {
		c.PrometheusURL = v
	}
	if v, ok := urlMap["gitea"]; ok {
		c.GiteaURL = v
	}
	if v, ok := urlMap["odoo"]; ok {
		c.OdooURL = v
	}
	if v, ok := urlMap["affine"]; ok {
		c.AffineURL = v
	}
	if v, ok := urlMap["litellm"]; ok {
		c.LiteLLMURL = v
	}
	if v, ok := urlMap["onlyoffice"]; ok {
		c.OnlyOfficeURL = v
	}
	if v, ok := urlMap["strapi"]; ok {
		c.StrapiURL = v
	}
	if v, ok := urlMap["operaton"]; ok {
		c.OperatonURL = v
	}
	if v, ok := urlMap["n8n"]; ok {
		c.N8nURL = v
	}
	if v, ok := urlMap["nextcloud"]; ok {
		c.NextcloudURL = v
	}
	if v, ok := urlMap["mattermost"]; ok {
		c.MattermostURL = v
	}
	if v, ok := urlMap["mem0"]; ok {
		c.Mem0URL = v
	}
	if v, ok := urlMap["openclaw"]; ok {
		c.OpenClawURL = v
	}
}

func (c *Config) BaseDN() string {
	if c.Realm == "" {
		return ""
	}
	parts := strings.Split(strings.ToLower(c.Realm), ".")
	dcs := make([]string, len(parts))
	for i, p := range parts {
		dcs[i] = "DC=" + p
	}
	return strings.Join(dcs, ",")
}

func (c *Config) AdminDN() string {
	return "CN=Administrator,CN=Users," + c.BaseDN()
}

func (c *Config) SetupJSONPath() string {
	return filepath.Join(c.SharedDir, "setup.json")
}

func (c *Config) ProvisionedPath() string {
	return filepath.Join(c.SharedDir, ".polyon-provisioned")
}

func (c *Config) ResetInProgressPath() string {
	return filepath.Join(c.SharedDir, ".polyon-resetting")
}

func (c *Config) IsProvisioned() bool {
	if fileExists(c.ResetInProgressPath()) {
		return false
	}
	if fileExists(c.ProvisionedPath()) {
		return true
	}
	// Also check samba provisioned flag
	if fileExists("/var/lib/samba/.polyon-provisioned") {
		return true
	}
	return false
}

func (c *Config) EnvFilePath() string {
	return filepath.Join(c.PolyonDir, ".env")
}

// Reload re-reads config from disk.
func (c *Config) Reload() {
	c.loadSetupJSON()
	c.loadSMTPJSON()
	envMap := readEnvFile(c.EnvFilePath())
	
	if newVal := envFirst("POLYON_DB_PASSWORD", "DB_PASSWORD"); newVal != "" {
		c.DBPassword = newVal
	} else if v, ok := envMap["POLYON_DB_PASSWORD"]; ok {
		c.DBPassword = v
	} else if v, ok := envMap["DB_PASSWORD"]; ok {
		c.DBPassword = v
	}
	
	if newVal := envFirst("POLYON_KC_ADMIN_PASSWORD", "KC_ADMIN_PASSWORD"); newVal != "" {
		c.KCAdminPassword = newVal
	} else if v, ok := envMap["POLYON_KC_ADMIN_PASSWORD"]; ok {
		c.KCAdminPassword = v
	} else if v, ok := envMap["KC_ADMIN_PASSWORD"]; ok {
		c.KCAdminPassword = v
	}
	
	c.StalwartAdminUser = envMapOr(envMap, "STALWART_ADMIN_USER", c.StalwartAdminUser)
	
	if newVal := envFirst("POLYON_STALWART_ADMIN_PASSWORD", "STALWART_ADMIN_PASSWORD"); newVal != "" {
		c.StalwartAdminPassword = newVal
	} else if v, ok := envMap["POLYON_STALWART_ADMIN_PASSWORD"]; ok {
		c.StalwartAdminPassword = v
	} else if v, ok := envMap["STALWART_ADMIN_PASSWORD"]; ok {
		c.StalwartAdminPassword = v
	}
	
	if newVal := envFirst("POLYON_SEARCH_PASSWORD", "ELASTIC_PASSWORD"); newVal != "" {
		c.ElasticPassword = newVal
	} else if v, ok := envMap["POLYON_SEARCH_PASSWORD"]; ok {
		c.ElasticPassword = v
	} else if v, ok := envMap["ELASTIC_PASSWORD"]; ok {
		c.ElasticPassword = v
	}
	
	if newVal := envFirst("POLYON_RUSTFS_SECRET_KEY", "RUSTFS_ROOT_PASSWORD"); newVal != "" {
		c.RustFSSecretKey = newVal
	} else if v, ok := envMap["POLYON_RUSTFS_SECRET_KEY"]; ok {
		c.RustFSSecretKey = v
	} else if v, ok := envMap["RUSTFS_ROOT_PASSWORD"]; ok {
		c.RustFSSecretKey = v
	}
}

func (c *Config) loadSetupJSON() {
	data, err := os.ReadFile(c.SetupJSONPath())
	if err != nil {
		return
	}
	var setup struct {
		Realm         string `json:"realm"`
		Domain        string `json:"domain"`
		DNSForwarder  string `json:"dns_forwarder"`
		OrgName       string `json:"org_name"`
		FunctionLevel string `json:"function_level"`
		EnableMail    bool   `json:"enable_mail"`
		MailHostname  string `json:"mail_hostname"`
		MailServerIP  string `json:"mail_server_ip"`
		PolyonAdminPW string `json:"polyon_admin_password"`
		DCAdminPW     string `json:"dc_admin_password"`
	}
	if err := json.Unmarshal(data, &setup); err != nil {
		return
	}
	c.Realm = strings.ToUpper(strings.TrimSpace(setup.Realm))
	c.Domain = strings.ToUpper(strings.TrimSpace(setup.Domain))
	c.DNSForwarder = setup.DNSForwarder
	c.OrgName = setup.OrgName
	c.FunctionLevel = setup.FunctionLevel
	c.EnableMail = setup.EnableMail
	c.MailHostname = setup.MailHostname
	c.MailServerIP = setup.MailServerIP
	if setup.PolyonAdminPW != "" {
		c.PolyonAdminPassword = setup.PolyonAdminPW
	}
	if setup.DCAdminPW != "" {
		c.DCAdminPassword = setup.DCAdminPW
	}
}

func (c *Config) loadSMTPJSON() {
	path := filepath.Join(c.SecretsDir, "smtp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &c.SMTP)
}

// ReadEnvMap reads the project's .env file and returns a map.
func (c *Config) ReadEnvMap() map[string]string {
	return readEnvFile(c.EnvFilePath())
}

// ReadEnvFile reads a .env file and returns a map.
func ReadEnvFile(path string) map[string]string {
	return readEnvFile(path)
}

// WriteEnvFile writes a map back to .env, preserving comments.
func WriteEnvFile(path string, env map[string]string) error {
	existing, _ := os.ReadFile(path)
	lines := strings.Split(string(existing), "\n")

	var result []string
	written := map[string]bool{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}
		if idx := strings.Index(trimmed, "="); idx > 0 {
			key := strings.TrimSpace(trimmed[:idx])
			if val, ok := env[key]; ok {
				result = append(result, key+"="+val)
				written[key] = true
				continue
			}
		}
		result = append(result, line)
	}

	for k, v := range env {
		if !written[k] {
			result = append(result, k+"="+v)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
}

// helpers

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envFirst returns the first non-empty environment variable from the given keys.
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func envMapOr(m map[string]string, key, fallback string) string {
	// Prefer os.environ, then .env file
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return fallback
}

func readEnvFile(path string) map[string]string {
	env := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return env
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			env[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
		}
	}
	return env
}

func readFileStr(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
