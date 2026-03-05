package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/builder"
	"github.com/triangles/polyon-core/internal/gitea"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/store"
	"github.com/triangles/polyon-core/internal/template"
)

// RegisterSites registers homepage site management API routes.
func RegisterSites(r chi.Router, d *Deps) {
	r.Route("/sites", func(r chi.Router) {
		r.Get("/", listSites(d))
		r.Post("/", createSite(d))
		r.Get("/{id}", getSite(d))
		r.Delete("/{id}", deleteSite(d))
		r.Post("/{id}/build", triggerBuild(d))
		r.Get("/{id}/builds", listBuilds(d))
		r.Get("/{id}/builds/{bid}", getBuild(d))
		r.Get("/{id}/build-log", streamBuildLog(d))
		r.Post("/{id}/layout", saveLayout(d))
		r.Get("/{id}/layout", getLayout(d))
		r.Get("/{id}/git", getSiteGit(d))
		r.Post("/{id}/git/import", importExternalGit(d))
		r.Get("/{id}/commits", getSiteCommits(d))
		r.Get("/{id}/commits/{sha}/diff", getSiteCommitDiff(d))
		r.Get("/{id}/branches", getSiteBranches(d))
		r.Get("/{id}/files", getSiteFiles(d))
		r.Get("/{id}/files/*", getSiteFiles(d))
		r.Get("/{id}/file/*", getSiteFile(d))
		r.Post("/{id}/file/*", createSiteFile(d))
		r.Put("/{id}/file/*", updateSiteFile(d))
		r.Delete("/{id}/file/*", deleteSiteFile(d))
		// Domain management
		r.Post("/{id}/domain", setSiteDomain(d))
		r.Delete("/{id}/domain", removeSiteDomain(d))
		// Preview build
		r.Post("/{id}/preview", triggerPreview(d))
	})

	// Strapi webhook — receives content change events and triggers rebuild
	r.Post("/webhook/strapi", handleStrapiWebhook(d))

	// Gitea webhook — receives push events and triggers rebuild for git-sourced sites
	r.Post("/webhook/gitea", handleGiteaWebhook(d))
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]`)

func toSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		s = "site"
	}
	return s
}

// ── Handlers ──

func listSites(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		sites, err := d.Store.ListSites(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("list sites")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to list sites", "failed to list sites")
			return
		}
		httputil.RespondOK(w, sites)
	}
}

type createSiteReq struct {
	Name      string  `json:"name"`
	Method    string  `json:"method"`   // "editor" | "git" | "strapi"
	Template  string  `json:"template"` // "corporate" | "landing" | "blog" | "blank"
	RepoURL   *string `json:"repoUrl,omitempty"`
	Branch    *string `json:"branch,omitempty"`
	Framework *string `json:"framework,omitempty"`
	BuildCmd  *string `json:"buildCmd,omitempty"`
	OutputDir *string `json:"outputDir,omitempty"`
}

func createSite(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		var req createSiteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if req.Name == "" {
			httputil.RespondError(w, http.StatusBadRequest, "name required", "name required")
			return
		}
		// Normalize 'cms' alias → 'strapi' (Phase 1 rename)
		if req.Method == "cms" {
			req.Method = "strapi"
		}
		if req.Method != "git" && req.Method != "strapi" && req.Method != "editor" {
			httputil.RespondError(w, http.StatusBadRequest, "method must be 'cms', 'git', or 'strapi'", "method must be 'cms', 'git', or 'strapi'")
			return
		}
		if req.Method == "git" && (req.RepoURL == nil || *req.RepoURL == "") {
			httputil.RespondError(w, http.StatusBadRequest, "repoUrl required for git method", "repoUrl required for git method")
			return
		}

		id := uuid.New().String()
		slug := toSlug(req.Name)

		// Default branch
		branch := "main"
		if req.Branch != nil && *req.Branch != "" {
			branch = *req.Branch
		}

		// Default template
		tmpl := req.Template
		if tmpl == "" {
			tmpl = "corporate"
		}
		validTemplates := map[string]bool{"corporate": true, "landing": true, "blog": true, "blank": true}
		if !validTemplates[tmpl] {
			tmpl = "corporate"
		}

		site := &store.Site{
			ID:        id,
			Name:      req.Name,
			Slug:      slug,
			Method:    req.Method,
			Status:    "creating",
			Template:  tmpl,
			RepoURL:   req.RepoURL,
			Branch:    &branch,
			Framework: req.Framework,
			BuildCmd:  req.BuildCmd,
			OutputDir: req.OutputDir,
		}

		if err := d.Store.CreateSite(r.Context(), site); err != nil {
			if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
				// Slug conflict — append short ID
				site.Slug = slug + "-" + id[:8]
				if err2 := d.Store.CreateSite(r.Context(), site); err2 != nil {
					log.Error().Err(err2).Msg("create site retry")
					httputil.RespondError(w, http.StatusInternalServerError, "failed to create site", "failed to create site")
					return
				}
			} else {
				log.Error().Err(err).Msg("create site")
				httputil.RespondError(w, http.StatusInternalServerError, "failed to create site", "failed to create site")
				return
			}
		}

		// Create Gitea repo (always — even for strapi sites)
		if d.Gitea != nil {
			repoName := site.Slug
			desc := "HELIOS Homepage: " + site.Name
			if req.Method == "git" && req.RepoURL != nil && *req.RepoURL != "" {
				// Mirror external repo
				repo, err := d.Gitea.CreateMirror(repoName, *req.RepoURL, desc)
				if err != nil {
					log.Warn().Err(err).Str("site", id).Msg("gitea mirror failed")
				} else {
					log.Info().Str("repo", repo.FullName).Msg("gitea mirror created")
				}
			} else {
				// New empty repo for editor/strapi sites
				repo, err := d.Gitea.CreateRepo(repoName, desc)
				if err != nil {
					log.Warn().Err(err).Str("site", id).Msg("gitea repo create failed")
				} else {
					log.Info().Str("repo", repo.FullName).Msg("gitea repo created")
					// For strapi sites: push template files into the new repo
					if req.Method == "strapi" {
						go initTemplateFiles(d.Gitea, giteaOwner, repoName, tmpl)
					}
				}
			}
		}

		// Strapi sites: mark as draft then trigger initial build after template init
		if req.Method == "strapi" {
			if err := d.Store.UpdateSiteStatus(r.Context(), id, "draft"); err != nil {
				log.Warn().Err(err).Str("site", id).Msg("set strapi site status draft")
			}
			// Trigger initial build (after a brief delay so template files are ready)
			if d.Builder != nil && d.Store != nil {
				go func() {
					time.Sleep(5 * time.Second) // wait for template init goroutine
					buildID, buildErr := d.Store.CreateBuild(context.Background(), id, "init")
					if buildErr != nil {
						log.Warn().Err(buildErr).Str("site", id).Msg("init build create failed")
						return
					}
					d.Builder.BuildNextJSSite(id, buildID)
				}()
			}
		}

		// Git sites: build is triggered manually by user

		// Re-read to get timestamps
		created, err := d.Store.GetSite(r.Context(), id)
		if err != nil {
			// Return what we have
			httputil.RespondJSON(w, http.StatusCreated, site)
			return
		}
		httputil.RespondJSON(w, http.StatusCreated, created)
	}
}

func getSite(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		httputil.RespondOK(w, site)
	}
}

func deleteSite(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		if err := d.Store.DeleteSite(r.Context(), id); err != nil {
			log.Error().Err(err).Msg("delete site")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to delete site", "failed to delete site")
			return
		}
		// TODO: cleanup /data/sites/{id} directory
		httputil.RespondOK(w, map[string]string{"status": "deleted"})
	}
}

func triggerBuild(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		buildID, err := d.Store.CreateBuild(r.Context(), site.ID, "manual")
		if err != nil {
			log.Error().Err(err).Msg("create build")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to create build", "failed to create build")
			return
		}
		if d.Builder != nil {
			// Strapi method → Next.js SSG builder; others → generic git builder
			if site.Method == "strapi" {
				go d.Builder.BuildNextJSSite(site.ID, buildID)
			} else {
				go d.Builder.BuildGitSite(site.ID, buildID)
			}
		}
		httputil.RespondJSON(w, http.StatusCreated, map[string]interface{}{
			"buildId": buildID,
			"status":  "pending",
		})
	}
}

func listBuilds(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		builds, err := d.Store.ListBuilds(r.Context(), id, 20)
		if err != nil {
			log.Error().Err(err).Msg("list builds")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to list builds", "failed to list builds")
			return
		}
		httputil.RespondOK(w, builds)
	}
}

func getBuild(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		bidStr := chi.URLParam(r, "bid")
		bid, err := strconv.Atoi(bidStr)
		if err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid build id", "invalid build id")
			return
		}
		build, err := d.Store.GetBuild(r.Context(), bid)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "build not found", "build not found")
			return
		}
		httputil.RespondOK(w, build)
	}
}

func saveLayout(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if err := d.Store.UpdateSiteLayout(r.Context(), id, body); err != nil {
			log.Error().Err(err).Msg("save layout")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to save layout", "failed to save layout")
			return
		}
		httputil.RespondOK(w, map[string]string{"status": "saved"})
	}
}

func getLayout(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}
		if site.LayoutJSON == nil {
			httputil.RespondOK(w, json.RawMessage(`{}`))
			return
		}
		httputil.RespondOK(w, site.LayoutJSON)
	}
}

// streamBuildLog sends build log lines via SSE.
func streamBuildLog(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Builder == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "builder_unavailable", "builder not ready")
			return
		}

		id := chi.URLParam(r, "id")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		logKey := builder.BuildKey(id)

		// Send existing log from latest build
		if d.Store != nil {
			builds, err := d.Store.ListBuilds(r.Context(), id, 1)
			if err == nil && len(builds) > 0 && builds[0].Log != "" {
				for _, line := range strings.Split(builds[0].Log, "\n") {
					if line != "" {
						fmt.Fprintf(w, "data: %s\n\n", line)
					}
				}
				flusher.Flush()
			}
			// If build already done, send done event and return
			if len(builds) > 0 && (builds[0].Status == "success" || builds[0].Status == "failed") {
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", builds[0].Status)
				flusher.Flush()
				return
			}
		}

		ch := d.Builder.Logs.Subscribe(logKey)
		defer d.Builder.Logs.Unsubscribe(logKey, ch)

		for {
			select {
			case line, ok := <-ch:
				if !ok {
					fmt.Fprintf(w, "event: done\ndata: build complete\n\n")
					flusher.Flush()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}


// ── Strapi Webhook ──

// strapiWebhookPayload is the body Strapi sends on content changes.
type strapiWebhookPayload struct {
	Event     string                 `json:"event"`
	CreatedAt string                 `json:"createdAt"`
	Model     string                 `json:"model"`
	UID       string                 `json:"uid"`
	Entry     map[string]interface{} `json:"entry"`
}

// handleStrapiWebhook receives Strapi content-change webhooks and
// triggers a rebuild of all strapi-method sites.
// POST /api/v1/webhook/strapi
func handleStrapiWebhook(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Optional secret validation
		secret := os.Getenv("STRAPI_WEBHOOK_SECRET")
		if secret != "" {
			got := r.Header.Get("X-Strapi-Signature")
			if got != secret {
				log.Warn().Str("got", got).Msg("strapi webhook: invalid signature")
				httputil.RespondError(w, http.StatusUnauthorized, "invalid signature", "invalid signature")
				return
			}
		}

		var payload strapiWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}

		log.Info().
			Str("event", payload.Event).
			Str("model", payload.Model).
			Msg("strapi webhook received")

		// Only rebuild on publish/unpublish/create/update/delete events
		switch payload.Event {
		case "entry.publish", "entry.unpublish", "entry.create",
			"entry.update", "entry.delete", "media.create",
			"media.update", "media.delete":
			// Trigger rebuild
		default:
			httputil.RespondOK(w, map[string]string{"status": "ignored"})
			return
		}

		if d.Store == nil || d.Builder == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "not ready", "not ready")
			return
		}

		// Find all strapi-method sites
		sites, err := d.Store.ListSites(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("strapi webhook: list sites failed")
			httputil.RespondError(w, http.StatusInternalServerError, "list sites failed", "list sites failed")
			return
		}

		triggered := 0
		for _, s := range sites {
			if s.Method != "strapi" {
				continue
			}
			site := s // capture
			buildID, buildErr := d.Store.CreateBuild(r.Context(), site.ID, "webhook")
			if buildErr != nil {
				log.Warn().Err(buildErr).Str("site", site.ID).Msg("strapi webhook: create build failed")
				continue
			}
			go d.Builder.BuildNextJSSite(site.ID, buildID)
			triggered++
			log.Info().Str("site", site.ID).Str("slug", site.Slug).Msg("strapi webhook: build triggered")
		}

		httputil.RespondOK(w, map[string]interface{}{
			"status":    "triggered",
			"builds":    triggered,
			"event":     payload.Event,
		})
	}
}

// ── Gitea Webhook ──

// giteaWebhookPayload is a subset of the Gitea push webhook body.
type giteaWebhookPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Pusher struct {
		Username string `json:"login"`
	} `json:"pusher"`
	TotalCommits int `json:"total_commits"`
	Commits      []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		URL     string `json:"url"`
		Author  struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"author"`
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
		Modified []string `json:"modified"`
	} `json:"commits"`
}

