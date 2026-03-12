package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineModules registers Odoo module management routes.
func RegisterAppEngineModules(r chi.Router, d *Deps) {
	r.Route("/appengine/modules", func(r chi.Router) {
		r.Get("/", listAppEngineModules(d))
		r.Post("/{name}/install", installAppEngineModule(d))
		r.Post("/{name}/upgrade", upgradeAppEngineModule(d))
		r.Post("/{name}/uninstall", uninstallAppEngineModule(d))
	})
}

// odooModuleRecord is the structure returned to the frontend.
type odooModuleRecord struct {
	ID          int         `json:"id"`
	Name        string      `json:"name"`
	ShortDesc   string      `json:"shortdesc"`
	State       string      `json:"state"`
	Author      string      `json:"author"`
	Description string      `json:"description"`
	CategoryID  interface{} `json:"category_id"`
	IconData    string      `json:"icon_image"`
}

// listAppEngineModules returns Odoo application modules (all states by default).
func listAppEngineModules(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "ODOO_UNAVAILABLE", "Odoo 클라이언트를 사용할 수 없습니다")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = ctx

		// 기본 domain: application=true (기술 모듈 제외, 앱만)
		domain := []interface{}{
			[]interface{}{"application", "=", true},
		}

		fields := []string{"id", "name", "shortdesc", "state", "author", "description", "category_id", "icon_image"}
		records, err := d.OdooClient.SearchRead("ir.module.module", domain, fields, 0, 500)
		if err != nil {
			log.Error().Err(err).Msg("listAppEngineModules: SearchRead failed")
			httputil.RespondError(w, http.StatusInternalServerError, "ODOO_ERROR", "Odoo 모듈 목록 조회 실패: "+err.Error())
			return
		}

		// 쿼리 파라미터: state 필터 (클라이언트 사이드 필터링 지원)
		stateFilter := r.URL.Query().Get("state")

		modules := make([]odooModuleRecord, 0, len(records))
		for _, rec := range records {
			m := odooModuleRecord{}
			if v, ok := rec["id"].(float64); ok {
				m.ID = int(v)
			}
			if v, ok := rec["name"].(string); ok {
				m.Name = v
			}
			if v, ok := rec["shortdesc"].(string); ok {
				m.ShortDesc = v
			}
			if v, ok := rec["state"].(string); ok {
				m.State = v
			}
			if v, ok := rec["author"].(string); ok {
				m.Author = v
			}
			if v, ok := rec["description"].(string); ok {
				m.Description = v
			}
			m.CategoryID = rec["category_id"]
			if v, ok := rec["icon_image"].(string); ok {
				m.IconData = v
			}

			// state 필터 적용 (서버 사이드)
			if stateFilter != "" && stateFilter != "all" {
				if m.State != stateFilter {
					continue
				}
			}

			modules = append(modules, m)
		}

		httputil.RespondOK(w, map[string]interface{}{
			"modules": modules,
			"total":   len(modules),
		})
	}
}

// installAppEngineModule installs an Odoo module by technical name.
func installAppEngineModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "ODOO_UNAVAILABLE", "Odoo 클라이언트를 사용할 수 없습니다")
			return
		}

		name := chi.URLParam(r, "name")
		if name == "" {
			httputil.RespondError(w, http.StatusBadRequest, "MISSING_NAME", "모듈 이름이 필요합니다")
			return
		}

		// 모듈 ID 조회
		ids, err := findModuleIDs(d, name)
		if err != nil || len(ids) == 0 {
			httputil.RespondError(w, http.StatusNotFound, "MODULE_NOT_FOUND", fmt.Sprintf("모듈 '%s'을 찾을 수 없습니다", name))
			return
		}

		// button_immediate_install 호출 (60s timeout 사용)
		d.OdooClient.Call("ir.module.module", "button_immediate_install", []interface{}{ids}, nil)

		log.Info().Str("module", name).Ints("ids", ids).Msg("AppEngine module install triggered")
		httputil.RespondOK(w, map[string]interface{}{
			"status": "triggered",
			"module": name,
			"action": "install",
		})
	}
}

// upgradeAppEngineModule upgrades an installed Odoo module.
func upgradeAppEngineModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "ODOO_UNAVAILABLE", "Odoo 클라이언트를 사용할 수 없습니다")
			return
		}

		name := chi.URLParam(r, "name")
		if name == "" {
			httputil.RespondError(w, http.StatusBadRequest, "MISSING_NAME", "모듈 이름이 필요합니다")
			return
		}

		ids, err := findModuleIDs(d, name)
		if err != nil || len(ids) == 0 {
			httputil.RespondError(w, http.StatusNotFound, "MODULE_NOT_FOUND", fmt.Sprintf("모듈 '%s'을 찾을 수 없습니다", name))
			return
		}

		// button_immediate_upgrade 호출
		d.OdooClient.Call("ir.module.module", "button_immediate_upgrade", []interface{}{ids}, nil)

		log.Info().Str("module", name).Ints("ids", ids).Msg("AppEngine module upgrade triggered")
		httputil.RespondOK(w, map[string]interface{}{
			"status": "triggered",
			"module": name,
			"action": "upgrade",
		})
	}
}

// uninstallAppEngineModule uninstalls an Odoo module by technical name.
func uninstallAppEngineModule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, http.StatusServiceUnavailable, "ODOO_UNAVAILABLE", "Odoo 클라이언트를 사용할 수 없습니다")
			return
		}

		name := chi.URLParam(r, "name")
		if name == "" {
			httputil.RespondError(w, http.StatusBadRequest, "MISSING_NAME", "모듈 이름이 필요합니다")
			return
		}

		ids, err := findModuleIDs(d, name)
		if err != nil || len(ids) == 0 {
			httputil.RespondError(w, http.StatusNotFound, "MODULE_NOT_FOUND", fmt.Sprintf("모듈 '%s'을 찾을 수 없습니다", name))
			return
		}

		// button_immediate_uninstall 호출
		d.OdooClient.Call("ir.module.module", "button_immediate_uninstall", []interface{}{ids}, nil)

		log.Info().Str("module", name).Ints("ids", ids).Msg("AppEngine module uninstall triggered")
		httputil.RespondOK(w, map[string]interface{}{
			"status": "triggered",
			"module": name,
			"action": "uninstall",
		})
	}
}

// findModuleIDs returns the DB IDs for an Odoo module by technical name.
func findModuleIDs(d *Deps, name string) ([]int, error) {
	domain := []interface{}{
		[]interface{}{"name", "=", name},
	}
	records, err := d.OdooClient.SearchRead("ir.module.module", domain, []string{"id"}, 0, 10)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(records))
	for _, rec := range records {
		if v, ok := rec["id"].(float64); ok {
			ids = append(ids, int(v))
		}
	}
	return ids, nil
}
