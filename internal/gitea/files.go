package gitea

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// FileEntry represents a file or directory in a repo tree.
type FileEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" | "dir" | "symlink" | "submodule"
	Size        int64  `json:"size"`
	SHA         string `json:"sha,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	HTMLURL     string `json:"html_url,omitempty"`
}

// FileContent represents file content with metadata.
type FileContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	SHA         string `json:"sha"`
	Content     string `json:"content"`     // base64 encoded
	Encoding    string `json:"encoding"`    // "base64"
	DownloadURL string `json:"download_url,omitempty"`
}

// DiffResponse represents a commit diff.
type DiffResponse struct {
	Files []DiffFile `json:"files"`
}

// DiffFile represents a changed file in a diff.
type DiffFile struct {
	Filename    string `json:"filename"`
	Status      string `json:"status"` // added, modified, deleted, renamed
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	Patch       string `json:"patch,omitempty"`
	PreviousName string `json:"previous_filename,omitempty"`
}

// Branch represents a git branch.
type Branch struct {
	Name   string  `json:"name"`
	Commit *Commit `json:"commit,omitempty"`
}

// FileAction for create/update/delete operations.
type FileAction struct {
	Content string `json:"content,omitempty"` // base64 encoded (create/update)
	Message string `json:"message"`
	Branch  string `json:"branch,omitempty"`
	SHA     string `json:"sha,omitempty"` // required for update/delete
}

// ListFiles returns directory contents at a given path.
func (c *Client) ListFiles(owner, repo, ref, path string) ([]FileEntry, error) {
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s",
		c.BaseURL, owner, repo, url.PathEscape(path))
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea list files: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea list files: %d %s", resp.StatusCode, string(b))
	}
	var entries []FileEntry
	json.NewDecoder(resp.Body).Decode(&entries)
	return entries, nil
}

// GetFile returns file content at a given path.
func (c *Client) GetFile(owner, repo, ref, path string) (*FileContent, error) {
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s",
		c.BaseURL, owner, repo, url.PathEscape(path))
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea get file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gitea get file: %d %s", resp.StatusCode, string(b))
	}
	var fc FileContent
	json.NewDecoder(resp.Body).Decode(&fc)
	return &fc, nil
}

// CreateFile creates a new file in the repo.
func (c *Client) CreateFile(owner, repo, path string, action *FileAction) error {
	body, _ := json.Marshal(map[string]interface{}{
		"content": action.Content,
		"message": action.Message,
		"branch":  action.Branch,
	})
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s",
		c.BaseURL, owner, repo, url.PathEscape(path))
	req, _ := http.NewRequest("POST", u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea create file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitea create file: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

// UpdateFile updates an existing file in the repo.
func (c *Client) UpdateFile(owner, repo, path string, action *FileAction) error {
	body, _ := json.Marshal(map[string]interface{}{
		"content": action.Content,
		"message": action.Message,
		"branch":  action.Branch,
		"sha":     action.SHA,
	})
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s",
		c.BaseURL, owner, repo, url.PathEscape(path))
	req, _ := http.NewRequest("PUT", u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea update file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitea update file: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

// DeleteFile deletes a file from the repo.
func (c *Client) DeleteFile(owner, repo, path string, action *FileAction) error {
	body, _ := json.Marshal(map[string]interface{}{
		"message": action.Message,
		"branch":  action.Branch,
		"sha":     action.SHA,
	})
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s",
		c.BaseURL, owner, repo, url.PathEscape(path))
	req, _ := http.NewRequest("DELETE", u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitea delete file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitea delete file: %d %s", resp.StatusCode, string(b))
	}
	return nil
}

// ListBranches returns all branches for a repo.
func (c *Client) ListBranches(owner, repo string) ([]Branch, error) {
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches", c.BaseURL, owner, repo)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea branches: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gitea branches: %d", resp.StatusCode)
	}
	var branches []Branch
	json.NewDecoder(resp.Body).Decode(&branches)
	return branches, nil
}

// GetCommitDiff returns the diff for a specific commit.
func (c *Client) GetCommitDiff(owner, repo, sha string) (string, error) {
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/git/commits/%s.diff",
		c.BaseURL, owner, repo, sha)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "token "+c.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gitea diff: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gitea diff: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}