// handleGiteaWebhook receives Gitea push webhooks and triggers a rebuild
// for git-sourced sites whose Gitea repo matches the pushed repository.
// POST /api/v1/webhook/gitea
func handleGiteaWebhook(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only handle push events
		event := r.Header.Get("X-Gitea-Event")
		if event != "push" {
			httputil.RespondOK(w, map[string]string{"status": "ignored", "event": event})
			return
		}

		// Optional secret validation
		secret := os.Getenv("GITEA_WEBHOOK_SECRET")
		if secret != "" {
			got := r.Header.Get("X-Gitea-Signature")
			if got == "" {
				// Also check Authorization header as fallback
				got = r.Header.Get("Authorization")
			}
			// Simple token comparison (Gitea supports HMAC but also basic token)
			if got != secret && got != "Bearer "+secret {
				log.Warn().Str("event", event).Msg("gitea webhook: invalid signature")
				httputil.RespondError(w, http.StatusUnauthorized, "invalid signature", "invalid signature")
				return
			}
		}

		var payload giteaWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}

		repoName := payload.Repository.Name
		log.Info().
			Str("repo", payload.Repository.FullName).
			Str("ref", payload.Ref).
			Int("commits", payload.TotalCommits).
			Str("pusher", payload.Pusher.Username).
			Msg("gitea webhook received")

		if d.Store == nil || d.Builder == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "not ready", "not ready")
			return
		}

		// Find sites whose slug matches the repo name
		sites, err := d.Store.ListSites(r.Context())
		if err != nil {
			log.Error().Err(err).Msg("gitea webhook: list sites failed")
			httputil.RespondError(w, http.StatusInternalServerError, "list sites failed", "list sites failed")
			return
		}

		triggered := 0
		for _, s := range sites {
			// Match by slug (repo name) and check branch
			if s.Slug != repoName {
				continue
			}
			// Only rebuild if push is to the site's configured branch
			siteBranch := "main"
			if s.Branch != nil && *s.Branch != "" {
				siteBranch = *s.Branch
			}
			expectedRef := "refs/heads/" + siteBranch
			if payload.Ref != expectedRef {
				log.Debug().Str("site", s.ID).Str("ref", payload.Ref).Str("expected", expectedRef).
					Msg("gitea webhook: branch mismatch, skipping")
				continue
			}

			site := s
			buildID, buildErr := d.Store.CreateBuild(r.Context(), site.ID, "gitea-webhook")
			if buildErr != nil {
				log.Warn().Err(buildErr).Str("site", site.ID).Msg("gitea webhook: create build failed")
				continue
			}

			// Use appropriate builder based on method
			switch site.Method {
			case "strapi":
				go d.Builder.BuildNextJSSite(site.ID, buildID)
			default:
				go d.Builder.BuildGitSitePreview(site.ID, buildID)
			}
			triggered++
			log.Info().Str("site", site.ID).Str("slug", site.Slug).Msg("gitea webhook: build triggered")
		}

		// Parse #WS-{id} from commit messages and record workstream events
		if d.Store != nil {
			wsPattern := regexp.MustCompile(`#WS-(\d+)`)
			for _, c := range payload.Commits {
				matches := wsPattern.FindAllStringSubmatch(c.Message, -1)
				for _, m := range matches {
					wsID := "WS-" + m[1]
					filesChanged := len(c.Added) + len(c.Removed) + len(c.Modified)
					ref := c.ID
					if len(ref) > 7 {
						ref = ref[:7]
					}
					_ = d.Store.CreateWorkstreamEvent(r.Context(), &store.WorkstreamEvent{
						WorkstreamID: wsID,
						EventType:    "commit",
						RepoName:     payload.Repository.FullName,
						Ref:          ref,
						Author:       c.Author.Name,
						Message:      c.Message,
						URL:          c.URL,
						FilesChanged: filesChanged,
					})
				}
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"status": "triggered",
			"builds": triggered,
			"repo":   payload.Repository.FullName,
			"ref":    payload.Ref,
		})
	}
}

