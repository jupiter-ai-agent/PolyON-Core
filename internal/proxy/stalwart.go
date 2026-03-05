// Package proxy provides reverse proxy handlers for Stalwart and Elasticsearch.
package proxy

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/config"
)

func RegisterStalwart(r chi.Router, cfg *config.Config) {
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		path := chi.URLParam(r, "*")
		targetURL := cfg.StalwartURL + "/api/" + path

		proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Copy query params
		proxyReq.URL.RawQuery = r.URL.RawQuery

		// Set auth — Stalwart admin user from config (default: "stalwart")
		user := cfg.StalwartAdminUser
		pw := cfg.StalwartAdminPassword
		proxyReq.SetBasicAuth(user, pw)

		ct := r.Header.Get("Content-Type")
		if ct != "" {
			proxyReq.Header.Set("Content-Type", ct)
		} else {
			proxyReq.Header.Set("Content-Type", "application/json")
		}

		client := &http.Client{}
		resp, err := client.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
}
