package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ── n8n DB direct-query API ────────────────────────────────────────────────
// n8n Public API requires API key auth which is difficult to provision
// automatically, so we read from the n8n PostgreSQL database directly.

func n8nDBURL(d *Deps) string {
	// Same host (polyon-db), different database (n8n)
	if d.Cfg.DatabaseURL == "" {
		return ""
	}
	// Replace database name in DSN
	// Expected format: postgres://user:pass@host:port/polyon?...
	// We need:         postgres://user:pass@host:port/n8n?...
	base := d.Cfg.DatabaseURL
	// Find last '/' before '?' and replace db name
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '/' {
			rest := ""
			dbEnd := len(base)
			for j := i + 1; j < len(base); j++ {
				if base[j] == '?' {
					rest = base[j:]
					dbEnd = j
					break
				}
			}
			_ = dbEnd
			return base[:i+1] + "n8n" + rest
		}
	}
	return base
}

// RegisterAutomation registers n8n automation engine routes.
func RegisterAutomation(r chi.Router, d *Deps) {
	r.Route("/engines/automation", func(r chi.Router) {
		r.Get("/stats", automationStats(d))
		r.Get("/workflows", automationWorkflows(d))
		r.Get("/executions", automationExecutions(d))
		r.Get("/settings", automationSettings(d))
	})
}

// ── Stats (overview) ────────────────────────────────────────────────────────

func automationStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dsn := n8nDBURL(d)
		if dsn == "" {
			httputil.RespondError(w, 500, "config_error", "n8n database URL not available")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			httputil.RespondError(w, 502, "db_error", "cannot connect to n8n DB: "+err.Error())
			return
		}
		defer conn.Close(ctx)

		var totalWf, activeWf int
		conn.QueryRow(ctx, "SELECT count(*), count(*) FILTER (WHERE active) FROM workflow_entity WHERE \"isArchived\" = false").Scan(&totalWf, &activeWf)

		var totalExec, successExec, failedExec, runningExec, waitingExec int
		conn.QueryRow(ctx, `
			SELECT 
				count(*),
				count(*) FILTER (WHERE status = 'success'),
				count(*) FILTER (WHERE status IN ('error','crashed')),
				count(*) FILTER (WHERE status = 'running'),
				count(*) FILTER (WHERE status = 'waiting')
			FROM execution_entity WHERE "deletedAt" IS NULL
		`).Scan(&totalExec, &successExec, &failedExec, &runningExec, &waitingExec)

		httputil.RespondOK(w, map[string]interface{}{
			"workflows": map[string]int{
				"total":  totalWf,
				"active": activeWf,
			},
			"executions": map[string]int{
				"total":   totalExec,
				"success": successExec,
				"failed":  failedExec,
				"running": runningExec,
				"waiting": waitingExec,
			},
		})
	}
}

// ── Workflows list ──────────────────────────────────────────────────────────

type n8nWorkflow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func automationWorkflows(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dsn := n8nDBURL(d)
		if dsn == "" {
			httputil.RespondError(w, 500, "config_error", "n8n database URL not available")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			httputil.RespondError(w, 502, "db_error", "cannot connect to n8n DB: "+err.Error())
			return
		}
		defer conn.Close(ctx)

		rows, err := conn.Query(ctx, `
			SELECT id, name, active, "createdAt", "updatedAt"
			FROM workflow_entity
			WHERE "isArchived" = false
			ORDER BY "updatedAt" DESC
			LIMIT 100
		`)
		if err != nil {
			httputil.RespondError(w, 500, "query_error", err.Error())
			return
		}
		defer rows.Close()

		var workflows []n8nWorkflow
		for rows.Next() {
			var wf n8nWorkflow
			var createdAt, updatedAt time.Time
			if err := rows.Scan(&wf.ID, &wf.Name, &wf.Active, &createdAt, &updatedAt); err != nil {
				continue
			}
			wf.CreatedAt = createdAt.Format(time.RFC3339)
			wf.UpdatedAt = updatedAt.Format(time.RFC3339)
			workflows = append(workflows, wf)
		}
		if workflows == nil {
			workflows = []n8nWorkflow{}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"workflows": workflows,
			"total":     len(workflows),
		})
	}
}

// ── Executions list ─────────────────────────────────────────────────────────

type n8nExecution struct {
	ID         int    `json:"id"`
	WorkflowID string `json:"workflowId"`
	Workflow   string `json:"workflow"`
	Status     string `json:"status"`
	Mode       string `json:"mode"`
	StartedAt  string `json:"startedAt,omitempty"`
	StoppedAt  string `json:"stoppedAt,omitempty"`
	Finished   bool   `json:"finished"`
}

