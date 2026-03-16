package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/httputil"
)

var aiClient = &http.Client{Timeout: 15 * time.Second}

func litellmURL() string {
	if cfg := config.Get(); cfg != nil && cfg.LiteLLMURL != "" {
		return cfg.LiteLLMURL
	}
	if u := os.Getenv("LITELLM_URL"); u != "" {
		return u
	}
	return "http://polyon-ai:4000"
}

func litellmKey() string {
	return os.Getenv("LITELLM_MASTER_KEY")
}

// litellmProxy forwards a request to LiteLLM and writes the response back.
func litellmProxy(method, path string, body io.Reader, w http.ResponseWriter) {
	url := litellmURL() + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		httputil.RespondError(w, 500, "internal_error", "build request: "+err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+litellmKey())
	req.Header.Set("Content-Type", "application/json")

	resp, err := aiClient.Do(req)
	if err != nil {
		httputil.RespondError(w, 502, "gateway_error", "litellm unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// RegisterAI registers AI Gateway management routes.
func RegisterAI(r chi.Router, d *Deps) {
	r.Route("/ai", func(r chi.Router) {
		// Models
		r.Get("/models", aiListModels)
		r.Get("/models/info", aiModelInfo)
		r.Post("/models", aiAddModel)
		r.Put("/models/{modelID}/routing", aiUpdateModelRouting)
		r.Delete("/models/{modelID}", aiDeleteModel)

		// Service model (alias group) management
		r.Post("/models/group/add", aiGroupAddModel)
		r.Post("/models/group/remove", aiGroupRemoveModel)
		r.Delete("/models/service/{serviceName}", aiDeleteServiceModel)

		// Keys
		r.Get("/keys", aiListKeys)
		r.Post("/keys", aiGenerateKey)
		r.Delete("/keys/{keyID}", aiDeleteKey)

		// Usage
		r.Get("/usage/global", aiGlobalSpend)
		r.Get("/usage/logs", aiSpendLogs)
		r.Get("/usage/by-model", aiSpendByModel)
		r.Get("/usage/by-key", aiSpendByKey)

		// Settings / overview
		r.Get("/settings", aiGetSettings)

		// Health (per-model status)
		r.Get("/health", aiModelHealth)

		// Spend by model/key
		r.Get("/spend/models", aiSpendByModel)
		r.Get("/spend/keys", aiSpendByKey)

		// Agent status (OpenClaw gateway proxy)
		r.Get("/agents", aiListAgents)
		r.Get("/agents/{agentID}/status", aiAgentStatus)

		// Memory stats (Mem0 proxy)
		r.Get("/memory/stats", aiMemoryStats)
	})
}

// ── Models ──

func aiListModels(w http.ResponseWriter, r *http.Request) {
	litellmProxy("GET", "/v1/models", nil, w)
}

func aiAddModel(w http.ResponseWriter, r *http.Request) {
	litellmProxy("POST", "/model/new", r.Body, w)
}

// aiModelInfo returns detailed model info including alias → actual model mapping.
func aiModelInfo(w http.ResponseWriter, r *http.Request) {
	litellmProxy("GET", "/model/info", nil, w)
}

// aiUpdateModelRouting changes the actual model behind an alias.
func aiUpdateModelRouting(w http.ResponseWriter, r *http.Request) {
	modelID := chi.URLParam(r, "modelID")

	var body struct {
		ActualModel string `json:"actual_model"`
		APIKey      string `json:"api_key,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.RespondError(w, 400, "bad_request", "invalid JSON: "+err.Error())
		return
	}
	if body.ActualModel == "" {
		httputil.RespondError(w, 400, "bad_request", "actual_model is required")
		return
	}

	params := map[string]interface{}{
		"model": body.ActualModel,
	}
	if body.APIKey != "" {
		params["api_key"] = body.APIKey
	}

	payload := map[string]interface{}{
		"model_id":       modelID,
		"litellm_params": params,
	}
	payloadBytes, _ := json.Marshal(payload)
	litellmProxy("POST", "/model/update", strings.NewReader(string(payloadBytes)), w)
}

func aiDeleteModel(w http.ResponseWriter, r *http.Request) {
	modelID := chi.URLParam(r, "modelID")
	body := fmt.Sprintf(`{"id":"%s"}`, modelID)
	litellmProxy("POST", "/model/delete", strings.NewReader(body), w)
}

// aiGroupAddModel adds a real model to a service model (alias group).
// POST /ai/models/group/add
// Body: { "service_name": "polyon-default", "actual_model": "openai/gpt-4o-mini", "api_key": "..." }
func aiGroupAddModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServiceName string `json:"service_name"`
		ActualModel string `json:"actual_model"`
		APIKey      string `json:"api_key,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.RespondError(w, 400, "bad_request", "invalid JSON: "+err.Error())
		return
	}
	if body.ServiceName == "" || body.ActualModel == "" {
		httputil.RespondError(w, 400, "bad_request", "service_name and actual_model are required")
		return
	}

	params := map[string]interface{}{
		"model": body.ActualModel,
	}
	if body.APIKey != "" {
		params["api_key"] = body.APIKey
	}

	payload := map[string]interface{}{
		"model_name":     body.ServiceName,
		"litellm_params": params,
	}
	payloadBytes, _ := json.Marshal(payload)
	litellmProxy("POST", "/model/new", strings.NewReader(string(payloadBytes)), w)
}

// aiGroupRemoveModel removes a real model from a service model group by model_id.
// POST /ai/models/group/remove
// Body: { "model_id": "cf627eca-..." }
func aiGroupRemoveModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ModelID string `json:"model_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.RespondError(w, 400, "bad_request", "invalid JSON: "+err.Error())
		return
	}
	if body.ModelID == "" {
		httputil.RespondError(w, 400, "bad_request", "model_id is required")
		return
	}

	deleteBody := fmt.Sprintf(`{"id":"%s"}`, body.ModelID)
	litellmProxy("POST", "/model/delete", strings.NewReader(deleteBody), w)
}

// aiDeleteServiceModel deletes all models under a service model (alias group).
// DELETE /ai/models/service/{serviceName}
func aiDeleteServiceModel(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "serviceName")
	if serviceName == "" {
		httputil.RespondError(w, 400, "bad_request", "serviceName is required")
		return
	}

	// Fetch all model info to find IDs belonging to this service
	resp, err := proxyGet("/model/info")
	if err != nil {
		httputil.RespondError(w, 502, "gateway_error", "litellm unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var modelInfo struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				ID string `json:"id"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &modelInfo); err != nil {
		httputil.RespondError(w, 500, "parse_error", "failed to parse model info: "+err.Error())
		return
	}

	// Collect all model IDs with matching service name
	var ids []string
	for _, m := range modelInfo.Data {
		if m.ModelName == serviceName && m.ModelInfo.ID != "" {
			ids = append(ids, m.ModelInfo.ID)
		}
	}

	if len(ids) == 0 {
		httputil.RespondError(w, 404, "not_found", "no models found for service: "+serviceName)
		return
	}

	// Delete each model
	var deleteErrors []string
	for _, id := range ids {
		deleteBody := fmt.Sprintf(`{"id":"%s"}`, id)
		req, err := http.NewRequest("POST", litellmURL()+"/model/delete", strings.NewReader(deleteBody))
		if err != nil {
			deleteErrors = append(deleteErrors, "build request for "+id+": "+err.Error())
			continue
		}
		req.Header.Set("Authorization", "Bearer "+litellmKey())
		req.Header.Set("Content-Type", "application/json")

		delResp, err := aiClient.Do(req)
		if err != nil {
			deleteErrors = append(deleteErrors, "delete "+id+": "+err.Error())
			continue
		}
		delResp.Body.Close()
		if delResp.StatusCode >= 400 {
			deleteErrors = append(deleteErrors, fmt.Sprintf("delete %s: HTTP %d", id, delResp.StatusCode))
		}
	}

	if len(deleteErrors) > 0 {
		httputil.RespondError(w, 500, "partial_delete", strings.Join(deleteErrors, "; "))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	fmt.Fprintf(w, `{"deleted":%d,"service_name":"%s"}`, len(ids), serviceName)
}

// ── Keys ──

func aiListKeys(w http.ResponseWriter, r *http.Request) {
	url := litellmURL() + "/key/list"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		httputil.RespondError(w, 500, "internal_error", err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+litellmKey())

	resp, err := aiClient.Do(req)
	if err != nil {
		httputil.RespondError(w, 502, "gateway_error", "litellm unreachable")
		return
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		httputil.RespondOK(w, map[string]interface{}{"keys": []interface{}{}})
		return
	}
	httputil.RespondOK(w, map[string]interface{}{"keys": result})
}

func aiGenerateKey(w http.ResponseWriter, r *http.Request) {
	litellmProxy("POST", "/key/generate", r.Body, w)
}

func aiDeleteKey(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	body := fmt.Sprintf(`{"keys":["%s"]}`, keyID)
	litellmProxy("POST", "/key/delete", strings.NewReader(body), w)
}

// ── Usage ──

func aiGlobalSpend(w http.ResponseWriter, r *http.Request) {
	litellmProxy("GET", "/global/spend", nil, w)
}

func aiSpendLogs(w http.ResponseWriter, r *http.Request) {
	path := "/global/spend/logs"
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}
	litellmProxy("GET", path, nil, w)
}

func aiSpendByModel(w http.ResponseWriter, r *http.Request) {
	path := "/global/spend/models"
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}
	litellmProxy("GET", path, nil, w)
}

