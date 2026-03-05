package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/monitor"
)

const sentinelConfigFile = "sentinel-config.json"

func RegisterSentinel(r chi.Router, d *Deps) {
	r.Route("/sentinel", func(r chi.Router) {
		r.Get("/config", getSentinelConfig(d))
		r.Put("/config", putSentinelConfig(d))
		r.Post("/restart", restartSentinel(d))
		r.Get("/status", sentinelStatus(d))
		r.Get("/events", listSentinelEvents(d))
		r.Get("/models", listModels(d))
	})
}

func sentinelConfigPath(d *Deps) string {
	return filepath.Join(d.Cfg.SharedDir, sentinelConfigFile)
}

func readSentinelConfig(d *Deps) map[string]interface{} {
	path := sentinelConfigPath(d)
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{
			"enabled":            false,
			"provider":           "openrouter",
			"model":              "",
			"api_base":           "https://openrouter.ai/api/v1",
			"api_key":            "",
			"api_key_masked":     "미설정",
			"max_tokens":         4096,
			"temperature":        0.3,
			"heartbeat_interval": 5,
			"alert_thresholds":   map[string]interface{}{},
		}
	}
	var cfg map[string]interface{}
	json.Unmarshal(data, &cfg)
	// Mask API key
	if key, ok := cfg["api_key"].(string); ok && len(key) > 8 {
		cfg["api_key_masked"] = key[:4] + "..." + key[len(key)-4:]
	} else if key, ok := cfg["api_key"].(string); ok && key != "" {
		cfg["api_key_masked"] = "****"
	} else {
		cfg["api_key_masked"] = "미설정"
	}
	return cfg
}

func getSentinelConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg := readSentinelConfig(d)
		// Don't expose raw key in GET
		delete(cfg, "api_key")
		httputil.RespondOK(w, map[string]interface{}{"success": true, "config": cfg})
	}
}

func putSentinelConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read existing
		existing := readSentinelConfig(d)

		// Merge incoming
		var incoming map[string]interface{}
		json.NewDecoder(r.Body).Decode(&incoming)

		for k, v := range incoming {
			if k == "api_key_masked" {
				continue
			}
			// Merge alert_thresholds deeply
			if k == "alert_thresholds" {
				if newT, ok := v.(map[string]interface{}); ok {
					existT, _ := existing["alert_thresholds"].(map[string]interface{})
					if existT == nil {
						existT = map[string]interface{}{}
					}
					for tk, tv := range newT {
						existT[tk] = tv
					}
					existing["alert_thresholds"] = existT
					continue
				}
			}
			existing[k] = v
		}

		// Write
		os.MkdirAll(d.Cfg.SharedDir, 0755)
		data, _ := json.MarshalIndent(existing, "", "  ")
		os.WriteFile(sentinelConfigPath(d), data, 0644)

		httputil.RespondOK(w, map[string]interface{}{"success": true, "message": "설정 저장 완료"})
	}
}

func restartSentinel(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		monitor.RestartSentinelAgent(d.Cfg.SharedDir, d.Store)
		state := monitor.GetAgentState()
		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "Sentinel 재시작 완료",
			"state":   stateToMap(state),
		})
	}
}

func sentinelStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		state := monitor.GetAgentState()
		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"state":   stateToMap(state),
		})
	}
}

func listSentinelEvents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		limit := 20
		if limitStr != "" {
			if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
				limit = n
			}
		}

		if d.Store == nil {
			httputil.RespondOK(w, map[string]interface{}{
				"success": true,
				"events":  []interface{}{},
			})
			return
		}

		events, err := d.Store.ListSentinelEvents(limit)
		if err != nil {
			httputil.RespondError(w, 500, "DB_ERROR", err.Error())
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"events":  events,
		})
	}
}

// ── Model List ──────────────────────────────────────────────────────────────

type modelItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Free bool   `json:"free,omitempty"`
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func listModels(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		cfg := readSentinelConfig(d)
		apiKey, _ := cfg["api_key"].(string)
		apiBase, _ := cfg["api_base"].(string)

		var models []modelItem
		var errMsg string

		switch provider {
		case "openrouter":
			base := apiBase
			if base == "" {
				base = "https://openrouter.ai/api/v1"
			}
			models, errMsg = fetchOpenRouterModels(base)
		case "openai":
			base := apiBase
			if base == "" {
				base = "https://api.openai.com/v1"
			}
			models, errMsg = fetchOpenAIModels(base, apiKey)
		case "anthropic":
			models = anthropicModels()
		case "ollama":
			base := apiBase
			if base == "" {
				base = "http://localhost:11434"
			}
			models, errMsg = fetchOllamaModels(base)
		default:
			// custom or unknown → empty list
		}

		resp := map[string]interface{}{
			"success": true,
			"models":  models,
		}
		if errMsg != "" {
			resp["error"] = errMsg
		}
		httputil.RespondOK(w, resp)
	}
}

func fetchOpenRouterModels(base string) ([]modelItem, string) {
	req, err := http.NewRequest("GET", strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return nil, err.Error()
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "JSON 파싱 오류: " + err.Error()
	}

	var free, paid []modelItem
	for _, m := range result.Data {
		isFree := m.Pricing.Prompt == "0" || m.Pricing.Prompt == "0.0"
		item := modelItem{ID: m.ID, Name: m.Name, Free: isFree}
		if isFree {
			free = append(free, item)
		} else {
			paid = append(paid, item)
		}
	}

	sort.Slice(free, func(i, j int) bool { return free[i].Name < free[j].Name })
	sort.Slice(paid, func(i, j int) bool { return paid[i].Name < paid[j].Name })

	combined := append(free, paid...)
	if len(combined) > 200 {
		combined = combined[:200]
	}
	return combined, ""
}

func fetchOpenAIModels(base, apiKey string) ([]modelItem, string) {
	req, err := http.NewRequest("GET", strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return nil, err.Error()
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "JSON 파싱 오류: " + err.Error()
	}

	var models []modelItem
	for _, m := range result.Data {
		if !strings.HasPrefix(m.ID, "gpt-") {
			continue
		}
		name := formatOpenAIModelName(m.ID)
		models = append(models, modelItem{ID: m.ID, Name: name})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID > models[j].ID })
	return models, ""
}

func formatOpenAIModelName(id string) string {
	replacer := strings.NewReplacer(
		"gpt-4o-mini", "GPT-4o Mini",
		"gpt-4o", "GPT-4o",
		"gpt-4-turbo", "GPT-4 Turbo",
		"gpt-4", "GPT-4",
		"gpt-3.5-turbo", "GPT-3.5 Turbo",
	)
	if name := replacer.Replace(id); name != id {
		return name
	}
	return id
}

func anthropicModels() []modelItem {
	return []modelItem{
		{ID: "claude-opus-4-20250514", Name: "Claude Opus 4"},
		{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4"},
		{ID: "claude-haiku-3-20240307", Name: "Claude Haiku 3"},
	}
}

func fetchOllamaModels(base string) ([]modelItem, string) {
	url := fmt.Sprintf("%s/api/tags", strings.TrimRight(base, "/"))
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "JSON 파싱 오류: " + err.Error()
	}

	var models []modelItem
	for _, m := range result.Models {
		models = append(models, modelItem{ID: m.Name, Name: m.Name})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, ""
}

// stateToMap converts AgentSnapshot to a JSON-serialisable map.
func stateToMap(s monitor.AgentSnapshot) map[string]interface{} {
	m := map[string]interface{}{
		"running":      s.Running,
		"health":       s.Health,
		"last_result":  s.LastResult,
		"last_summary": s.LastSummary,
	}
	if !s.LastRun.IsZero() {
		m["last_run"] = s.LastRun.Format("2006-01-02T15:04:05Z07:00")
	}
	if s.ErrorMsg != "" {
		m["error_msg"] = s.ErrorMsg
	}
	return m
}
