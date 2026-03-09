package prc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// AIProvider provisions LiteLLM Virtual Keys for modules.
type AIProvider struct {
	Endpoint  string // e.g., "http://polyon-ai:4000"
	MasterKey string // LiteLLM master key
}

func (p *AIProvider) Type() string        { return "ai" }
func (p *AIProvider) DependsOn() []string { return nil }

func (p *AIProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// Build key generation request
	keyReq := map[string]any{
		"key_alias":  fmt.Sprintf("mod-%s", claim.ModuleID),
		"max_budget": 50.0, // $50/month default
		"metadata": map[string]string{
			"module_id": claim.ModuleID,
			"managed":   "prc",
		},
	}

	// Rate limit
	if rl := claim.ConfigString("rateLimit", ""); rl != "" {
		keyReq["tpm_limit"] = 100000  // tokens per minute
		keyReq["rpm_limit"] = 1000    // requests per minute
	}

	body, _ := json.Marshal(keyReq)
	url := p.Endpoint + "/key/generate"
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.MasterKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LiteLLM key generate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("LiteLLM key generate: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Key string `json:"key"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Key == "" {
		return nil, fmt.Errorf("LiteLLM returned empty key")
	}

	log.Info().Str("module", claim.ModuleID).Msg("PRC: LiteLLM Virtual Key provisioned")

	return Credentials{
		"endpoint": p.Endpoint + "/v1",
		"apiKey":   result.Key,
	}, nil
}

func (p *AIProvider) Deprovision(ctx context.Context, claim Claim) error {
	client := &http.Client{Timeout: 10 * time.Second}

	body, _ := json.Marshal(map[string]string{
		"key_alias": fmt.Sprintf("mod-%s", claim.ModuleID),
	})
	url := p.Endpoint + "/key/delete"
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.MasterKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("PRC: LiteLLM key deletion failed")
		return nil
	}
	resp.Body.Close()
	return nil
}

func (p *AIProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	// Phase 2: LiteLLM /key/info API
	return StatusProvisioned, nil
}