func aiSpendByKey(w http.ResponseWriter, r *http.Request) {
	path := "/global/spend/keys"
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}
	litellmProxy("GET", path, nil, w)
}

// ── Settings (aggregated overview) ──

func aiGetSettings(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{"status": "unknown"}
	var modelCount, keyCount int

	// Health
	if hResp, err := proxyGet("/health/liveliness"); err == nil {
		health["status"] = "healthy"
		if v := hResp.Header.Get("x-litellm-version"); v != "" {
			health["version"] = v
		}
		hResp.Body.Close()
	} else {
		health["status"] = "down"
		health["error"] = err.Error()
	}

	// Model count
	if mResp, err := proxyGet("/v1/models"); err == nil {
		raw, _ := io.ReadAll(mResp.Body)
		mResp.Body.Close()
		var md struct {
			Data []interface{} `json:"data"`
		}
		if json.Unmarshal(raw, &md) == nil {
			modelCount = len(md.Data)
		}
	}

	// Key count
	if kResp, err := proxyGet("/key/list"); err == nil {
		raw, _ := io.ReadAll(kResp.Body)
		kResp.Body.Close()
		var keys []interface{}
		if json.Unmarshal(raw, &keys) == nil {
			keyCount = len(keys)
		}
	}

	httputil.RespondOK(w, map[string]interface{}{
		"health":     health,
		"modelCount": modelCount,
		"keyCount":   keyCount,
	})
	log.Debug().Int("models", modelCount).Int("keys", keyCount).Msg("AI settings fetched")
}

