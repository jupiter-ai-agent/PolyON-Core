package gitea

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to the internal Gitea instance.
type Client struct {
	BaseURL string // e.g. http://polyon-gitea:3000
	Token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Repo represents a Gitea repository (subset).
type Repo struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	CloneURL      string    `json:"clone_url"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	Empty         bool      `json:"empty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Commit represents a Gitea commit (subset).
type Commit struct {
	SHA     string       `json:"sha"`
	Message string       `json:"message,omitempty"`
	Created time.Time    `json:"created,omitempty"`
	Commit  *CommitMeta  `json:"commit,omitempty"`
	HTMLURL string       `json:"html_url,omitempty"`
}

type CommitMeta struct {
	Message string        `json:"message"`
	Author  *CommitAuthor `json:"author,omitempty"`
}

type CommitAuthor struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date"`
}

// CreateRepo creates a new repository under the admin user.
func (c *Client) CreateRepo(name, description string) (*Repo, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"name":          name,
		"description":   description,
		"private":       true,
		"auto_init":     true,
		"default_branch": "main",
	})
	req, _ := http.NewRequest("POST", c.BaseURL+"/api/v1/user/repos", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea create repo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea create repo: %d %s", resp.StatusCode, string(b))
	}
	var repo Repo
	json.NewDecoder(resp.Body).Decode(&repo)
	return &repo, nil
}

// DeleteRepo deletes a repository.
func (c *Client) DeleteRepo(owner, name string) error {
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/api/v1/repos/%s/%s", c.BaseURL, owner, name), nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea delete repo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		return fmt.Errorf("gitea delete repo: %d", resp.StatusCode)
	}
	return nil
}

// GetRepo returns repository info.
func (c *Client) GetRepo(owner, name string) (*Repo, error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/repos/%s/%s", c.BaseURL, owner, name), nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea get repo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitea get repo: %d", resp.StatusCode)
	}
	var repo Repo
	json.NewDecoder(resp.Body).Decode(&repo)
	return &repo, nil
}

// ListAllRepos returns all repos visible to the token (admin).
func (c *Client) ListAllRepos() ([]Repo, error) {
	url := fmt.Sprintf("%s/api/v1/repos/search?limit=50&token=%s", c.BaseURL, c.Token)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea list repos: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea list repos: %d %s", resp.StatusCode, string(b))
	}
	var result struct {
		Data []Repo `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Data, nil
}

// ListCommits returns recent commits for a repo.
func (c *Client) ListCommits(owner, name string, limit int) ([]Commit, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/commits?limit=%d", c.BaseURL, owner, name, limit)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitea commits: %d", resp.StatusCode)
	}
	var commits []Commit
	json.NewDecoder(resp.Body).Decode(&commits)
	return commits, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// LDAP Authentication Source provisioning
// ──────────────────────────────────────────────────────────────────────────────

// LDAPConfig holds parameters for configuring a Gitea LDAP authentication source.
type LDAPConfig struct {
	Host            string
	Port            int
	BindDN          string
	BindPassword    string
	UserSearchBase  string
	AdminDN         string // used to derive admin_filter
	DCAdminPassword string // alias for BindPassword (kept for parity with other engines)
}

// giteaAuthSource is a subset of the Gitea authentication source JSON.
type giteaAuthSource struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"` // 6 = LDAP via BindDN
}

// ListAuthSources checks Gitea admin API health by listing users (limit=1).
// Gitea 1.23 removed /api/v1/admin/auths, so we use /admin/users as a
// reachability + admin-token validity check.
func (c *Client) ListAuthSources() ([]giteaAuthSource, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/api/v1/admin/users?limit=1", nil)
	if err != nil {
		return nil, fmt.Errorf("gitea admin health: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea admin health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea admin health: %d %s", resp.StatusCode, string(b))
	}
	// Return a synthetic entry indicating Gitea is reachable and admin API works.
	return []giteaAuthSource{{ID: 0, Name: "admin-api-ok", Type: 0}}, nil
}

// ldapAuthSourceBody builds the request body for creating/updating a Gitea LDAP auth source.
func ldapAuthSourceBody(cfg LDAPConfig) map[string]interface{} {
	host := cfg.Host
	if host == "" {
		host = "polyon-dc"
	}
	port := cfg.Port
	if port == 0 {
		port = 389
	}

	adminFilter := ""
	if cfg.AdminDN != "" {
		// Extract the DC parts to build the domain-specific admin filter.
		// e.g. CN=Administrator,CN=Users,DC=cmars,DC=kr → DC=cmars,DC=kr
		adminFilter = fmt.Sprintf("(memberOf=CN=Domain Admins,CN=Users,%s)", cfg.UserSearchBase)
	}

	bindPassword := cfg.BindPassword
	if bindPassword == "" {
		bindPassword = cfg.DCAdminPassword
	}

	return map[string]interface{}{
		"name":                    "HELIOS AD",
		"type":                    6, // LDAP via BindDN
		"is_active":               true,
		"security_protocol":       0, // unencrypted
		"host":                    host,
		"port":                    port,
		"bind_dn":                 cfg.BindDN,
		"bind_password":           bindPassword,
		"user_search_base":        cfg.UserSearchBase,
		"user_filter":             "(&(objectClass=user)(sAMAccountName=%s))",
		"admin_filter":            adminFilter,
		"attribute_username":      "sAMAccountName",
		"attribute_name":          "givenName",
		"attribute_surname":       "sn",
		"attribute_mail":          "mail",
		"attribute_ssh_public_key": "",
		"skip_tls_verify":         true,
	}
}

