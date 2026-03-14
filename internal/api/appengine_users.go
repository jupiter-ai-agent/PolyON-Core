package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineUsers registers AppEngine user management routes.
func RegisterAppEngineUsers(r chi.Router, d *Deps) {
	r.Route("/appengine/users", func(r chi.Router) {
		r.Get("/", listAppEngineUsers(d))
		r.Get("/{id}", getAppEngineUser(d))
		r.Put("/{id}/groups", updateAppEngineUserGroups(d))
	})
	r.Route("/appengine/groups", func(r chi.Router) {
		r.Get("/", listAppEngineGroups(d))
	})
}

// odooUserRecord maps Odoo res.users fields we care about.
type odooUserRecord struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	Login    string      `json:"login"`
	Email    interface{} `json:"email"`
	Active   bool        `json:"active"`
	GroupIDs interface{} `json:"group_ids"`
}

// odooGroupRecord maps Odoo res.groups fields.
type odooGroupRecord struct {
	ID         int64       `json:"id"`
	Name       string      `json:"name"`
	FullName   interface{} `json:"full_name"`
	CategoryID interface{} `json:"category_id"`
}

// toOdooUser converts a raw Odoo map to odooUserRecord.
func toOdooUser(m map[string]interface{}) odooUserRecord {
	u := odooUserRecord{}
	if v, ok := m["id"]; ok {
		switch x := v.(type) {
		case float64:
			u.ID = int64(x)
		}
	}
	if v, ok := m["name"].(string); ok {
		u.Name = v
	}
	if v, ok := m["login"].(string); ok {
		u.Login = v
	}
	u.Email = m["email"]
	if v, ok := m["active"].(bool); ok {
		u.Active = v
	}
	u.GroupIDs = m["group_ids"]
	return u
}

// toOdooGroup converts a raw Odoo map to odooGroupRecord.
func toOdooGroup(m map[string]interface{}) odooGroupRecord {
	g := odooGroupRecord{}
	if v, ok := m["id"]; ok {
		switch x := v.(type) {
		case float64:
			g.ID = int64(x)
		}
	}
	if v, ok := m["name"].(string); ok {
		g.Name = v
	}
	g.FullName = m["full_name"]
	g.CategoryID = m["category_id"]
	return g
}

// syncedUserRecord represents an Odoo user imported via Sync Wizard policy.
type syncedUserRecord struct {
	ID           int64       `json:"id"`
	Login        string      `json:"login"`
	Name         string      `json:"name"`
	Email        interface{} `json:"email"`
	Active       bool        `json:"active"`
	GroupIDs     interface{} `json:"group_ids,omitempty"`
	LdapID       interface{} `json:"ldap_id"`
	SyncMode     string      `json:"sync_mode"`     // group | enable | disable
	IsSyncTarget bool        `json:"is_sync_target"` // Sync Wizard 판정 결과
}

// listAppEngineUsers returns Odoo users imported via Sync Wizard policy (ldap_id set).
// Merges sync policy (sync_mode, is_sync_target) from ldap.sync.wizard.user.line.
func listAppEngineUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		// 1. Sync Wizard 정책으로 import된 사용자만 조회 (ldap_id != False)
		domain := []interface{}{
			[]interface{}{"ldap_id", "!=", false},
			[]interface{}{"share", "=", false},
		}
		fields := []string{"id", "name", "login", "email", "active", "group_ids", "ldap_id"}
		records, err := d.OdooClient.SearchRead("res.users", domain, fields, 0, 0)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("SearchRead users failed: %v", err))
			return
		}

		users := make([]syncedUserRecord, 0, len(records))
		loginIndex := make(map[string]int) // login → users 인덱스
		for _, rec := range records {
			u := toOdooUser(rec)
			sr := syncedUserRecord{
				ID:       u.ID,
				Login:    u.Login,
				Name:     u.Name,
				Email:    u.Email,
				Active:   u.Active,
				GroupIDs: u.GroupIDs,
				LdapID:   rec["ldap_id"],
				SyncMode: "group", // 기본값
			}
			loginIndex[u.Login] = len(users)
			users = append(users, sr)
		}

		// 2. Sync Wizard user lines에서 정책 정보 merge
		wizardLines, err := d.OdooClient.SearchRead(
			"ldap.sync.wizard.user.line",
			[]interface{}{},
			[]string{"screen_name", "sync_mode", "is_sync_target"},
			0, 0,
		)
		if err == nil {
			for _, line := range wizardLines {
				screenName, _ := line["screen_name"].(string)
				syncMode, _ := line["sync_mode"].(string)
				isSyncTarget, _ := line["is_sync_target"].(bool)
				if idx, ok := loginIndex[screenName]; ok {
					users[idx].SyncMode = syncMode
					users[idx].IsSyncTarget = isSyncTarget
				}
			}
		}

		// 3. is_sync_target = false인 사용자는 표시하지 않음
		// 이 페이지는 Sync Wizard 정책에 의해 실제 import된 계정만 관리
		imported := make([]syncedUserRecord, 0, len(users))
		for _, u := range users {
			if u.IsSyncTarget {
				imported = append(imported, u)
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"users":   imported,
			"count":   len(imported),
		})
	}
}

