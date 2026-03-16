package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

const (
	defaultEmbedURL  = "http://polyon-embed:4001"
	defaultSearchURL = "http://polyon-search:9200"
	knnBoost         = 0.7
	bm25Boost        = 0.3
)

// SearchDocument represents a document to index
type SearchDocument struct {
	Module       string         `json:"module"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Title        string         `json:"title"`
	Content      string         `json:"content"`
	Owner        string         `json:"owner"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// SearchResult represents a search result
type SearchResult struct {
	Module       string         `json:"module"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Title        string         `json:"title"`
	Score        float64        `json:"score"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// RegisterSearch registers the /search routes under the given router.
func RegisterSearch(r chi.Router, d *Deps) {
	r.Route("/search", searchRoutes(d))
}

func searchRoutes(d *Deps) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/index", indexDocument(d))
		r.Get("/query", searchQuery(d))
		r.Delete("/index/{id}", deleteDocument(d))
		r.Get("/status", searchStatus(d))
	}
}

// embedURL returns EMBED_URL env or default
func embedURL(d *Deps) string {
	if u := d.Cfg.EmbedURL; u != "" {
		return u
	}
	return defaultEmbedURL
}

// searchURL returns SEARCH_URL env or default
func searchURL(d *Deps) string {
	if u := d.Cfg.SearchURL; u != "" {
		return u
	}
	return defaultSearchURL
}

// getVector calls polyon-embed to get text vector
func getVector(embedBase, text, textType string) ([]float64, error) {
	body, _ := json.Marshal(map[string]string{"text": text, "type": textType})
	resp, err := http.Post(embedBase+"/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Vector []float64 `json:"vector"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Vector, nil
}

// indexDocument indexes a document asynchronously
func indexDocument(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var doc SearchDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Fire and forget — async indexing
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			indexName := "polyon_" + doc.Module

			// Get vector from embed service
			vector, err := getVector(embedURL(d), doc.Content, "passage")
			if err != nil {
				log.Warn().Err(err).Str("module", doc.Module).Msg("embed failed, indexing without vector")
			}

			// Build document
			osDoc := map[string]any{
				"module":        doc.Module,
				"resource_type": doc.ResourceType,
				"resource_id":   doc.ResourceID,
				"title":         doc.Title,
				"content":       doc.Content,
				"owner":         doc.Owner,
				"metadata":      doc.Metadata,
				"indexed_at":    time.Now().UTC().Format(time.RFC3339),
			}
			if len(vector) > 0 {
				osDoc["content_vector"] = vector
			}

			docID := fmt.Sprintf("%s_%s", doc.Module, doc.ResourceID)
			body, _ := json.Marshal(osDoc)
			url := fmt.Sprintf("%s/%s/_doc/%s", searchURL(d), indexName, docID)

			req, _ := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Error().Err(err).Str("index", indexName).Msg("opensearch index failed")
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			log.Debug().Str("index", indexName).Str("id", docID).Msg("document indexed")
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"queued": true})
	}
}

