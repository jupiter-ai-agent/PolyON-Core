package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	goldap "github.com/go-ldap/ldap/v3"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineLDAP registers LDAP diagnostic/management routes for AppEngine.
func RegisterAppEngineLDAP(r chi.Router, d *Deps) {
	r.Route("/appengine/ldap", func(r chi.Router) {
		r.Get("/config", getAppEngineLDAPConfig(d))
		r.Put("/config/{id}", updateAppEngineLDAPConfig(d))
		r.Post("/test-connection", testAppEngineLDAPConnection(d))
		r.Get("/users", listAppEngineLDAPUsers(d))
		r.Get("/groups", listAppEngineLDAPGroups(d))
		r.Post("/sync/{id}", syncAppEngineLDAPUsers(d))
		r.Post("/import/{id}", importAppEngineLDAPUsers(d))

		// Sync Wizard endpoints
		r.Route("/wizard", func(r chi.Router) {
			r.Get("/", getAppEngineLDAPWizard(d))
			r.Post("/refresh", refreshAppEngineLDAPWizard(d))
			r.Get("/groups", getAppEngineLDAPWizardGroups(d))
			r.Put("/groups", updateAppEngineLDAPWizardGroups(d))
			r.Get("/users", getAppEngineLDAPWizardUsers(d))
			r.Put("/users", updateAppEngineLDAPWizardUsers(d))
			r.Get("/schedule", getAppEngineLDAPWizardSchedule(d))
			r.Put("/schedule", updateAppEngineLDAPWizardSchedule(d))
			r.Post("/sync", syncAppEngineLDAPWizard(d))
		})
	})
}

// ── LDAP config fields we read from Odoo res.company.ldap ────────────────────

var ldapConfigFields = []string{
	// Odoo base auth_ldap fields
	"ldap_server", "ldap_server_port", "ldap_tls",
	"ldap_binddn",
	"ldap_base", "ldap_filter",
	"create_user",
	// polyon_ldap_connector extended fields
	"users_dn", "auth_search_filter", "user_search_filter",
	"ldap_attr_login", "ldap_attr_email",
	"ldap_attr_firstname", "ldap_attr_middlename",
	"ldap_attr_lastname", "ldap_attr_fullname",
	"ldap_attr_jobtitle", "ldap_attr_photo",
	"groups_dn", "group_filter", "group_attribute",
	"sync_groups", "create_role_per_group",
	"last_sync_date", "last_sync_status", "last_sync_user_count",
}

