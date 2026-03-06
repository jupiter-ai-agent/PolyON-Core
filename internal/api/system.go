package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterSystem(r chi.Router, d *Deps) {
	r.Route("/system", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			httputil.RespondOK(w, map[string]string{"status": "ok", "timestamp": time.Now().UTC().Format(time.RFC3339)})
		})
		r.Get("/resources", systemResources(d))
		r.Get("/audit", auditLog(d))
		r.Post("/backup", createBackup(d))
		r.Get("/backups", listBackups(d))
		r.Get("/prometheus/alerts", prometheusAlerts(d))
		r.Get("/prometheus/rules", prometheusRules(d))
		r.Get("/prometheus/query", prometheusQuery(d))
		r.Get("/prometheus/query_range", prometheusQueryRange(d))
	})
}

func systemResources(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		result := map[string]interface{}{}

		// CPU
		if loadavg, err := os.ReadFile("/proc/loadavg"); err == nil {
			var l1 float64
			fmt.Sscanf(string(loadavg), "%f", &l1)
			result["load_avg"] = fmt.Sprintf("%.2f", l1)
			cpus := 0
			if n, err := os.ReadDir("/sys/devices/system/cpu"); err == nil {
				for _, e := range n {
					if len(e.Name()) > 3 && e.Name()[:3] == "cpu" {
						if _, err := strconv.Atoi(e.Name()[3:]); err == nil {
							cpus++
						}
					}
				}
			}
			if cpus == 0 {
				cpus = 1
			}
			result["cpu_percent"] = fmt.Sprintf("%.1f", (l1/float64(cpus))*100)
		}

		// Memory
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			mem := parseMeminfo(string(data))
			total := mem["MemTotal"] / 1024
			avail := mem["MemAvailable"] / 1024
			if avail == 0 {
				avail = (mem["MemFree"] + mem["Buffers"] + mem["Cached"]) / 1024
			}
			result["memory_total_mb"] = total
			result["memory_used_mb"] = total - avail
		}

		// Disk
		var stat syscall.Statfs_t
		if err := syscall.Statfs("/", &stat); err == nil {
			diskTotal := stat.Blocks * uint64(stat.Bsize) / (1024 * 1024 * 1024)
			diskFree := stat.Bavail * uint64(stat.Bsize) / (1024 * 1024 * 1024)
			result["disk_total_gb"] = diskTotal
			result["disk_used_gb"] = diskTotal - diskFree
		}

		// Uptime
		if data, err := os.ReadFile("/proc/uptime"); err == nil {
			var secs float64
			fmt.Sscanf(string(data), "%f", &secs)
			days := int(secs) / 86400
			hours := (int(secs) % 86400) / 3600
			mins := (int(secs) % 3600) / 60
			if days > 0 {
				result["uptime"] = fmt.Sprintf("%dd %dh %dm", days, hours, mins)
			} else {
				result["uptime"] = fmt.Sprintf("%dh %dm", hours, mins)
			}
		}

		// Containers (K8s mode: 0 when Docker not available)
		if d.Docker != nil {
			containers, err := d.Docker.ContainerList(context.Background())
			if err == nil {
				result["container_count"] = len(containers)
			} else {
				result["container_count"] = 0
			}
		} else {
			// K8s environment: no Docker client available
			result["container_count"] = 0
		}

		// Elasticsearch stats
		esClient := &http.Client{Timeout: 5 * time.Second}
		if resp, err := esClient.Get(d.Cfg.ElasticURL + "/_stats"); err == nil {
			defer resp.Body.Close()
			var esData struct {
				All struct {
					Total struct {
						Docs struct {
							Count int64 `json:"count"`
						} `json:"docs"`
						Store struct {
							SizeInBytes int64 `json:"size_in_bytes"`
						} `json:"store"`
					} `json:"total"`
				} `json:"_all"`
			}
			if json.NewDecoder(resp.Body).Decode(&esData) == nil {
				result["es_docs"] = esData.All.Total.Docs.Count
				sizeBytes := esData.All.Total.Store.SizeInBytes
				if sizeBytes > 1024*1024*1024 {
					result["es_size"] = fmt.Sprintf("%.2f GB", float64(sizeBytes)/(1024*1024*1024))
				} else if sizeBytes > 1024*1024 {
					result["es_size"] = fmt.Sprintf("%.2f MB", float64(sizeBytes)/(1024*1024))
				} else if sizeBytes > 1024 {
					result["es_size"] = fmt.Sprintf("%.2f KB", float64(sizeBytes)/1024)
				} else {
					result["es_size"] = fmt.Sprintf("%d B", sizeBytes)
				}
			}
		}

		httputil.RespondOK(w, result)
	}
}