// ConfigureLDAP is a no-op placeholder.
// Gitea 1.23 removed /api/v1/admin/auths — LDAP auth sources must be
// configured via `gitea admin auth` CLI inside the container, or via
// the Gitea web admin UI.  The existing "HELIOS SSO" (OAuth2, id=3)
// was already created via CLI during initial setup.
func (c *Client) ConfigureLDAP(cfg LDAPConfig) error {
	// Verify Gitea is reachable first.
	_, err := c.ListAuthSources()
	if err != nil {
		return fmt.Errorf("gitea ConfigureLDAP: cannot reach Gitea admin API: %w", err)
	}
	// LDAP configuration must be done via CLI:
	//   docker exec polyon-gitea gitea admin auth add-ldap-simple ...
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Push / Pull Mirror management
// ──────────────────────────────────────────────────────────────────────────────

// PushMirror represents a Gitea push mirror configuration.
type PushMirror struct {
	ID            int64  `json:"id"`
	RemoteName    string `json:"remote_name"`
	RemoteAddress string `json:"remote_address"`
	SyncOnCommit  bool   `json:"sync_on_commit"`
	Interval      string `json:"interval"`
	CreatedAt     string `json:"created_unix,omitempty"`
	LastSync      string `json:"last_update,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

// ListPushMirrors lists push mirrors for a repo.
func (c *Client) ListPushMirrors(owner, repo string) ([]PushMirror, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/push_mirrors", c.BaseURL, owner, repo)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea list push mirrors: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea list push mirrors: %d %s", resp.StatusCode, string(b))
	}
	var mirrors []PushMirror
	json.NewDecoder(resp.Body).Decode(&mirrors)
	return mirrors, nil
}

// AddPushMirror adds a push mirror to a repo.
func (c *Client) AddPushMirror(owner, repo, remoteAddress, interval, token string, syncOnCommit bool) (*PushMirror, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"remote_address":  remoteAddress,
		"remote_username": "oauth2",
		"remote_password": token,
		"interval":        interval,
		"sync_on_commit":  syncOnCommit,
	})
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/push_mirrors", c.BaseURL, owner, repo)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea add push mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea add push mirror: %d %s", resp.StatusCode, string(b))
	}
	var mirror PushMirror
	json.NewDecoder(resp.Body).Decode(&mirror)
	return &mirror, nil
}

// DeletePushMirror deletes a push mirror.
func (c *Client) DeletePushMirror(owner, repo string, mirrorID int64) error {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/push_mirrors/%d", c.BaseURL, owner, repo, mirrorID)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea delete push mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitea delete push mirror: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

// SyncPushMirror triggers a manual push mirror sync.
func (c *Client) SyncPushMirror(owner, repo string, mirrorID int64) error {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/push_mirrors/%d/sync", c.BaseURL, owner, repo, mirrorID)
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea sync push mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitea sync push mirror: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

// CreatePullMirror creates a pull mirror repo from an external source.
func (c *Client) CreatePullMirror(name, cloneAddr, description, interval string) (*Repo, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"clone_addr":      cloneAddr,
		"repo_name":       name,
		"repo_owner":      "polyon",
		"mirror":          true,
		"mirror_interval": interval,
		"private":         true,
		"description":     description,
		"service":         "git",
	})
	req, _ := http.NewRequest("POST", c.BaseURL+"/api/v1/repos/migrate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea create pull mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea create pull mirror: %d %s", resp.StatusCode, string(b))
	}
	var repo Repo
	json.NewDecoder(resp.Body).Decode(&repo)
	return &repo, nil
}

// ListRepos lists all repos for an owner (user or org).
func (c *Client) ListRepos(owner string) ([]Repo, error) {
	url := fmt.Sprintf("%s/api/v1/orgs/%s/repos?limit=50", c.BaseURL, owner)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea list repos: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Fallback: try user repos
		url2 := fmt.Sprintf("%s/api/v1/user/repos?limit=50", c.BaseURL)
		req2, _ := http.NewRequest("GET", url2, nil)
		req2.Header.Set("Authorization", "token "+c.Token)
		resp2, err2 := c.http.Do(req2)
		if err2 != nil {
			return nil, fmt.Errorf("gitea list repos: %w", err2)
		}
		defer resp2.Body.Close()
		var repos []Repo
		json.NewDecoder(resp2.Body).Decode(&repos)
		return repos, nil
	}
	var repos []Repo
	json.NewDecoder(resp.Body).Decode(&repos)
	return repos, nil
}

// CreateMirror creates a mirror repo from an external URL.
func (c *Client) CreateMirror(name, cloneAddr, description string) (*Repo, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"clone_addr":  cloneAddr,
		"repo_name":   name,
		"repo_owner":  "polyon",
		"mirror":      false,
		"private":     true,
		"description": description,
		"service":     "git",
	})
	req, _ := http.NewRequest("POST", c.BaseURL+"/api/v1/repos/migrate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea mirror: %d %s", resp.StatusCode, string(b))
	}
	var repo Repo
	json.NewDecoder(resp.Body).Decode(&repo)
	return &repo, nil
}
