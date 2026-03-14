package prc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Engine is the PRC orchestrator. It initializes providers, runs the saga,
// resolves env templates, and manages claim state in the DB.
type Engine struct {
	saga *SagaExecutor
	pool *pgxpool.Pool
}

// NewEngine creates a PRC engine from environment configuration.
// It initializes all Foundation providers using the provided PG pool
// and environment variables for admin credentials.
func NewEngine(pool *pgxpool.Pool) *Engine {
	providers := buildProviders(pool)
	return &Engine{
		saga: NewSagaExecutor(providers),
		pool: pool,
	}
}

// Provision processes all claims for a module, provisions resources,
// and returns the resolved environment variables.
func (e *Engine) Provision(ctx context.Context, moduleID string, claims []Claim, envTemplates map[string]string) (map[string]string, error) {
	if len(claims) == 0 {
		return map[string]string{}, nil
	}

	// Inject moduleID into all claims
	for i := range claims {
		claims[i].ModuleID = moduleID
	}

	// 1. Run saga (topological sort + provision)
	creds, logs, err := e.saga.Execute(ctx, claims)
	if err != nil {
		// Record failure
		e.recordSagaLogs(ctx, moduleID, logs)
		return nil, err
	}

	// 2. Record success
	e.recordSagaLogs(ctx, moduleID, logs)

	// 3. Save claim state to DB
	for _, pr := range logs {
		e.saveClaim(ctx, moduleID, pr)
	}

	// 4. Resolve env templates
	resolved, err := ResolveEnvTemplate(envTemplates, creds)
	if err != nil {
		log.Warn().Err(err).Str("module", moduleID).
			Msg("PRC: some env templates unresolved (non-fatal)")
	}

	return resolved, nil
}

// Deprovision removes all provisioned resources for a module.
func (e *Engine) Deprovision(ctx context.Context, moduleID string, claims []Claim) error {
	for i := range claims {
		claims[i].ModuleID = moduleID
	}

	e.saga.Compensate(ctx, claims)

	// Update claim status in DB
	e.pool.Exec(ctx,
		`UPDATE module_claims SET status='removed', updated_at=NOW() WHERE module_id=$1`,
		moduleID)

	return nil
}

