package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog/log"
)

// Component represents a PolyON system component in the System Manifest.
// This is the single source of truth for what PolyON is made of.
type Component struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Category        string          `json:"category"`         // core | engine | process | ai | infra | monitoring
	SortOrder       int             `json:"sort_order"`
	ContainerName   string          `json:"container_name"`
	Engine          string          `json:"engine"`           // underlying tech: keycloak, stalwart, openclaw, etc.
	Host            string          `json:"host"`
	Port            int             `json:"port"`
	HealthEndpoint  string          `json:"health_endpoint"`
	HealthMethod    string          `json:"health_method"`    // GET | TCP
	Version         string          `json:"version"`
	Icon            string          `json:"icon"`
	Accent          string          `json:"accent"`
	DependsOn       json.RawMessage `json:"depends_on"`       // JSON array of component IDs
	Status          string          `json:"status"`           // planned | deployed | active | disabled
	Config          json.RawMessage `json:"config,omitempty"` // component-specific settings
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// seedComponents is the canonical system manifest — 24 components across 6 sectors.
var seedComponents = []Component{
	// ── engine: 사원이 사용하는 업무 서비스 ──
	// NOTE: Stalwart Mail → foundation 카테고리로 이동 (Foundation #6)
	{ID: "mattermost", Name: "PolyON Chat", Description: "팀 메신저 (Mattermost)", Category: "engine", SortOrder: 1,
		ContainerName: "polyon-mattermost", Engine: "mattermost", Host: "polyon-mattermost", Port: 8065,
		HealthEndpoint: "/api/v4/system/ping", HealthMethod: "GET", Icon: "chat", Accent: "#0058CC",
		DependsOn: j(`["postgresql","polyon-auth"]`), Status: "available"},
	{ID: "drive", Name: "PP Drive", Description: "파일 스토리지 (WebDAV, 공유, 버전)", Category: "engine", SortOrder: 3,
		ContainerName: "polyon-drive", Engine: "drive", Host: "polyon-drive", Port: 8080,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "folder", Accent: "#0082C9",
		DependsOn: j(`["postgresql","rustfs","polyon-dc"]`), Status: "planned"},
	{ID: "onlyoffice", Name: "PolyON Office", Description: "문서 편집 (OnlyOffice Docs)", Category: "engine", SortOrder: 4,
		ContainerName: "polyon-office", Engine: "onlyoffice", Host: "polyon-office", Port: 80,
		HealthEndpoint: "/healthcheck", HealthMethod: "GET", Icon: "document", Accent: "#FF6F3D",
		DependsOn: j(`["postgresql"]`), Status: "planned"},
	{ID: "affine", Name: "PolyON Wiki", Description: "지식 관리 (AFFiNE)", Category: "engine", SortOrder: 5,
		ContainerName: "polyon-wiki", Engine: "affine", Host: "polyon-wiki", Port: 3010,
		HealthEndpoint: "/info", HealthMethod: "GET", Icon: "notebook", Accent: "#1E96EB",
		DependsOn: j(`["postgresql","redis"]`), Status: "planned"},
	{ID: "odoo", Name: "PolyON ERP", Description: "전사 자원관리 (Odoo)", Category: "engine", SortOrder: 6,
		ContainerName: "polyon-appengine", Engine: "odoo", Host: "polyon-appengine", Port: 8069,
		HealthEndpoint: "/web/health", HealthMethod: "GET", Icon: "dataBase", Accent: "#714B67",
		DependsOn: j(`["postgresql"]`), Status: "planned"},
	// NOTE: Gitea → foundation 카테고리로 이동 (Foundation #7)
	{ID: "runner", Name: "PolyON CI/CD", Description: "CI/CD 실행기 (Gitea Actions Runner)", Category: "engine", SortOrder: 7,
		ContainerName: "polyon-runner", Engine: "gitea-actions", Host: "polyon-runner", Port: 0,
		HealthEndpoint: "", HealthMethod: "", Icon: "build", Accent: "#609926",
		DependsOn: j(`["gitea"]`), Status: "planned"},
	{ID: "strapi", Name: "PolyON CMS", Description: "웹사이트 빌더 · CMS (Strapi)", Category: "engine", SortOrder: 8,
		ContainerName: "polyon-strapi", Engine: "strapi", Host: "polyon-strapi", Port: 1337,
		HealthEndpoint: "/_health", HealthMethod: "GET", Icon: "application", Accent: "#4945FF",
		DependsOn: j(`["postgresql"]`), Status: "planned"},

	// ── ai: AI 에이전트 및 지능 레이어 ──
	// NOTE: LiteLLM(AI Gateway) → foundation 카테고리로 이동 (Foundation #8)
	{ID: "openclaw", Name: "PolyON Agent", Description: "AI Agent Runtime (OpenClaw)", Category: "ai", SortOrder: 1,
		ContainerName: "polyon-agent", Engine: "openclaw", Host: "polyon-agent", Port: 18789,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "bot", Accent: "#10b981",
		DependsOn: j(`["polyon-ai","mem0","polyon-auth"]`), Status: "planned"},
	{ID: "mem0", Name: "PolyON Memory", Description: "AI 기억 레이어 (Mem0)", Category: "ai", SortOrder: 3,
		ContainerName: "polyon-mem0", Engine: "mem0", Host: "polyon-mem0", Port: 8888,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "dataBase", Accent: "#8b5cf6",
		DependsOn: j(`["postgresql"]`), Status: "planned"},

	// ── process: 비즈니스 프로세스 및 업무 자동화 (2) ──
	{ID: "operaton", Name: "PolyON BPMN", Description: "비즈니스 프로세스 (Operaton)", Category: "process", SortOrder: 1,
		ContainerName: "polyon-operaton", Engine: "operaton", Host: "polyon-operaton", Port: 8080,
		HealthEndpoint: "/engine-rest/engine", HealthMethod: "GET", Icon: "flow", Accent: "#fc5a10",
		DependsOn: j(`["postgresql"]`), Status: "planned"},
	{ID: "n8n", Name: "PolyON Auto", Description: "워크플로우 자동화 (n8n)", Category: "process", SortOrder: 2,
		ContainerName: "polyon-n8n", Engine: "n8n", Host: "polyon-n8n", Port: 5678,
		HealthEndpoint: "/healthz", HealthMethod: "GET", Icon: "flow", Accent: "#ea4b71",
		DependsOn: j(`["postgresql"]`), Status: "planned"},

	// ── foundation: PolyON 플랫폼 Foundation (Platform + Infrastructure + Capability) ──
	// Platform
	{ID: "polyon-console", Name: "PolyON Console", Description: "Admin Console", Category: "foundation", SortOrder: 1,
		ContainerName: "polyon-console", Engine: "nginx", Host: "polyon-console", Port: 80,
		HealthEndpoint: "/", HealthMethod: "GET", Icon: "dashboard", Accent: "#161616",
		DependsOn: j(`["polyon-core"]`), Status: "active"},
	{ID: "polyon-core", Name: "PolyON Core", Description: "API Gateway (Go)", Category: "foundation", SortOrder: 2,
		ContainerName: "polyon-core", Engine: "go-chi", Host: "polyon-core", Port: 8000,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "settings", Accent: "#393939",
		DependsOn: j(`["postgresql","polyon-auth","polyon-dc"]`), Status: "active"},
	{ID: "polyon-dc", Name: "PolyON AD DC", Description: "Active Directory", Category: "foundation", SortOrder: 3,
		ContainerName: "polyon-dc", Engine: "samba", Host: "polyon-dc", Port: 636,
		HealthEndpoint: "", HealthMethod: "TCP", Icon: "group", Accent: "#6929c4",
		DependsOn: j(`[]`), Status: "active"},
	{ID: "keycloak", Name: "PolyON Auth", Description: "SSO / Authentication (Keycloak)", Category: "foundation", SortOrder: 4,
		ContainerName: "polyon-auth", Engine: "keycloak", Host: "polyon-auth", Port: 8080,
		HealthEndpoint: "/health/ready", HealthMethod: "GET", Icon: "locked", Accent: "#4d9de0",
		DependsOn: j(`["postgresql"]`), Status: "active"},
	{ID: "opa", Name: "PolyON Policy", Description: "인가 · 정책 엔진 (OPA)", Category: "foundation", SortOrder: 5,
		ContainerName: "polyon-opa", Engine: "opa", Host: "polyon-opa", Port: 8181,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "policy", Accent: "#7d4e00",
		DependsOn: j(`["keycloak"]`), Status: "active"},
	// Infrastructure
	{ID: "postgresql", Name: "PostgreSQL", Description: "Database", Category: "foundation", SortOrder: 6,
		ContainerName: "polyon-db", Engine: "postgresql", Host: "polyon-db", Port: 5432,
		HealthEndpoint: "", HealthMethod: "TCP", Icon: "dataBase", Accent: "#336791",
		DependsOn: j(`[]`), Status: "active"},
	{ID: "redis", Name: "Redis", Description: "Cache", Category: "foundation", SortOrder: 7,
		ContainerName: "polyon-redis", Engine: "redis", Host: "polyon-redis", Port: 6379,
		HealthEndpoint: "", HealthMethod: "TCP", Icon: "dataBase", Accent: "#dc382d",
		DependsOn: j(`[]`), Status: "active"},
	{ID: "opensearch", Name: "OpenSearch", Description: "Search (OpenSearch 3.5.0)", Category: "foundation", SortOrder: 8,
		ContainerName: "polyon-search", Engine: "opensearch", Host: "polyon-search", Port: 9200,
		HealthEndpoint: "/_cluster/health", HealthMethod: "GET", Icon: "search", Accent: "#fed10a",
		DependsOn: j(`[]`), Status: "active"},
	{ID: "rustfs", Name: "RustFS", Description: "Object Storage", Category: "foundation", SortOrder: 9,
		ContainerName: "polyon-rustfs", Engine: "rustfs", Host: "polyon-rustfs", Port: 9000,
		HealthEndpoint: "", HealthMethod: "TCP", Icon: "dataBase", Accent: "#dea584",
		DependsOn: j(`[]`), Status: "active"},
	{ID: "traefik", Name: "Traefik", Description: "Reverse Proxy", Category: "foundation", SortOrder: 10,
		ContainerName: "", Engine: "traefik", Host: "polyon-proxy", Port: 8080,
		HealthEndpoint: "/ping", HealthMethod: "GET", Icon: "network4", Accent: "#24a1c1",
		DependsOn: j(`[]`), Status: "active"},
	{ID: "stalwart", Name: "Stalwart Mail", Description: "Email Server", Category: "foundation", SortOrder: 11,
		ContainerName: "polyon-mail", Engine: "stalwart", Host: "polyon-mail", Port: 443,
		HealthEndpoint: "/healthz", HealthMethod: "GET", Icon: "email", Accent: "#198038",
		DependsOn: j(`["postgresql","polyon-dc"]`), Status: "active"},
	{ID: "gitea", Name: "Gitea", Description: "Git Repository", Category: "foundation", SortOrder: 12,
		ContainerName: "polyon-gitea", Engine: "gitea", Host: "polyon-gitea", Port: 3000,
		HealthEndpoint: "/api/v1/version", HealthMethod: "GET", Icon: "code", Accent: "#609926",
		DependsOn: j(`["postgresql"]`), Status: "active"},
	// Capability — polyon-embed: Foundation 확장 (Search Stack)
	{ID: "polyon-embed", Name: "PolyON Embed", Description: "Embedding Service (multilingual-e5-base)", Category: "foundation", SortOrder: 13,
		ContainerName: "polyon-embed", Engine: "fastapi", Host: "polyon-embed", Port: 4001,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "VectorMath", Accent: "#6929C4",
		DependsOn: j(`["opensearch","postgresql"]`), Status: "planned"},
	// AI 섹션
	{ID: "polyon-ai", Name: "AI Gateway", Description: "AI Gateway (LiteLLM)", Category: "ai", SortOrder: 1,
		ContainerName: "polyon-ai", Engine: "litellm", Host: "polyon-ai", Port: 4000,
		HealthEndpoint: "/health", HealthMethod: "GET", Icon: "watsonHealth3rdParty", Accent: "#8a3ffc",
		DependsOn: j(`["postgresql","redis"]`), Status: "active"},
	{ID: "appengine", Name: "PolyON AppEngine", Description: "ERP · HR · 회계 엔진 (Odoo 기반)", Category: "foundation", SortOrder: 14,
		ContainerName: "polyon-appengine", Engine: "odoo", Host: "polyon-appengine", Port: 8069,
		HealthEndpoint: "/web/health", HealthMethod: "GET", Icon: "enterprise", Accent: "#714B67",
		DependsOn: j(`["postgresql","keycloak"]`), Status: "active"},

	// ── monitoring: 관측 및 대시보드 (5) ──
	{ID: "prometheus", Name: "Prometheus", Description: "Metrics", Category: "monitoring", SortOrder: 1,
		ContainerName: "polyon-prometheus", Engine: "prometheus", Host: "polyon-prometheus", Port: 9090,
		HealthEndpoint: "/-/healthy", HealthMethod: "GET", Icon: "chartLine", Accent: "#e6522c",
		DependsOn: j(`[]`), Status: "planned"},
	{ID: "grafana", Name: "Grafana", Description: "Dashboard", Category: "monitoring", SortOrder: 2,
		ContainerName: "polyon-grafana", Engine: "grafana", Host: "polyon-grafana", Port: 3000,
		HealthEndpoint: "/api/health", HealthMethod: "GET", Icon: "dashboard", Accent: "#f46800",
		DependsOn: j(`["prometheus"]`), Status: "planned"},
	{ID: "elasticvue", Name: "Elasticvue", Description: "OpenSearch 관리 UI", Category: "monitoring", SortOrder: 3,
		ContainerName: "polyon-elasticvue", Engine: "elasticvue", Host: "polyon-elasticvue", Port: 8080,
		HealthEndpoint: "/", HealthMethod: "GET", Icon: "search", Accent: "#fed10a",
		DependsOn: j(`["opensearch"]`), Status: "active"},
	{ID: "pgweb", Name: "Pgweb", Description: "PostgreSQL 관리 UI", Category: "monitoring", SortOrder: 4,
		ContainerName: "polyon-pgweb", Engine: "pgweb", Host: "polyon-pgweb", Port: 8081,
		HealthEndpoint: "/", HealthMethod: "GET", Icon: "dataBase", Accent: "#336791",
		DependsOn: j(`["postgresql"]`), Status: "active"},
	{ID: "redis-commander", Name: "Redis Commander", Description: "Redis 관리 UI", Category: "monitoring", SortOrder: 5,
		ContainerName: "polyon-redis-commander", Engine: "redis-commander", Host: "polyon-redis-commander", Port: 8081,
		HealthEndpoint: "/", HealthMethod: "GET", Icon: "dataBase", Accent: "#dc382d",
		DependsOn: j(`["redis"]`), Status: "active"},
}

