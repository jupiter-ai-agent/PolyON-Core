// Package monitor provides the sentinel agent goroutine.
package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/store"
)

// ─── Config ─────────────────────────────────────────────────────────────────

type sentinelConfig struct {
	Enabled           bool                   `json:"enabled"`
	Provider          string                 `json:"provider"`
	Model             string                 `json:"model"`
	APIBase           string                 `json:"api_base"`
	APIKey            string                 `json:"api_key"`
	MaxTokens         int                    `json:"max_tokens"`
	Temperature       float64                `json:"temperature"`
	HeartbeatInterval int                    `json:"heartbeat_interval"`
	AlertThresholds   map[string]interface{} `json:"alert_thresholds"`
}

func loadSentinelConfig(sharedDir string) (*sentinelConfig, error) {
	path := filepath.Join(sharedDir, "sentinel-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg sentinelConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.3
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5
	}
	return &cfg, nil
}

// ─── AgentState ──────────────────────────────────────────────────────────────

// AgentSnapshot is a lock-free copy of the agent state for external callers.
type AgentSnapshot struct {
	Running     bool      `json:"running"`
	LastRun     time.Time `json:"last_run"`
	LastResult  string    `json:"last_result"`  // "ok", "warning", "critical", "error"
	LastSummary string    `json:"last_summary"`
	Health      string    `json:"health"` // "healthy", "error", "stopped"
	ErrorMsg    string    `json:"error_msg,omitempty"`
}

// agentState holds the in-memory state (unexported to force use of GetAgentState).
type agentState struct {
	mu          sync.RWMutex
	running     bool
	lastRun     time.Time
	lastResult  string
	lastSummary string
	health      string
	errorMsg    string
}

func (s *agentState) snapshot() AgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return AgentSnapshot{
		Running:     s.running,
		LastRun:     s.lastRun,
		LastResult:  s.lastResult,
		LastSummary: s.lastSummary,
		Health:      s.health,
		ErrorMsg:    s.errorMsg,
	}
}

func (s *agentState) setRunning(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = v
	if !v {
		s.health = "stopped"
	}
}

func (s *agentState) setResult(result, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun = time.Now().UTC()
	s.lastResult = result
	s.lastSummary = summary
	s.health = "healthy"
	s.errorMsg = ""
}

func (s *agentState) setError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun = time.Now().UTC()
	s.lastResult = "error"
	s.health = "error"
	s.errorMsg = msg
}

// ─── Global singleton ────────────────────────────────────────────────────────

var (
	globalAgentState = &agentState{health: "stopped"}
	agentStopCh      chan struct{}
	agentMu          sync.Mutex
)

// GetAgentState returns a snapshot of the current agent state.
func GetAgentState() AgentSnapshot {
	return globalAgentState.snapshot()
}

// StartSentinelAgent starts the sentinel agent goroutine.
// sharedDir is the path containing sentinel-config.json.
func StartSentinelAgent(sharedDir string, st *store.Store) {
	agentMu.Lock()
	defer agentMu.Unlock()

	// Stop any existing agent first
	if agentStopCh != nil {
		close(agentStopCh)
		agentStopCh = nil
		globalAgentState.setRunning(false)
		time.Sleep(100 * time.Millisecond)
	}

	cfg, err := loadSentinelConfig(sharedDir)
	if err != nil {
		log.Info().Str("err", err.Error()).Msg("[Sentinel] Config not found, agent not started")
		globalAgentState.setRunning(false)
		return
	}

	if !cfg.Enabled {
		log.Info().Msg("[Sentinel] Disabled in config")
		globalAgentState.setRunning(false)
		return
	}
	if cfg.APIKey == "" {
		log.Info().Msg("[Sentinel] No API key configured, agent not started")
		globalAgentState.setRunning(false)
		return
	}
	if cfg.Model == "" {
		log.Info().Msg("[Sentinel] No model configured, agent not started")
		globalAgentState.setRunning(false)
		return
	}

	stopChan := make(chan struct{})
	agentStopCh = stopChan
	globalAgentState.mu.Lock()
	globalAgentState.running = true
	globalAgentState.health = "healthy"
	globalAgentState.mu.Unlock()

	interval := time.Duration(cfg.HeartbeatInterval) * time.Minute
	log.Info().
		Str("model", cfg.Model).
		Dur("interval", interval).
		Msg("[Sentinel] Agent started")

	go runSentinelLoop(sharedDir, st, stopChan, interval)
}