func proxyGet(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", litellmURL()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+litellmKey())
	return aiClient.Do(req)
}

// ── Agents ──

func aiListAgents(w http.ResponseWriter, r *http.Request) {
	agentURL := os.Getenv("OPENCLAW_URL")
	if agentURL == "" {
		agentURL = "http://polyon-agent:18789"
	}

	agent := map[string]interface{}{
		"id":        "polyon",
		"name":      "@polyon",
		"model":     "openai/polyon-default",
		"status":    "unknown",
		"memory_mb": 768,
	}

	// Health check
	resp, err := aiClient.Get(agentURL + "/health")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			agent["status"] = "healthy"
		} else {
			agent["status"] = "degraded"
		}
	} else {
		agent["status"] = "down"
	}

	httputil.RespondOK(w, map[string]interface{}{
		"agents": []interface{}{agent},
		"count":  1,
	})
}

func aiAgentStatus(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agentID")
	if agentID != "polyon" {
		httputil.RespondError(w, 404, "not_found", "agent not found")
		return
	}

	agentURL := os.Getenv("OPENCLAW_URL")
	if agentURL == "" {
		agentURL = "http://polyon-agent:18789"
	}

	status := "unknown"
	resp, err := aiClient.Get(agentURL + "/health")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			status = "healthy"
		} else {
			status = "degraded"
		}
	} else {
		status = "down"
	}

	httputil.RespondOK(w, map[string]interface{}{
		"id":        "polyon",
		"name":      "@polyon",
		"model":     "openai/polyon-default",
		"status":    status,
		"memory_mb": 768,
	})
}

// ── Memory ──

func aiMemoryStats(w http.ResponseWriter, r *http.Request) {
	memURL := os.Getenv("MEMORY_URL")
	if memURL == "" {
		memURL = "http://polyon-memory.polyon.svc:8080"
	}

	stats := map[string]interface{}{
		"status":   "unknown",
		"count":    0,
		"backend":  memURL,
		"endpoint": memURL + "/memory/search",
	}

	resp, err := aiClient.Get(memURL + "/health")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			stats["status"] = "healthy"
		} else {
			stats["status"] = "degraded"
		}
	} else {
		stats["status"] = "down"
	}

	httputil.RespondOK(w, stats)
}

// aiModelHealth returns per-model health status from LiteLLM /health endpoint.
func aiModelHealth(w http.ResponseWriter, r *http.Request) {
	resp, err := proxyGet("/health")
	if err != nil {
		httputil.RespondError(w, 502, "gateway_error", "litellm unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var data map[string]interface{}
	if json.Unmarshal(raw, &data) != nil {
		httputil.RespondError(w, 502, "parse_error", "failed to parse health response")
		return
	}
	httputil.RespondOK(w, data)
}
