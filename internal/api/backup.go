package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/store"
)

// backupProgress tracks the current in-flight backup status.
var backupProgress = struct {
	mu      sync.RWMutex
	ID      string `json:"id"`
	Phase   string `json:"phase"`
	Step    string `json:"step"`
	Error   string `json:"error"`
}{Phase: "idle"}

// RegisterBackup registers backup/restore API routes.
func RegisterBackup(r chi.Router, d *Deps) {
	r.Route("/backup", func(r chi.Router) {
		r.Post("/", startBackup(d))
		r.Get("/", listBackupsNew(d))
		r.Get("/status", backupStatus(d))
		r.Get("/{id}", getBackup(d))
		r.Post("/{id}/restore", startRestore(d))
		r.Delete("/{id}", deleteBackup(d))
	})
}

// POST /api/v1/backup — start a new backup
func startBackup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := time.Now().Format("20060102-150405")
		backupDir := fmt.Sprintf("/backup/polyon/%s", id)

		if err := os.MkdirAll(backupDir, 0755); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "mkdir failed", err.Error())
			return
		}

		rec := &store.BackupRecord{
			ID:        id,
			Tier:      1,
			Status:    "running",
			Path:      backupDir + "/",
			CreatedAt: time.Now(),
		}
		if err := d.Store.CreateBackup(r.Context(), rec); err != nil {
			log.Warn().Err(err).Str("id", id).Msg("backup: create record")
		}

		// Set global progress
		backupProgress.mu.Lock()
		backupProgress.ID = id
		backupProgress.Phase = "running"
		backupProgress.Step = "starting"
		backupProgress.Error = ""
		backupProgress.mu.Unlock()

		go runBackup(d, id, backupDir)

		httputil.RespondJSON(w, http.StatusAccepted, map[string]string{
			"id":     id,
			"status": "running",
			"path":   backupDir + "/",
		})
	}
}

// runBackup executes the full backup sequence in a goroutine.
func runBackup(d *Deps, id, backupDir string) {
	setStep := func(step string) {
		backupProgress.mu.Lock()
		backupProgress.Step = step
		backupProgress.mu.Unlock()
		log.Info().Str("id", id).Str("step", step).Msg("backup: step")
	}

	fail := func(msg string, err error) {
		log.Warn().Err(err).Str("id", id).Msg("backup: " + msg)
		backupProgress.mu.Lock()
		backupProgress.Phase = "failed"
		backupProgress.Error = err.Error()
		backupProgress.mu.Unlock()
		_ = d.Store.UpdateBackup(context.Background(), id, "failed", 0, err.Error())
	}

	// a. PostgreSQL dump
	setStep("postgres")
	dbFile := filepath.Join(backupDir, "db.sql")
	if err := dumpPostgres(dbFile); err != nil {
		log.Warn().Err(err).Str("id", id).Msg("backup: postgres dump failed (non-fatal, continuing)")
	}

	// b. Samba backup
	setStep("samba")
	sambaDir := filepath.Join(backupDir, "samba")
	if err := os.MkdirAll(sambaDir, 0755); err == nil {
		if _, err := d.Docker.ExecSambaTool(d.Cfg.DCContainer,
			"domain", "backup", "online", "--targetdir=/var/lib/samba/backup"); err != nil {
			log.Warn().Err(err).Str("id", id).Msg("backup: samba-tool failed (non-fatal)")
		} else {
			// docker cp samba backup out
			cpCmd := exec.Command("docker", "cp",
				d.Cfg.DCContainer+":/var/lib/samba/backup/.", sambaDir)
			if out, err := cpCmd.CombinedOutput(); err != nil {
				log.Warn().Err(err).Str("out", string(out)).Msg("backup: docker cp samba failed (non-fatal)")
			}
		}
	}

	// c. Config files
	setStep("config")
	polyonDir := d.Cfg.PolyonDir
	if polyonDir == "" {
		polyonDir = "/polyon"
	}
	configDir := filepath.Join(backupDir, "config")
	if err := os.MkdirAll(configDir, 0755); err == nil {
		copyConfigFiles(polyonDir, configDir)
	}

	// d. Calculate size + mark complete
	setStep("finalizing")
	size := dirSize(backupDir)

	if err := d.Store.UpdateBackup(context.Background(), id, "complete", size, ""); err != nil {
		fail("update record", err)
		return
	}

	backupProgress.mu.Lock()
	backupProgress.Phase = "complete"
	backupProgress.Step = "done"
	backupProgress.mu.Unlock()

	log.Info().Str("id", id).Int64("size", size).Msg("backup: complete")
	d.Store.LogAction("BACKUP_COMPLETE", "system", id, fmt.Sprintf("Backup complete, size=%d", size), "system", "")
}

// dumpPostgres runs pg_dumpall via docker exec and writes to outFile.
func dumpPostgres(outFile string) error {
	f, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer f.Close()

	cmd := exec.Command("docker", "exec", "polyon-db", "pg_dumpall", "-U", "polyon")
	cmd.Stdout = f
	var stderr []byte
	pr, pw, err2 := os.Pipe()
	if err2 == nil {
		cmd.Stderr = pw
		if err := cmd.Run(); err != nil {
			pw.Close()
			io.ReadAll(pr)
			return fmt.Errorf("pg_dumpall: %w", err)
		}
		pw.Close()
		stderr, _ = io.ReadAll(pr)
		_ = stderr
	} else {
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pg_dumpall: %w", err)
		}
	}
	return nil
}

