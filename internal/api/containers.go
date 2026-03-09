package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterContainers(r chi.Router, d *Deps) {
	r.Route("/containers", func(r chi.Router) {
		r.Get("/", listContainers(d))
		r.Post("/{name}/restart", restartContainer(d))
		r.Get("/{name}/logs", containerLogs(d))
		r.Get("/volumes", listVolumes(d))
		r.Get("/topology", containerTopology(d))
	})
}

func listContainers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		containers, err := d.Docker.ContainerList(context.Background())
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"success": false, "error": err.Error(), "containers": []interface{}{}})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "containers": containers, "total": len(containers)})
	}
}

func restartContainer(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if !strings.HasPrefix(name, "polyon-") {
			httputil.RespondError(w, 400, "INVALID", "Only polyon-* containers can be managed")
			return
		}
		err := d.Docker.ContainerRestart(context.Background(), name)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true})
	}
}

func containerLogs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if !strings.HasPrefix(name, "polyon-") {
			httputil.RespondError(w, 400, "INVALID", "Only polyon-* containers can be managed")
			return
		}
		tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		if tail <= 0 {
			tail = 50
		}
		logs, err := d.Docker.ContainerLogs(context.Background(), name, tail)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "logs": logs})
	}
}

func listVolumes(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		vols, err := d.Docker.SystemDfVolumes(context.Background())
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"volumes": []interface{}{}, "error": err.Error()})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"volumes": vols})
	}
}

func containerTopology(d *Deps) http.HandlerFunc {
	type topoNode struct {
		ID     string `json:"id"`
		Label  string `json:"label"`
		Group  string `json:"group"`
		Status string `json:"status"`
	}
	type topoLink struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Type   string `json:"type"`
	}

	topology := map[string]struct {
		Group string
		Label string
		Deps  []string
	}{
		"polyon-traefik":        {"core", "Traefik", nil},
		"polyon-auth":           {"core", "Keycloak", nil},
		"polyon-core":           {"core", "PolyON Core", []string{"polyon-auth"}},
		"polyon-console":             {"foundation", "PolyON Console", []string{"polyon-core", "polyon-traefik"}},
		"polyon-db":             {"data", "PostgreSQL", nil},
		"polyon-redis":          {"data", "Redis", nil},
		"polyon-search":             {"data", "Elasticsearch", nil},
		"polyon-rustfs":         {"data", "RustFS", nil},
		"polyon-dc":             {"service", "PolyON AD DC", nil},
		"polyon-mail":           {"service", "Stalwart Mail", []string{"polyon-db", "polyon-dc", "polyon-search", "polyon-rustfs"}},
		"polyon-prometheus":     {"monitor", "Prometheus", []string{"polyon-db", "polyon-redis", "polyon-rustfs", "polyon-search"}},
		"polyon-grafana":        {"monitor", "Grafana", []string{"polyon-prometheus"}},
		"polyon-pg-exporter":    {"monitor", "PG Exporter", []string{"polyon-db"}},
		"polyon-redis-exporter": {"monitor", "Redis Exporter", []string{"polyon-redis"}},
		"polyon-search-exporter":    {"monitor", "ES Exporter", []string{"polyon-search"}},
		"polyon-pgadmin":        {"admin", "pgAdmin", []string{"polyon-db"}},
		"polyon-redisinsight":   {"admin", "RedisInsight", []string{"polyon-redis"}},
		"polyon-elasticvue":     {"admin", "Elasticvue", []string{"polyon-search"}},
		"polyon-sentinel":       {"monitor", "Sentinel", []string{"polyon-db", "polyon-redis", "polyon-search"}},
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		statusMap := d.Docker.ContainerStatusMap(context.Background())
		var nodes []topoNode
		var links []topoLink
		for id, info := range topology {
			status := "unknown"
			if raw, ok := statusMap[id]; ok {
				if strings.Contains(raw, "healthy") {
					status = "healthy"
				} else if strings.Contains(raw, "Up") {
					status = "running"
				} else {
					status = "stopped"
				}
			}
			nodes = append(nodes, topoNode{id, info.Label, info.Group, status})
			for _, dep := range info.Deps {
				links = append(links, topoLink{id, dep, "depends_on"})
			}
		}
		httputil.RespondOK(w, map[string]interface{}{"nodes": nodes, "links": links})
	}
}
