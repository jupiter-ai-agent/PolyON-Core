package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetConfig retrieves a single config value by key.
// Returns ("", pgx.ErrNoRows) if key does not exist.
func (s *Store) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM polyon_config WHERE key = $1`, key,
	).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", pgx.ErrNoRows
		}
		return "", err
	}
	return value, nil
}

// SetConfig upserts a single config key/value pair.
func (s *Store) SetConfig(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO polyon_config (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value,
	)
	return err
}

// GetConfigs retrieves multiple config values in a single query.
// Returns a map of key → value for keys that exist; missing keys are omitted.
func (s *Store) GetConfigs(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	// Build parameter list ($1, $2, ...)
	args := make([]interface{}, len(keys))
	for i, k := range keys {
		args[i] = k
	}

	rows, err := s.pool.Query(ctx,
		`SELECT key, value FROM polyon_config WHERE key = ANY($1)`,
		keys,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, rows.Err()
}

// SetConfigs upserts multiple key/value pairs atomically.
func (s *Store) SetConfigs(ctx context.Context, kvs map[string]string) error {
	if len(kvs) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for k, v := range kvs {
		_, err := tx.Exec(ctx,
			`INSERT INTO polyon_config (key, value, updated_at)
			 VALUES ($1, $2, NOW())
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
			k, v,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UpdateAppDomain sets the subdomain and domain_status for an app.
func (s *Store) UpdateAppDomain(ctx context.Context, appID, subdomain, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE polyon_apps SET subdomain = $1, domain_status = $2, updated_at = NOW()
		 WHERE id = $3`,
		subdomain, status, appID,
	)
	return err
}
