package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineStorage registers AppEngine storage overview routes.
func RegisterAppEngineStorage(r chi.Router, d *Deps) {
	r.Route("/appengine/storage", func(r chi.Router) {
		r.Get("/overview", getStorageOverview(d))
	})
}

// ── Response Types ─────────────────────────────────────────────────────────

type storageOverviewResponse struct {
	PRC         prcInfo         `json:"prc"`
	Attachments attachmentsInfo `json:"attachments"`
	S3          s3Info          `json:"s3"`
}

type prcInfo struct {
	Claims []prcClaim `json:"claims"`
}

type prcClaim struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Details string `json:"details"`
}

type attachmentsInfo struct {
	TotalCount int64            `json:"total_count"`
	TotalBytes int64            `json:"total_bytes"`
	ByStorage  []storageGroup   `json:"by_storage"`
	ByModel    []modelGroup     `json:"by_model"`
}

type storageGroup struct {
	Type  string `json:"type"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
}

type modelGroup struct {
	Model string `json:"model"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
}

type s3Info struct {
	Endpoint    string `json:"endpoint"`
	Bucket      string `json:"bucket"`
	ObjectCount int64  `json:"object_count"`
	TotalBytes  int64  `json:"total_bytes"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

// ── Handler ────────────────────────────────────────────────────────────────

func getStorageOverview(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		resp := storageOverviewResponse{}

		// 1. PRC Claims (module_id = 'erpengine')
		resp.PRC = fetchPRCClaims(ctx, d)

		// 2. Attachment stats via Odoo RPC
		resp.Attachments = fetchAttachmentStats(ctx, d)

		// 3. S3 / RustFS bucket info
		resp.S3 = fetchS3Info(ctx, d)

		httputil.RespondOK(w, resp)
	}
}

// ── PRC Claims ─────────────────────────────────────────────────────────────

func fetchPRCClaims(ctx context.Context, d *Deps) prcInfo {
	info := prcInfo{Claims: []prcClaim{}}

	if d.PRC == nil {
		return info
	}

	claims, err := d.PRC.ListClaims(ctx, "", "", "erpengine")
	if err != nil {
		log.Warn().Err(err).Msg("appengine storage: failed to list prc claims")
		return info
	}

	for _, c := range claims {
		details := buildClaimDetails(c.ClaimType, c.Config)
		info.Claims = append(info.Claims, prcClaim{
			Type:    c.ClaimType,
			Status:  c.Status,
			Details: details,
		})
	}
	return info
}

// buildClaimDetails parses claim config JSON and returns a human-readable detail string.
func buildClaimDetails(claimType string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}

	str := func(key string) string {
		if v, ok := cfg[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	switch claimType {
	case "database":
		dbname := str("dbname")
		if dbname == "" {
			dbname = str("database")
		}
		host := str("host")
		if host == "" {
			host = str("hostname")
		}
		if dbname != "" && host != "" {
			return dbname + " @ " + host
		}
		if dbname != "" {
			return dbname
		}
	case "objectStorage":
		bucket := str("bucket")
		endpoint := str("endpoint")
		if bucket != "" && endpoint != "" {
			return bucket + " @ " + endpoint
		}
		if bucket != "" {
			return bucket
		}
	case "smtp":
		user := str("username")
		if user == "" {
			user = str("user")
		}
		host := str("host")
		port := str("port")
		if user != "" && host != "" {
			if port != "" {
				return user + "@ @ " + host + ":" + port
			}
			return user + " @ " + host
		}
	case "auth":
		clientID := str("clientId")
		if clientID == "" {
			clientID = str("client_id")
		}
		realm := str("realm")
		if clientID != "" && realm != "" {
			return clientID + " (" + realm + " realm)"
		}
		if clientID != "" {
			return clientID
		}
	}
	return ""
}

// ── Attachments ────────────────────────────────────────────────────────────

func fetchAttachmentStats(ctx context.Context, d *Deps) attachmentsInfo {
	info := attachmentsInfo{
		ByStorage: []storageGroup{
			{Type: "S3(RustFS)", Count: 0, Bytes: 0},
			{Type: "Local filestore", Count: 0, Bytes: 0},
			{Type: "DB 직접저장", Count: 0, Bytes: 0},
		},
		ByModel: []modelGroup{},
	}

	if d.OdooClient == nil {
		log.Warn().Msg("appengine storage: OdooClient is nil, skipping attachment stats")
		return info
	}

	// SearchRead with a reasonable limit; Odoo has no GROUP BY in RPC.
	// We fetch all records and aggregate client-side.
	// Limit to 5000 to avoid timeout on very large instances.
	records, err := d.OdooClient.SearchRead(
		"ir.attachment",
		[]interface{}{},
		[]string{"id", "store_fname", "file_size", "res_model"},
		0, 5000,
	)
	if err != nil {
		log.Warn().Err(err).Msg("appengine storage: failed to fetch ir.attachment")
		return info
	}

	// Aggregate
	storageMap := map[string]*storageGroup{}
	for i := range info.ByStorage {
		storageMap[info.ByStorage[i].Type] = &info.ByStorage[i]
	}

	modelMap := map[string]*modelGroup{}

	for _, rec := range records {
		// store_fname may be false (bool) when stored in DB
		var storeFname string
		switch v := rec["store_fname"].(type) {
		case string:
			storeFname = v
		case bool:
			storeFname = ""
		}

		var fileSize int64
		switch v := rec["file_size"].(type) {
		case float64:
			fileSize = int64(v)
		}

		var resModel string
		switch v := rec["res_model"].(type) {
		case string:
			resModel = v
		}

		// Classify storage type
		var storageType string
		if strings.HasPrefix(storeFname, "s3://") {
			storageType = "S3(RustFS)"
		} else if storeFname == "" {
			storageType = "DB 직접저장"
		} else {
			storageType = "Local filestore"
		}

		if sg, ok := storageMap[storageType]; ok {
			sg.Count++
			sg.Bytes += fileSize
		}

		info.TotalCount++
		info.TotalBytes += fileSize

		// Aggregate by model
		if resModel != "" {
			if mg, ok := modelMap[resModel]; ok {
				mg.Count++
				mg.Bytes += fileSize
			} else {
				mg := &modelGroup{Model: resModel, Count: 1, Bytes: fileSize}
				modelMap[resModel] = mg
			}
		}
	}

	// Convert model map to slice (sorted by count desc)
	for _, mg := range modelMap {
		info.ByModel = append(info.ByModel, *mg)
	}

	return info
}

// ── S3 / RustFS ────────────────────────────────────────────────────────────

func fetchS3Info(ctx context.Context, d *Deps) s3Info {
	endpoint := d.Cfg.RustFSEndpoint
	if endpoint == "" {
		endpoint = "http://polyon-rustfs:9000"
	}

	// Find the erpengine bucket from PRC claims
	bucket := "erpengine"
	if d.PRC != nil {
		claims, err := d.PRC.ListClaims(ctx, "", "objectStorage", "erpengine")
		if err == nil && len(claims) > 0 {
			var cfg map[string]interface{}
			if err := json.Unmarshal(claims[0].Config, &cfg); err == nil {
				if b, ok := cfg["bucket"].(string); ok && b != "" {
					bucket = b
				}
			}
		}
	}

	info := s3Info{
		Endpoint: endpoint,
		Bucket:   bucket,
	}

	// Build minio client
	ep := strings.TrimPrefix(endpoint, "https://")
	ep = strings.TrimPrefix(ep, "http://")
	secure := strings.HasPrefix(endpoint, "https://")

	client, err := minio.New(ep, &minio.Options{
		Creds:  credentials.NewStaticV4(d.Cfg.RustFSAccessKey, d.Cfg.RustFSSecretKey, ""),
		Secure: secure,
	})
	if err != nil {
		info.Status = "error"
		info.Error = err.Error()
		return info
	}

	// Check if bucket exists
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		info.Status = "error"
		info.Error = err.Error()
		return info
	}
	if !exists {
		info.Status = "bucket_not_found"
		return info
	}

	// Count objects and total bytes
	var objCount int64
	var totalBytes int64

	objCtx, objCancel := context.WithTimeout(ctx, 10*time.Second)
	defer objCancel()

	for obj := range client.ListObjects(objCtx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			break
		}
		objCount++
		totalBytes += obj.Size
	}

	info.ObjectCount = objCount
	info.TotalBytes = totalBytes
	info.Status = "ok"
	return info
}
