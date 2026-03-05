package store

import (
	"context"
	"time"
)

// BackupRecord represents a single backup entry in the database.
type BackupRecord struct {
	ID        string    `json:"id"`         // YYYYMMDD-HHMMSS
	Tier      int       `json:"tier"`       // 1 or 2
	Status    string    `json:"status"`     // running, complete, failed
	Size      int64     `json:"size"`       // bytes
	Path      string    `json:"path"`       // /backup/polyon/YYYYMMDD-HHMMSS/
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateBackup inserts a new backup record.
func (s *Store) CreateBackup(ctx context.Context, rec *BackupRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO polyon_backups (id, tier, status, size, path, error, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		rec.ID, rec.Tier, rec.Status, rec.Size, rec.Path, rec.Error, rec.CreatedAt,
	)
	return err
}

// UpdateBackup updates status, size, and error for a backup record.
func (s *Store) UpdateBackup(ctx context.Context, id string, status string, size int64, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE polyon_backups SET status=$1, size=$2, error=$3 WHERE id=$4`,
		status, size, errMsg, id,
	)
	return err
}

// ListBackups returns the most recent 50 backups ordered by created_at DESC.
func (s *Store) ListBackups(ctx context.Context) ([]BackupRecord, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tier, status, size, path, error, created_at
		 FROM polyon_backups
		 ORDER BY created_at DESC LIMIT 50`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []BackupRecord
	for rows.Next() {
		var r BackupRecord
		if err := rows.Scan(&r.ID, &r.Tier, &r.Status, &r.Size, &r.Path, &r.Error, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	if records == nil {
		records = []BackupRecord{}
	}
	return records, nil
}

// GetBackup returns a single backup record by ID.
func (s *Store) GetBackup(ctx context.Context, id string) (*BackupRecord, error) {
	var r BackupRecord
	err := s.pool.QueryRow(ctx,
		`SELECT id, tier, status, size, path, error, created_at
		 FROM polyon_backups WHERE id=$1`,
		id,
	).Scan(&r.ID, &r.Tier, &r.Status, &r.Size, &r.Path, &r.Error, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteBackupRecord removes a backup record from the database.
func (s *Store) DeleteBackupRecord(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM polyon_backups WHERE id=$1`, id)
	return err
}