// StopSentinelAgent stops the running sentinel agent.
func StopSentinelAgent() {
	agentMu.Lock()
	defer agentMu.Unlock()
	if agentStopCh != nil {
		close(agentStopCh)
		agentStopCh = nil
		globalAgentState.setRunning(false)
		log.Info().Msg("[Sentinel] Agent stopped")
	}
}

// RestartSentinelAgent stops and restarts the agent (re-reads config).
func RestartSentinelAgent(sharedDir string, st *store.Store) {
	StopSentinelAgent()
	time.Sleep(200 * time.Millisecond)
	StartSentinelAgent(sharedDir, st)
}

// ─── Main loop ────────────────────────────────────────────────────────────────

func runSentinelLoop(sharedDir string, st *store.Store, stop <-chan struct{}, interval time.Duration) {
	// Run immediately on start, then on interval
	runOnce(sharedDir, st)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Reload config every tick (interval may have changed)
			cfg, err := loadSentinelConfig(sharedDir)
			if err != nil {
				log.Warn().Err(err).Msg("[Sentinel] Failed to reload config")
				runOnce(sharedDir, st)
				continue
			}
			newInterval := time.Duration(cfg.HeartbeatInterval) * time.Minute
			if newInterval != interval {
				log.Info().Dur("new_interval", newInterval).Msg("[Sentinel] Interval changed, restarting ticker")
				ticker.Reset(newInterval)
				interval = newInterval
			}
			runOnce(sharedDir, st)
		case <-stop:
			return
		}
	}
}

// ─── Single run ──────────────────────────────────────────────────────────────

func runOnce(sharedDir string, st *store.Store) {
	log.Debug().Msg("[Sentinel] Running analysis cycle")

	cfg, err := loadSentinelConfig(sharedDir)
	if err != nil {
		globalAgentState.setError("config read failed: " + err.Error())
		return
	}

	// 1. Collect system status
	status, err := collectSystemStatus(st)
	if err != nil {
		log.Warn().Err(err).Msg("[Sentinel] Failed to collect status")
		globalAgentState.setError("status collection failed: " + err.Error())
		return
	}

	// 2. Call LLM
	result, err := callLLM(cfg, status)
	if err != nil {
		log.Warn().Err(err).Msg("[Sentinel] LLM call failed")
		globalAgentState.setError("LLM call failed: " + err.Error())
		return
	}

	log.Info().
		Str("status", result.Status).
		Str("summary", result.Summary).
		Int("alerts", len(result.Alerts)).
		Msg("[Sentinel] Analysis complete")

	// 3. Update agent state
	globalAgentState.setResult(result.Status, result.Summary)

	// 4. Save event to DB
	alertsGenerated := 0
	if st != nil {
		detailsJSON := map[string]interface{}{
			"alerts": result.Alerts,
		}
		if err := st.CreateSentinelEvent(result.Status, result.Summary, detailsJSON, len(result.Alerts)); err != nil {
			log.Warn().Err(err).Msg("[Sentinel] Failed to save event")
		}

		// 5. Create alerts for anomalies
		for _, a := range result.Alerts {
			level := strings.ToUpper(a.Level)
			if level != "WARN" && level != "WARNING" && level != "CRITICAL" && level != "INFO" {
				level = "WARN"
			}
			if level == "WARNING" {
				level = "WARN"
			}
			if _, err := st.CreateAlert(level, a.Service, a.Message, "sentinel",
				map[string]interface{}{"sentinel_analysis": true, "llm_model": cfg.Model}, nil); err != nil {
				log.Warn().Err(err).Str("service", a.Service).Msg("[Sentinel] Failed to create alert")
			} else {
				alertsGenerated++
			}
		}
	}

	log.Debug().Int("alerts_generated", alertsGenerated).Msg("[Sentinel] Cycle complete")
}

// ─── System status collection ─────────────────────────────────────────────────

type systemStatus struct {
	Timestamp  string                   `json:"timestamp"`
	System     map[string]interface{}   `json:"system"`
	Containers []map[string]interface{} `json:"containers"`
	RecentAlerts []map[string]interface{} `json:"recent_alerts"`
}

