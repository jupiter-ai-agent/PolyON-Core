package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/triangles/polyon-core/internal/httputil"
)

// stripHTTP removes the http:// or https:// scheme prefix from a URL.
// Returns "host:port" suitable for TCP dial.
func stripHTTP(u string) string {
	if after, ok := strings.CutPrefix(u, "http://"); ok {
		return after
	}
	if after, ok := strings.CutPrefix(u, "https://"); ok {
		return after
	}
	return u
}

func RegisterDatabases(r chi.Router, d *Deps) {
	r.Route("/databases", func(r chi.Router) {
		r.Get("/status", dbStatus(d))
		r.Get("/rustfs/stats", rustfsStats(d))
	})
}

func dbStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// PostgreSQL
		pg := map[string]interface{}{"status": "down"}
		if d.Store != nil && d.Store.Pool() != nil {
			if err := d.Store.Pool().Ping(ctx); err == nil {
				pg["status"] = "up"
			}
		}

		// Redis
		redis := map[string]interface{}{"status": "down"}
		if tcpCheck("polyon-redis:6379", 2*time.Second) {
			redis["status"] = "up"
		}

		// Elasticsearch
		es := map[string]interface{}{"status": "down"}
		if esStatus := httpCheck(d.Cfg.ElasticURL+"/_cluster/health", 2*time.Second); esStatus != "" {
			es["status"] = "up"
			es["cluster"] = esStatus
			// Fetch docs/store stats
			esClient := &http.Client{Timeout: 3 * time.Second}
			if resp, err := esClient.Get(d.Cfg.ElasticURL + "/_stats"); err == nil {
				defer resp.Body.Close()
				var esData struct {
					All struct {
						Total struct {
							Docs  struct{ Count int64 `json:"count"` } `json:"docs"`
							Store struct{ SizeInBytes int64 `json:"size_in_bytes"` } `json:"store"`
						} `json:"total"`
					} `json:"_all"`
				}
				if json.NewDecoder(resp.Body).Decode(&esData) == nil {
					es["docs_count"] = esData.All.Total.Docs.Count
					sb := esData.All.Total.Store.SizeInBytes
					if sb > 1024*1024*1024 {
						es["store_size"] = fmt.Sprintf("%.2f GB", float64(sb)/(1024*1024*1024))
					} else if sb > 1024*1024 {
						es["store_size"] = fmt.Sprintf("%.2f MB", float64(sb)/(1024*1024))
					} else if sb > 1024 {
						es["store_size"] = fmt.Sprintf("%.2f KB", float64(sb)/1024)
					} else {
						es["store_size"] = fmt.Sprintf("%d B", sb)
					}
				}
			}
		}

		// RustFS
		rustfs := map[string]interface{}{"status": "down"}
		if httpCode(d.Cfg.RustFSEndpoint+"/minio/health/live", 2*time.Second) == 200 {
			rustfs["status"] = "up"
		} else if tcpCheck(stripHTTP(d.Cfg.RustFSEndpoint), 2*time.Second) {
			rustfs["status"] = "up"
		}

		// Stalwart — use StalwartURL from config (DB-backed: stalwart-admin host:port)
		stalwart := map[string]interface{}{"status": "down"}
		stalwartBase := d.Cfg.StalwartURL // e.g. http://polyon-mail:8080
		if httpCode(stalwartBase+"/healthz", 2*time.Second) == 200 {
			stalwart["status"] = "up"
		} else if tcpCheck(stripHTTP(stalwartBase), 2*time.Second) {
			stalwart["status"] = "up"
		}

		httputil.RespondOK(w, map[string]interface{}{
			"postgresql":    pg,
			"redis":         redis,
			"elasticsearch": es,
			"rustfs":        rustfs,
			"stalwart":      stalwart,
		})
	}
}

func rustfsStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint := d.Cfg.RustFSEndpoint
		// strip http:// prefix for minio-go
		ep := endpoint
		if len(ep) > 7 && ep[:7] == "http://" {
			ep = ep[7:]
		}

		client, err := minio.New(ep, &minio.Options{
			Creds:  credentials.NewStaticV4(d.Cfg.RustFSAccessKey, d.Cfg.RustFSSecretKey, ""),
			Secure: false,
		})
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "error", "error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// List buckets
		buckets, err := client.ListBuckets(ctx)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{"status": "error", "error": err.Error()})
			return
		}

		type bucketInfo struct {
			Name         string    `json:"name"`
			CreatedAt    time.Time `json:"createdAt"`
			TotalObjects int64     `json:"totalObjects"`
			TotalSize    int64     `json:"totalSize"`
		}

		var totalObjects int64
		var totalSize int64
		bucketList := make([]bucketInfo, 0, len(buckets))

		for _, b := range buckets {
			var bObjects int64
			var bSize int64
			for obj := range client.ListObjects(ctx, b.Name, minio.ListObjectsOptions{Recursive: true}) {
				if obj.Err != nil {
					break
				}
				bObjects++
				bSize += obj.Size
			}
			totalObjects += bObjects
			totalSize += bSize
			bucketList = append(bucketList, bucketInfo{
				Name:         b.Name,
				CreatedAt:    b.CreationDate,
				TotalObjects: bObjects,
				TotalSize:    bSize,
			})
		}

		// Build bucket array matching UI field names
		uiBuckets := make([]map[string]interface{}, 0, len(bucketList))
		for _, b := range bucketList {
			uiBuckets = append(uiBuckets, map[string]interface{}{
				"name":       b.Name,
				"createdAt":  b.CreatedAt,
				"objects":    b.TotalObjects,
				"size_bytes": b.TotalSize,
			})
		}

		httputil.RespondOK(w, map[string]interface{}{
			"status":           "ok",
			"total_buckets":    len(buckets),
			"total_objects":    totalObjects,
			"total_size_bytes": totalSize,
			"buckets":          uiBuckets,
		})
	}
}

func tcpCheck(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func httpCheck(url string, timeout time.Duration) string {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "connected"
	}
	return ""
}

func httpCode(url string, timeout time.Duration) int {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
