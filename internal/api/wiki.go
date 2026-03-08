package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/engine/affine"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterWiki(r chi.Router, d *Deps) {
	r.Route("/engines/wiki", func(r chi.Router) {
		r.Get("/status", wikiStatus(d))
		r.Get("/info", wikiInfo(d))
		// LDAP sync removed — OIDC JIT provisioning
	})
}

func wikiStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client := affine.NewClient(d.Cfg.AffineURL)
		
		healthy, _ := client.Health()
		version, _ := client.Version()
		
		status := "down"
		if healthy {
			status = "healthy"
		}
		
		httputil.RespondOK(w, map[string]interface{}{
			"status":  status,
			"version": version,
			"engine":  "affine",
			"url":     d.Cfg.AffineURL,
		})
	}
}

func wikiInfo(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client := affine.NewClient(d.Cfg.AffineURL)
		version, err := client.Version()
		if err != nil {
			httputil.RespondError(w, 503, "WIKI_UNREACHABLE", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{
			"version": version,
			"engine":  "affine",
		})
	}
}

