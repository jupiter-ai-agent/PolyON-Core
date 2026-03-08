package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/module"
	"github.com/triangles/polyon-core/internal/store"
)

// RegisterModules registers the Module System API routes.
func RegisterModules(r chi.Router, d *Deps) {
	r.Route("/modules", func(r chi.Router) {
		r.Get("/catalog", listModuleCatalog(d))
		r.Get("/nav", listModuleNav(d))
		r.Post("/register", registerModule(d))
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", getModule(d))
			r.Post("/install", installModule(d))
			r.Post("/uninstall", uninstallModule(d))
			r.Get("/install/status", installStatus(d))
		})
	})
}

// listModuleCatalog returns all modules, optionally filtered by category and status.
func listModuleCatalog(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		category := r.URL.Query().Get("category")
		status := r.URL.Query().Get("status")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		modules, err := d.Store.ListModules(ctx, category, status)
		if err != nil {
			log.Error().Err(err).Msg("listModuleCatalog: DB query failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "모듈 목록 조회 실패")
			return
		}

		httputil.RespondOK(w, map[string]any{
			"modules": modules,
		})
	}
}

// listModuleNav returns navigation info for active modules.
func listModuleNav(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		navs, err := d.Store.ListActiveModuleNav(ctx)
		if err != nil {
			log.Error().Err(err).Msg("listModuleNav: DB query failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "모듈 네비게이션 조회 실패")
			return
		}

		httputil.RespondOK(w, map[string]any{
			"modules": navs,
		})
	}
}

