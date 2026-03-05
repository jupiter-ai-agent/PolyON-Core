package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/store"
)

// RegisterApps registers app lifecycle API routes.
func RegisterApps(r chi.Router, d *Deps) {
	r.Route("/apps", func(r chi.Router) {
		r.Get("/", listApps(d))
		r.Get("/{id}", getApp(d))
		r.Post("/{id}/install", installApp(d))
		r.Post("/{id}/start", startApp(d))
		r.Post("/{id}/stop", stopApp(d))
		r.Put("/{id}/domain", putAppDomain(d))
		r.Delete("/{id}/domain", deleteAppDomain(d))
	})
}

// AppMeta is re-exported from store for backward compatibility within the api package.
type AppMeta = store.AppMeta

// AppWithStatus is AppMeta + live container status.
type AppWithStatus struct {
	AppMeta
	Status      string `json:"status"`
	StatusLabel string `json:"statusLabel"`
}

// resolveStatus determines app status from container state.
func resolveStatus(meta AppMeta, containerStates map[string]string) (string, string) {
	if meta.BaseStatus == "coming-soon" {
		return "coming-soon", "Coming Soon"
	}

	if len(meta.Containers) == 0 {
		// No containers (e.g. external SaaS integrations)
		return meta.BaseStatus, statusLabel(meta.BaseStatus)
	}

	// De-duplicate containers (e.g. calendar & contacts both use polyon-mail)
	seen := map[string]bool{}
	var uniqueContainers []string
	for _, c := range meta.Containers {
		if !seen[c] {
			seen[c] = true
			uniqueContainers = append(uniqueContainers, c)
		}
	}

	allRunning := true
	anyExists := false

	for _, cname := range uniqueContainers {
		state, exists := containerStates[cname]
		if !exists {
			allRunning = false
			continue
		}
		anyExists = true
		if !strings.HasPrefix(state, "running") && state != "Up" && !strings.HasPrefix(state, "Up ") {
			allRunning = false
		}
	}

	if !anyExists {
		// No containers running or existing
		return meta.BaseStatus, statusLabel(meta.BaseStatus)
	}
	if allRunning {
		return "active", "Active"
	}
	return "stopped", "중지됨"
}

func statusLabel(s string) string {
	switch s {
	case "active":
		return "Active"
	case "available":
		return "설치 가능"
	case "requires-setup":
		return "설정 필요"
	case "coming-soon":
		return "Coming Soon"
	case "stopped":
		return "중지됨"
	default:
		return s
	}
}

// getContainerStates fetches all container states from Docker.
func getContainerStates(r *http.Request, d *Deps) map[string]string {
	states := map[string]string{}
	if d.Docker == nil {
		return states
	}
	containers, err := d.Docker.ContainerList(r.Context())
	if err == nil {
		for _, c := range containers {
			states[c.Name] = c.State
		}
	}
	return states
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func listApps(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		catalog, err := d.Store.ListApps(ctx)
		if err != nil {
			log.Error().Err(err).Msg("listApps: DB query failed")
			httputil.RespondError(w, 500, "DB_ERROR", "앱 목록 조회 실패: "+err.Error())
			return
		}

		containerStates := getContainerStates(r, d)

		var apps []AppWithStatus
		for _, meta := range catalog {
			status, label := resolveStatus(meta, containerStates)
			apps = append(apps, AppWithStatus{
				AppMeta:     meta,
				Status:      status,
				StatusLabel: label,
			})
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"apps":    apps,
			"total":   len(apps),
		})
	}
}

func getApp(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		meta, err := d.Store.GetApp(ctx, id)
		if err != nil {
			if err == pgx.ErrNoRows {
				httputil.RespondError(w, 404, "NOT_FOUND", "앱을 찾을 수 없습니다: "+id)
			} else {
				log.Error().Err(err).Str("id", id).Msg("getApp: DB query failed")
				httputil.RespondError(w, 500, "DB_ERROR", "앱 조회 실패: "+err.Error())
			}
			return
		}

		containerStates := getContainerStates(r, d)
		status, label := resolveStatus(*meta, containerStates)

		httputil.RespondOK(w, map[string]interface{}{
			"success":     true,
			"app":         meta,
			"status":      status,
			"statusLabel": label,
		})
	}
}

