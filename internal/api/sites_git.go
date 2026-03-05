package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/gitea"
	"github.com/triangles/polyon-core/internal/httputil"
)

const giteaOwner = "polyon"

// helper: resolve site slug from site ID
func siteSlug(d *Deps, r *http.Request) (string, error) {
	siteID := chi.URLParam(r, "id")
	site, err := d.Store.GetSite(r.Context(), siteID)
	if err != nil || site == nil {
		return "", err
	}
	return site.Slug, nil
}

// helper: extract file path from wildcard
func filePath(r *http.Request) string {
	p := chi.URLParam(r, "*")
	p = strings.TrimPrefix(p, "/")
	return p
}

// helper: get ref from query param
func refParam(r *http.Request) string {
	return r.URL.Query().Get("ref")
}

func requireGitea(d *Deps, w http.ResponseWriter) bool {
	if d.Store == nil || d.Gitea == nil {
		httputil.RespondError(w, http.StatusServiceUnavailable, "service not ready", "service not ready")
		return false
	}
	return true
}

// GET /sites/{id}/files[/*path] — list directory contents
func getSiteFiles(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		path := filePath(r)
		ref := refParam(r)

		entries, err := d.Gitea.ListFiles(giteaOwner, slug, ref, path)
		if err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("path", path).Msg("list files")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, entries)
	}
}

// GET /sites/{id}/file/*path — get file content
func getSiteFile(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		path := filePath(r)
		ref := refParam(r)

		fc, err := d.Gitea.GetFile(giteaOwner, slug, ref, path)
		if err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("path", path).Msg("get file")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		if fc == nil {
			httputil.RespondError(w, http.StatusNotFound, "file not found", "file not found")
			return
		}
		httputil.RespondJSON(w, http.StatusOK, fc)
	}
}

// POST /sites/{id}/file/*path — create file
func createSiteFile(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		path := filePath(r)

		var action gitea.FileAction
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if action.Message == "" {
			action.Message = "Create " + path
		}
		if action.Branch == "" {
			action.Branch = "main"
		}

		if err := d.Gitea.CreateFile(giteaOwner, slug, path, &action); err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("path", path).Msg("create file")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusCreated, map[string]string{"status": "created"})
	}
}

// PUT /sites/{id}/file/*path — update file
func updateSiteFile(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		path := filePath(r)

		var action gitea.FileAction
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if action.Message == "" {
			action.Message = "Update " + path
		}
		if action.Branch == "" {
			action.Branch = "main"
		}
		if action.SHA == "" {
			httputil.RespondError(w, http.StatusBadRequest, "sha required for update", "sha required")
			return
		}

		if err := d.Gitea.UpdateFile(giteaOwner, slug, path, &action); err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("path", path).Msg("update file")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// DELETE /sites/{id}/file/*path — delete file
func deleteSiteFile(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		path := filePath(r)

		var action gitea.FileAction
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if action.Message == "" {
			action.Message = "Delete " + path
		}
		if action.SHA == "" {
			httputil.RespondError(w, http.StatusBadRequest, "sha required for delete", "sha required")
			return
		}

		if err := d.Gitea.DeleteFile(giteaOwner, slug, path, &action); err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("path", path).Msg("delete file")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// GET /sites/{id}/branches — list branches
func getSiteBranches(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		branches, err := d.Gitea.ListBranches(giteaOwner, slug)
		if err != nil {
			httputil.RespondJSON(w, http.StatusOK, []interface{}{})
			return
		}
		httputil.RespondJSON(w, http.StatusOK, branches)
	}
}

// GET /sites/{id}/commits/{sha}/diff — get commit diff
func getSiteCommitDiff(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		slug, err := siteSlug(d, r)
		if err != nil || slug == "" {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		sha := chi.URLParam(r, "sha")
		diff, err := d.Gitea.GetCommitDiff(giteaOwner, slug, sha)
		if err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("sha", sha).Msg("commit diff")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, map[string]string{"diff": diff})
	}
}

// getSiteGit returns the internal Gitea repo info for a site.
func getSiteGit(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		siteID := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), siteID)
		if err != nil || site == nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		repo, err := d.Gitea.GetRepo(giteaOwner, site.Slug)
		if err != nil || repo == nil {
			httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"linked": false,
			})
			return
		}
		httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"linked":        true,
			"repoName":      repo.FullName,
			"cloneUrl":      repo.CloneURL,
			"defaultBranch": repo.DefaultBranch,
			"empty":         repo.Empty,
			"updatedAt":     repo.UpdatedAt,
		})
	}
}

// getSiteCommits returns recent commits from the Gitea repo.
func getSiteCommits(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		siteID := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), siteID)
		if err != nil || site == nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		commits, err := d.Gitea.ListCommits(giteaOwner, site.Slug, 10)
		if err != nil {
			httputil.RespondJSON(w, http.StatusOK, []interface{}{})
			return
		}
		httputil.RespondJSON(w, http.StatusOK, commits)
	}
}

// importExternalGit imports an external git repo into the site's internal Gitea repo.
func importExternalGit(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireGitea(d, w) {
			return
		}
		siteID := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), siteID)
		if err != nil || site == nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}

		var req struct {
			RepoURL string `json:"repoUrl"`
			Branch  string `json:"branch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if req.RepoURL == "" {
			httputil.RespondError(w, http.StatusBadRequest, "repoUrl required", "repoUrl required")
			return
		}
		if req.Branch == "" {
			req.Branch = "main"
		}

		// Delete existing repo if any, then create mirror
		_ = d.Gitea.DeleteRepo(giteaOwner, site.Slug)

		repo, err := d.Gitea.CreateMirror(site.Slug, req.RepoURL, "HELIOS Homepage: "+site.Name)
		if err != nil {
			log.Error().Err(err).Str("site", siteID).Str("url", req.RepoURL).Msg("import git failed")
			httputil.RespondError(w, http.StatusBadGateway, "git import failed", err.Error())
			return
		}

		// Update site record
		repoURL := req.RepoURL
		site.RepoURL = &repoURL
		site.Branch = &req.Branch
		_ = d.Store.UpdateSiteRepoInfo(r.Context(), siteID, repoURL, req.Branch)

		log.Info().Str("repo", repo.FullName).Str("mirror", req.RepoURL).Msg("external git imported")
		httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "imported",
			"repoName": repo.FullName,
			"cloneUrl": repo.CloneURL,
		})
	}
}