func collectSystemStatus(st *store.Store) (*systemStatus, error) {
	status := &systemStatus{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Fetch system metrics from local API
	sysMetrics, err := fetchSystemMetrics()
	if err != nil {
		log.Warn().Err(err).Msg("[Sentinel] Could not fetch system metrics")
		status.System = map[string]interface{}{"error": err.Error()}
	} else {
		status.System = sysMetrics
	}

	// Fetch container list from Docker API (via local HTTP)
	containers, err := fetchContainerStatus()
	if err != nil {
		log.Warn().Err(err).Msg("[Sentinel] Could not fetch container status")
		status.Containers = []map[string]interface{}{}
	} else {
		status.Containers = containers
	}

	// Recent alerts from DB
	if st != nil {
		alerts, _, err := st.ListAlerts("", "", 10, false)
		if err == nil {
			for _, a := range alerts {
				status.RecentAlerts = append(status.RecentAlerts, map[string]interface{}{
					"timestamp": a.Timestamp.Format(time.RFC3339),
					"level":     a.Level,
					"service":   a.Service,
					"message":   a.Message,
				})
			}
		}
	}
	if status.RecentAlerts == nil {
		status.RecentAlerts = []map[string]interface{}{}
	}

	return status, nil
}

func fetchSystemMetrics() (map[string]interface{}, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://localhost:8000/api/v1/system/resources")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	// Extract "data" field if present
	if data, ok := result["data"].(map[string]interface{}); ok {
		return data, nil
	}
	return result, nil
}

func fetchContainerStatus() ([]map[string]interface{}, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://localhost:8000/api/v1/containers")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Containers []map[string]interface{} `json:"containers"`
		Data       []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		var raw []map[string]interface{}
		if err2 := json.Unmarshal(body, &raw); err2 != nil {
			return nil, err
		}
		return raw, nil
	}
	if result.Containers != nil {
		return result.Containers, nil
	}
	if result.Data != nil {
		return result.Data, nil
	}
	return []map[string]interface{}{}, nil
}

// ─── LLM call ────────────────────────────────────────────────────────────────

const sentinelSystemPrompt = `You are Sentinel, an infrastructure monitoring AI agent for PolyON.
Analyze the system status and report any anomalies.
Respond ONLY in JSON format with no additional text or markdown:
{
  "status": "ok" | "warning" | "critical",
  "summary": "한국어로 간단한 요약",
  "alerts": [
    {"level": "WARN|CRITICAL", "service": "서비스명", "message": "한국어 메시지"}
  ]
}
If everything is normal, return status "ok" with empty alerts array.`

type llmAlert struct {
	Level   string `json:"level"`
	Service string `json:"service"`
	Message string `json:"message"`
}

type llmResult struct {
	Status  string     `json:"status"`
	Summary string     `json:"summary"`
	Alerts  []llmAlert `json:"alerts"`
}

type openRouterRequest struct {
	Model       string                   `json:"model"`
	Messages    []map[string]string      `json:"messages"`
	MaxTokens   int                      `json:"max_tokens"`
	Temperature float64                  `json:"temperature"`
}

type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

func callLLM(cfg *sentinelConfig, status *systemStatus) (*llmResult, error) {
	statusJSON, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal status: %w", err)
	}

	reqBody := openRouterRequest{
		Model: cfg.Model,
		Messages: []map[string]string{
			{"role": "system", "content": sentinelSystemPrompt},
			{"role": "user", "content": string(statusJSON)},
		},
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	apiURL := cfg.APIBase
	if !strings.HasSuffix(apiURL, "/") {
		apiURL += "/"
	}
	apiURL += "chat/completions"

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://polyon.oplabs.co.kr")
	req.Header.Set("X-Title", "PolyON Sentinel")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(body, &orResp); err != nil {
		return nil, fmt.Errorf("parse response: %w, body: %s", err, string(body))
	}

	if orResp.Error != nil {
		return nil, fmt.Errorf("API error %d: %s", orResp.Error.Code, orResp.Error.Message)
	}

	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response, body: %s", string(body))
	}

	content := strings.TrimSpace(orResp.Choices[0].Message.Content)

	// Strip markdown code fences if present
	content = stripMarkdownJSON(content)

	var result llmResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		log.Error().Str("content", content).Err(err).Msg("[Sentinel] Failed to parse LLM JSON response")
		// Return a safe fallback instead of panicking
		return &llmResult{
			Status:  "error",
			Summary: "LLM 응답 파싱 실패: " + err.Error(),
			Alerts:  []llmAlert{},
		}, nil
	}

	if result.Status == "" {
		result.Status = "ok"
	}
	if result.Alerts == nil {
		result.Alerts = []llmAlert{}
	}

	return &result, nil
}

func stripMarkdownJSON(s string) string {
	// Remove ```json ... ``` or ``` ... ```
	if idx := strings.Index(s, "```json"); idx != -1 {
		s = s[idx+7:]
	} else if idx := strings.Index(s, "```"); idx != -1 {
		s = s[idx+3:]
	}
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	// Find first { and last } to extract JSON
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		s = s[start : end+1]
	}
	return strings.TrimSpace(s)
}