func installApp(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		meta, err := d.Store.GetApp(ctx, id)
		if err != nil {
			if err == pgx.ErrNoRows {
				httputil.RespondError(w, 404, "NOT_FOUND", "앱을 찾을 수 없습니다: "+id)
			} else {
				httputil.RespondError(w, 500, "DB_ERROR", "앱 조회 실패: "+err.Error())
			}
			return
		}

		if meta.BaseStatus == "coming-soon" {
			httputil.RespondError(w, 400, "NOT_AVAILABLE", "이 앱은 아직 설치할 수 없습니다")
			return
		}

		if len(meta.Containers) == 0 {
			// External SaaS — no containers to start
			httputil.RespondOK(w, map[string]interface{}{
				"success": true,
				"message": meta.Name + " 설정이 완료되었습니다 (외부 서비스)",
			})
			return
		}

		if d.Docker == nil {
			httputil.RespondError(w, 503, "DOCKER_UNAVAILABLE", "Docker 클라이언트를 사용할 수 없습니다")
			return
		}

		// Start containers that exist but are stopped; log ones that don't exist
		started := []string{}
		notFound := []string{}

		for _, cname := range meta.Containers {
			exists, state := d.Docker.ContainerExists(ctx, cname)
			if !exists {
				notFound = append(notFound, cname)
				log.Warn().Str("container", cname).Str("app", id).Msg("Container not found for install")
				continue
			}
			if state == "running" {
				started = append(started, cname)
				continue
			}
			if err := d.Docker.ContainerStart(ctx, cname); err != nil {
				log.Error().Err(err).Str("container", cname).Msg("Failed to start container")
				httputil.RespondError(w, 500, "START_FAILED", "컨테이너 시작 실패: "+err.Error())
				return
			}
			started = append(started, cname)
			log.Info().Str("container", cname).Str("app", id).Msg("Container started")
		}

		msg := meta.Name + " 설치가 완료되었습니다"
		if len(notFound) > 0 {
			msg = meta.Name + " 컨테이너를 찾을 수 없습니다. Docker Compose로 배포 후 다시 시도하세요."
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success":   true,
			"message":   msg,
			"started":   started,
			"not_found": notFound,
		})
	}
}

func startApp(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		meta, err := d.Store.GetApp(ctx, id)
		if err != nil {
			if err == pgx.ErrNoRows {
				httputil.RespondError(w, 404, "NOT_FOUND", "앱을 찾을 수 없습니다: "+id)
			} else {
				httputil.RespondError(w, 500, "DB_ERROR", "앱 조회 실패: "+err.Error())
			}
			return
		}

		if d.Docker == nil {
			httputil.RespondError(w, 503, "DOCKER_UNAVAILABLE", "Docker 클라이언트를 사용할 수 없습니다")
			return
		}

		started := []string{}
		for _, cname := range meta.Containers {
			exists, state := d.Docker.ContainerExists(ctx, cname)
			if !exists {
				continue
			}
			if state == "running" {
				started = append(started, cname)
				continue
			}
			if err := d.Docker.ContainerStart(ctx, cname); err != nil {
				httputil.RespondError(w, 500, "START_FAILED", "컨테이너 시작 실패: "+err.Error())
				return
			}
			started = append(started, cname)
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": meta.Name + " 이(가) 시작되었습니다",
			"started": started,
		})
	}
}

func stopApp(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		meta, err := d.Store.GetApp(ctx, id)
		if err != nil {
			if err == pgx.ErrNoRows {
				httputil.RespondError(w, 404, "NOT_FOUND", "앱을 찾을 수 없습니다: "+id)
			} else {
				httputil.RespondError(w, 500, "DB_ERROR", "앱 조회 실패: "+err.Error())
			}
			return
		}

		if d.Docker == nil {
			httputil.RespondError(w, 503, "DOCKER_UNAVAILABLE", "Docker 클라이언트를 사용할 수 없습니다")
			return
		}

		stopped := []string{}
		for _, cname := range meta.Containers {
			exists, _ := d.Docker.ContainerExists(ctx, cname)
			if !exists {
				continue
			}
			if err := d.Docker.ContainerStop(ctx, cname); err != nil {
				httputil.RespondError(w, 500, "STOP_FAILED", "컨테이너 중지 실패: "+err.Error())
				return
			}
			stopped = append(stopped, cname)
			log.Info().Str("container", cname).Str("app", id).Msg("Container stopped")
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": meta.Name + " 이(가) 중지되었습니다",
			"stopped": stopped,
		})
	}
}

