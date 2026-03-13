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

// adUserRecord represents an AD user merged with Odoo registration status.
type adUserRecord struct {
	Login    string      `json:"login"`
	Name     string      `json:"name"`
	Email    string      `json:"email"`
	ID       int64       `json:"id,omitempty"`
	Active   bool        `json:"active"`
	GroupIDs interface{} `json:"group_ids,omitempty"`
}

// listAppEngineUsers returns all AD users (via LDAP) merged with Odoo registration info.
func listAppEngineUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. AD LDAP에서 사용자 목록 조회
		adUsers := make([]adUserRecord, 0)
		if d.LDAP != nil {
			baseDN := d.Cfg.BaseDN()
			filter := "(&(objectClass=user)(!(isCriticalSystemObject=TRUE))(!(sAMAccountName=krbtgt))(!(sAMAccountName=Guest)))"
			attrs := []string{"sAMAccountName", "displayName", "mail", "userPrincipalName"}
			entries, err := d.LDAP.SearchSubtree(baseDN, filter, attrs)
			if err == nil {
				seen := make(map[string]bool)
				for _, e := range entries {
					login := e.Get("sAMAccountName")
					if login == "" || seen[login] {
						continue
					}
					seen[login] = true
					email := e.Get("mail")
					if email == "" {
						email = e.Get("userPrincipalName")
					}
					adUsers = append(adUsers, adUserRecord{
						Login: login,
						Name:  e.Get("displayName"),
						Email: email,
					})
				}
			}
		}

		// 2. Odoo res.users에서 등록 상태 조회 후 매핑
		if d.OdooClient != nil {
			domain := []interface{}{[]interface{}{"share", "=", false}}
			fields := []string{"id", "login", "active", "group_ids"}
			records, err := d.OdooClient.SearchRead("res.users", domain, fields, 0, 0)
			if err == nil {
				odooMap := make(map[string]odooUserRecord)
				for _, rec := range records {
					u := toOdooUser(rec)
					odooMap[u.Login] = u
				}
				for i, u := range adUsers {
					if ou, ok := odooMap[u.Login]; ok {
						adUsers[i].ID = ou.ID
						adUsers[i].Active = ou.Active
						adUsers[i].GroupIDs = ou.GroupIDs
					}
				}
				// Odoo에만 있는 사용자 (OIDC로 가입, AD에 없는 경우) 추가
				adLogins := make(map[string]bool)
				for _, u := range adUsers {
					adLogins[u.Login] = true
				}
				for _, ou := range odooMap {
					if !adLogins[ou.Login] && ou.Login != "admin" {
						adUsers = append(adUsers, adUserRecord{
							Login:      ou.Login,
							ID:     ou.ID,
							Active: ou.Active,
							GroupIDs:   ou.GroupIDs,
						})
					}
				}
			}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"users":   adUsers,
			"count":   len(adUsers),
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
