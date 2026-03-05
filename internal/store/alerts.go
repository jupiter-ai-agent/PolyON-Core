package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Alert struct {
	ID           int                    `json:"id"`
	Timestamp    time.Time              `json:"timestamp"`
	Level        string                 `json:"level"`
	Source       string                 `json:"source"`
	Service      string                 `json:"service"`
	Message      string                 `json:"message"`
	Details      map[string]interface{} `json:"details"`
	Acknowledged bool                   `json:"acknowledged"`
	AckNote      *string                `json:"ack_note"`
	AckAt        *time.Time             `json:"ack_at"`
}

func (s *Store) CreateAlert(level, service, message, source string, details map[string]interface{}, timestamp *time.Time) (*Alert, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ts := time.Now().UTC()
	if timestamp != nil {
		ts = *timestamp
	}

	detailsJSON, _ := json.Marshal(details)

	var id int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO sentinel_alerts (timestamp, level, source, service, message, details)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		ts, level, source, service, message, detailsJSON).Scan(&id)
	if err != nil {
		return nil, err
	}

	return &Alert{
		ID: id, Timestamp: ts, Level: level, Source: source,
		Service: service, Message: message, Details: details,
	}, nil
}

func (s *Store) ListAlerts(level, service string, limit int, unackedOnly bool) ([]Alert, int, error) {
	if s == nil || s.pool == nil {
		return nil, 0, nil
	}
	ctx := context.Background()

	query := `SELECT id, timestamp, level, source, service, message, details, acknowledged, ack_note, ack_at FROM sentinel_alerts`
	countQ := `SELECT count(*) FROM sentinel_alerts`
	var where string
	var args []interface{}
	n := 1

	if level != "" {
		where = fmt.Sprintf(" WHERE level = $%d", n)
		args = append(args, level)
		n++
	}
	if service != "" {
		if where == "" {
			where = fmt.Sprintf(" WHERE service = $%d", n)
		} else {
			where += fmt.Sprintf(" AND service = $%d", n)
		}
		args = append(args, service)
		n++
	}
	if unackedOnly {
		if where == "" {
			where = " WHERE acknowledged = false"
		} else {
			where += " AND acknowledged = false"
		}
	}

	var total int
	s.pool.QueryRow(ctx, countQ+where, args...).Scan(&total)

	query += where + fmt.Sprintf(" ORDER BY timestamp DESC LIMIT $%d", n)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var detailsJSON []byte
		rows.Scan(&a.ID, &a.Timestamp, &a.Level, &a.Source, &a.Service, &a.Message,
			&detailsJSON, &a.Acknowledged, &a.AckNote, &a.AckAt)
		json.Unmarshal(detailsJSON, &a.Details)
		alerts = append(alerts, a)
	}
	return alerts, total, nil
}

func (s *Store) GetAlertSummary() map[string]interface{} {
	if s == nil || s.pool == nil {
		return map[string]interface{}{"total": 0}
	}
	ctx := context.Background()

	counts := map[string]int{"INFO": 0, "WARN": 0, "CRITICAL": 0}
	unacked := map[string]int{"INFO": 0, "WARN": 0, "CRITICAL": 0}

	rows, err := s.pool.Query(ctx,
		`SELECT level, acknowledged, count(*) FROM sentinel_alerts GROUP BY level, acknowledged`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var level string
			var acked bool
			var count int
			rows.Scan(&level, &acked, &count)
			counts[level] += count
			if !acked {
				unacked[level] += count
			}
		}
	}

	total := counts["INFO"] + counts["WARN"] + counts["CRITICAL"]

	var lastEvent *string
	var ts time.Time
	if err := s.pool.QueryRow(ctx, `SELECT timestamp FROM sentinel_alerts ORDER BY timestamp DESC LIMIT 1`).Scan(&ts); err == nil {
		s := ts.Format(time.RFC3339)
		lastEvent = &s
	}

	return map[string]interface{}{
		"total":          total,
		"counts":         counts,
		"unacknowledged": unacked,
		"last_event":     lastEvent,
	}
}

func (s *Store) AckAlert(id int, note string) (*Alert, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx,
		`UPDATE sentinel_alerts SET acknowledged=true, ack_note=$1, ack_at=$2 WHERE id=$3`,
		note, now, id)
	if err != nil {
		return nil, err
	}
	return &Alert{ID: id, Acknowledged: true, AckNote: &note, AckAt: &now}, nil
}

func (s *Store) ClearAlerts(level string) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	ctx := context.Background()
	var tag interface{ RowsAffected() int64 }
	var err error
	if level != "" {
		tag, err = s.pool.Exec(ctx, `DELETE FROM sentinel_alerts WHERE level=$1`, level)
	} else {
		tag, err = s.pool.Exec(ctx, `DELETE FROM sentinel_alerts`)
	}
	if err != nil {
		return 0, err
	}
	_ = tag
	return 0, nil // pgx CommandTag doesn't have RowsAffected easily in this pattern
}

func (s *Store) GetAgentStatus() map[string]interface{} {
	if s == nil || s.pool == nil {
		return map[string]interface{}{"status": "unknown", "message": "DB not available"}
	}
	ctx := context.Background()

	var lastTs time.Time
	var lastMsg string
	err := s.pool.QueryRow(ctx,
		`SELECT timestamp, message FROM sentinel_alerts ORDER BY timestamp DESC LIMIT 1`).Scan(&lastTs, &lastMsg)
	if err != nil {
		return map[string]interface{}{"status": "unknown", "message": "에이전트로부터 수신된 데이터 없음"}
	}

	age := time.Since(lastTs)

	var critCount, warnCount int
	s.pool.QueryRow(ctx,
		`SELECT count(*) FROM sentinel_alerts WHERE level='CRITICAL' AND acknowledged=false ORDER BY timestamp DESC LIMIT 20`).Scan(&critCount)
	s.pool.QueryRow(ctx,
		`SELECT count(*) FROM sentinel_alerts WHERE level='WARN' AND acknowledged=false ORDER BY timestamp DESC LIMIT 20`).Scan(&warnCount)

	status := "healthy"
	if age > 10*time.Minute {
		status = "offline"
	} else if critCount > 0 {
		status = "critical"
	} else if warnCount > 0 {
		status = "warning"
	}

	return map[string]interface{}{
		"status":          status,
		"last_event":      lastTs.Format(time.RFC3339),
		"last_message":    lastMsg,
		"recent_critical": critCount,
		"recent_warn":     warnCount,
		"age_seconds":     int(age.Seconds()),
	}
}

func (s *Store) GetAlertMetrics() map[string]interface{} {
	if s == nil || s.pool == nil {
		return map[string]interface{}{}
	}
	ctx := context.Background()
	metrics := map[string]interface{}{}

	rows, err := s.pool.Query(ctx,
		`SELECT level, acknowledged, count(*) FROM sentinel_alerts GROUP BY level, acknowledged`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var level string
			var acked bool
			var count int
			rows.Scan(&level, &acked, &count)
			key := fmt.Sprintf(`sentinel_alerts_total{level="%s",acknowledged="%v"}`, level, acked)
			metrics[key] = count
		}
	}

	var ts float64
	if err := s.pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM timestamp) FROM sentinel_alerts ORDER BY timestamp DESC LIMIT 1`).Scan(&ts); err == nil {
		metrics["sentinel_last_event_timestamp"] = ts
	}

	return metrics
}