// ldapConfigResponse mirrors res.company.ldap fields for the Console.
type ldapConfigResponse struct {
	ID int64 `json:"id"`

	// Server Information
	LDAPServer     string `json:"ldap_server"`
	LDAPServerPort int    `json:"ldap_server_port"`
	LDAPTls        bool   `json:"ldap_tls"`

	// Login Information
	LDAPBindDN string `json:"ldap_binddn"`

	// Process Parameter
	LDAPBase   string `json:"ldap_base"`
	LDAPFilter string `json:"ldap_filter"`
	CreateUser bool   `json:"create_user"`

	// Users
	UsersDN          string `json:"users_dn"`
	AuthSearchFilter string `json:"auth_search_filter"`
	UserSearchFilter string `json:"user_search_filter"`

	// User Mapping
	AttrLogin      string `json:"ldap_attr_login"`
	AttrEmail      string `json:"ldap_attr_email"`
	AttrFirstName  string `json:"ldap_attr_firstname"`
	AttrMiddleName string `json:"ldap_attr_middlename"`
	AttrLastName   string `json:"ldap_attr_lastname"`
	AttrFullName   string `json:"ldap_attr_fullname"`
	AttrJobTitle   string `json:"ldap_attr_jobtitle"`
	AttrPhoto      string `json:"ldap_attr_photo"`

	// Groups
	GroupsDN           string `json:"groups_dn"`
	GroupFilter        string `json:"group_filter"`
	GroupAttribute     string `json:"group_attribute"`
	SyncGroups         bool   `json:"sync_groups"`
	CreateRolePerGroup bool   `json:"create_role_per_group"`

	// Sync Status
	LastSyncDate      string `json:"last_sync_date"`
	LastSyncStatus    string `json:"last_sync_status"`
	LastSyncUserCount int    `json:"last_sync_user_count"`
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func odooStr(m map[string]interface{}, key string) string {
	v := m[key]
	if v == nil || v == false {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func odooBool(m map[string]interface{}, key string) bool {
	v := m[key]
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func odooInt(m map[string]interface{}, key string) int {
	v := m[key]
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func odooID(m map[string]interface{}) int64 {
	v := m["id"]
	if n, ok := v.(float64); ok {
		return int64(n)
	}
	return 0
}

func mapToLDAPConfig(m map[string]interface{}) ldapConfigResponse {
	return ldapConfigResponse{
		ID:             odooID(m),
		LDAPServer:     odooStr(m, "ldap_server"),
		LDAPServerPort: odooInt(m, "ldap_server_port"),
		LDAPTls:        odooBool(m, "ldap_tls"),
		LDAPBindDN:     odooStr(m, "ldap_binddn"),
		LDAPBase:       odooStr(m, "ldap_base"),
		LDAPFilter:     odooStr(m, "ldap_filter"),
		CreateUser:     odooBool(m, "create_user"),

		UsersDN:          odooStr(m, "users_dn"),
		AuthSearchFilter: odooStr(m, "auth_search_filter"),
		UserSearchFilter: odooStr(m, "user_search_filter"),

		AttrLogin:      odooStr(m, "ldap_attr_login"),
		AttrEmail:      odooStr(m, "ldap_attr_email"),
		AttrFirstName:  odooStr(m, "ldap_attr_firstname"),
		AttrMiddleName: odooStr(m, "ldap_attr_middlename"),
		AttrLastName:   odooStr(m, "ldap_attr_lastname"),
		AttrFullName:   odooStr(m, "ldap_attr_fullname"),
		AttrJobTitle:   odooStr(m, "ldap_attr_jobtitle"),
		AttrPhoto:      odooStr(m, "ldap_attr_photo"),

		GroupsDN:           odooStr(m, "groups_dn"),
		GroupFilter:        odooStr(m, "group_filter"),
		GroupAttribute:     odooStr(m, "group_attribute"),
		SyncGroups:         odooBool(m, "sync_groups"),
		CreateRolePerGroup: odooBool(m, "create_role_per_group"),

		LastSyncDate:      odooStr(m, "last_sync_date"),
		LastSyncStatus:    odooStr(m, "last_sync_status"),
		LastSyncUserCount: odooInt(m, "last_sync_user_count"),
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// GET /appengine/ldap/config
// Returns all res.company.ldap records from Odoo.
func getAppEngineLDAPConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		records, err := d.OdooClient.SearchRead(
			"res.company.ldap",
			[]interface{}{},
			ldapConfigFields,
			0, 10,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Odoo 조회 실패: %v", err))
			return
		}

		configs := make([]ldapConfigResponse, 0, len(records))
		for _, rec := range records {
			configs = append(configs, mapToLDAPConfig(rec))
		}

		httputil.RespondOK(w, map[string]interface{}{
			"configs": configs,
			"total":   len(configs),
		})
	}
}

// PUT /appengine/ldap/config/{id}
// Updates res.company.ldap record in Odoo.
func updateAppEngineLDAPConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		var id int
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_id", "유효하지 않은 LDAP 설정 ID")
			return
		}

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_body", "요청 본문 파싱 실패")
			return
		}

		// Whitelist updatable fields
		allowed := map[string]bool{
			"users_dn": true, "auth_search_filter": true, "user_search_filter": true,
			"ldap_attr_login": true, "ldap_attr_email": true,
			"ldap_attr_firstname": true, "ldap_attr_middlename": true,
			"ldap_attr_lastname": true, "ldap_attr_fullname": true,
			"ldap_attr_jobtitle": true, "ldap_attr_photo": true,
			"groups_dn": true, "group_filter": true, "group_attribute": true,
			"sync_groups": true, "create_role_per_group": true,
		}
		vals := map[string]interface{}{}
		for k, v := range body {
			if allowed[k] {
				vals[k] = v
			}
		}

		_, err := d.OdooClient.Call(
			"res.company.ldap", "write",
			[]interface{}{[]int{id}, vals},
			nil,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Odoo 업데이트 실패: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]string{"status": "updated"})
	}
}

