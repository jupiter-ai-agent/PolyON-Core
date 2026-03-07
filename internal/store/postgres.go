// Package store provides PostgreSQL persistence for audit logs and alerts.
package store

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Store wraps a PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a new Store, running migrations.
// Retries connection up to maxRetries times with backoff if DB is not ready.
func New(databaseURL string) (*Store, error) {
	const maxRetries = 15
	const initialBackoff = 2 * time.Second

	var pool *pgxpool.Pool
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		var err error
		pool, err = pgxpool.New(ctx, databaseURL)
		if err != nil {
			cancel()
			lastErr = err
			backoff := initialBackoff * time.Duration(attempt)
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			log.Warn().Err(err).Int("attempt", attempt).Int("max", maxRetries).
				Dur("retry_in", backoff).Msg("DB connection failed, retrying...")
			time.Sleep(backoff)
			continue
		}

		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			cancel()
			lastErr = err
			backoff := initialBackoff * time.Duration(attempt)
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			log.Warn().Err(err).Int("attempt", attempt).Int("max", maxRetries).
				Dur("retry_in", backoff).Msg("DB ping failed, retrying...")
			time.Sleep(backoff)
			continue
		}

		cancel()
		log.Info().Int("attempt", attempt).Msg("DB connected successfully")
		s := &Store{pool: pool}
		s.migrate()
		return s, nil
	}

	return nil, fmt.Errorf("DB connection failed after %d attempts: %w", maxRetries, lastErr)
}

// EnsureStrapiDB creates the `strapi` database if it doesn't exist.
// Uses the same credentials as the main polyon DB.
// Called during startup to ensure Strapi's DB is ready before the container starts.
func EnsureStrapiDB(databaseURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Connect to the `polyon` DB as polyon user (already have access)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		log.Warn().Err(err).Msg("EnsureStrapiDB: cannot connect to DB")
		return
	}
	defer conn.Close(context.Background())

	// Check if strapi DB exists
	var exists bool
	err = conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='strapi')`).Scan(&exists)
	if err != nil {
		log.Warn().Err(err).Msg("EnsureStrapiDB: check failed")
		return
	}
	if exists {
		log.Debug().Msg("EnsureStrapiDB: strapi DB already exists")
		return
	}

	// CREATE DATABASE cannot run inside a transaction, use Exec directly
	// Note: pgx executes outside transaction by default
	dbUser := "polyon"
	if u := os.Getenv("DB_USER"); u != "" {
		dbUser = u
	}
	_, err = conn.Exec(ctx,
		fmt.Sprintf(`CREATE DATABASE strapi OWNER %s`, dbUser))
	if err != nil {
		// May fail if another instance created it concurrently — not a problem
		log.Warn().Err(err).Msg("EnsureStrapiDB: create failed (may be ok if already exists)")
		return
	}
	log.Info().Msg("EnsureStrapiDB: strapi database created")
}

// Close closes the connection pool.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// Pool returns the underlying pgxpool for direct use if needed.
func (s *Store) Pool() *pgxpool.Pool {
	if s == nil {
		return nil
	}
	return s.pool
}

func (s *Store) migrate() {
	ctx := context.Background()

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
		`CREATE INDEX IF NOT EXISTS idx_alerts_timestamp ON sentinel_alerts(timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_level ON sentinel_alerts(level)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_service ON sentinel_alerts(service)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_acked ON sentinel_alerts(acknowledged)`,
		`CREATE TABLE IF NOT EXISTS sentinel_events (
			id SERIAL PRIMARY KEY,
			timestamp TIMESTAMPTZ DEFAULT NOW(),
			status TEXT NOT NULL,
			summary TEXT NOT NULL,
			details JSONB DEFAULT '{}',
			alerts_generated INT DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sentinel_events_timestamp ON sentinel_events(timestamp DESC)`,
		// Homepage sites
		`CREATE TABLE IF NOT EXISTS sites (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(100) NOT NULL,
			slug VARCHAR(100) NOT NULL UNIQUE,
			method VARCHAR(10) NOT NULL CHECK (method IN ('editor','git','strapi')),
			status VARCHAR(20) NOT NULL DEFAULT 'creating',
			layout_json JSONB,
			repo_url VARCHAR(500),
			branch VARCHAR(100) DEFAULT 'main',
			framework VARCHAR(20),
			build_cmd VARCHAR(500),
			output_dir VARCHAR(200),
			domain VARCHAR(255),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Migration: broaden sites.method check to include 'strapi' for existing DBs
		`DO $$ BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.constraint_column_usage
				WHERE table_name = 'sites' AND constraint_name LIKE '%sites_method%'
			) THEN
				ALTER TABLE sites DROP CONSTRAINT IF EXISTS sites_method_check;
				ALTER TABLE sites ADD CONSTRAINT sites_method_check
					CHECK (method IN ('editor', 'git', 'strapi'));
			END IF;
		END $$`,
		`CREATE INDEX IF NOT EXISTS idx_sites_slug ON sites(slug)`,
		`CREATE INDEX IF NOT EXISTS idx_sites_status ON sites(status)`,
		// Migration: add template column to existing sites tables
		`ALTER TABLE sites ADD COLUMN IF NOT EXISTS template VARCHAR(20) DEFAULT 'corporate'`,
		`CREATE TABLE IF NOT EXISTS site_builds (
			id SERIAL PRIMARY KEY,
			site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			finished_at TIMESTAMPTZ,
			log TEXT DEFAULT '',
			trigger VARCHAR(20) DEFAULT 'manual',
			error TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_site_builds_site ON site_builds(site_id)`,
		// polyon_config — key/value store for global platform configuration
		`CREATE TABLE IF NOT EXISTS polyon_config (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		// Migration: add domain_status to polyon_apps
		`ALTER TABLE polyon_apps ADD COLUMN IF NOT EXISTS domain_status TEXT DEFAULT 'unconfigured'`,
		// Workstream Events — Git commit/PR events linked to Workstream IDs
		`CREATE TABLE IF NOT EXISTS workstream_events (
			id SERIAL PRIMARY KEY,
			workstream_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			repo_name TEXT NOT NULL,
			ref TEXT DEFAULT '',
			author TEXT DEFAULT '',
			message TEXT DEFAULT '',
			url TEXT DEFAULT '',
			files_changed INT DEFAULT 0,
			additions INT DEFAULT 0,
			deletions INT DEFAULT 0,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ws_events_wsid ON workstream_events(workstream_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ws_events_created ON workstream_events(created_at DESC)`,
		// Backup records — Phase 1
		`CREATE TABLE IF NOT EXISTS polyon_backups (
			id TEXT PRIMARY KEY,
			tier INT NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'running',
			size BIGINT DEFAULT 0,
			path TEXT NOT NULL,
			error TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
	}

	for _, m := range migrations {
		if _, err := s.pool.Exec(ctx, m); err != nil {
			log.Warn().Err(err).Msg("Migration failed")
		}
	}

	// Apps catalog table
	s.migrateApps()

	// Infrastructure services table (Traefik routing source of truth)
	s.migrateInfraServices()

	// System Manifest — single source of truth for all PolyON components
	s.migrateComponents()

	// Module System — PP module registry and lifecycle management
	s.migrateModules()
}
