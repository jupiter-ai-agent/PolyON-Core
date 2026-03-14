package api

import (
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