func parseMeminfo(data string) map[string]int {
	m := map[string]int{}
	for _, line := range splitLines(data) {
		if len(line) < 5 {
			continue
		}
		var key string
		var val int
		fmt.Sscanf(line, "%s %d", &key, &val)
		key = key[:len(key)-1] // remove ':'
		m[key] = val
	}
	return m
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func auditLog(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		objectType := r.URL.Query().Get("object_type")
		action := r.URL.Query().Get("action")

		logs, total, err := d.Store.GetAuditLogs(limit, offset, objectType, action)
		if err != nil {
			httputil.RespondError(w, 500, "DB_ERROR", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"logs": logs, "total": total})
	}
}

func createBackup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if d.Docker == nil {
			httputil.RespondError(w, 500, "BACKUP_UNAVAILABLE", "Docker client not available in K8s environment")
			return
		}

		result, err := d.Docker.ExecSambaTool(d.Cfg.DCContainer,
			"domain", "backup", "online", "--targetdir=/var/lib/samba/backup")
		if err != nil {
			httputil.RespondError(w, 500, "BACKUP_FAILED", err.Error())
			return
		}
		d.Store.LogAction("BACKUP", "system", "domain", "Online backup created", "system", "")
		httputil.RespondOK(w, map[string]interface{}{"success": true, "output": result})
	}
}

func listBackups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if d.Docker == nil {
			httputil.RespondOK(w, map[string]interface{}{"success": false, "error": "Docker client not available in K8s environment", "backups": []string{}})
			return
		}

		output, err := d.Docker.ExecSambaTool(d.Cfg.DCContainer, "ls", "-la", "/var/lib/samba/backup/")
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"success": false, "error": err.Error(), "backups": []string{}})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "backups": splitLines(output)})
	}
}

func prometheusQuery(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		t := r.URL.Query().Get("time")
		promURL := d.Cfg.PrometheusURL + "/prometheus/api/v1/query?query=" + url.QueryEscape(q)
		if t != "" {
			promURL += "&time=" + url.QueryEscape(t)
		}
		resp, err := http.Get(promURL)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "error", "error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 32*1024)
		for {
			n, _ := resp.Body.Read(buf)
			if n == 0 {
				break
			}
			w.Write(buf[:n])
		}
	}
}

func prometheusQueryRange(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		start := r.URL.Query().Get("start")
		end := r.URL.Query().Get("end")
		step := r.URL.Query().Get("step")
		promURL := d.Cfg.PrometheusURL + "/prometheus/api/v1/query_range?query=" + url.QueryEscape(q)
		if start != "" {
			promURL += "&start=" + url.QueryEscape(start)
		}
		if end != "" {
			promURL += "&end=" + url.QueryEscape(end)
		}
		if step != "" {
			promURL += "&step=" + url.QueryEscape(step)
		}
		resp, err := http.Get(promURL)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "error", "error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 32*1024)
		for {
			n, _ := resp.Body.Read(buf)
			if n == 0 {
				break
			}
			w.Write(buf[:n])
		}
	}
}

func prometheusAlerts(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp, err := http.Get(d.Cfg.PrometheusURL + "/prometheus/api/v1/alerts")
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "error", "error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 32*1024)
		for {
			n, _ := resp.Body.Read(buf)
			if n == 0 {
				break
			}
			w.Write(buf[:n])
		}
	}
}

func prometheusRules(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp, err := http.Get(d.Cfg.PrometheusURL + "/prometheus/api/v1/rules")
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "error", "error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 32*1024)
		for {
			n, _ := resp.Body.Read(buf)
			if n == 0 {
				break
			}
			w.Write(buf[:n])
		}
	}
}