func automationExecutions(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dsn := n8nDBURL(d)
		if dsn == "" {
			httputil.RespondError(w, 500, "config_error", "n8n database URL not available")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			httputil.RespondError(w, 502, "db_error", "cannot connect to n8n DB: "+err.Error())
			return
		}
		defer conn.Close(ctx)

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
				limit = v
			}
		}

		statusFilter := r.URL.Query().Get("status")
		query := `
			SELECT e.id, e."workflowId", COALESCE(w.name, ''), e.status, e.mode,
			       e."startedAt", e."stoppedAt", e.finished
			FROM execution_entity e
			LEFT JOIN workflow_entity w ON w.id = e."workflowId"
			WHERE e."deletedAt" IS NULL
		`
		args := []interface{}{}
		if statusFilter != "" {
			query += " AND e.status = $1"
			args = append(args, statusFilter)
		}
		query += ` ORDER BY e."startedAt" DESC NULLS LAST LIMIT ` + strconv.Itoa(limit)

		rows, err := conn.Query(ctx, query, args...)
		if err != nil {
			httputil.RespondError(w, 500, "query_error", err.Error())
			return
		}
		defer rows.Close()

		var executions []n8nExecution
		for rows.Next() {
			var ex n8nExecution
			var startedAt, stoppedAt *time.Time
			if err := rows.Scan(&ex.ID, &ex.WorkflowID, &ex.Workflow, &ex.Status, &ex.Mode, &startedAt, &stoppedAt, &ex.Finished); err != nil {
				continue
			}
			if startedAt != nil {
				ex.StartedAt = startedAt.Format(time.RFC3339)
			}
			if stoppedAt != nil {
				ex.StoppedAt = stoppedAt.Format(time.RFC3339)
			}
			executions = append(executions, ex)
		}
		if executions == nil {
			executions = []n8nExecution{}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"executions": executions,
			"total":      len(executions),
		})
	}
}

// ── Settings ────────────────────────────────────────────────────────────────

func automationSettings(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result := map[string]interface{}{}

		// 1. Container info from Docker
		if d.Docker != nil {
			containers, err := d.Docker.ContainerList(ctx)
			if err == nil {
				for _, c := range containers {
					if c.Name == "polyon-n8n" {
						result["container"] = map[string]string{
							"name":   c.Name,
							"image":  c.Image,
							"status": c.Status,
							"state":  c.State,
						}
						break
					}
				}
			}

			// Get environment variables via raw Docker inspect
			if d.Docker != nil {
				info, err := d.Docker.RawInspect(ctx, "polyon-n8n")
				if err == nil {
					// Parse env vars into categorized groups
					engine := map[string]string{}
					database := map[string]string{}
					auth := map[string]string{}
					other := map[string]string{}

					for _, env := range info.Env {
						parts := strings.SplitN(env, "=", 2)
						if len(parts) != 2 {
							continue
						}
						k, v := parts[0], parts[1]

						// Mask passwords/secrets
						kl := strings.ToLower(k)
						if strings.Contains(kl, "password") || strings.Contains(kl, "secret") {
							v = v[:min(3, len(v))] + "********"
						}

						switch {
						case strings.HasPrefix(k, "DB_"):
							database[k] = v
						case strings.HasPrefix(k, "N8N_SSO_") || strings.HasPrefix(k, "N8N_AUTH"):
							auth[k] = v
						case strings.HasPrefix(k, "N8N_"):
							engine[k] = v
						case k == "GENERIC_TIMEZONE":
							engine[k] = v
						default:
							// skip system env vars (PATH, HOME, etc.)
							if k == "PATH" || k == "HOME" || k == "NODE_VERSION" || k == "YARN_VERSION" || k == "HOSTNAME" {
								continue
							}
							other[k] = v
						}
					}

					result["engine"] = sortedMap(engine)
					result["database"] = sortedMap(database)
					result["auth"] = sortedMap(auth)
					if len(other) > 0 {
						result["other"] = sortedMap(other)
					}

					// Resource limits
					if info.Memory > 0 {
						result["resources"] = map[string]interface{}{
							"memoryLimit": info.Memory,
						}
					}
				}
			}
		}

		httputil.RespondOK(w, result)
	}
}

// sortedMap converts a map to a sorted slice of key-value pairs for consistent display.
func sortedMap(m map[string]string) []map[string]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]map[string]string, len(keys))
	for i, k := range keys {
		result[i] = map[string]string{"key": k, "value": m[k]}
	}
	return result
}