// POST /appengine/ldap/test-connection
// Tests LDAP connectivity using Core's LDAP client (using PP AD config).
func testAppEngineLDAPConnection(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ldapURL := d.Cfg.LDAPURL
		if ldapURL == "" {
			sambaHost := d.Cfg.SambaHost
			if sambaHost == "" {
				sambaHost = "polyon-dc"
			}
			ldapURL = fmt.Sprintf("ldap://%s:389", sambaHost)
		}

		conn, err := goldap.DialURL(ldapURL)
		if err != nil {
			httputil.RespondOK(w, map[string]interface{}{
				"success":    false,
				"message":    fmt.Sprintf("연결 실패: %v", err),
				"latency_ms": time.Since(start).Milliseconds(),
			})
			return
		}
		defer conn.Close()

		if err := conn.Bind(d.Cfg.AdminDN(), d.Cfg.DCAdminPassword); err != nil {
			httputil.RespondOK(w, map[string]interface{}{
				"success":    false,
				"message":    fmt.Sprintf("인증 실패: %v", err),
				"latency_ms": time.Since(start).Milliseconds(),
			})
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success":    true,
			"message":    fmt.Sprintf("연결 성공 (%s)", ldapURL),
			"latency_ms": time.Since(start).Milliseconds(),
		})
	}
}

// GET /appengine/ldap/users?ldap_id=1
// Calls action_test_ldap_users on Odoo and returns the wizard line results.
func listAppEngineLDAPUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID := 0
		if v := r.URL.Query().Get("ldap_id"); v != "" {
			fmt.Sscanf(v, "%d", &ldapID)
		}
		if ldapID == 0 {
			// use first available LDAP config
			recs, err := d.OdooClient.SearchRead("res.company.ldap", []interface{}{}, []string{"id"}, 0, 1)
			if err != nil || len(recs) == 0 {
				httputil.RespondError(w, http.StatusBadGateway, "odoo_error", "LDAP 설정을 찾을 수 없습니다")
				return
			}
			ldapID = int(odooID(recs[0]))
		}

		// Call action_test_ldap_users → returns act_window with res_id (wizard id)
		result, err := d.OdooClient.Call(
			"res.company.ldap", "action_test_ldap_users",
			[]interface{}{[]int{ldapID}},
			nil,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Odoo 사용자 테스트 실패: %v", err))
			return
		}

		// Parse wizard ID from the action result
		wizardID := extractWizardID(result)
		if wizardID == 0 {
			// Possibly an error notification was returned
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", "LDAP 사용자 wizard 생성 실패 — LDAP 연결을 확인하세요")
			return
		}

		// Read wizard lines
		lines, err := d.OdooClient.SearchRead(
			"ldap.test.users.wizard.line",
			[]interface{}{[]interface{}{"wizard_id", "=", wizardID}},
			[]string{"screen_name", "email", "first_name", "last_name", "job_title", "group_count", "is_complete"},
			0, 500,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("wizard 결과 조회 실패: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"users": lines,
			"total": len(lines),
		})
	}
}

// GET /appengine/ldap/groups?ldap_id=1
// Calls action_test_ldap_groups on Odoo and returns the wizard line results.
func listAppEngineLDAPGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID := 0
		if v := r.URL.Query().Get("ldap_id"); v != "" {
			fmt.Sscanf(v, "%d", &ldapID)
		}
		if ldapID == 0 {
			recs, err := d.OdooClient.SearchRead("res.company.ldap", []interface{}{}, []string{"id"}, 0, 1)
			if err != nil || len(recs) == 0 {
				httputil.RespondError(w, http.StatusBadGateway, "odoo_error", "LDAP 설정을 찾을 수 없습니다")
				return
			}
			ldapID = int(odooID(recs[0]))
		}

		result, err := d.OdooClient.Call(
			"res.company.ldap", "action_test_ldap_groups",
			[]interface{}{[]int{ldapID}},
			nil,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Odoo 그룹 테스트 실패: %v", err))
			return
		}

		wizardID := extractWizardID(result)
		if wizardID == 0 {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", "LDAP 그룹 wizard 생성 실패 — LDAP 연결을 확인하세요")
			return
		}

		lines, err := d.OdooClient.SearchRead(
			"ldap.test.groups.wizard.line",
			[]interface{}{[]interface{}{"wizard_id", "=", wizardID}},
			[]string{"sequence", "name", "description", "member_count"},
			0, 500,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("wizard 결과 조회 실패: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"groups": lines,
			"total":  len(lines),
		})
	}
}

