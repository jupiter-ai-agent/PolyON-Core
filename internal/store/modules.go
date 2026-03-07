package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// Module represents a PolyON module in the Module System.
type Module struct {
	ID           string          `json:"id" db:"id"`
	Name         string          `json:"name" db:"name"`
	Description  string          `json:"description" db:"description"`
	Category     string          `json:"category" db:"category"`
	Version      string          `json:"version" db:"version"`
	Engine       string          `json:"engine" db:"engine"`
	Image        string          `json:"image" db:"image"`
	Icon         string          `json:"icon" db:"icon"`
	Accent       string          `json:"accent" db:"accent"`
	Status       string          `json:"status" db:"status"`
	Requires     json.RawMessage `json:"requires" db:"requires"`
	OptionalDeps json.RawMessage `json:"optionalDeps" db:"optional_deps"`
	Manifest     json.RawMessage `json:"manifest" db:"manifest"`
	InstalledAt  *time.Time      `json:"installedAt,omitempty" db:"installed_at"`
	UpdatedAt    time.Time       `json:"updatedAt" db:"updated_at"`
	CreatedAt    time.Time       `json:"createdAt" db:"created_at"`
}

// ModuleNav represents navigation information for a module.
type ModuleNav struct {
	ModuleID    string          `json:"moduleId" db:"module_id"`
	Title       string          `json:"title" db:"title"`
	Section     string          `json:"section" db:"section"`
	Icon        string          `json:"icon" db:"icon"`
	DefaultPath string          `json:"defaultPath" db:"default_path"`
	SortOrder   int             `json:"sortOrder" db:"sort_order"`
	NavItems    json.RawMessage `json:"items" db:"nav_items"`
	Routes      json.RawMessage `json:"routes" db:"routes"`
	CreatedAt   time.Time       `json:"createdAt" db:"created_at"`
}

// ModuleEvent represents an event in the module lifecycle.
type ModuleEvent struct {
	ID        int             `json:"id" db:"id"`
	ModuleID  string          `json:"moduleId" db:"module_id"`
	EventType string          `json:"eventType" db:"event_type"`
	Status    string          `json:"status" db:"status"`
	Message   *string         `json:"message,omitempty" db:"message"`
	Details   json.RawMessage `json:"details,omitempty" db:"details"`
	CreatedAt time.Time       `json:"createdAt" db:"created_at"`
}

