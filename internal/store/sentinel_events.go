package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SentinelEvent represents a single LLM analysis cycle result.
type SentinelEvent struct {
	ID              int                    `json:"id"`
	Timestamp       time.Time              `json:"timestamp"`
	Status          string                 `json:"status"`
	Summary         string                 `json:"summary"`
	Details         map[string]interface{} `json:"details"`
	AlertsGenerated int                    `json:"alerts_generated"`
}

// CreateSentinelEvent inserts a new sentinel event into the DB.
func (s *Store) CreateSentinelEvent(status, summary string, details map[string]interface{}, alertsGenerated int) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("store not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	detailsJSON, _ := json.Marshal(details)

	_, err := s.pool.Exec(ctx,
		`INSERT INTO sentinel_events (status, summary, details, alerts_generated)
		 VALUES ($1, $2, $3, $4)`,
		status, summary, detailsJSON, alertsGenerated)
	return err
}

// ListSentinelEvents returns the most recent sentinel events.
func (s *Store) ListSentinelEvents(limit int) ([]SentinelEvent, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT id, timestamp, status, summary, details, alerts_generated
		 FROM sentinel_events
		 ORDER BY timestamp DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []SentinelEvent
	for rows.Next() {
		var e SentinelEvent
		var detailsJSON []byte
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Status, &e.Summary, &detailsJSON, &e.AlertsGenerated); err != nil {
			continue
		}
		json.Unmarshal(detailsJSON, &e.Details)
		events = append(events, e)
	}
	if events == nil {
		events = []SentinelEvent{}
	}
	return events, nil
}