// recordSagaLogs writes provisioning results to claim_saga_log.
func (e *Engine) recordSagaLogs(ctx context.Context, moduleID string, logs []ProvisionResult) {
	for _, pr := range logs {
		action := "provision"
		status := pr.Status
		e.pool.Exec(ctx,
			`INSERT INTO claim_saga_log (module_id, action, claim_type, status, duration_ms, error_message)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			moduleID, action, pr.ClaimType, status, pr.DurationMs, pr.Error)
	}
}

// saveClaim writes or updates a claim record in module_claims.
func (e *Engine) saveClaim(ctx context.Context, moduleID string, pr ProvisionResult) {
	credsJSON, _ := json.Marshal(pr.Credentials)
	e.pool.Exec(ctx,
		`INSERT INTO module_claims (module_id, claim_type, credentials, status)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (module_id, claim_type) DO UPDATE SET
			credentials = EXCLUDED.credentials,
			status = EXCLUDED.status,
			updated_at = NOW()`,
		moduleID, pr.ClaimType, credsJSON, pr.Status)
}

// MigrateSchema creates PRC-related tables.
func MigrateSchema(ctx context.Context, pool *pgxpool.Pool) {
	ddls := []string{
		`CREATE TABLE IF NOT EXISTS module_claims (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			module_id     VARCHAR(64) NOT NULL,
			claim_type    VARCHAR(32) NOT NULL,
			claim_config  JSONB NOT NULL DEFAULT '{}',
			credentials   JSONB,
			status        VARCHAR(20) NOT NULL DEFAULT 'pending',
			error_message TEXT,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(module_id, claim_type)
		)`,
		`CREATE TABLE IF NOT EXISTS claim_saga_log (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			module_id  VARCHAR(64) NOT NULL,
			action     VARCHAR(20) NOT NULL,
			claim_type VARCHAR(32) NOT NULL,
			status     VARCHAR(20) NOT NULL,
			duration_ms INT,
			error_message TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS redis_db_allocations (
			db_number    INT PRIMARY KEY,
			module_id    VARCHAR(64),
			allocated_at TIMESTAMPTZ
		)`,
		// FK CASCADE 제약조건 추가 (module_claims)
		`ALTER TABLE module_claims 
		 ADD CONSTRAINT IF NOT EXISTS fk_module_claims_module 
		 FOREIGN KEY (module_id) REFERENCES polyon_modules(id) ON DELETE CASCADE`,
	}

	for _, ddl := range ddls {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			log.Warn().Err(err).Msg("PRC: migration failed")
		}
	}

	// Seed Redis DB allocations (0-15), 0 reserved for system
	pool.Exec(ctx, `INSERT INTO redis_db_allocations (db_number) SELECT g FROM generate_series(0,15) g ON CONFLICT DO NOTHING`)
	pool.Exec(ctx, `UPDATE redis_db_allocations SET module_id='system' WHERE db_number=0 AND module_id IS NULL`)
	// Reserve DB 1 for Core session, DB 2 for LiteLLM
	pool.Exec(ctx, `UPDATE redis_db_allocations SET module_id='core' WHERE db_number=1 AND module_id IS NULL`)
	pool.Exec(ctx, `UPDATE redis_db_allocations SET module_id='litellm' WHERE db_number=2 AND module_id IS NULL`)

	log.Info().Msg("PRC: schema migration complete")
}

// buildProviders creates all Foundation providers from environment.
func buildProviders(pool *pgxpool.Pool) []ResourceProvider {
	providers := []ResourceProvider{
		// 1. Database (PostgreSQL)
		&DatabaseProvider{
			Pool:          pool,
			Host:          envOr("PG_HOST", "polyon-db"),
			Port:          envOr("PG_PORT", "5432"),
			AdminUser:     envOr("PG_ADMIN_USER", "postgres"),
			AdminPassword: envOrMulti([]string{"PG_ADMIN_PASSWORD", "POSTGRES_PASSWORD", "POLYON_POSTGRES_PASSWORD"}, ""),
		},
		// 2. Object Storage (RustFS)
		&ObjectStorageProvider{
			Endpoint:  envOr("RUSTFS_ENDPOINT", "polyon-rustfs:9000"),
			AccessKey: envOrMulti([]string{"RUSTFS_ACCESS_KEY", "RUSTFS_ROOT_USER"}, ""),
			SecretKey: envOrMulti([]string{"RUSTFS_SECRET_KEY", "RUSTFS_ROOT_PASSWORD"}, ""),
		},
		// 3. Directory (Samba AD DC)
		&DirectoryProvider{
			Host:          envOr("LDAP_HOST", "polyon-dc"),
			Port:          envOr("LDAP_PORT", "389"),
			BaseDN:        envOr("LDAP_BASE_DN", ""),
			AdminUser:     envOr("LDAP_ADMIN_USER", "Administrator"),
			AdminPassword: envOr("SAMBA_ADMIN_PASSWORD", ""),
			ExecFn:        nil, // Set by caller if K8s exec available
		},
		// 4. Cache (Redis)
		&CacheProvider{
			Pool: pool,
			Host: envOr("REDIS_HOST", "polyon-redis"),
			Port: envOr("REDIS_PORT", "6379"),
		},
		// 5. Search (OpenSearch)
		&SearchProvider{
			Endpoint: envOr("SEARCH_ENDPOINT", "http://polyon-search:9200"),
		},
		// 6. SMTP (Stalwart Mail)
		&SmtpProvider{
			Host:       envOr("SMTP_HOST", "polyon-mail"),
			Port:       envOr("SMTP_PORT", "587"),
			BaseDomain: envOr("BASE_DOMAIN", ""),
		},
		// 7. Git (Gitea)
		&GitProvider{
			Endpoint:      envOr("GITEA_ENDPOINT", "http://polyon-gitea:3000"),
			AdminUser:     envOr("GITEA_ADMIN_USER", "polyon-admin"),
			AdminPassword: envOr("GITEA_ADMIN_PASSWORD", ""),
		},
		// 8. AI (LiteLLM)
		&AIProvider{
			Endpoint:  envOr("AI_ENDPOINT", "http://polyon-ai:4000"),
			MasterKey: envOr("LITELLM_MASTER_KEY", ""),
		},
		// 9. Auth (Keycloak OIDC)
		&AuthProvider{
			AdminURL:      envOrMulti([]string{"KC_ADMIN_URL", "KEYCLOAK_URL", "POLYON_AUTH_URL"}, "http://polyon-auth:8080"),
			Realm:         envOr("KC_REALM", "polyon"),
			AdminUser:     envOr("KC_ADMIN_USER", "admin"),
			AdminPassword: envOrMulti([]string{"KC_ADMIN_PASSWORD", "POLYON_KC_ADMIN_PASSWORD"}, ""),
			BaseDomain:    envOrMulti([]string{"BASE_DOMAIN", "POLYON_DOMAIN"}, ""),
			AuthDomain:    envOr("POLYON_AUTH_DOMAIN", ""),
		},
	}

	return providers
}

// SetDirectoryExecFn sets the DC exec function (for K8s kubectl exec).
func (e *Engine) SetDirectoryExecFn(fn func(ctx context.Context, args []string) (string, error)) {
	for _, p := range e.saga.providers {
		if dp, ok := p.(*DirectoryProvider); ok {
			dp.ExecFn = fn
		}
	}
}

// GetClaimsForModule returns saved claims for a module.
func (e *Engine) GetClaimsForModule(ctx context.Context, moduleID string) ([]map[string]any, error) {
	rows, err := e.pool.Query(ctx,
		`SELECT claim_type, claim_config, credentials, status, created_at, updated_at
		 FROM module_claims WHERE module_id=$1 ORDER BY created_at`, moduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var claims []map[string]any
	for rows.Next() {
		var claimType, status string
		var config, creds json.RawMessage
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&claimType, &config, &creds, &status, &createdAt, &updatedAt); err != nil {
			continue
		}
		claims = append(claims, map[string]any{
			"type":        claimType,
			"config":      config,
			"credentials": creds,
			"status":      status,
			"createdAt":   createdAt,
			"updatedAt":   updatedAt,
		})
	}
	return claims, nil
}

// ── Dashboard Query Methods ──

// ProviderInfo represents a Foundation provider with claim statistics.
type ProviderInfo struct {
	Type       string `json:"type"`
	Service    string `json:"service"`
	Status     string `json:"status"`
	ClaimCount int    `json:"claimCount"`
	BoundCount int    `json:"boundCount"`
}

// ClaimInfo represents a module claim with metadata.
type ClaimInfo struct {
	ID         string          `json:"id"`
	ModuleID   string          `json:"moduleId"`
	ModuleName string          `json:"moduleName"`
	ClaimType  string          `json:"claimType"`
	Config     json.RawMessage `json:"config"`
	Status     string          `json:"status"`
	Error      *string         `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// SagaLogEntry represents a provisioning history entry.
type SagaLogEntry struct {
	ID         string    `json:"id"`
	ModuleID   string    `json:"moduleId"`
	Action     string    `json:"action"`
	ClaimType  string    `json:"claimType"`
	Status     string    `json:"status"`
	DurationMs *int      `json:"durationMs,omitempty"`
	Error      *string   `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListProviders returns all Foundation providers with claim counts.
func (e *Engine) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	// Provider 정의
	defs := []struct{ Type, Service string }{
		{"database", "PostgreSQL"},
		{"cache", "Redis"},
		{"search", "OpenSearch"},
		{"objectStorage", "RustFS"},
		{"directory", "Samba AD DC"},
		{"smtp", "Stalwart Mail"},
		{"git", "Gitea"},
		{"ai", "LiteLLM"},
		{"auth", "Keycloak"},
	}

	counts := map[string][2]int{} // [total, bound]
	rows, err := e.pool.Query(ctx,
		`SELECT claim_type, COUNT(*), 
		        SUM(CASE WHEN status IN ('bound','provisioned') THEN 1 ELSE 0 END)
		 FROM module_claims WHERE status != 'removed'
		 GROUP BY claim_type`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ct string
			var total, bound int
			rows.Scan(&ct, &total, &bound)
			counts[ct] = [2]int{total, bound}
		}
	}

	result := make([]ProviderInfo, len(defs))
	for i, d := range defs {
		c := counts[d.Type]
		result[i] = ProviderInfo{
			Type:       d.Type,
			Service:    d.Service,
			Status:     "healthy",
			ClaimCount: c[0],
			BoundCount: c[1],
		}
	}
	return result, nil
}

// ListClaims returns claims with optional filters.
func (e *Engine) ListClaims(ctx context.Context, statusFilter, typeFilter, moduleFilter string) ([]ClaimInfo, error) {
	query := `SELECT mc.id, mc.module_id, mc.claim_type, mc.claim_config, mc.status,
	                 mc.error_message, mc.created_at, mc.updated_at,
	                 COALESCE(pa.name, mc.module_id)
	          FROM module_claims mc
	          LEFT JOIN polyon_apps pa ON pa.id = mc.module_id
	          WHERE mc.status != 'removed'`
	args := []any{}
	idx := 1

	if statusFilter != "" {
		query += fmt.Sprintf(" AND mc.status = $%d", idx)
		args = append(args, statusFilter)
		idx++
	}
	if typeFilter != "" {
		query += fmt.Sprintf(" AND mc.claim_type = $%d", idx)
		args = append(args, typeFilter)
		idx++
	}
	if moduleFilter != "" {
		query += fmt.Sprintf(" AND mc.module_id = $%d", idx)
		args = append(args, moduleFilter)
		idx++
	}
	query += " ORDER BY mc.created_at DESC"

	rows, err := e.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ClaimInfo
	for rows.Next() {
		var c ClaimInfo
		if err := rows.Scan(&c.ID, &c.ModuleID, &c.ClaimType, &c.Config, &c.Status,
			&c.Error, &c.CreatedAt, &c.UpdatedAt, &c.ModuleName); err != nil {
			continue
		}
		items = append(items, c)
	}
	return items, nil
}

// GetProviderResources returns claims for a specific provider type.
func (e *Engine) GetProviderResources(ctx context.Context, providerType string) ([]ClaimInfo, error) {
	return e.ListClaims(ctx, "", providerType, "")
}

// ListSagaLog returns provisioning history.
func (e *Engine) ListSagaLog(ctx context.Context, moduleFilter string, limit int) ([]SagaLogEntry, error) {
	query := `SELECT id, module_id, action, claim_type, status, duration_ms, error_message, created_at
	          FROM claim_saga_log WHERE 1=1`
	args := []any{}
	idx := 1

	if moduleFilter != "" {
		query += fmt.Sprintf(" AND module_id = $%d", idx)
		args = append(args, moduleFilter)
		idx++
	}
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", idx)
	args = append(args, limit)

	rows, err := e.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SagaLogEntry
	for rows.Next() {
		var s SagaLogEntry
		if err := rows.Scan(&s.ID, &s.ModuleID, &s.Action, &s.ClaimType, &s.Status,
			&s.DurationMs, &s.Error, &s.CreatedAt); err != nil {
			continue
		}
		items = append(items, s)
	}
	return items, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envOrMulti tries multiple env var names, returning the first non-empty value.
func envOrMulti(keys []string, fallback string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return fallback
}