// PUT /api/v1/apps/{id}/domain — set subdomain for an app.
func putAppDomain(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		var req struct {
			Subdomain string `json:"subdomain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "INVALID_JSON", "요청 바디 파싱 실패: "+err.Error())
			return
		}
		subdomain := strings.ToLower(strings.TrimSpace(req.Subdomain))
		if subdomain == "" {
			httputil.RespondError(w, 400, "MISSING_SUBDOMAIN", "subdomain 필드가 필요합니다")
			return
		}

		// Verify app exists and get metadata
		meta, err := d.Store.GetApp(ctx, id)
		if err != nil {
			if err == pgx.ErrNoRows {
				httputil.RespondError(w, 404, "NOT_FOUND", "앱을 찾을 수 없습니다: "+id)
			} else {
				log.Error().Err(err).Str("id", id).Msg("putAppDomain: GetApp failed")
				httputil.RespondError(w, 500, "DB_ERROR", "앱 조회 실패: "+err.Error())
			}
			return
		}

		if err := d.Store.UpdateAppDomain(ctx, id, subdomain, "configured"); err != nil {
			log.Error().Err(err).Str("id", id).Msg("putAppDomain: UpdateAppDomain failed")
			httputil.RespondError(w, 500, "DB_ERROR", "도메인 설정 실패: "+err.Error())
			return
		}

		cfg := d.Cfg
		stepResults := []map[string]string{{"step": "db", "status": "ok"}}

		// Step 2: DNS — register A record in AD DNS
		dnsStatus := "ok"
		if d.Samba != nil && cfg.Realm != "" && cfg.MailServerIP != "" {
			result := d.Samba.AddDNSRecord(cfg.Realm, subdomain, "A", cfg.MailServerIP)
			if !result.Success && !strings.Contains(result.Error, "already exists") {
				log.Warn().Str("app", id).Str("error", result.Error).Msg("putAppDomain: DNS add warning")
				dnsStatus = "warn:" + result.Error
			}
		} else {
			dnsStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "dns", "status": dnsStatus})

		// Step 3: Keycloak — create OIDC client in polyon realm
		kcStatus := "ok"
		// Prefer DB service_base_domain over AD Realm
		baseDomain := ""
		if d.Store != nil {
			configs, _ := d.Store.GetConfigs(ctx, []string{"service_base_domain"})
			baseDomain = configs["service_base_domain"]
		}
		if baseDomain == "" {
			baseDomain = strings.ToLower(cfg.Realm)
		}
		if baseDomain != "" && cfg.KeycloakURL != "" {
			kc, kcErr := newKCClient(cfg.KeycloakURL, cfg.KCAdminUser, cfg.KCAdminPassword)
			if kcErr != nil {
				log.Warn().Err(kcErr).Str("app", id).Msg("putAppDomain: KC client init failed")
				kcStatus = "warn:" + kcErr.Error()
			} else {
				appRedirectBase := fmt.Sprintf("https://%s.%s", subdomain, baseDomain)
				kcErr = kc.createOrUpdateAppClient("polyon", id, appRedirectBase)
				if kcErr != nil {
					log.Warn().Err(kcErr).Str("app", id).Msg("putAppDomain: KC client create/update failed")
					kcStatus = "warn:" + kcErr.Error()
				}
			}
		} else {
			kcStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "keycloak", "status": kcStatus})

		// Step 4: Traefik — add router + service
		traefikStatus := "ok"
		if d.Traefik != nil && meta.BackendURL != "" && baseDomain != "" {
			if err := d.Traefik.SetAppDomain(id, subdomain, baseDomain, meta.BackendURL); err != nil {
				log.Warn().Err(err).Str("app", id).Msg("putAppDomain: Traefik SetAppDomain failed")
				traefikStatus = "warn:" + err.Error()
			}
		} else {
			traefikStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "traefik", "status": traefikStatus})

		// Regenerate Traefik config (updates 10-apps.yml)
		go triggerTraefikRegenerate(d, ctx)

		httputil.RespondOK(w, map[string]interface{}{
			"success":   true,
			"subdomain": subdomain,
			"steps":     stepResults,
		})
	}
}

// DELETE /api/v1/apps/{id}/domain — clear subdomain for an app.
func deleteAppDomain(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		if d.Store == nil {
			httputil.RespondError(w, 503, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		// Fetch app (need subdomain for DNS removal)
		meta, err := d.Store.GetApp(ctx, id)
		if err != nil {
			if err == pgx.ErrNoRows {
				httputil.RespondError(w, 404, "NOT_FOUND", "앱을 찾을 수 없습니다: "+id)
			} else {
				log.Error().Err(err).Str("id", id).Msg("deleteAppDomain: GetApp failed")
				httputil.RespondError(w, 500, "DB_ERROR", "앱 조회 실패: "+err.Error())
			}
			return
		}

		oldSubdomain := meta.Subdomain

		if err := d.Store.UpdateAppDomain(ctx, id, "", "unconfigured"); err != nil {
			log.Error().Err(err).Str("id", id).Msg("deleteAppDomain: UpdateAppDomain failed")
			httputil.RespondError(w, 500, "DB_ERROR", "도메인 해제 실패: "+err.Error())
			return
		}

		cfg := d.Cfg
		stepResults := []map[string]string{{"step": "db", "status": "ok"}}

		// Step 2: DNS — remove A record
		dnsStatus := "ok"
		if d.Samba != nil && oldSubdomain != "" && cfg.Realm != "" && cfg.MailServerIP != "" {
			result := d.Samba.DeleteDNSRecord(cfg.Realm, oldSubdomain, "A", cfg.MailServerIP)
			if !result.Success && !strings.Contains(result.Error, "not found") {
				log.Warn().Str("app", id).Str("error", result.Error).Msg("deleteAppDomain: DNS delete warning")
				dnsStatus = "warn:" + result.Error
			}
		} else {
			dnsStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "dns", "status": dnsStatus})

		// Step 3: Keycloak — remove OIDC client from polyon realm
		kcStatus := "ok"
		if cfg.KeycloakURL != "" {
			kc, kcErr := newKCClient(cfg.KeycloakURL, cfg.KCAdminUser, cfg.KCAdminPassword)
			if kcErr != nil {
				log.Warn().Err(kcErr).Str("app", id).Msg("deleteAppDomain: KC client init failed")
				kcStatus = "warn:" + kcErr.Error()
			} else {
				exists, clientUID, kcErr := kc.clientExists("polyon", id)
				if kcErr != nil {
					log.Warn().Err(kcErr).Str("app", id).Msg("deleteAppDomain: KC clientExists failed")
					kcStatus = "warn:" + kcErr.Error()
				} else if exists && clientUID != "" {
					kcErr = kc.deleteClient("polyon", clientUID)
					if kcErr != nil {
						log.Warn().Err(kcErr).Str("app", id).Msg("deleteAppDomain: KC client delete failed")
						kcStatus = "warn:" + kcErr.Error()
					}
				}
			}
		} else {
			kcStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "keycloak", "status": kcStatus})

		// Step 4: Traefik — remove router + service
		traefikStatus := "ok"
		if d.Traefik != nil {
			if err := d.Traefik.RemoveAppDomain(id); err != nil {
				log.Warn().Err(err).Str("app", id).Msg("deleteAppDomain: Traefik RemoveAppDomain failed")
				traefikStatus = "warn:" + err.Error()
			}
		} else {
			traefikStatus = "skipped"
		}
		stepResults = append(stepResults, map[string]string{"step": "traefik", "status": traefikStatus})

		// Regenerate Traefik config (updates 10-apps.yml)
		go triggerTraefikRegenerate(d, ctx)

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "앱 도메인이 해제되었습니다",
			"steps":   stepResults,
		})
	}
}
