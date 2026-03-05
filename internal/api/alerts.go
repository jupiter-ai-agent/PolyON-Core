package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterAlerts(r chi.Router, d *Deps) {
	r.Post("/", postAlert(d))
	r.Get("/", getAlerts(d))
	r.Get("/summary", alertSummary(d))
	r.Patch("/{id}", ackAlert(d))
	r.Delete("/", clearAlerts(d))
	r.Get("/agent-status", agentStatus(d))
	r.Get("/metrics", alertMetrics(d))
}

func postAlert(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Level     string                 `json:"level"`
			Source    string                 `json:"source"`
			Service   string                 `json:"service"`
			Message   string                 `json:"message"`
			Details   map[string]interface{} `json:"details"`
			Timestamp *string                `json:"timestamp"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Source == "" {
			req.Source = "sentinel"
		}
		alert, err := d.Store.CreateAlert(strings.ToUpper(req.Level), req.Service, req.Message, req.Source, req.Details, nil)
		if err != nil {
			httputil.RespondError(w, 500, "DB_ERROR", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"status": "ok", "id": alert.ID})
	}
}

func getAlerts(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		level := r.URL.Query().Get("level")
		service := r.URL.Query().Get("service")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		unacked := r.URL.Query().Get("unacked_only") == "true"
		alerts, total, _ := d.Store.ListAlerts(level, service, limit, unacked)
		httputil.RespondOK(w, map[string]interface{}{"total": total, "items": alerts})
	}
}

func alertSummary(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httputil.RespondOK(w, d.Store.GetAlertSummary())
	}
}

func ackAlert(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		var req struct {
			Note string `json:"note"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		_, err := d.Store.AckAlert(id, req.Note)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "not_found", "id": id})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"status": "ok", "id": id})
	}
}

func clearAlerts(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		level := r.URL.Query().Get("level")
		count, _ := d.Store.ClearAlerts(level)
		httputil.RespondOK(w, map[string]interface{}{"status": "ok", "cleared": count})
	}
}

func agentStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httputil.RespondOK(w, d.Store.GetAgentStatus())
	}
}

func alertMetrics(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		metrics := d.Store.GetAlertMetrics()
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "# HELP sentinel_alerts_total Total sentinel alerts by level and ack status")
		fmt.Fprintln(w, "# TYPE sentinel_alerts_total gauge")
		for k, v := range metrics {
			fmt.Fprintf(w, "%s %v\n", k, v)
		}
	}
}