// getAppEngineUser returns a single Odoo user with group details.
func getAppEngineUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		idStr := chi.URLParam(r, "id")
		uid, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid user id")
			return
		}

		// Fetch user
		userDomain := []interface{}{[]interface{}{"id", "=", int(uid)}}
		userFields := []string{"id", "name", "login", "email", "active", "group_ids"}
		userRecords, err := d.OdooClient.SearchRead("res.users", userDomain, userFields, 0, 1)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("SearchRead users failed: %v", err))
			return
		}
		if len(userRecords) == 0 {
			httputil.RespondError(w, 404, "NOT_FOUND", "User not found")
			return
		}
		user := toOdooUser(userRecords[0])

		// Extract group IDs
		var groupIDs []int
		if ids, ok := user.GroupIDs.([]interface{}); ok {
			for _, gid := range ids {
				switch x := gid.(type) {
				case float64:
					groupIDs = append(groupIDs, int(x))
				}
			}
		}

		// Fetch group details
		var groups []odooGroupRecord
		if len(groupIDs) > 0 {
			groupDomain := []interface{}{[]interface{}{"id", "in", groupIDs}}
			groupFields := []string{"id", "name", "full_name", "category_id"}
			groupRecords, err := d.OdooClient.SearchRead("res.groups", groupDomain, groupFields, 0, 0)
			if err != nil {
				httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("SearchRead groups failed: %v", err))
				return
			}
			for _, g := range groupRecords {
				groups = append(groups, toOdooGroup(g))
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"user":    user,
			"groups":  groups,
		})
	}
}

// updateAppEngineUserGroups replaces the user's groups_id with the provided list.
func updateAppEngineUserGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		idStr := chi.URLParam(r, "id")
		uid, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid user id")
			return
		}

		var req struct {
			GroupIDs []int `json:"group_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}

		// Odoo write: (6, 0, ids) replaces the many2many
		groupCmd := []interface{}{[]interface{}{6, 0, req.GroupIDs}}
		vals := map[string]interface{}{
			"group_ids": groupCmd,
		}
		_, err = d.OdooClient.Call("res.users", "write", []interface{}{[]int{int(uid)}, vals}, nil)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("write groups failed: %v", err))
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"message": "그룹이 업데이트되었습니다.",
		})
	}
}

// listAppEngineGroups returns all available Odoo groups.
func listAppEngineGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.OdooClient == nil {
			httputil.RespondError(w, 503, "ODOO_UNAVAILABLE", "Odoo client not configured")
			return
		}

		fields := []string{"id", "name", "full_name", "category_id"}
		records, err := d.OdooClient.SearchRead("res.groups", []interface{}{}, fields, 0, 0)
		if err != nil {
			httputil.RespondError(w, 500, "ODOO_ERROR", fmt.Sprintf("SearchRead groups failed: %v", err))
			return
		}

		groups := make([]odooGroupRecord, 0, len(records))
		for _, g := range records {
			groups = append(groups, toOdooGroup(g))
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"groups":  groups,
			"count":   len(groups),
		})
	}
}