// registerModule registers a new module from a Docker image.
func registerModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		var req struct {
			Image string `json:"image"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "INVALID_BODY", "잘못된 요청 본문입니다")
			return
		}

		if req.Image == "" {
			httputil.RespondError(w, http.StatusBadRequest, "MISSING_IMAGE", "이미지 정보가 필요합니다")
			return
		}

		// Phase 2: manifest 취득 — catalog 우선, 없으면 이미지에서 추출
		var manifest *module.Manifest

		// 2-a: embedded catalog에서 먼저 찾기 (즉시 응답)
		if m := module.FindCatalogManifest(req.Image); m != nil {
			manifest = m
			log.Info().Str("image", req.Image).Str("id", m.Metadata.ID).Msg("registerModule: using catalog manifest")
		} else if d.Kube != nil {
			// 2-b: 3rd-party — 이미지에서 module.yaml 추출 (느림)
			log.Info().Str("image", req.Image).Msg("registerModule: extracting manifest from image")
			raw, err := d.Kube.ExtractModuleManifest(r.Context(), req.Image)
			if err != nil {
				log.Error().Err(err).Str("image", req.Image).Msg("registerModule: manifest extraction failed")
				httputil.RespondError(w, http.StatusBadRequest, "EXTRACT_FAILED",
					"모듈 매니페스트 추출 실패: "+err.Error())
				return
			}
			m, err := module.ParseManifest(raw)
			if err != nil {
				log.Error().Err(err).Msg("registerModule: manifest parse failed")
				httputil.RespondError(w, http.StatusBadRequest, "INVALID_MANIFEST",
					"모듈 매니페스트 파싱 실패: "+err.Error())
				return
			}
			manifest = m
		} else {
			// 2-c: Fallback stub
			log.Warn().Msg("registerModule: K8s not available, using stub manifest")
			manifest = &module.Manifest{
				Metadata: module.Metadata{
					ID:       generateModuleID(req.Image),
					Name:     extractImageName(req.Image),
					Version:  "1.0.0",
					Category: "engine",
					Icon:     "Application",
					Accent:   "#0f62fe",
				},
			}
		}

		moduleID := manifest.Metadata.ID

		// 기존 모듈 체크
		existing, _ := d.Store.GetModule(r.Context(), moduleID)
		if existing != nil {
			httputil.RespondError(w, http.StatusConflict, "MODULE_EXISTS", "이미 등록된 모듈입니다: "+moduleID)
			return
		}

		// manifest를 항상 JSON으로 변환 (YAML → JSON, DB jsonb 컬럼)
		manifestJSON, _ := json.Marshal(manifest)

		requiresJSON, _ := json.Marshal(manifest.Spec.Requires)
		optionalJSON, _ := json.Marshal(manifest.Spec.Optional)

		mod := store.Module{
			ID:          moduleID,
			Name:        manifest.Metadata.Name,
			Description: manifest.Metadata.Description,
			Category:    manifest.Metadata.Category,
			Version:     manifest.Metadata.Version,
			Engine:      manifest.Spec.Engine,
			Image:       req.Image,
			Icon:        manifest.Metadata.Icon,
			Accent:      manifest.Metadata.Accent,
			Status:      "available",
			Requires:    requiresJSON,
			OptionalDeps: optionalJSON,
			Manifest:    manifestJSON,
		}

		if err := d.Store.CreateModule(r.Context(), mod); err != nil {
			log.Error().Err(err).Msg("registerModule: DB insert failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "모듈 등록 실패")
			return
		}

		// 이벤트 기록
		d.Store.CreateModuleEvent(r.Context(), moduleID, "register", "completed",
			"Module registered successfully", map[string]any{"image": req.Image})

		log.Info().Str("module_id", moduleID).Str("image", req.Image).Msg("Module registered")

		httputil.RespondOK(w, map[string]any{
			"status": "registered",
			"module": mod,
		})
	}
}

// getModule returns a single module by ID.
func getModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")
		module, err := d.Store.GetModule(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "NOT_FOUND", "모듈을 찾을 수 없습니다: "+id)
			return
		}

		httputil.RespondOK(w, module)
	}
}

// installModule installs a module with full K8s deployment pipeline.
func installModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		if d.Kube == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "KUBE_UNAVAILABLE", "Kubernetes 클라이언트를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")

		// Parse optional request body for subdomain
		var reqBody struct {
			Subdomain string `json:"subdomain"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody) // optional body

		// 1. 모듈 존재 확인 — 없으면 catalog에서 자동 등록 시도
		moduleRecord, err := d.Store.GetModule(r.Context(), id)
		if err != nil {
			// catalog에서 자동 등록 시도
			catalogManifest := module.FindCatalogManifestByID(id)
			if catalogManifest == nil {
				httputil.RespondError(w, http.StatusNotFound, "NOT_FOUND", "모듈을 찾을 수 없습니다: "+id)
				return
			}
			log.Info().Str("module_id", id).Msg("Auto-registering catalog module for install")
			manifestJSON, _ := json.Marshal(catalogManifest)
			requiresJSON, _ := json.Marshal(catalogManifest.Spec.Requires)
			optionalJSON, _ := json.Marshal(catalogManifest.Spec.Optional)
			autoRec := store.Module{
				ID:          catalogManifest.Metadata.ID,
				Name:        catalogManifest.Metadata.Name,
				Description: catalogManifest.Metadata.Description,
				Category:    catalogManifest.Metadata.Category,
				Version:     catalogManifest.Metadata.Version,
				Engine:      catalogManifest.Spec.Engine,
				Image:       catalogManifest.Spec.Resources.Image,
				Icon:        catalogManifest.Metadata.Icon,
				Accent:      catalogManifest.Metadata.Accent,
				Status:      "available",
				Manifest:    manifestJSON,
				Requires:    requiresJSON,
				OptionalDeps: optionalJSON,
			}
			if err := d.Store.CreateModule(r.Context(), autoRec); err != nil {
				httputil.RespondError(w, http.StatusInternalServerError, "AUTO_REGISTER_FAILED", "자동 등록 실패: "+err.Error())
				return
			}
			moduleRecord, _ = d.Store.GetModule(r.Context(), id)
			if moduleRecord == nil {
				httputil.RespondError(w, http.StatusInternalServerError, "AUTO_REGISTER_FAILED", "자동 등록 후 모듈 조회 실패")
				return
			}
		}

		if moduleRecord.Status == "active" {
			httputil.RespondError(w, http.StatusConflict, "ALREADY_INSTALLED", "이미 설치된 모듈입니다")
			return
		}

		// 2. manifest에서 spec 파싱
		var manifest module.Manifest
		if err := json.Unmarshal(moduleRecord.Manifest, &manifest); err != nil {
			log.Error().Err(err).Str("module_id", id).Msg("Failed to parse module manifest")
			httputil.RespondError(w, http.StatusInternalServerError, "INVALID_MANIFEST", "모듈 매니페스트 파싱 실패")
			return
		}

		// 2.5 Subdomain override from request body
		if reqBody.Subdomain != "" {
			manifest.Spec.Ingress.Subdomain = reqBody.Subdomain
		}

		// 3. 의존성 체크 (기본 체크만, Foundation은 항상 존재)
		for _, dep := range manifest.Spec.Requires {
			if dep.ID != "postgresql" && dep.ID != "keycloak" && dep.ID != "opensearch" {
				// Check if required module is active
				depModule, err := d.Store.GetModule(r.Context(), dep.ID)
				if err != nil || depModule.Status != "active" {
					httputil.RespondError(w, http.StatusPreconditionFailed, "DEPENDENCY_NOT_MET", 
						fmt.Sprintf("필수 의존성 '%s'이 설치되지 않았습니다", dep.ID))
					return
				}
			}
		}

		// 설치 시작 이벤트 기록
		if err := d.Store.CreateModuleEvent(r.Context(), id, "install", "started",
			"Module installation started", nil); err != nil {
			log.Error().Err(err).Msg("installModule: failed to create start event")
		}

		// status를 installing으로 변경
		if err := d.Store.UpdateModuleStatus(r.Context(), id, "installing"); err != nil {
			log.Error().Err(err).Msg("installModule: failed to update status to installing")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "설치 상태 업데이트 실패")
			return
		}

		// 4. DB 프로비저닝 (spec.database.create == true일 때)
		if manifest.Spec.Database.Create {
			dbPassword, err := store.GeneratePassword(24)
			if err != nil {
				log.Error().Err(err).Msg("Failed to generate database password")
				d.Store.UpdateModuleStatus(r.Context(), id, "error")
				httputil.RespondError(w, http.StatusInternalServerError, "PASSWORD_GEN_ERROR", "데이터베이스 패스워드 생성 실패")
				return
			}

			if err := d.Store.CreateModuleDatabase(r.Context(), manifest.Spec.Database.Name, 
				manifest.Spec.Database.User, dbPassword); err != nil {
				log.Error().Err(err).Str("module_id", id).Msg("Database provisioning failed")
				d.Store.UpdateModuleStatus(r.Context(), id, "error")
				d.Store.CreateModuleEvent(r.Context(), id, "install", "failed", "Database provisioning failed", nil)
				httputil.RespondError(w, http.StatusInternalServerError, "DB_PROVISION_ERROR", "데이터베이스 프로비저닝 실패")
				return
			}

			// Update secret with actual database password
			dbURL := store.GetDatabaseConnection("polyon-db", "5432", 
				manifest.Spec.Database.Name, manifest.Spec.Database.User, dbPassword)
			
			secretData := map[string][]byte{
				"DATABASE_URL": []byte(dbURL),
				"DB_PASSWORD":  []byte(dbPassword),
			}
			
			// We'll update this after K8s secret creation
			defer func() {
				if err := d.Kube.UpdateSecretData(r.Context(), id, secretData); err != nil {
					log.Error().Err(err).Str("module_id", id).Msg("Failed to update secret with DB password")
				}
			}()
		}

		// 5-6. K8s Secret + Deployment + Service + Ingress 생성
		if err := d.Kube.DeployModule(r.Context(), id, manifest.Spec); err != nil {
			log.Error().Err(err).Str("module_id", id).Msg("K8s deployment failed")
			d.Store.UpdateModuleStatus(r.Context(), id, "error")
			d.Store.CreateModuleEvent(r.Context(), id, "install", "failed", "K8s deployment failed", map[string]any{"error": err.Error()})
			httputil.RespondError(w, http.StatusInternalServerError, "K8S_DEPLOY_ERROR", "K8s 배포 실패: "+err.Error())
			return
		}

		// 7. nav 정보 등록 (manifest.spec.admin.nav에서 추출)
		navItems, err := module.MarshalNavItems(manifest.Spec.Admin.Nav.Items)
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal nav items")
			navItems = json.RawMessage("[]")
		}

		routes, err := module.MarshalRoutes(manifest.Spec.Admin.ConvertToRoutes())
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal routes")
			routes = json.RawMessage("[]")
		}

		navInfo := store.ModuleNav{
			ModuleID:    id,
			Title:       manifest.Spec.Admin.Nav.Title,
			Section:     manifest.Spec.Admin.Nav.Section,
			Icon:        manifest.Spec.Admin.Nav.Icon,
			DefaultPath: manifest.Spec.Admin.Nav.DefaultPath,
			SortOrder:   manifest.Spec.Admin.Nav.SortOrder,
			NavItems:    navItems,
			Routes:      routes,
		}

		if err := d.Store.SaveModuleNav(r.Context(), navInfo); err != nil {
			log.Error().Err(err).Msg("installModule: failed to save nav info")
		}

		// 7.5 Update app subdomain + base_status in polyon_apps
		if manifest.Spec.Ingress.Subdomain != "" {
			if err := d.Store.UpdateAppDomain(r.Context(), id, manifest.Spec.Ingress.Subdomain, "active"); err != nil {
				log.Warn().Err(err).Str("module_id", id).Msg("Failed to update app subdomain (non-fatal)")
			}
		}
		// Mark app as active in polyon_apps (for domain/portal pages)
		if _, err := d.Store.Pool().Exec(r.Context(),
			`UPDATE polyon_apps SET base_status = 'active', updated_at = NOW() WHERE id = $1`, id); err != nil {
			log.Warn().Err(err).Str("module_id", id).Msg("Failed to update app base_status (non-fatal)")
		}

		// 8. status → active
		if err := d.Store.UpdateModuleStatus(r.Context(), id, "active"); err != nil {
			log.Error().Err(err).Msg("installModule: failed to update status to active")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "설치 완료 상태 업데이트 실패")
			return
		}

		// Wait for deployment to be ready, then run post-install provisioning
		go func() {
			// background context — HTTP 요청 종료 후에도 계속 실행
			bgCtx := context.Background()
			if err := d.Kube.WaitForDeploymentReady(bgCtx, id, 5*time.Minute); err != nil {
				log.Error().Err(err).Str("module_id", id).Msg("Deployment readiness check failed")
				d.Store.CreateModuleEvent(bgCtx, id, "install", "warning",
					"Deployment may not be ready", map[string]any{"error": err.Error()})
				return
			}
			// Post-install: LDAP 바인딩, OIDC 등 자동 프로비저닝
			PostInstallProvisioning(bgCtx, d, id, &manifest)
		}()

		// 9. 이벤트 기록
		if err := d.Store.CreateModuleEvent(r.Context(), id, "install", "completed",
			"Module installation completed successfully", map[string]any{
				"version": moduleRecord.Version,
				"image":   moduleRecord.Image,
				"host":    fmt.Sprintf("%s.cmars.com", manifest.Spec.Ingress.Subdomain),
			}); err != nil {
			log.Error().Err(err).Msg("installModule: failed to create completion event")
		}

		log.Info().Str("module_id", id).Str("version", moduleRecord.Version).Msg("Module installed successfully")

		// component 상태도 동기화
		if _, err := d.Store.Pool().Exec(r.Context(),
			`UPDATE polyon_components SET status='active' WHERE id=$1`, id); err != nil {
			log.Warn().Err(err).Str("module_id", id).Msg("Failed to sync component status")
		}

		httputil.RespondOK(w, map[string]any{
			"status": "installed",
			"module": map[string]any{
				"id":      id,
				"status":  "active",
				"version": moduleRecord.Version,
				"url":     fmt.Sprintf("https://%s.cmars.com", manifest.Spec.Ingress.Subdomain),
			},
		})
	}
}