// ── Template Initializer ──

// initTemplateFiles commits the chosen Next.js template files
// into a freshly-created Gitea repo.
// Runs in a goroutine — failures are logged but non-fatal.
func initTemplateFiles(gc *gitea.Client, owner, repoName, tmpl string) {
	log.Info().Str("repo", owner+"/"+repoName).Str("template", tmpl).Msg("initialising template")

	var files map[string]string
	switch tmpl {
	case "landing":
		files = template.LandingTemplateFiles()
	case "blog":
		files = template.BlogTemplateFiles()
	case "blank":
		files = template.BlankTemplateFiles()
	default:
		files = template.CorporateTemplateFiles()
	}

	for path, content := range files {
		action := &gitea.FileAction{
			Content: content, // base64 encoded by template package
			Message: "init: " + tmpl + " template — " + path,
			Branch:  "main",
		}
		if err := gc.CreateFile(owner, repoName, path, action); err != nil {
			log.Warn().Err(err).
				Str("repo", owner+"/"+repoName).
				Str("path", path).
				Msg("template init: file commit failed")
		}
	}
	log.Info().Str("repo", owner+"/"+repoName).Str("template", tmpl).Int("files", len(files)).Msg("template initialised")
}

// ── Domain API ──

type setDomainReq struct {
	Domain string `json:"domain"`
}