// migrateModules creates the module system tables.
func (s *Store) migrateModules() {
	ctx := context.Background()

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS polyon_modules (
			id               TEXT PRIMARY KEY,
			name             TEXT NOT NULL,
			description      TEXT NOT NULL DEFAULT '',
			category         TEXT NOT NULL,
			version          TEXT NOT NULL DEFAULT '',
			engine           TEXT NOT NULL DEFAULT '',
			image            TEXT NOT NULL,
			icon             TEXT NOT NULL DEFAULT '',
			accent           TEXT NOT NULL DEFAULT '#393939',
			status           TEXT NOT NULL DEFAULT 'available',
			requires         JSONB NOT NULL DEFAULT '[]',
			optional_deps    JSONB NOT NULL DEFAULT '[]',
			manifest         JSONB NOT NULL DEFAULT '{}',
			installed_at     TIMESTAMPTZ,
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS polyon_module_nav (
			module_id        TEXT PRIMARY KEY REFERENCES polyon_modules(id) ON DELETE CASCADE,
			title            TEXT NOT NULL,
			section          TEXT NOT NULL DEFAULT 'SERVICES',
			icon             TEXT NOT NULL DEFAULT '',
			default_path     TEXT NOT NULL,
			sort_order       INTEGER NOT NULL DEFAULT 50,
			nav_items        JSONB NOT NULL DEFAULT '[]',
			routes           JSONB NOT NULL DEFAULT '[]',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS polyon_module_events (
			id               SERIAL PRIMARY KEY,
			module_id        TEXT NOT NULL,
			event_type       TEXT NOT NULL,
			status           TEXT NOT NULL,
			message          TEXT,
			details          JSONB DEFAULT '{}',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_modules_category ON polyon_modules(category)`,
		`CREATE INDEX IF NOT EXISTS idx_modules_status ON polyon_modules(status)`,
		`CREATE INDEX IF NOT EXISTS idx_module_nav_section ON polyon_module_nav(section)`,
		`CREATE INDEX IF NOT EXISTS idx_module_events_module ON polyon_module_events(module_id)`,
		`CREATE INDEX IF NOT EXISTS idx_module_events_created ON polyon_module_events(created_at DESC)`,
	}

	for _, migration := range migrations {
		if _, err := s.pool.Exec(ctx, migration); err != nil {
			log.Warn().Err(err).Msg("Module migration failed")
		}
	}
}

// ListModules returns all modules, optionally filtered by category and status.
func (s *Store) ListModules(ctx context.Context, category, status string) ([]Module, error) {
	query := `
		SELECT id, name, description, category, version, engine, image, icon, accent,
		       status, requires, optional_deps, manifest, installed_at, updated_at, created_at
		FROM polyon_modules
		WHERE 1=1`
	args := []any{}
	argNum := 1

	if category != "" {
		query += ` AND category = $` + fmt.Sprintf("%d", argNum)
		args = append(args, category)
		argNum++
	}
	if status != "" {
		query += ` AND status = $` + fmt.Sprintf("%d", argNum)
		args = append(args, status)
	}

	query += ` ORDER BY category, name`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var modules []Module
	for rows.Next() {
		var m Module
		err := rows.Scan(
			&m.ID, &m.Name, &m.Description, &m.Category, &m.Version, &m.Engine,
			&m.Image, &m.Icon, &m.Accent, &m.Status, &m.Requires, &m.OptionalDeps,
			&m.Manifest, &m.InstalledAt, &m.UpdatedAt, &m.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		modules = append(modules, m)
	}

	return modules, rows.Err()
}

// GetModule returns a single module by ID.
func (s *Store) GetModule(ctx context.Context, id string) (*Module, error) {
	query := `
		SELECT id, name, description, category, version, engine, image, icon, accent,
		       status, requires, optional_deps, manifest, installed_at, updated_at, created_at
		FROM polyon_modules
		WHERE id = $1`

	var m Module
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&m.ID, &m.Name, &m.Description, &m.Category, &m.Version, &m.Engine,
		&m.Image, &m.Icon, &m.Accent, &m.Status, &m.Requires, &m.OptionalDeps,
		&m.Manifest, &m.InstalledAt, &m.UpdatedAt, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &m, nil
}

// CreateModule inserts a new module into the database.
func (s *Store) CreateModule(ctx context.Context, module Module) error {
	query := `
		INSERT INTO polyon_modules (
			id, name, description, category, version, engine, image, icon, accent,
			status, requires, optional_deps, manifest
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	_, err := s.pool.Exec(ctx, query,
		module.ID, module.Name, module.Description, module.Category, module.Version,
		module.Engine, module.Image, module.Icon, module.Accent, module.Status,
		module.Requires, module.OptionalDeps, module.Manifest,
	)
	return err
}

// UpdateModuleStatus updates only the status field of a module.
func (s *Store) UpdateModuleStatus(ctx context.Context, id, status string) error {
	query := `
		UPDATE polyon_modules 
		SET status = $2, updated_at = NOW()
		WHERE id = $1`

	_, err := s.pool.Exec(ctx, query, id, status)
	return err
}

// DeleteModule removes a module from the database.
func (s *Store) DeleteModule(ctx context.Context, id string) error {
	query := `DELETE FROM polyon_modules WHERE id = $1`
	_, err := s.pool.Exec(ctx, query, id)
	return err
}

// GetModuleNav returns navigation info for a specific module.
func (s *Store) GetModuleNav(ctx context.Context, moduleId string) (*ModuleNav, error) {
	query := `
		SELECT module_id, title, section, icon, default_path, sort_order,
		       nav_items, routes, created_at
		FROM polyon_module_nav
		WHERE module_id = $1`

	var nav ModuleNav
	err := s.pool.QueryRow(ctx, query, moduleId).Scan(
		&nav.ModuleID, &nav.Title, &nav.Section, &nav.Icon, &nav.DefaultPath,
		&nav.SortOrder, &nav.NavItems, &nav.Routes, &nav.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &nav, nil
}

// ListActiveModuleNav returns navigation info for all active modules.
func (s *Store) ListActiveModuleNav(ctx context.Context) ([]ModuleNav, error) {
	query := `
		SELECT n.module_id, n.title, n.section, n.icon, n.default_path, n.sort_order,
		       n.nav_items, n.routes, n.created_at
		FROM polyon_module_nav n
		JOIN polyon_modules m ON n.module_id = m.id
		WHERE m.status = 'active'
		ORDER BY n.section, n.sort_order, n.title`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var navs []ModuleNav
	for rows.Next() {
		var nav ModuleNav
		err := rows.Scan(
			&nav.ModuleID, &nav.Title, &nav.Section, &nav.Icon, &nav.DefaultPath,
			&nav.SortOrder, &nav.NavItems, &nav.Routes, &nav.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		navs = append(navs, nav)
	}

	return navs, rows.Err()
}

// SaveModuleNav inserts or updates module navigation info.
func (s *Store) SaveModuleNav(ctx context.Context, nav ModuleNav) error {
	query := `
		INSERT INTO polyon_module_nav (
			module_id, title, section, icon, default_path, sort_order,
			nav_items, routes
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		ON CONFLICT (module_id) DO UPDATE SET
			title = EXCLUDED.title,
			section = EXCLUDED.section,
			icon = EXCLUDED.icon,
			default_path = EXCLUDED.default_path,
			sort_order = EXCLUDED.sort_order,
			nav_items = EXCLUDED.nav_items,
			routes = EXCLUDED.routes`

	_, err := s.pool.Exec(ctx, query,
		nav.ModuleID, nav.Title, nav.Section, nav.Icon, nav.DefaultPath,
		nav.SortOrder, nav.NavItems, nav.Routes,
	)
	return err
}

// DeleteModuleNav removes navigation info for a module.
func (s *Store) DeleteModuleNav(ctx context.Context, moduleId string) error {
	query := `DELETE FROM polyon_module_nav WHERE module_id = $1`
	_, err := s.pool.Exec(ctx, query, moduleId)
	return err
}

// CreateModuleEvent records an event in the module lifecycle.
func (s *Store) CreateModuleEvent(ctx context.Context, moduleId, eventType, status, message string, details map[string]any) error {
	var detailsJSON json.RawMessage
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return err
		}
		detailsJSON = b
	}

	query := `
		INSERT INTO polyon_module_events (module_id, event_type, status, message, details)
		VALUES ($1, $2, $3, $4, $5)`

	var msg *string
	if message != "" {
		msg = &message
	}

	_, err := s.pool.Exec(ctx, query, moduleId, eventType, status, msg, detailsJSON)
	return err
}

// ListModuleEvents returns recent events for a module.
func (s *Store) ListModuleEvents(ctx context.Context, moduleId string, limit int) ([]ModuleEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, module_id, event_type, status, message, details, created_at
		FROM polyon_module_events
		WHERE module_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, query, moduleId, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ModuleEvent
	for rows.Next() {
		var event ModuleEvent
		err := rows.Scan(
			&event.ID, &event.ModuleID, &event.EventType, &event.Status,
			&event.Message, &event.Details, &event.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	return events, rows.Err()
}