// copyConfigFiles copies .env, secrets/, docker-compose*.yml from src to dst.
func copyConfigFiles(src, dst string) {
	patterns := []string{".env", "docker-compose.yml", "docker-compose*.yml"}
	for _, pat := range patterns {
		matches, _ := filepath.Glob(filepath.Join(src, pat))
		for _, src := range matches {
			name := filepath.Base(src)
			data, err := os.ReadFile(src)
			if err != nil {
				log.Warn().Err(err).Str("file", src).Msg("backup: read config file")
				continue
			}
			if err := os.WriteFile(filepath.Join(dst, name), data, 0600); err != nil {
				log.Warn().Err(err).Str("file", name).Msg("backup: write config file")
			}
		}
	}

	// secrets/ directory
	secretsSrc := filepath.Join(src, "secrets")
	secretsDst := filepath.Join(dst, "secrets")
	if info, err := os.Stat(secretsSrc); err == nil && info.IsDir() {
		copyDir(secretsSrc, secretsDst)
	}
}

// copyDir recursively copies a directory.
func copyDir(src, dst string) {
	os.MkdirAll(dst, 0700)
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(srcPath, dstPath)
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				continue
			}
			_ = os.WriteFile(dstPath, data, 0600)
		}
	}
}

// dirSize returns the total size of a directory tree in bytes.
func dirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// GET /api/v1/backup
func listBackupsNew(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := d.Store.ListBackups(r.Context())
		if err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "db error", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"backups": records,
		})
	}
}

// GET /api/v1/backup/status
func backupStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		backupProgress.mu.RLock()
		defer backupProgress.mu.RUnlock()
		httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"id":    backupProgress.ID,
			"phase": backupProgress.Phase,
			"step":  backupProgress.Step,
			"error": backupProgress.Error,
		})
	}
}

// GET /api/v1/backup/{id}
func getBackup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rec, err := d.Store.GetBackup(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "not found", err.Error())
			return
		}
		httputil.RespondJSON(w, http.StatusOK, rec)
	}
}

// POST /api/v1/backup/{id}/restore
func startRestore(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var req struct {
			Confirm       string `json:"confirm"`
			AdminPassword string `json:"admin_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid JSON", "invalid JSON")
			return
		}
		if req.Confirm != "RESTORE" {
			httputil.RespondError(w, http.StatusBadRequest, "confirmation required", "send confirm=RESTORE")
			return
		}

		// Verify admin password
		if d.LDAP != nil {
			if err := d.LDAP.VerifyBind(d.Cfg.AdminDN(), req.AdminPassword); err != nil {
				httputil.RespondError(w, http.StatusUnauthorized, "invalid credentials", err.Error())
				return
			}
		}

		rec, err := d.Store.GetBackup(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "backup not found", err.Error())
			return
		}

		go runRestore(d, rec)

		httputil.RespondJSON(w, http.StatusAccepted, map[string]string{
			"id":     id,
			"status": "restore_started",
		})
	}
}

// runRestore executes the restore sequence.
func runRestore(d *Deps, rec *store.BackupRecord) {
	log.Info().Str("id", rec.ID).Msg("restore: starting")

	backupDir := rec.Path
	polyonDir := d.Cfg.PolyonDir
	if polyonDir == "" {
		polyonDir = "/polyon"
	}

	// a. Services down
	log.Info().Msg("restore: compose down")
	downCmd := exec.Command("docker", "compose",
		"-f", filepath.Join(polyonDir, "docker-compose.yml"),
		"down", "--timeout", "30")
	downCmd.Dir = polyonDir
	if out, err := downCmd.CombinedOutput(); err != nil {
		log.Warn().Err(err).Str("out", string(out)).Msg("restore: compose down failed (continuing)")
	}

	// b. Restore PostgreSQL
	dbFile := filepath.Join(backupDir, "db.sql")
	if _, err := os.Stat(dbFile); err == nil {
		log.Info().Msg("restore: psql restore")
		psqlCmd := exec.Command("docker", "exec", "-i", "polyon-db",
			"psql", "-U", "polyon")
		f, err := os.Open(dbFile)
		if err == nil {
			psqlCmd.Stdin = f
			if out, err := psqlCmd.CombinedOutput(); err != nil {
				log.Warn().Err(err).Str("out", string(out)).Msg("restore: psql failed (non-fatal)")
			}
			f.Close()
		}
	}

	// c. Restore config files
	configDir := filepath.Join(backupDir, "config")
	if info, err := os.Stat(configDir); err == nil && info.IsDir() {
		log.Info().Msg("restore: config files")
		copyDir(configDir, polyonDir)
	}

	// d. Services up
	log.Info().Msg("restore: compose up")
	upCmd := exec.Command("docker", "compose",
		"-f", filepath.Join(polyonDir, "docker-compose.yml"),
		"up", "-d")
	upCmd.Dir = polyonDir
	if out, err := upCmd.CombinedOutput(); err != nil {
		log.Warn().Err(err).Str("out", string(out)).Msg("restore: compose up failed")
	}

	log.Info().Str("id", rec.ID).Msg("restore: complete")
	d.Store.LogAction("RESTORE_COMPLETE", "system", rec.ID, "Restore completed", "system", "")
}

// DELETE /api/v1/backup/{id}
func deleteBackup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		rec, err := d.Store.GetBackup(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "backup not found", err.Error())
			return
		}

		// Remove directory
		if rec.Path != "" {
			if err := os.RemoveAll(rec.Path); err != nil {
				log.Warn().Err(err).Str("path", rec.Path).Msg("deleteBackup: remove dir")
			}
		}

		// Remove DB record
		if err := d.Store.DeleteBackupRecord(r.Context(), id); err != nil {
			httputil.RespondError(w, http.StatusInternalServerError, "db error", err.Error())
			return
		}

		log.Info().Str("id", id).Msg("backup deleted")
		httputil.RespondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
