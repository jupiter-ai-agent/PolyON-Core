package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
	"gopkg.in/yaml.v3"
)

const (
	prometheusContainer = "polyon-prometheus"
	alertRulesPath      = "/etc/prometheus/alert_rules.yml"
)

// ─── Data structures ──────────────────────────────────────────────────────────

type AlertRuleFile struct {
	Groups []AlertGroup `yaml:"groups" json:"groups"`
}

type AlertGroup struct {
	Name  string      `yaml:"name" json:"name"`
	Rules []AlertRule `yaml:"rules" json:"rules"`
}

type AlertRule struct {
	Alert       string            `yaml:"alert" json:"alert"`
	Expr        string            `yaml:"expr" json:"expr"`
	For         string            `yaml:"for,omitempty" json:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// ─── Router registration ──────────────────────────────────────────────────────

func RegisterAlertRules(r chi.Router, d *Deps) {
	r.Get("/", listAlertRules(d))
	r.Post("/", createAlertRule(d))
	r.Post("/reload", reloadPrometheus(d))
	r.Put("/{group}/{alert}", updateAlertRule(d))
	r.Delete("/{group}/{alert}", deleteAlertRule(d))
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func readAlertRuleFile(d *Deps) (*AlertRuleFile, error) {
	data, err := d.Docker.CopyFromContainer(prometheusContainer, alertRulesPath)
	if err != nil {
		return nil, fmt.Errorf("read alert_rules.yml: %w", err)
	}

	var f AlertRuleFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse alert_rules.yml: %w", err)
	}
	if f.Groups == nil {
		f.Groups = []AlertGroup{}
	}
	return &f, nil
}

func writeAlertRuleFile(d *Deps, f *AlertRuleFile) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal alert_rules.yml: %w", err)
	}
	return d.Docker.CopyToContainer(prometheusContainer, alertRulesPath, data)
}

func triggerReload(d *Deps) error {
	_, err := d.Docker.ExecInContainer(prometheusContainer, []string{
		"wget", "-qO-", "--post-data", "", "http://localhost:9090/prometheus/-/reload",
	})
	return err
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// GET /api/v1/alert-rules
func listAlertRules(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := readAlertRuleFile(d)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		httputil.RespondOK(w, f)
	}
}

// POST /api/v1/alert-rules
// Body: { "group": "...", "rule": { AlertRule } }
func createAlertRule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Group string    `json:"group"`
			Rule  AlertRule `json:"rule"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "bad_request", "invalid request: "+err.Error())
			return
		}
		if req.Group == "" || req.Rule.Alert == "" {
			httputil.RespondError(w, http.StatusBadRequest, "bad_request", "group and rule.alert are required")
			return
		}

		f, err := readAlertRuleFile(d)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}

		// Find or create group
		found := false
		for i, g := range f.Groups {
			if g.Name == req.Group {
				// Check for duplicate
				for _, rule := range g.Rules {
					if rule.Alert == req.Rule.Alert {
						httputil.RespondError(w, http.StatusConflict, "conflict",
						fmt.Sprintf("rule '%s' already exists in group '%s'", req.Rule.Alert, req.Group))
						return
					}
				}
				f.Groups[i].Rules = append(f.Groups[i].Rules, req.Rule)
				found = true
				break
			}
		}
		if !found {
			f.Groups = append(f.Groups, AlertGroup{
				Name:  req.Group,
				Rules: []AlertRule{req.Rule},
			})
		}

		if err := writeAlertRuleFile(d, f); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if err := triggerReload(d); err != nil {
			// Non-fatal: file is written, reload may retry
			httputil.RespondOK(w, map[string]interface{}{
				"success":       true,
				"reload_error":  err.Error(),
				"groups":        f.Groups,
			})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "groups": f.Groups})
	}
}

// PUT /api/v1/alert-rules/{group}/{alert}
func updateAlertRule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupName := chi.URLParam(r, "group")
		alertName := chi.URLParam(r, "alert")

		// URL-decode slashes if any
		groupName = strings.ReplaceAll(groupName, "%2F", "/")
		alertName = strings.ReplaceAll(alertName, "%2F", "/")

		var rule AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "bad_request", "invalid request: "+err.Error())
			return
		}

		f, err := readAlertRuleFile(d)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}

		updated := false
		for gi, g := range f.Groups {
			if g.Name == groupName {
				for ri, existingRule := range g.Rules {
					if existingRule.Alert == alertName {
						// Preserve alert name if not set in body
						if rule.Alert == "" {
							rule.Alert = alertName
						}
						f.Groups[gi].Rules[ri] = rule
						updated = true
						break
					}
				}
				break
			}
		}

		if !updated {
			httputil.RespondError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("rule '%s' not found in group '%s'", alertName, groupName))
			return
		}

		if err := writeAlertRuleFile(d, f); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if err := triggerReload(d); err != nil {
			httputil.RespondOK(w, map[string]interface{}{
				"success":      true,
				"reload_error": err.Error(),
				"groups":       f.Groups,
			})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "groups": f.Groups})
	}
}

// DELETE /api/v1/alert-rules/{group}/{alert}
func deleteAlertRule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupName := chi.URLParam(r, "group")
		alertName := chi.URLParam(r, "alert")

		groupName = strings.ReplaceAll(groupName, "%2F", "/")
		alertName = strings.ReplaceAll(alertName, "%2F", "/")

		f, err := readAlertRuleFile(d)
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}

		deleted := false
		for gi, g := range f.Groups {
			if g.Name == groupName {
				newRules := make([]AlertRule, 0, len(g.Rules))
				for _, rule := range g.Rules {
					if rule.Alert == alertName {
						deleted = true
						continue
					}
					newRules = append(newRules, rule)
				}
				f.Groups[gi].Rules = newRules
				// Remove empty group
				if len(newRules) == 0 {
					f.Groups = append(f.Groups[:gi], f.Groups[gi+1:]...)
				}
				break
			}
		}

		if !deleted {
			httputil.RespondError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("rule '%s' not found in group '%s'", alertName, groupName))
			return
		}

		if err := writeAlertRuleFile(d, f); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if err := triggerReload(d); err != nil {
			httputil.RespondOK(w, map[string]interface{}{
				"success":      true,
				"reload_error": err.Error(),
				"groups":       f.Groups,
			})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "groups": f.Groups})
	}
}

// POST /api/v1/alert-rules/reload
func reloadPrometheus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := triggerReload(d); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "internal_error", "reload failed: "+err.Error())
			return
		}
		httputil.RespondOK(w, map[string]string{"status": "reloaded"})
	}
}
