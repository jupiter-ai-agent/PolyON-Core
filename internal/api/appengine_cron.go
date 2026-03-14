package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineCron registers AppEngine scheduled task (ir.cron) management routes.
func RegisterAppEngineCron(r chi.Router, d *Deps) {
	r.Route("/appengine/cron", func(r chi.Router) {
		r.Get("/", listAppEngineCron(d))
		r.Put("/{id}", updateAppEngineCron(d))
		r.Post("/{id}/run", runAppEngineCron(d))
	})
}

// cronRecord maps ir.cron fields (Odoo 19).
type cronRecord struct {
	ID             int64       `json:"id"`
	Name           string      `json:"name"`
	Active         bool        `json:"active"`
	IntervalNumber int         `json:"interval_number"`
	IntervalType   string      `json:"interval_type"` // minutes/hours/days/weeks/months
	NextCall       interface{} `json:"nextcall"`
	LastCall       interface{} `json:"lastcall"`
	Priority       int         `json:"priority"`
	FailureCount   int         `json:"failure_count"`
}

// toCronRecord converts a raw Odoo map to cronRecord (Odoo 19).
func toCronRecord(m map[string]interface{}) cronRecord {
	c := cronRecord{}
	if v, ok := m["id"]; ok {
		switch x := v.(type) {
		case float64:
			c.ID = int64(x)
		}
	}
	// Odoo 19: name은 ir_act_server의 name을 통해 오거나 cron_name 사용
	if v, ok := m["cron_name"].(string); ok && v != "" {
		c.Name = v
	} else if v, ok := m["name"].(string); ok {
		c.Name = v
	}
	if v, ok := m["active"].(bool); ok {
		c.Active = v
	}
	if v, ok := m["interval_number"].(float64); ok {
		c.IntervalNumber = int(v)
	}
	if v, ok := m["interval_type"].(string); ok {
		c.IntervalType = v
	}
	c.NextCall = m["nextcall"]
	c.LastCall = m["lastcall"]
	if v, ok := m["priority"].(float64); ok {
		c.Priority = int(v)
	}
	if v, ok := m["failure_count"].(float64); ok {
		c.FailureCount = int(v)
	}
	return c
}

// listAppEngineCron returns all ir.cron records.
func listAppEngineCron(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		fields := []string{"id", "cron_name", "active", "interval_number", "interval_type", "nextcall", "lastcall", "priority", "failure_count"}
		records, err := d.OdooClient.SearchRead("ir.cron", []interface{}{}, fields, 0, 0)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("SearchRead ir.cron failed: %v", err))
			return
		}

		crons := make([]cronRecord, 0, len(records))
		for _, rec := range records {
			crons = append(crons, toCronRecord(rec))
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"crons":   crons,
			"count":   len(crons),
		})
	}
}

// updateAppEngineCron updates an ir.cron record fields.
func updateAppEngineCron(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		idStr := chi.URLParam(r, "id")
		cronID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid cron id")
			return
		}

		var req struct {
			Active         *bool   `json:"active"`
			IntervalNumber *int    `json:"interval_number"`
			IntervalType   *string `json:"interval_type"`
			NextCall       *string `json:"nextcall"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}

		vals := map[string]interface{}{}
		if req.Active != nil {
			vals["active"] = *req.Active
		}
		if req.IntervalNumber != nil {
			vals["interval_number"] = *req.IntervalNumber
		}
		if req.IntervalType != nil {
			vals["interval_type"] = *req.IntervalType
		}
		if req.NextCall != nil {
			vals["nextcall"] = *req.NextCall
		}

		if len(vals) == 0 {
			httputil.RespondError(w, 400, "BAD_REQUEST", "No fields to update")
			return
		}

		_, err = d.OdooClient.Call("ir.cron", "write", []interface{}{[]int{int(cronID)}, vals}, nil)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("write ir.cron failed: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "스케줄러가 업데이트되었습니다.",
		})
	}
}

// runAppEngineCron triggers an ir.cron task manually.
func runAppEngineCron(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		idStr := chi.URLParam(r, "id")
		cronID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid cron id")
			return
		}

		_, err = d.OdooClient.Call("ir.cron", "method_direct_trigger", []interface{}{[]int{int(cronID)}}, nil)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("method_direct_trigger failed: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "스케줄러가 실행되었습니다.",
		})
	}
}
