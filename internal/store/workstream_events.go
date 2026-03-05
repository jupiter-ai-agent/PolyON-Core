package store

import (
	"context"
	"time"
)

// WorkstreamEvent represents a Git event linked to a Workstream ID.
type WorkstreamEvent struct {
	ID           int       `json:"id"`
	WorkstreamID string    `json:"workstream_id"` // e.g. "WS-123"
	EventType    string    `json:"event_type"`    // commit, branch_created, pr_opened, pr_merged
	RepoName     string    `json:"repo_name"`
	Ref          string    `json:"ref"`     // branch or SHA
	Author       string    `json:"author"`
	Message      string    `json:"message"` // commit message or PR title
	URL          string    `json:"url"`     // link to commit/PR
	FilesChanged int       `json:"files_changed"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateWorkstreamEvent inserts a new workstream event record.
func (s *Store) CreateWorkstreamEvent(ctx context.Context, e *WorkstreamEvent) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workstream_events
			(workstream_id, event_type, repo_name, ref, author, message, url, files_changed, additions, deletions)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		e.WorkstreamID, e.EventType, e.RepoName, e.Ref, e.Author, e.Message,
		e.URL, e.FilesChanged, e.Additions, e.Deletions,
	)
	return err
}

// ListWorkstreamEvents returns events for a specific Workstream ID.
func (s *Store) ListWorkstreamEvents(ctx context.Context, workstreamID string, limit int) ([]WorkstreamEvent, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, workstream_id, event_type, repo_name, ref, author, message, url,
		       files_changed, additions, deletions, created_at
		FROM workstream_events
		WHERE workstream_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, workstreamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []WorkstreamEvent
	for rows.Next() {
		var e WorkstreamEvent
		if err := rows.Scan(&e.ID, &e.WorkstreamID, &e.EventType, &e.RepoName, &e.Ref,
			&e.Author, &e.Message, &e.URL, &e.FilesChanged, &e.Additions, &e.Deletions, &e.CreatedAt); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// ListRecentWorkstreamEvents returns the most recent events across all Workstreams.
func (s *Store) ListRecentWorkstreamEvents(ctx context.Context, limit int) ([]WorkstreamEvent, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, workstream_id, event_type, repo_name, ref, author, message, url,
		       files_changed, additions, deletions, created_at
		FROM workstream_events
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []WorkstreamEvent
	for rows.Next() {
		var e WorkstreamEvent
		if err := rows.Scan(&e.ID, &e.WorkstreamID, &e.EventType, &e.RepoName, &e.Ref,
			&e.Author, &e.Message, &e.URL, &e.FilesChanged, &e.Additions, &e.Deletions, &e.CreatedAt); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}
