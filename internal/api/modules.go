package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
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

		modules, err := d.Store.ListModules(r.Context(), category, status)
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

		navs, err := d.Store.ListActiveModuleNav(r.Context())
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

		// TODO: Phase 2에서 이미지에서 manifest 추출 구현
		// 현재는 stub으로 처리
		moduleID := generateModuleID(req.Image)

		// 기존 모듈 체크
		existing, _ := d.Store.GetModule(r.Context(), moduleID)
		if existing != nil {
			httputil.RespondError(w, http.StatusConflict, "MODULE_EXISTS", "이미 등록된 모듈입니다")
			return
		}

		// stub manifest 생성
		manifest := map[string]any{
			"apiVersion": "polyon.io/v1",
			"kind":       "Module",
			"metadata": map[string]any{
				"id":          moduleID,
				"name":        extractImageName(req.Image),
				"version":     "1.0.0",
				"category":    "engine",
				"icon":        "Application",
				"accent":      "#0f62fe",
				"description": "Module registered from " + req.Image,
			},
			"spec": map[string]any{
				"engine": "unknown",
				"resources": map[string]any{
					"image": req.Image,
				},
				"admin": map[string]any{
					"nav": map[string]any{
						"title":       extractImageName(req.Image),
						"section":     "SERVICES",
						"icon":        "Application",
						"defaultPath": "/" + moduleID,
						"sortOrder":   50,
					},
				},
			},
		}

		manifestJSON, _ := json.Marshal(manifest)
		metadata := manifest["metadata"].(map[string]any)

		module := store.Module{
			ID:          moduleID,
			Name:        metadata["name"].(string),
			Description: metadata["description"].(string),
			Category:    metadata["category"].(string),
			Version:     metadata["version"].(string),
			Engine:      "unknown",
			Image:       req.Image,
			Icon:        metadata["icon"].(string),
			Accent:      metadata["accent"].(string),
			Status:      "available",
			Requires:    json.RawMessage("[]"),
			OptionalDeps: json.RawMessage("[]"),
			Manifest:    manifestJSON,
		}

		if err := d.Store.CreateModule(r.Context(), module); err != nil {
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
			"module": module,
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

// installModule installs a module.
func installModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")

		// 모듈 존재 확인
		module, err := d.Store.GetModule(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "NOT_FOUND", "모듈을 찾을 수 없습니다: "+id)
			return
		}

		if module.Status == "active" {
			httputil.RespondError(w, http.StatusConflict, "ALREADY_INSTALLED", "이미 설치된 모듈입니다")
			return
		}

		// 설치 시작 이벤트 기록
		err = d.Store.CreateModuleEvent(r.Context(), id, "install", "started",
			"Module installation started", nil)
		if err != nil {
			log.Error().Err(err).Msg("installModule: failed to create start event")
		}

		// TODO: Phase 2에서 실제 K8s 배포 로직 구현
		// 현재는 stub으로 status만 변경

		// status를 installing으로 변경
		if err := d.Store.UpdateModuleStatus(r.Context(), id, "installing"); err != nil {
			log.Error().Err(err).Msg("installModule: failed to update status to installing")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "설치 상태 업데이트 실패")
			return
		}

		// stub nav 정보 생성 (manifest에서 추출 예정)
		var manifest map[string]any
		json.Unmarshal(module.Manifest, &manifest)
		
		navInfo := store.ModuleNav{
			ModuleID:    id,
			Title:       module.Name,
			Section:     "SERVICES",
			Icon:        module.Icon,
			DefaultPath: "/" + id,
			SortOrder:   50,
			NavItems:    json.RawMessage("[]"),
			Routes:      json.RawMessage("[]"),
		}

		// nav 정보 저장
		if err := d.Store.SaveModuleNav(r.Context(), navInfo); err != nil {
			log.Error().Err(err).Msg("installModule: failed to save nav info")
		}

		// status를 active로 변경
		if err := d.Store.UpdateModuleStatus(r.Context(), id, "active"); err != nil {
			log.Error().Err(err).Msg("installModule: failed to update status to active")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "설치 완료 상태 업데이트 실패")
			return
		}

		// 설치 완료 이벤트 기록
		err = d.Store.CreateModuleEvent(r.Context(), id, "install", "completed",
			"Module installation completed", map[string]any{
				"version": module.Version,
				"image":   module.Image,
			})
		if err != nil {
			log.Error().Err(err).Msg("installModule: failed to create completion event")
		}

		log.Info().Str("module_id", id).Msg("Module installed successfully")

		httputil.RespondOK(w, map[string]any{
			"status": "installed",
			"module": map[string]any{
				"id":     id,
				"status": "active",
			},
		})
	}
}

// uninstallModule uninstalls a module.
func uninstallModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "데이터베이스를 사용할 수 없습니다")
			return
		}

		id := chi.URLParam(r, "id")

		// 모듈 존재 확인
		module, err := d.Store.GetModule(r.Context(), id)
		if err != nil {
			httputil.RespondError(w, http.StatusNotFound, "NOT_FOUND", "모듈을 찾을 수 없습니다: "+id)
			return
		}

		if module.Status != "active" {
			httputil.RespondError(w, http.StatusConflict, "NOT_INSTALLED", "설치되지 않은 모듈입니다")
			return
		}

		// TODO: Phase 2에서 역의존성 체크 구현
		// 현재는 기본 검사만

		// 제거 시작 이벤트 기록
		err = d.Store.CreateModuleEvent(r.Context(), id, "uninstall", "started",
			"Module uninstallation started", nil)
		if err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to create start event")
		}

		// status를 uninstalling으로 변경
		if err := d.Store.UpdateModuleStatus(r.Context(), id, "uninstalling"); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to update status")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "제거 상태 업데이트 실패")
			return
		}

		// TODO: Phase 2에서 실제 K8s 리소스 삭제 구현

		// nav 정보 삭제 (CASCADE로 자동 삭제되지만 명시적 호출)
		if err := d.Store.DeleteModuleNav(r.Context(), id); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to delete nav")
		}

		// 모듈 레코드 삭제
		if err := d.Store.DeleteModule(r.Context(), id); err != nil {
			log.Error().Err(err).Msg("uninstallModule: failed to delete module")
			httputil.RespondError(w, http.StatusInternalServerError, "DB_ERROR", "모듈 삭제 실패")
			return
		}

		log.Info().Str("module_id", id).Msg("Module uninstalled successfully")

		httputil.RespondOK(w, map[string]any{
			"status": "uninstalled",
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