// searchQuery performs hybrid search
func searchQuery(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q parameter required", http.StatusBadRequest)
			return
		}

		modulesParam := r.URL.Query().Get("modules")
		var modules []string
		if modulesParam != "" {
			modules = strings.Split(modulesParam, ",")
		}

		size := 10

		// Get query vector
		vector, err := getVector(embedURL(d), q, "query")
		if err != nil {
			log.Warn().Err(err).Msg("embed failed for query, falling back to BM25 only")
		}

		// Build hybrid query
		var queryBody map[string]any
		if len(vector) > 0 {
			queryBody = map[string]any{
				"size": size,
				"query": map[string]any{
					"bool": map[string]any{
						"should": []any{
							map[string]any{
								"multi_match": map[string]any{
									"query":  q,
									"fields": []string{"title^2", "content"},
									"boost":  bm25Boost,
								},
							},
							map[string]any{
								"knn": map[string]any{
									"content_vector": map[string]any{
										"vector": vector,
										"k":      size * 2,
										"boost":  knnBoost,
									},
								},
							},
						},
					},
				},
			}
		} else {
			queryBody = map[string]any{
				"size": size,
				"query": map[string]any{
					"multi_match": map[string]any{
						"query":  q,
						"fields": []string{"title^2", "content"},
					},
				},
			}
		}

		// Determine indices
		var indices []string
		if len(modules) > 0 {
			for _, m := range modules {
				indices = append(indices, "polyon_"+strings.TrimSpace(m))
			}
		} else {
			indices = []string{"polyon_*"}
		}

		indexStr := strings.Join(indices, ",")
		url := fmt.Sprintf("%s/%s/_search", searchURL(d), indexStr)

		body, _ := json.Marshal(queryBody)
		req, err := http.NewRequestWithContext(r.Context(), "POST", url, bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "search failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var osResp struct {
			Hits struct {
				Hits []struct {
					Score  float64        `json:"_score"`
					Source map[string]any `json:"_source"`
				} `json:"hits"`
				Total struct{ Value int } `json:"total"`
			} `json:"hits"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&osResp); err != nil {
			http.Error(w, "decode failed", http.StatusInternalServerError)
			return
		}

		var results []SearchResult
		for _, h := range osResp.Hits.Hits {
			sr := SearchResult{
				Score: h.Score,
			}
			if v, ok := h.Source["module"].(string); ok {
				sr.Module = v
			}
			if v, ok := h.Source["resource_type"].(string); ok {
				sr.ResourceType = v
			}
			if v, ok := h.Source["resource_id"].(string); ok {
				sr.ResourceID = v
			}
			if v, ok := h.Source["title"].(string); ok {
				sr.Title = v
			}
			if v, ok := h.Source["metadata"].(map[string]any); ok {
				sr.Metadata = v
			}
			results = append(results, sr)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": results,
			"total":   osResp.Hits.Total.Value,
			"query":   q,
		})
	}
}

// deleteDocument removes a document from index
func deleteDocument(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		module := r.URL.Query().Get("module")
		if module == "" {
			http.Error(w, "module parameter required", http.StatusBadRequest)
			return
		}

		url := fmt.Sprintf("%s/polyon_%s/_doc/%s", searchURL(d), module, id)
		req, _ := http.NewRequestWithContext(r.Context(), "DELETE", url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	}
}

// searchStatus returns Search Stack status
func searchStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Check embed service
		embedStatus := "down"
		embedLoaded := false
		if resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(embedURL(d) + "/health"); err == nil {
			defer resp.Body.Close()
			var h struct {
				Status      string `json:"status"`
				ModelLoaded bool   `json:"model_loaded"`
			}
			if json.NewDecoder(resp.Body).Decode(&h) == nil {
				embedStatus = h.Status
				embedLoaded = h.ModelLoaded
			}
		}

		// Check OpenSearch
		osStatus := "down"
		req, _ := http.NewRequestWithContext(ctx, "GET", searchURL(d)+"/_cluster/health", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			var h struct {
				Status string `json:"status"`
			}
			if json.NewDecoder(resp.Body).Decode(&h) == nil {
				osStatus = h.Status
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"embed": map[string]any{
				"status":       embedStatus,
				"model_loaded": embedLoaded,
				"url":          embedURL(d),
			},
			"opensearch": map[string]any{
				"status": osStatus,
				"url":    searchURL(d),
			},
			"stack_ready": embedStatus == "ok" && embedLoaded && osStatus != "down",
		})
	}
}

// InitFoundationIndices creates kNN indices for foundation modules
func InitFoundationIndices(searchBase string) {
	indices := []string{"polyon_mail", "polyon_drive", "polyon_chat"}
	for _, idx := range indices {
		createKNNIndex(searchBase, idx)
	}
}

func createKNNIndex(searchBase, indexName string) {
	body := map[string]any{
		"settings": map[string]any{
			"knn":                  true,
			"number_of_shards":     1,
			"number_of_replicas":   0,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"module":        map[string]any{"type": "keyword"},
				"resource_type": map[string]any{"type": "keyword"},
				"resource_id":   map[string]any{"type": "keyword"},
				"title":         map[string]any{"type": "text", "analyzer": "standard"},
				"content":       map[string]any{"type": "text", "analyzer": "standard"},
				"owner":         map[string]any{"type": "keyword"},
				"indexed_at":    map[string]any{"type": "date"},
				"content_vector": map[string]any{
					"type":      "knn_vector",
					"dimension": 768,
					"method": map[string]any{
						"name":       "hnsw",
						"space_type": "cosinesimil",
						"engine":     "faiss",
						"parameters": map[string]any{"m": 16, "ef_construction": 100},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	url := searchBase + "/" + indexName
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn().Err(err).Str("index", indexName).Msg("kNN index create failed")
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	log.Info().Str("index", indexName).Msg("kNN index ensured")
}