// j is a helper to create json.RawMessage from string literals.
func j(s string) json.RawMessage { return json.RawMessage(s) }

// migrateComponents creates the polyon_components table and seeds the initial manifest.
func (s *Store) migrateComponents() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS polyon_components (
			id               TEXT PRIMARY KEY,
			name             TEXT NOT NULL,
			description      TEXT NOT NULL DEFAULT '',
			category         TEXT NOT NULL,
			sort_order       INTEGER NOT NULL DEFAULT 0,
			container_name   TEXT NOT NULL DEFAULT '',
			engine           TEXT NOT NULL DEFAULT '',
			host             TEXT NOT NULL DEFAULT '',
			port             INTEGER NOT NULL DEFAULT 0,
			health_endpoint  TEXT NOT NULL DEFAULT '',
			health_method    TEXT NOT NULL DEFAULT 'GET',
			version          TEXT NOT NULL DEFAULT '',
			icon             TEXT NOT NULL DEFAULT '',
			accent           TEXT NOT NULL DEFAULT '#393939',
			depends_on       JSONB NOT NULL DEFAULT '[]',
			status           TEXT NOT NULL DEFAULT 'planned',
			config           JSONB NOT NULL DEFAULT '{}',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		log.Warn().Err(err).Msg("migrateComponents: table creation failed")
		return
	}

	// Migration: old ID 정리
	s.pool.Exec(ctx, `DELETE FROM polyon_components WHERE id = 'litellm'`)
	s.pool.Exec(ctx, `DELETE FROM polyon_components WHERE id = 'nextcloud'`)

	for _, c := range seedComponents {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO polyon_components (id, name, description, category, sort_order,
				container_name, engine, host, port, health_endpoint, health_method,
				version, icon, accent, depends_on, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name, description = EXCLUDED.description,
				category = EXCLUDED.category, sort_order = EXCLUDED.sort_order,
				container_name = EXCLUDED.container_name, engine = EXCLUDED.engine,
				host = EXCLUDED.host, port = EXCLUDED.port,
				health_endpoint = EXCLUDED.health_endpoint, health_method = EXCLUDED.health_method,
				icon = EXCLUDED.icon, accent = EXCLUDED.accent,
				depends_on = EXCLUDED.depends_on,
				status = EXCLUDED.status,
				updated_at = NOW()
		`, c.ID, c.Name, c.Description, c.Category, c.SortOrder,
			c.ContainerName, c.Engine, c.Host, c.Port, c.HealthEndpoint, c.HealthMethod,
			c.Version, c.Icon, c.Accent, c.DependsOn, c.Status)
		if err != nil {
			log.Warn().Err(err).Str("id", c.ID).Msg("migrateComponents: seed insert failed")
		}
	}
	log.Info().Int("count", len(seedComponents)).Msg("polyon_components table ready")
}

// ListComponents returns components, optionally filtered by category and/or status.
func (s *Store) ListComponents(ctx context.Context, category, status string) ([]Component, error) {
	query := `SELECT id, name, description, category, sort_order,
		container_name, engine, host, port, health_endpoint, health_method,
		version, icon, accent, depends_on, status, config, created_at, updated_at
		FROM polyon_components WHERE 1=1`
	args := []any{}
	idx := 1

	if category != "" {
		query += ` AND category = $` + itoa(idx)
		args = append(args, category)
		idx++
	}
	if status != "" {
		query += ` AND status = $` + itoa(idx)
		args = append(args, status)
		idx++
	}
	query += ` ORDER BY CASE category
		WHEN 'foundation' THEN 1 WHEN 'engine' THEN 2 WHEN 'ai' THEN 3
		WHEN 'process' THEN 4 WHEN 'monitoring' THEN 5
		ELSE 99 END, sort_order, id`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Component
	for rows.Next() {
		var c Component
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Category, &c.SortOrder,
			&c.ContainerName, &c.Engine, &c.Host, &c.Port, &c.HealthEndpoint, &c.HealthMethod,
			&c.Version, &c.Icon, &c.Accent, &c.DependsOn, &c.Status, &c.Config,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// GetComponent returns a single component by ID.
func (s *Store) GetComponent(ctx context.Context, id string) (*Component, error) {
	var c Component
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, category, sort_order,
			container_name, engine, host, port, health_endpoint, health_method,
			version, icon, accent, depends_on, status, config, created_at, updated_at
		FROM polyon_components WHERE id = $1
	`, id).Scan(&c.ID, &c.Name, &c.Description, &c.Category, &c.SortOrder,
		&c.ContainerName, &c.Engine, &c.Host, &c.Port, &c.HealthEndpoint, &c.HealthMethod,
		&c.Version, &c.Icon, &c.Accent, &c.DependsOn, &c.Status, &c.Config,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateComponent updates mutable fields of a component.
func (s *Store) UpdateComponent(ctx context.Context, id string, updates map[string]any) error {
	// Build dynamic SET clause from allowed fields
	allowed := map[string]bool{
		"name": true, "description": true, "category": true, "sort_order": true,
		"container_name": true, "engine": true, "host": true, "port": true,
		"health_endpoint": true, "health_method": true, "version": true,
		"icon": true, "accent": true, "depends_on": true, "status": true, "config": true,
	}

	setClauses := ""
	args := []any{}
	idx := 1
	for k, v := range updates {
		if !allowed[k] {
			continue
		}
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += k + " = $" + itoa(idx)
		args = append(args, v)
		idx++
	}
	if setClauses == "" {
		return nil
	}
	setClauses += ", updated_at = NOW()"
	args = append(args, id)

	_, err := s.pool.Exec(ctx,
		`UPDATE polyon_components SET `+setClauses+` WHERE id = $`+itoa(idx),
		args...)
	return err
}

// UpdateComponentVersion sets the runtime-detected version for a component.
func (s *Store) UpdateComponentVersion(ctx context.Context, id, version string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE polyon_components SET version = $2, updated_at = NOW() WHERE id = $1
	`, id, version)
	return err
}

// UpdateComponentStatus sets the status for a component.
func (s *Store) UpdateComponentStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE polyon_components SET status = $2, updated_at = NOW() WHERE id = $1
	`, id, status)
	return err
}

// ComponentTopology returns a list of {id, depends_on} pairs for graph visualization.
func (s *Store) ComponentTopology(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, category, depends_on, status FROM polyon_components ORDER BY category, sort_order
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []map[string]any
	for rows.Next() {
		var id, name, cat, status string
		var deps json.RawMessage
		if err := rows.Scan(&id, &name, &cat, &deps, &status); err != nil {
			return nil, err
		}
		nodes = append(nodes, map[string]any{
			"id": id, "name": name, "category": cat, "depends_on": deps, "status": status,
		})
	}
	return nodes, rows.Err()
}

// itoa is defined in audit.go
