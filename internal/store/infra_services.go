package store

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// InfraService represents a core infrastructure service used by Traefik routing.
type InfraService struct {
	ID         string `json:"id"`         // e.g. "sentinel", "ui", "core", "auth"
	Name       string `json:"name"`       // 표시명
	Host       string `json:"host"`       // Docker 컨테이너명 (e.g. "polyon-sentinel")
	Port       int    `json:"port"`       // 서비스 포트 (e.g. 8080)
	Protocol   string `json:"protocol"`   // "http" or "tcp"
	Category   string `json:"category"`   // "infra" | "monitoring" | "mail-tcp"
	EntryPoint string `json:"entrypoint"` // Traefik entrypoint (e.g. "grafana", "keycloak")
	PathRules  string `json:"pathRules"`  // JSON array: [{"path":"/api/sentinel","priority":200,"service":"sentinel"}]
	Enabled    bool   `json:"enabled"`
}

// seedInfraServices is the canonical infrastructure service catalog seeded into DB on first run.
var seedInfraServices = []InfraService{
	// ── Foundation 인프라 서비스 ──
	{
		ID: "postgresql", Name: "PostgreSQL", Host: "polyon-db", Port: 5432,
		Protocol: "tcp", Category: "infra", Enabled: true,
	},
	{
		ID: "redis", Name: "Redis", Host: "polyon-redis", Port: 6379,
		Protocol: "tcp", Category: "infra", Enabled: true,
	},
	{
		ID: "samba-dc", Name: "Samba AD DC", Host: "polyon-dc", Port: 389,
		Protocol: "ldap", Category: "infra", Enabled: true,
	},
	{
		ID: "keycloak", Name: "Keycloak SSO", Host: "polyon-auth", Port: 8080,
		Protocol: "http", Category: "infra", Enabled: true,
	},
	{
		ID: "opensearch", Name: "OpenSearch", Host: "polyon-search", Port: 9200,
		Protocol: "http", Category: "infra", Enabled: true,
	},
	{
		ID: "rustfs", Name: "RustFS S3", Host: "polyon-rustfs", Port: 9000,
		Protocol: "http", Category: "infra", Enabled: true,
	},
	// ── infra (00-infra.yml services + :1111 management routers) ──
	{
		ID: "sentinel", Name: "Sentinel", Host: "polyon-sentinel", Port: 8080,
		Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
		PathRules: `[{"path":"/api/sentinel","priority":200},{"path":"/api/setup","priority":150},{"path":"/api/reset","priority":150}]`,
		Enabled:   true,
	},
	{
		ID: "ui", Name: "Console UI", Host: "polyon-console", Port: 80,
		Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
		PathRules: `[{"path":"/","priority":1}]`,
		Enabled:   true,
	},
	{
		ID: "core", Name: "Core API", Host: "polyon-core", Port: 8000,
		Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
		PathRules: `[{"path":"/api/v1","priority":120},{"path":"/health","priority":110},{"path":"/mail-proxy","priority":110},{"path":"/es-proxy","priority":110},{"path":"/api/alerts","priority":110}]`,
		Enabled:   true,
	},
	{
		ID: "auth", Name: "Keycloak SSO", Host: "polyon-auth", Port: 8080,
		Protocol: "http", Category: "infra", EntryPoint: "polyon-console",
		PathRules: `[{"path":"/auth","priority":100}]`,
		Enabled:   true,
	},

	// ── monitoring (30-monitoring.yml — 개별 entrypoint 서비스) ──
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

	// ── mail-tcp (40-mail-tcp.yml — TCP passthrough) ──
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

	// ── engine (application services — URL config source) ──
	{
		ID: "elasticsearch", Name: "Elasticsearch", Host: "polyon-search", Port: 9200,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "prometheus", Name: "Prometheus", Host: "polyon-prometheus", Port: 9090,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "gitea", Name: "Gitea", Host: "polyon-gitea", Port: 3000,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "odoo", Name: "Odoo", Host: "polyon-appengine", Port: 8069,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "affine", Name: "AFFiNE", Host: "polyon-wiki", Port: 3010,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "litellm", Name: "LiteLLM", Host: "polyon-ai", Port: 4000,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "onlyoffice", Name: "ONLYOFFICE", Host: "polyon-office", Port: 80,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "strapi", Name: "Strapi CMS", Host: "polyon-strapi", Port: 1337,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "nextcloud", Name: "Nextcloud", Host: "polyon-drive", Port: 80,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "operaton", Name: "Operaton BPMN", Host: "polyon-operaton", Port: 8080,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "n8n", Name: "n8n Automation", Host: "polyon-n8n", Port: 5678,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	{
		ID: "mattermost", Name: "Mattermost", Host: "polyon-mattermost", Port: 8065,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
	// rustfs-api is the S3 API port (9000); existing "rustfs" (9001) is the Console UI
	{
		ID: "rustfs-api", Name: "RustFS API", Host: "polyon-rustfs", Port: 9000,
		Protocol: "http", Category: "engine", EntryPoint: "", Enabled: true,
	},
}

// migrateInfraServices creates the polyon_infra_services table and seeds the initial catalog.
// Called from migrate() during Store init.
func (s *Store) migrateInfraServices() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create table
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS polyon_infra_services (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			host        TEXT NOT NULL,
			port        INTEGER NOT NULL DEFAULT 8080,
			protocol    TEXT NOT NULL DEFAULT 'http',
			category    TEXT NOT NULL DEFAULT 'infra',
			entrypoint  TEXT NOT NULL DEFAULT '',
			path_rules  TEXT NOT NULL DEFAULT '',
			enabled     BOOLEAN NOT NULL DEFAULT true,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		log.Warn().Err(err).Msg("migrateInfraServices: table creation failed")
		return
	}

	// Seed initial catalog (ON CONFLICT DO NOTHING — preserves manual edits)
	for _, svc := range seedInfraServices {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO polyon_infra_services (id, name, host, port, protocol, category, entrypoint, path_rules, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
			  host = EXCLUDED.host,
			  port = EXCLUDED.port,
			  name = EXCLUDED.name
		`, svc.ID, svc.Name, svc.Host, svc.Port, svc.Protocol,
			svc.Category, svc.EntryPoint, svc.PathRules, svc.Enabled)
		if err != nil {
			log.Warn().Err(err).Str("service", svc.ID).Msg("migrateInfraServices: seed insert failed")
		}
	}
	log.Debug().Msg("migrateInfraServices: polyon_infra_services table ready")
}

// ListInfraServices returns all enabled infra services from the DB.
func (s *Store) ListInfraServices(ctx context.Context) ([]InfraService, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, host, port, protocol, category, entrypoint, path_rules, enabled
		FROM polyon_infra_services
		WHERE enabled = true
		ORDER BY category, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []InfraService
	for rows.Next() {
		var svc InfraService
		if err := rows.Scan(
			&svc.ID, &svc.Name, &svc.Host, &svc.Port,
			&svc.Protocol, &svc.Category, &svc.EntryPoint,
			&svc.PathRules, &svc.Enabled,
		); err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	return services, rows.Err()
}

// GetInfraService returns a single infra service by ID.
func (s *Store) GetInfraService(ctx context.Context, id string) (*InfraService, error) {
	var svc InfraService
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, host, port, protocol, category, entrypoint, path_rules, enabled
		FROM polyon_infra_services
		WHERE id = $1
	`, id).Scan(
		&svc.ID, &svc.Name, &svc.Host, &svc.Port,
		&svc.Protocol, &svc.Category, &svc.EntryPoint,
		&svc.PathRules, &svc.Enabled,
	)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

// UpdateInfraService updates the host and port of an infra service.
func (s *Store) UpdateInfraService(ctx context.Context, id string, host string, port int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE polyon_infra_services
		SET host = $2, port = $3, updated_at = NOW()
		WHERE id = $1
	`, id, host, port)
	return err
}
