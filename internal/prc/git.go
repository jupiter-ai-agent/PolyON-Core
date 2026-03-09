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

// GitProvider provisions Gitea organizations, repos, and API tokens for modules.
type GitProvider struct {
	Endpoint string // e.g., "http://polyon-gitea:3000"
	AdminUser string
	AdminPassword string
}

func (p *GitProvider) Type() string        { return "git" }
func (p *GitProvider) DependsOn() []string { return nil }

func (p *GitProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	org := claim.ConfigString("org", "polyon-modules")
	repo := claim.ConfigString("repo", claim.ModuleID+"-data")

	client := &http.Client{Timeout: 15 * time.Second}

	// 1. Ensure org exists
	if err := p.ensureOrg(ctx, client, org); err != nil {
		return nil, fmt.Errorf("ensure org %s: %w", org, err)
	}

	// 2. Create repo
	if err := p.ensureRepo(ctx, client, org, repo); err != nil {
		return nil, fmt.Errorf("ensure repo %s/%s: %w", org, repo, err)
	}

	// 3. Create API token for module
	token, err := p.createToken(ctx, client, claim.ModuleID)
	if err != nil {
		log.Warn().Err(err).Msg("PRC: token creation failed, using basic auth creds")
		token = "" // fallback
	}

	repoURL := fmt.Sprintf("%s/%s/%s.git", p.Endpoint, org, repo)

	return Credentials{
		"url":   repoURL,
		"token": token,
		"org":   org,
		"repo":  repo,
	}, nil
}

func (p *GitProvider) Deprovision(ctx context.Context, claim Claim) error {
	org := claim.ConfigString("org", "polyon-modules")
	repo := claim.ConfigString("repo", claim.ModuleID+"-data")

	client := &http.Client{Timeout: 10 * time.Second}

	// Delete repo
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s", p.Endpoint, org, repo)
	req, _ := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
	resp, err := client.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("PRC: repo deletion failed")
	} else {
		resp.Body.Close()
	}

	return nil
}

func (p *GitProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	org := claim.ConfigString("org", "polyon-modules")
	repo := claim.ConfigString("repo", claim.ModuleID+"-data")

	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s", p.Endpoint, org, repo)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
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

func (p *GitProvider) ensureOrg(ctx context.Context, client *http.Client, org string) error {
	// Check if exists
	url := fmt.Sprintf("%s/api/v1/orgs/%s", p.Endpoint, org)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}

	// Create
	body, _ := json.Marshal(map[string]string{
		"username":   org,
		"visibility": "private",
	})
	req, _ = http.NewRequestWithContext(ctx, "POST", p.Endpoint+"/api/v1/orgs", bytes.NewReader(body))
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 422 { // 422 = already exists
		return fmt.Errorf("create org: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *GitProvider) ensureRepo(ctx context.Context, client *http.Client, org, repo string) error {
	// Check if exists
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s", p.Endpoint, org, repo)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}

	// Create
	body, _ := json.Marshal(map[string]any{
		"name":       repo,
		"private":    true,
		"auto_init":  true,
	})
	url = fmt.Sprintf("%s/api/v1/orgs/%s/repos", p.Endpoint, org)
	req, _ = http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 409 {
		return fmt.Errorf("create repo: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *GitProvider) createToken(ctx context.Context, client *http.Client, moduleID string) (string, error) {
	tokenName := fmt.Sprintf("mod-%s-token", moduleID)
	body, _ := json.Marshal(map[string]any{
		"name":   tokenName,
		"scopes": []string{"repo"},
	})
	url := fmt.Sprintf("%s/api/v1/users/%s/tokens", p.Endpoint, p.AdminUser)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.SetBasicAuth(p.AdminUser, p.AdminPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("create token: HTTP %d", resp.StatusCode)
	}

	var result struct {
		SHA1 string `json:"sha1"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.SHA1, nil
}