// uninstallModule uninstalls a module with full cleanup.
func uninstallModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		if d.Kube == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "KUBE_UNAVAILABLE", "Kubernetes 클라이언트를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")

		// Parse request body for data policy
		var req struct {
			DataPolicy string `json:"dataPolicy"` // "delete" | "keep"
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.DataPolicy == "" {
			req.DataPolicy = "keep" // Default to keeping data
		}

		// 모듈 존재 확인
		moduleRecord, err := d.Store.GetModule(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "NOT_FOUND", "모듈을 찾을 수 없습니다: "+id)
			return
		}

		if moduleRecord.Status != "active" {
			httputil.RespondError(w, http.StatusConflict, "NOT_INSTALLED", "설치되지 않은 모듈입니다")
			return
		}

		// Parse manifest for cleanup configuration
		var manifest module.Manifest
		if err := json.Unmarshal(moduleRecord.Manifest, &manifest); err != nil {
			log.Error().Err(err).Str("module_id", id).Msg("Failed to parse module manifest for uninstall")
			// Continue with basic cleanup
		}

		// TODO: 역의존성 체크 - 이 모듈에 의존하는 다른 모듈이 있는지 확인
		// For Phase 2, we skip this check

		// 제거 시작 이벤트 기록
		if err := d.Store.CreateModuleEvent(r.Context(), id, "uninstall", "started",
			"Module uninstallation started", map[string]any{"dataPolicy": req.DataPolicy}); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to create start event")
		}

		// status를 uninstalling으로 변경
		if err := d.Store.UpdateModuleStatus(r.Context(), id, "uninstalling"); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to update status")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "제거 상태 업데이트 실패")
			return
		}

		// 1. K8s 리소스 삭제 (Deployment, Service, Ingress, Secret)
		if err := d.Kube.DeleteModule(r.Context(), id); err != nil {
			log.Error().Err(err).Str("module_id", id).Msg("K8s resource deletion failed")
			// Continue with database cleanup even if K8s deletion fails
		}

		// 2. DB 삭제 (데이터 정책에 따라)
		if req.DataPolicy == "delete" && manifest.Spec.Database.Create {
			if err := d.Store.DeleteModuleDatabase(r.Context(), manifest.Spec.Database.Name, 
				manifest.Spec.Database.User); err != nil {
				log.Error().Err(err).Str("module_id", id).Msg("Database deletion failed")
				// Continue with record cleanup
			}
		}

		// 3. nav 정보 삭제 (CASCADE로 자동 삭제되지만 명시적 호출)
		if err := d.Store.DeleteModuleNav(r.Context(), id); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to delete nav")
		}

		// 3.5 Revert app base_status to available
		if _, err := d.Store.Pool().Exec(r.Context(),
			`UPDATE polyon_apps SET base_status = 'available', updated_at = NOW() WHERE id = $1`, id); err != nil {
			log.Warn().Err(err).Msg("uninstallModule: failed to revert app base_status")
		}

		// 4. module 레코드 삭제
		if err := d.Store.DeleteModule(r.Context(), id); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to delete module record")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "모듈 레코드 삭제 실패")
			return
		}

		// 제거 완료 이벤트 기록 (별도 테이블이므로 module 삭제 후에도 기록 가능)
		d.Store.CreateModuleEvent(r.Context(), id, "uninstall", "completed",
			"Module uninstallation completed", map[string]any{
				"dataPolicy": req.DataPolicy,
				"version":    moduleRecord.Version,
			})

		log.Info().Str("module_id", id).Str("data_policy", req.DataPolicy).Msg("Module uninstalled successfully")

		// component 상태 복원
		if _, err := d.Store.Pool().Exec(r.Context(),
			`UPDATE polyon_components SET status='planned' WHERE id=$1`, id); err != nil {
			log.Warn().Err(err).Str("module_id", id).Msg("Failed to sync component status")
		}

		httputil.RespondOK(w, map[string]any{
			"status":     "uninstalled",
			"dataPolicy": req.DataPolicy,
		})
	}
}

