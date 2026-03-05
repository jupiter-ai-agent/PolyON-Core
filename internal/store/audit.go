package store

import (
	"context"
	"time"
)

type AuditEntry struct {
	ID         int       `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Action     string    `json:"action"`
	ObjectType string    `json:"object_type"`
	ObjectName string    `json:"object_name"`
	Actor      string    `json:"actor"`
	Details    string    `json:"details"`
	IPAddress  string    `json:"ip_address"`
}

func (s *Store) LogAction(action, objectType, objectName, details, actor, ip string) {
	if s == nil || s.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.pool.Exec(ctx,
		`INSERT INTO audit_log (action, object_type, object_name, details, actor, ip_address) VALUES ($1,$2,$3,$4,$5,$6)`,
		action, objectType, objectName, details, actor, ip)
}

func (s *Store) GetAuditLogs(limit, offset int, objectType, action string) ([]AuditEntry, int, error) {
	if s == nil || s.pool == nil {
		return nil, 0, nil
	}
	ctx := context.Background()

	// Build query
	query := `SELECT id, timestamp, action, object_type, object_name, actor, details, ip_address FROM audit_log`
	countQuery := `SELECT count(*) FROM audit_log`
	var args []interface{}
	var where string
	argN := 1

	if objectType != "" {
		where += ` WHERE object_type = $` + itoa(argN)
		args = append(args, objectType)
		argN++
	}
	if action != "" {
		if where == "" {
			where += ` WHERE`
		} else {
			where += ` AND`
		}
		where += ` action = $` + itoa(argN)
		args = append(args, action)
		argN++
	}

	query += where + ` ORDER BY timestamp DESC LIMIT $` + itoa(argN) + ` OFFSET $` + itoa(argN+1)
	countQuery += where
	args = append(args, limit, offset)

	var total int
	s.pool.QueryRow(ctx, countQuery, args[:argN-1]...).Scan(&total)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		rows.Scan(&e.ID, &e.Timestamp, &e.Action, &e.ObjectType, &e.ObjectName, &e.Actor, &e.Details, &e.IPAddress)
		entries = append(entries, e)
	}
	return entries, total, nil
}

func itoa(i int) string {
	return string(rune('0' + i))
}
