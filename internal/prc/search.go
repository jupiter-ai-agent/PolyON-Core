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

// SearchProvider provisions OpenSearch indices for modules.
type SearchProvider struct {
	Endpoint string // e.g., "http://polyon-search:9200"
}

func (p *SearchProvider) Type() string        { return "search" }
func (p *SearchProvider) DependsOn() []string { return nil }

func (p *SearchProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	index := claim.ConfigString("index", claim.ModuleID)
	shards := claim.ConfigInt("shards", 1)
	replicas := claim.ConfigInt("replicas", 0)

	// Create index with settings
	settings := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   shards,
			"number_of_replicas": replicas,
		},
	}

	body, _ := json.Marshal(settings)
	url := fmt.Sprintf("%s/%s", p.Endpoint, index)

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create index %s: %w", index, err)
	}
	defer resp.Body.Close()

	// 200 or 400 (already exists) are both OK
	if resp.StatusCode != 200 && resp.StatusCode != 400 {
		return nil, fmt.Errorf("create index %s: HTTP %d", index, resp.StatusCode)
	}

	log.Info().Str("index", index).Msg("PRC: OpenSearch index created")

	return Credentials{
		"url":   p.Endpoint,
		"index": index,
	}, nil
}

func (p *SearchProvider) Deprovision(ctx context.Context, claim Claim) error {
	index := claim.ConfigString("index", claim.ModuleID)
	url := fmt.Sprintf("%s/%s", p.Endpoint, index)

	req, _ := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Str("index", index).Msg("PRC: index deletion failed")
		return nil
	}
	resp.Body.Close()
	return nil
}

func (p *SearchProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	index := claim.ConfigString("index", claim.ModuleID)
	url := fmt.Sprintf("%s/%s", p.Endpoint, index)

	req, _ := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return StatusError, err
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		return StatusProvisioned, nil
	}
	return StatusNotFound, nil
}