// POST /appengine/ldap/sync/{id}
// Calls action_sync_selected on the ldap.sync.wizard for the given LDAP record.
func syncAppEngineLDAPUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		var ldapID int
		if _, err := fmt.Sscanf(idStr, "%d", &ldapID); err != nil || ldapID <= 0 {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_id", "유효하지 않은 LDAP 설정 ID")
			return
		}

		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}

		_, err = d.OdooClient.Call(
			"ldap.sync.wizard", "action_sync_selected",
			[]interface{}{[]int{wizardID}},
			nil,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("동기화 실패: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{"status": "ok"})
	}
}

// POST /appengine/ldap/import/{id}
// Calls action_refresh_from_ldap + action_sync_selected on the ldap.sync.wizard.
func importAppEngineLDAPUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		var ldapID int
		if _, err := fmt.Sscanf(idStr, "%d", &ldapID); err != nil || ldapID <= 0 {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_id", "유효하지 않은 LDAP 설정 ID")
			return
		}

		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}

		// Refresh from LDAP then sync
		if _, err = d.OdooClient.Call(
			"ldap.sync.wizard", "action_refresh_from_ldap",
			[]interface{}{[]int{wizardID}},
			nil,
		); err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("LDAP 갱신 실패: %v", err))
			return
		}
		if _, err = d.OdooClient.Call(
			"ldap.sync.wizard", "action_sync_selected",
			[]interface{}{[]int{wizardID}},
			nil,
		); err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("동기화 실패: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{"status": "ok"})
	}
}

// getOrCreateSyncWizard returns an existing ldap.sync.wizard ID for the given
// LDAP record, or creates a new one if none exists.
func getOrCreateSyncWizard(d *Deps, ldapID int) (int, error) {
	wizards, err := d.OdooClient.SearchRead(
		"ldap.sync.wizard",
		[]interface{}{[]interface{}{"ldap_id", "=", ldapID}},
		[]string{"id"},
		0, 1,
	)
	if err != nil {
		return 0, err
	}
	if len(wizards) > 0 {
		return int(odooID(wizards[0])), nil
	}

	// Create wizard
	result, err := d.OdooClient.Call(
		"ldap.sync.wizard", "create",
		[]interface{}{map[string]interface{}{"ldap_id": ldapID}},
		nil,
	)
	if err != nil {
		return 0, err
	}
	if id, ok := result.(float64); ok {
		return int(id), nil
	}
	return 0, fmt.Errorf("wizard 생성 결과 파싱 실패")
}

// ── Utility ──────────────────────────────────────────────────────────────────

// extractWizardID parses the wizard res_id from an Odoo act_window action result.
func extractWizardID(result interface{}) int {
	if result == nil {
		return 0
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return 0
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}
	if v, ok := m["res_id"]; ok {
		if n, ok := v.(float64); ok {
			return int(n)
		}
	}
	return 0
}

// ── Sync Wizard Endpoints ────────────────────────────────────────────────────

var wizardFields = []string{
	"id", "ldap_id", "ldap_server_name", "user_count", "group_count",
	"sync_user_count", "selected_group_count", "sync_enabled", "sync_interval",
	"last_sync_date", "last_sync_status", "last_sync_user_count",
}

var wizardGroupFields = []string{
	"id", "wizard_id", "selected", "sequence", "name", "description",
	"member_count", "ldap_dn",
}

