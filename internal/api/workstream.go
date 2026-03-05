package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/store"
)

// RegisterWorkstream registers Workstream event API routes.
func RegisterWorkstream(r chi.Router, d *Deps) {
	r.Route("/workstream", func(r chi.Router) {
		r.Get("/events", listWorkstreamEvents(d))           // ?ws_id=WS-123&limit=50
		r.Get("/events/recent", listRecentEvents(d))        // ?limit=20
		r.Get("/events/summary", workstreamEventSummary(d)) // summary stats
	})
}

func listWorkstreamEvents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondOK(w, []store.WorkstreamEvent{})
			return
		}
		wsID := r.URL.Query().Get("ws_id")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 50
		}

		events, err := d.Store.ListWorkstreamEvents(r.Context(), wsID, limit)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "failed to list events", err.Error())
			return
		}
		if events == nil {
			events = []store.WorkstreamEvent{}
		}
		httputil.RespondOK(w, events)
	}
}

func listRecentEvents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondOK(w, []store.WorkstreamEvent{})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 20
		}

		events, err := d.Store.ListRecentWorkstreamEvents(r.Context(), limit)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "failed to list events", err.Error())
			return
		}
		if events == nil {
			events = []store.WorkstreamEvent{}
		}
		httputil.RespondOK(w, events)
	}
}

// WorkstreamSummaryEntry holds per-workstream event count.
type WorkstreamSummaryEntry struct {
	WorkstreamID string `json:"workstream_id"`
	Count        int    `json:"count"`
}

func workstreamEventSummary(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondOK(w, []WorkstreamSummaryEntry{})
			return
		}

		rows, err := d.Store.Pool().Query(r.Context(), `
			SELECT workstream_id, COUNT(*) AS cnt
			FROM workstream_events
			GROUP BY workstream_id
			ORDER BY cnt DESC
			LIMIT 100`)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "failed to query summary", err.Error())
			return
		}
		defer rows.Close()

		var summary []WorkstreamSummaryEntry
		for rows.Next() {
			var e WorkstreamSummaryEntry
			if err := rows.Scan(&e.WorkstreamID, &e.Count); err != nil {
				continue
			}
			summary = append(summary, e)
		}
		if summary == nil {
			summary = []WorkstreamSummaryEntry{}
		}
		httputil.RespondOK(w, summary)
	}
}