// setSiteDomain sets a custom domain for a site and adds a Traefik route.
// POST /api/v1/sites/{id}/domain
func setSiteDomain(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")

		var req setDomainReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if req.Domain == "" {
			httputil.RespondError(w, http.StatusBadRequest, "domain required", "domain required")
			return
		}

		site, err := d.Store.GetSite(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}

		// Update DB
		if err := d.Store.UpdateSiteDomain(r.Context(), id, &req.Domain); err != nil {
			log.Error().Err(err).Msg("set domain: db update")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to update domain", "failed to update domain")
			return
		}

		// Update Traefik dynamic config
		if d.Traefik != nil {
			if err := d.Traefik.SetDomain(id, req.Domain, site.Slug); err != nil {
				log.Warn().Err(err).Str("site", id).Str("domain", req.Domain).Msg("traefik: set domain failed")
				// Non-fatal — domain saved in DB, Traefik can be fixed manually
			}
		}

		// Update nginx vhost if builder available
		if d.Builder != nil {
			go func() {
				if err := d.Builder.GenerateNginxConfig(r.Context(), id, site.Slug, req.Domain); err != nil {
					log.Warn().Err(err).Str("site", id).Msg("nginx vhost update failed")
				}
			}()
		}

		httputil.RespondOK(w, map[string]string{"status": "ok", "domain": req.Domain})
	}
}

