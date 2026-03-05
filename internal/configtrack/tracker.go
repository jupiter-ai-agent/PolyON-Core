package configtrack

import (
	"encoding/base64"
	"fmt"
	"log"

	"github.com/triangles/polyon-core/internal/gitea"
)

// ConfigTracker tracks PolyON configuration changes in a Gitea repository.
type ConfigTracker struct {
	gitea  *gitea.Client
	owner  string // "polyon"
	repo   string // "polyon-config"
	logger *log.Logger
}

// New creates a ConfigTracker.
func New(g *gitea.Client, owner, repo string, logger *log.Logger) *ConfigTracker {
	if logger == nil {
		logger = log.Default()
	}
	return &ConfigTracker{
		gitea:  g,
		owner:  owner,
		repo:   repo,
		logger: logger,
	}
}

// EnsureRepo creates the polyon-config repository if it does not exist.
func (t *ConfigTracker) EnsureRepo() error {
	existing, err := t.gitea.GetRepo(t.owner, t.repo)
	if err != nil {
		return fmt.Errorf("configtrack: get repo: %w", err)
	}
	if existing != nil {
		t.logger.Printf("configtrack: repo %s/%s already exists", t.owner, t.repo)
		return nil
	}

	_, err = t.gitea.CreateRepo(t.repo, "PolyON IaC configuration repository (auto-managed)")
	if err != nil {
		return fmt.Errorf("configtrack: create repo: %w", err)
	}
	t.logger.Printf("configtrack: created repo %s/%s", t.owner, t.repo)
	return nil
}

// CommitFile creates or updates a single file in the config repo.
// content is plain text — it will be base64-encoded before sending to Gitea API.
// author is used in the commit message attribution (optional, may be empty).
func (t *ConfigTracker) CommitFile(path, content, message, author string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	// Check whether the file already exists (need SHA for updates).
	existing, err := t.gitea.GetFile(t.owner, t.repo, "main", path)
	if err != nil {
		return fmt.Errorf("configtrack: get file %s: %w", path, err)
	}

	commitMsg := message
	if author != "" {
		commitMsg = fmt.Sprintf("%s (by %s)", message, author)
	}

	if existing != nil {
		// File exists — update it.
		err = t.gitea.UpdateFile(t.owner, t.repo, path, &gitea.FileAction{
			Content: encoded,
			Message: commitMsg,
			Branch:  "main",
			SHA:     existing.SHA,
		})
		if err != nil {
			return fmt.Errorf("configtrack: update file %s: %w", path, err)
		}
		t.logger.Printf("configtrack: updated %s — %s", path, commitMsg)
	} else {
		// File does not exist — create it.
		err = t.gitea.CreateFile(t.owner, t.repo, path, &gitea.FileAction{
			Content: encoded,
			Message: commitMsg,
			Branch:  "main",
		})
		if err != nil {
			return fmt.Errorf("configtrack: create file %s: %w", path, err)
		}
		t.logger.Printf("configtrack: created %s — %s", path, commitMsg)
	}
	return nil
}

// CommitInitialConfig writes multiple files to the config repo in one logical operation.
// configs is a map of file path → plain text content.
// Each file is committed individually with the shared message "Initial PolyON configuration".
func (t *ConfigTracker) CommitInitialConfig(configs map[string]string) error {
	const msg = "Initial PolyON configuration"
	for path, content := range configs {
		if err := t.CommitFile(path, content, msg, ""); err != nil {
			return fmt.Errorf("configtrack: initial commit of %s: %w", path, err)
		}
	}
	return nil
}

// GetHistory returns up to limit recent commits for the config repo.
func (t *ConfigTracker) GetHistory(limit int) ([]gitea.Commit, error) {
	commits, err := t.gitea.ListCommits(t.owner, t.repo, limit)
	if err != nil {
		return nil, fmt.Errorf("configtrack: list commits: %w", err)
	}
	return commits, nil
}

// GetFileDiff returns the unified diff for a specific commit SHA.
func (t *ConfigTracker) GetFileDiff(sha string) (string, error) {
	diff, err := t.gitea.GetCommitDiff(t.owner, t.repo, sha)
	if err != nil {
		return "", fmt.Errorf("configtrack: get diff %s: %w", sha, err)
	}
	return diff, nil
}
