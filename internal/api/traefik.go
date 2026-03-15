package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterTraefik registers Traefik management routes.
func RegisterTraefik(r chi.Router, d *Deps) {
	r.Route("/traefik", func(r chi.Router) {
		r.Get("/overview", traefikOverview(d))
		r.Get("/routers", traefikRouters(d))
		r.Get("/services", traefikServices(d))
		r.Get("/middlewares", traefikMiddlewares(d))
		r.Get("/entrypoints", traefikEntryPoints(d))
	})
}

var traefikClient = &http.Client{Timeout: 5 * time.Second}

// traefikAPIURL returns Traefik API base URL from config or default.
func traefikAPIURL(d *Deps) string {
	if url := d.Cfg.TraefikAPIURL; url != "" {
		return strings.TrimRight(url, "/")
	}
	return "http://traefik.kube-system.svc:8080"
}

func traefikGet(d *Deps, path string) ([]byte, error) {
	url := traefikAPIURL(d) + path
	resp, err := traefikClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("traefik API 연결 실패: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("Traefik API 인증 필요 (api.insecure=true 설정 확인)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Traefik API %s: HTTP %d", path, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// GET /api/v1/traefik/overview
func traefikOverview(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := traefikGet(d, "/api/overview")
		if err != nil {
			httputil.RespondError(w, 502, "TRAEFIK_ERROR", err.Error())
			return
		}
		var data interface{}
		json.Unmarshal(body, &data)
		httputil.RespondOK(w, map[string]interface{}{
			"overview": data,
			"api_url":  traefikAPIURL(d),
		})
	}
}

// GET /api/v1/traefik/routers?protocol=http|tcp
func traefikRouters(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		protocol := r.URL.Query().Get("protocol")
		if protocol == "" {
			protocol = "http"
		}

		path := "/api/" + protocol + "/routers"
		body, err := traefikGet(d, path)
		if err != nil {
			httputil.RespondError(w, 502, "TRAEFIK_ERROR", err.Error())
			return
		}

		var routers []map[string]interface{}
		json.Unmarshal(body, &routers)

		// @internal 라우터 제외 (dashboard/api/ping/prometheus)
		var filtered []map[string]interface{}
		for _, rt := range routers {
			name, _ := rt["name"].(string)
			if !strings.HasSuffix(name, "@internal") {
				filtered = append(filtered, rt)
			}
		}
		if filtered == nil {
			filtered = []map[string]interface{}{}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"routers":  filtered,
			"total":    len(filtered),
			"protocol": protocol,
		})
	}
}

// GET /api/v1/traefik/services?protocol=http|tcp
func traefikServices(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		protocol := r.URL.Query().Get("protocol")
		if protocol == "" {
			protocol = "http"
		}

		path := "/api/" + protocol + "/services"
		body, err := traefikGet(d, path)
		if err != nil {
			httputil.RespondError(w, 502, "TRAEFIK_ERROR", err.Error())
			return
		}

		var services []map[string]interface{}
		json.Unmarshal(body, &services)

		// @internal 서비스 제외
		var filtered []map[string]interface{}
		for _, svc := range services {
			name, _ := svc["name"].(string)
			if !strings.HasSuffix(name, "@internal") {
				filtered = append(filtered, svc)
			}
		}
		if filtered == nil {
			filtered = []map[string]interface{}{}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"services": filtered,
			"total":    len(filtered),
			"protocol": protocol,
		})
	}
}

// GET /api/v1/traefik/middlewares
func traefikMiddlewares(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := traefikGet(d, "/api/http/middlewares")
		if err != nil {
			httputil.RespondError(w, 502, "TRAEFIK_ERROR", err.Error())
			return
		}
		var middlewares []map[string]interface{}
		json.Unmarshal(body, &middlewares)
		if middlewares == nil {
			middlewares = []map[string]interface{}{}
		}
		httputil.RespondOK(w, map[string]interface{}{
			"middlewares": middlewares,
			"total":       len(middlewares),
		})
	}
}

// GET /api/v1/traefik/entrypoints
func traefikEntryPoints(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := traefikGet(d, "/api/entrypoints")
		if err != nil {
			httputil.RespondError(w, 502, "TRAEFIK_ERROR", err.Error())
			return
		}
		var entrypoints []map[string]interface{}
		json.Unmarshal(body, &entrypoints)
		if entrypoints == nil {
			entrypoints = []map[string]interface{}{}
		}
		httputil.RespondOK(w, map[string]interface{}{
			"entrypoints": entrypoints,
			"total":       len(entrypoints),
		})
	}
}
