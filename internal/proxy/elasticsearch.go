package proxy

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/config"
)

func RegisterElasticsearch(r chi.Router, cfg *config.Config) {
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		path := chi.URLParam(r, "*")
		targetURL := cfg.ElasticURL + "/" + path

		proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		proxyReq.URL.RawQuery = r.URL.RawQuery
		proxyReq.SetBasicAuth("elastic", cfg.ElasticPassword)

		if ct := r.Header.Get("Content-Type"); ct != "" {
			proxyReq.Header.Set("Content-Type", ct)
		}

		client := &http.Client{}
		resp, err := client.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()

		// CORS for Elasticvue
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,HEAD,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-Requested-With")
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
}