var wizardUserFields = []string{
	"id", "wizard_id", "sync_mode", "is_sync_target", "screen_name",
	"email", "first_name", "last_name", "job_title", "group_count",
	"ldap_dn",
}

// resolveWizardLDAPID extracts ldap_id from query param; falls back to first LDAP record.
func resolveWizardLDAPID(d *Deps, r *http.Request) (int, error) {
	ldapID := 0
	if v := r.URL.Query().Get("ldap_id"); v != "" {
		fmt.Sscanf(v, "%d", &ldapID)
	}
	if ldapID == 0 {
		recs, err := d.OdooClient.SearchRead("res.company.ldap", []interface{}{}, []string{"id"}, 0, 1)
		if err != nil || len(recs) == 0 {
			return 0, fmt.Errorf("LDAP 설정을 찾을 수 없습니다")
		}
		ldapID = int(odooID(recs[0]))
	}
	return ldapID, nil
}

// GET /appengine/ldap/wizard?ldap_id=N
func getAppEngineLDAPWizard(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}
		records, err := d.OdooClient.SearchRead(
			"ldap.sync.wizard",
			[]interface{}{[]interface{}{"id", "=", wizardID}},
			wizardFields, 0, 1,
		)
		if err != nil || len(records) == 0 {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", "wizard 조회 실패")
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"wizard": records[0]})
	}
}

// POST /appengine/ldap/wizard/refresh?ldap_id=N
func refreshAppEngineLDAPWizard(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}
		if _, err = d.OdooClient.Call(
			"ldap.sync.wizard", "action_refresh_from_ldap",
			[]interface{}{[]int{wizardID}}, nil,
		); err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("LDAP 갱신 실패: %v", err))
			return
		}
		records, err := d.OdooClient.SearchRead(
			"ldap.sync.wizard",
			[]interface{}{[]interface{}{"id", "=", wizardID}},
			wizardFields, 0, 1,
		)
		if err != nil || len(records) == 0 {
			httputil.RespondOK(w, map[string]interface{}{"status": "ok"})
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"wizard": records[0], "status": "ok"})
	}
}

// GET /appengine/ldap/wizard/groups?ldap_id=N
func getAppEngineLDAPWizardGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}
		lines, err := d.OdooClient.SearchRead(
			"ldap.sync.wizard.group.line",
			[]interface{}{[]interface{}{"wizard_id", "=", wizardID}},
			wizardGroupFields, 0, 500,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("그룹 목록 조회 실패: %v", err))
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"groups": lines, "total": len(lines)})
	}
}