// removeSiteDomain clears the custom domain for a site and removes the Traefik route.
// DELETE /api/v1/sites/{id}/domain
func removeSiteDomain(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")

		if _, err := d.Store.GetSite(r.Context(), id); err != nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}

		if err := d.Store.UpdateSiteDomain(r.Context(), id, nil); err != nil {
			log.Error().Err(err).Msg("remove domain: db update")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to remove domain", "failed to remove domain")
			return
		}

		// Remove Traefik route
		if d.Traefik != nil {
			if err := d.Traefik.RemoveDomain(id); err != nil {
				log.Warn().Err(err).Str("site", id).Msg("traefik: remove domain failed")
			}
		}

		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// ── Preview API ──

// triggerPreview triggers a preview build of the site (draft content).
// POST /api/v1/sites/{id}/preview
func triggerPreview(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB not ready", "DB not ready")
			return
		}
		id := chi.URLParam(r, "id")
		site, err := d.Store.GetSite(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "site not found", "site not found")
			return
		}

		buildID, err := d.Store.CreateBuild(r.Context(), site.ID, "preview")
		if err != nil {
			log.Error().Err(err).Msg("preview: create build")
			httputil.RespondError(w, http.StatusInternalServerError, "failed to create build", "failed to create build")
			return
		}

		if d.Builder != nil {
			if site.Method == "strapi" {
				go d.Builder.BuildNextJSSitePreview(site.ID, buildID)
			} else {
				go d.Builder.BuildGitSitePreview(site.ID, buildID)
			}
		}

		// Preview URL is served at /preview/{site-id}/
		previewURL := "/preview/" + site.ID + "/"

		httputil.RespondJSON(w, http.StatusCreated, map[string]interface{}{
			"buildId":    buildID,
			"status":     "pending",
			"previewUrl": previewURL,
		})
	}
}
