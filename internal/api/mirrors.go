package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterMirrors registers Git mirror management API routes.
func RegisterMirrors(r chi.Router, d *Deps) {
	r.Route("/mirrors", func(r chi.Router) {
		r.Get("/status", mirrorStatus(d))
		r.Get("/{owner}/{repo}/push", listPushMirrors(d))
		r.Post("/{owner}/{repo}/push", addPushMirror(d))
		r.Delete("/{owner}/{repo}/push/{mirrorId}", deletePushMirror(d))
		r.Post("/pull", createPullMirror(d))
	})
}

// GET /api/v1/mirrors/status — all repos with push mirror info
func mirrorStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Gitea == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "gitea not ready", "gitea not ready")
			return
		}

		repos, err := d.Gitea.ListRepos("polyon")
		if err != nil {
			log.Warn().Err(err).Msg("mirror status: list repos")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}

		type repoMirrorInfo struct {
			Name        string      `json:"name"`
			FullName    string      `json:"full_name"`
			CloneURL    string      `json:"clone_url"`
			Empty       bool        `json:"empty"`
			Mirror      bool        `json:"mirror"`
			PushMirrors interface{} `json:"push_mirrors"`
		}

		var result []repoMirrorInfo
		for _, repo := range repos {
			info := repoMirrorInfo{
				Name:     repo.Name,
				FullName: repo.FullName,
				CloneURL: repo.CloneURL,
				Empty:    repo.Empty,
			}

			mirrors, err := d.Gitea.ListPushMirrors("polyon", repo.Name)
			if err != nil {
				info.PushMirrors = []interface{}{}
			} else {
				info.PushMirrors = mirrors
			}
			result = append(result, info)
		}

		httputil.RespondJSON(w, http.StatusOK, result)
	}
}

// GET /api/v1/mirrors/{owner}/{repo}/push
func listPushMirrors(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Gitea == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "gitea not ready", "gitea not ready")
			return
		}
		owner := chi.URLParam(r, "owner")
		repo := chi.URLParam(r, "repo")

		mirrors, err := d.Gitea.ListPushMirrors(owner, repo)
		if err != nil {
			log.Warn().Err(err).Str("repo", owner+"/"+repo).Msg("list push mirrors")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, mirrors)
	}
}

// POST /api/v1/mirrors/{owner}/{repo}/push
func addPushMirror(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Gitea == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "gitea not ready", "gitea not ready")
			return
		}
		owner := chi.URLParam(r, "owner")
		repo := chi.URLParam(r, "repo")

		var req struct {
			RemoteAddress string `json:"remote_address"`
			Token         string `json:"token"`
			Interval      string `json:"interval"`
			SyncOnCommit  bool   `json:"sync_on_commit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if req.RemoteAddress == "" {
			httputil.RespondError(w, http.StatusBadRequest, "remote_address required", "remote_address required")
			return
		}
		if req.Interval == "" {
			req.Interval = "1h"
		}

		mirror, err := d.Gitea.AddPushMirror(owner, repo, req.RemoteAddress, req.Interval, req.Token, req.SyncOnCommit)
		if err != nil {
			log.Warn().Err(err).Str("repo", owner+"/"+repo).Msg("add push mirror")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		log.Info().Str("repo", owner+"/"+repo).Str("remote", req.RemoteAddress).Msg("push mirror added")
		httputil.RespondJSON(w, http.StatusCreated, mirror)
	}
}

// DELETE /api/v1/mirrors/{owner}/{repo}/push/{mirrorId}
func deletePushMirror(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Gitea == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "gitea not ready", "gitea not ready")
			return
		}
		owner := chi.URLParam(r, "owner")
		repo := chi.URLParam(r, "repo")
		idStr := chi.URLParam(r, "mirrorId")
		mirrorID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid mirror ID", "invalid mirror ID")
			return
		}

		if err := d.Gitea.DeletePushMirror(owner, repo, mirrorID); err != nil {
			log.Warn().Err(err).Str("repo", owner+"/"+repo).Int64("mirror", mirrorID).Msg("delete push mirror")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		log.Info().Str("repo", owner+"/"+repo).Int64("mirror", mirrorID).Msg("push mirror deleted")
		httputil.RespondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// POST /api/v1/mirrors/pull
func createPullMirror(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Gitea == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "gitea not ready", "gitea not ready")
			return
		}

		var req struct {
			Name        string `json:"name"`
			CloneAddr   string `json:"clone_addr"`
			Description string `json:"description"`
			Interval    string `json:"interval"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if req.Name == "" || req.CloneAddr == "" {
			httputil.RespondError(w, http.StatusBadRequest, "name and clone_addr required", "name and clone_addr required")
			return
		}
		if req.Interval == "" {
			req.Interval = "8h"
		}

		repo, err := d.Gitea.CreatePullMirror(req.Name, req.CloneAddr, req.Description, req.Interval)
		if err != nil {
			log.Warn().Err(err).Str("name", req.Name).Msg("create pull mirror")
			httputil.RespondError(w, http.StatusBadGateway, "git error", err.Error())
			return
		}
		log.Info().Str("repo", repo.FullName).Str("source", req.CloneAddr).Msg("pull mirror created")
		httputil.RespondJSON(w, http.StatusCreated, map[string]interface{}{
			"status":   "created",
			"repoName": repo.FullName,
			"cloneUrl": repo.CloneURL,
		})
	}
}

// Ensure fmt is used (for strconv import).
var _ = fmt.Sprintf