// PUT /appengine/ldap/wizard/groups?ldap_id=N
// Body: {"select_all": true} | {"deselect_all": true} | {"groups": [{"id":1,"selected":true}]}
func updateAppEngineLDAPWizardGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}

		var body struct {
			SelectAll   bool `json:"select_all"`
			DeselectAll bool `json:"deselect_all"`
			Groups      []struct {
				ID       int  `json:"id"`
				Selected bool `json:"selected"`
			} `json:"groups"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_body", "요청 본문 파싱 실패")
			return
		}

		if body.SelectAll || body.DeselectAll {
			lines, err := d.OdooClient.SearchRead(
				"ldap.sync.wizard.group.line",
				[]interface{}{[]interface{}{"wizard_id", "=", wizardID}},
				[]string{"id"}, 0, 500,
			)
			if err != nil {
				httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("그룹 라인 조회 실패: %v", err))
				return
			}
			ids := make([]int, 0, len(lines))
			for _, l := range lines {
				ids = append(ids, int(odooID(l)))
			}
			if len(ids) > 0 {
				if _, err = d.OdooClient.Call(
					"ldap.sync.wizard.group.line", "write",
					[]interface{}{ids, map[string]interface{}{"selected": body.SelectAll}},
					nil,
				); err != nil {
					httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("그룹 선택 변경 실패: %v", err))
					return
				}
			}
		} else if len(body.Groups) > 0 {
			for _, g := range body.Groups {
				if _, err = d.OdooClient.Call(
					"ldap.sync.wizard.group.line", "write",
					[]interface{}{[]int{g.ID}, map[string]interface{}{"selected": g.Selected}},
					nil,
				); err != nil {
					httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("그룹 %d 변경 실패: %v", g.ID, err))
					return
				}
			}
		}

		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// GET /appengine/ldap/wizard/users?ldap_id=N
func getAppEngineLDAPWizardUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}
		lines, err := d.OdooClient.SearchRead(
			"ldap.sync.wizard.user.line",
			[]interface{}{[]interface{}{"wizard_id", "=", wizardID}},
			wizardUserFields, 0, 1000,
		)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("사용자 목록 조회 실패: %v", err))
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"users": lines, "total": len(lines)})
	}
}

// PUT /appengine/ldap/wizard/users?ldap_id=N
// Body: {"set_all": "group|enable|disable"} | {"users": [{"id":1,"sync_mode":"enable"}]}
func updateAppEngineLDAPWizardUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}

		var body struct {
			SetAll string `json:"set_all"`
			Users  []struct {
				ID       int    `json:"id"`
				SyncMode string `json:"sync_mode"`
			} `json:"users"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_body", "요청 본문 파싱 실패")
			return
		}

		if body.SetAll != "" {
			lines, err := d.OdooClient.SearchRead(
				"ldap.sync.wizard.user.line",
				[]interface{}{[]interface{}{"wizard_id", "=", wizardID}},
				[]string{"id"}, 0, 1000,
			)
			if err != nil {
				httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("사용자 라인 조회 실패: %v", err))
				return
			}
			ids := make([]int, 0, len(lines))
			for _, l := range lines {
				ids = append(ids, int(odooID(l)))
			}
			if len(ids) > 0 {
				if _, err = d.OdooClient.Call(
					"ldap.sync.wizard.user.line", "write",
					[]interface{}{ids, map[string]interface{}{"sync_mode": body.SetAll}},
					nil,
				); err != nil {
					httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("사용자 정책 일괄 변경 실패: %v", err))
					return
				}
			}
		} else if len(body.Users) > 0 {
			for _, u := range body.Users {
				if _, err = d.OdooClient.Call(
					"ldap.sync.wizard.user.line", "write",
					[]interface{}{[]int{u.ID}, map[string]interface{}{"sync_mode": u.SyncMode}},
					nil,
				); err != nil {
					httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("사용자 %d 변경 실패: %v", u.ID, err))
					return
				}
			}
		}

		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// GET /appengine/ldap/wizard/schedule?ldap_id=N
func getAppEngineLDAPWizardSchedule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}
		records, err := d.OdooClient.SearchRead(
			"ldap.sync.wizard",
			[]interface{}{[]interface{}{"id", "=", wizardID}},
			[]string{"id", "sync_enabled", "sync_interval", "last_sync_date", "last_sync_status", "last_sync_user_count"},
			0, 1,
		)
		if err != nil || len(records) == 0 {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", "스케줄 조회 실패")
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"schedule": records[0]})
	}
}