// installStatus returns the installation status of a module.
func installStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")
		limitStr := r.URL.Query().Get("limit")
		limit := 10
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}

		events, err := d.Store.ListModuleEvents(r.Context(), id, limit)
		if err != nil {
			log.Error().Err(err).Msg("installStatus: DB query failed")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "설치 상태 조회 실패")
			return
		}

		// 현재 모듈 상태 조회
		module, _ := d.Store.GetModule(r.Context(), id)
		var currentStatus string
		if module != nil {
			currentStatus = module.Status
		} else {
			currentStatus = "not_found"
		}

		httputil.RespondOK(w, map[string]any{
			"moduleId":      id,
			"currentStatus": currentStatus,
			"events":        events,
		})
	}
}

// generateModuleID creates a module ID from an image name.
func generateModuleID(image string) string {
	// Extract repo name from image (e.g., "jupitertriangles/polyon-chat:v1.0.0" -> "polyon-chat")
	parts := strings.Split(image, "/")
	if len(parts) > 1 {
		nameTag := parts[len(parts)-1]
		name := strings.Split(nameTag, ":")[0]
		return strings.ToLower(name)
	}
	
	// fallback
	name := strings.Split(image, ":")[0]
	return strings.ToLower(strings.ReplaceAll(name, "/", "-"))
}

// extractImageName extracts a display name from an image.
func extractImageName(image string) string {
	parts := strings.Split(image, "/")
	if len(parts) > 1 {
		nameTag := parts[len(parts)-1]
		name := strings.Split(nameTag, ":")[0]
		return strings.Title(strings.ReplaceAll(name, "-", " "))
	}
	
	name := strings.Split(image, ":")[0]
	return strings.Title(strings.ReplaceAll(name, "-", " "))
}