// PUT /appengine/ldap/wizard/schedule?ldap_id=N
// Body: {"sync_enabled": true, "sync_interval": 60}
func updateAppEngineLDAPWizardSchedule(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}

		var body struct {
			SyncEnabled  bool `json:"sync_enabled"`
			SyncInterval int  `json:"sync_interval"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "invalid_body", "요청 본문 파싱 실패")
			return
		}

		if _, err = d.OdooClient.Call(
			"ldap.sync.wizard", "write",
			[]interface{}{[]int{wizardID}, map[string]interface{}{
				"sync_enabled":  body.SyncEnabled,
				"sync_interval": body.SyncInterval,
			}},
			nil,
		); err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("스케줄 저장 실패: %v", err))
			return
		}
		httputil.RespondOK(w, map[string]string{"status": "ok"})
	}
}

// POST /appengine/ldap/wizard/sync?ldap_id=N
func syncAppEngineLDAPWizard(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapID, err := resolveWizardLDAPID(d, r)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", err.Error())
			return
		}
		wizardID, err := getOrCreateSyncWizard(d, ldapID)
		if err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("Sync wizard 조회 실패: %v", err))
			return
		}
		if _, err = d.OdooClient.Call(
			"ldap.sync.wizard", "action_sync_selected",
			[]interface{}{[]int{wizardID}}, nil,
		); err != nil {
			httputil.RespondError(w, http.StatusBadGateway, "odoo_error", fmt.Sprintf("동기화 실패: %v", err))
			return
		}
		// Stalwart에 AD 사용자 동기화 (Option C: internal directory + LDAP auth)
		go syncWizardUsersToStalwart(d, ldapID)
		httputil.RespondOK(w, map[string]interface{}{"status": "ok"})
	}
}

// syncWizardUsersToStalwart: Sync Wizard의 is_sync_target=true 사용자를 Stalwart internal directory에 등록.
// 비밀번호 없이 등록하여 LDAP 인증에만 의존 (Option C).
func syncWizardUsersToStalwart(d *Deps, ldapID int) {
	if d.OdooClient == nil || d.Cfg.StalwartURL == "" {
		return
	}

	// DB에서 mail_domain 조회
	mailDomain := ""
	if d.Store != nil {
		if configs, err := d.Store.GetConfigs(context.Background(), []string{"mail_domain"}); err == nil {
			mailDomain = configs["mail_domain"]
		}
	}

	// 1. Sync Target 사용자 조회
	lines, err := d.OdooClient.SearchRead(
		"ldap.sync.wizard.user.line",
		[]interface{}{
			[]interface{}{"is_sync_target", "=", true},
		},
		[]string{"screen_name", "email", "first_name", "last_name"},
		0, 0,
	)
	if err != nil {
		return
	}

	// 2. Stalwart에 사용자 등록 (없으면 생성, 있으면 업데이트)
	for _, line := range lines {
		login, _ := line["screen_name"].(string)
		email, _ := line["email"].(string)
		firstName, _ := line["first_name"].(string)
		lastName, _ := line["last_name"].(string)

		if login == "" {
			continue
		}
		if email == "" {
			domain := mailDomain
			if domain == "" {
				domain = "cmars.com"
			}
			email = login + "@" + domain
		}

		name := firstName + " " + lastName
		if name == " " {
			name = login
		}

		// GET으로 존재 확인
		resp, err := stalwartDo(d, "GET", "/api/principal/"+login, nil)
		if err != nil {
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == 200 {
			// 이미 존재하면 PATCH
			pr, _ := stalwartDo(d, "PATCH", "/api/principal/"+login, []map[string]interface{}{
				{"action": "set", "field": "displayName", "value": name},
				{"action": "set", "field": "email", "value": []string{email}},
			})
			if pr != nil {
				pr.Body.Close()
			}
		} else if resp.StatusCode == 404 {
			// 신규 생성 (비밀번호 없음 - LDAP 인증 전용)
			r2, err2 := stalwartDo(d, "POST", "/api/principal", map[string]interface{}{
				"type":        "individual",
				"name":        login,
				"displayName": name,
				"emails":      []string{email},
				"secrets":     []string{},
			})
			if err2 == nil && r2 != nil {
				r2.Body.Close()
			}
		}
	}

	// 3. non-target 사용자 Stalwart에서 삭제
	nonTargets, err := d.OdooClient.SearchRead(
		"ldap.sync.wizard.user.line",
		[]interface{}{
			[]interface{}{"is_sync_target", "=", false},
		},
		[]string{"screen_name"},
		0, 0,
	)
	if err == nil {
		for _, line := range nonTargets {
			login, _ := line["screen_name"].(string)
			if login == "" {
				continue
			}
			r, err := stalwartDo(d, "DELETE", "/api/principal/"+login, nil)
			if err == nil && r != nil {
				r.Body.Close()
			}
		}
	}
}



// extractNotificationMessage pulls the human-readable message from an Odoo
// display_notification action result.
func extractNotificationMessage(result interface{}) string {
	if result == nil {
		return ""
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if params, ok := m["params"].(map[string]interface{}); ok {
		if msg, ok := params["message"].(string); ok {
			return msg
		}
	}
	return ""
}
