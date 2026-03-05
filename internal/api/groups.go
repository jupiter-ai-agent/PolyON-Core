package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/engine/nextcloud"
	"github.com/triangles/polyon-core/internal/httputil"
)

var groupTypeMap = map[string]struct {
	Prefix string
	OU     string
	Label  string
}{
	"org":     {"SG-ORG-", "OU=01_Org,OU=SecurityGroups", "조직 (Organization)"},
	"role":    {"SG-ROLE-", "OU=02_Role,OU=SecurityGroups", "역할 (Role)"},
	"project": {"SG-PROJ-", "OU=03_Project,OU=SecurityGroups", "프로젝트 (Project)"},
	"system":  {"SG-SYS-", "OU=04_System,OU=SecurityGroups", "시스템 (System)"},
}

func RegisterGroups(r chi.Router, d *Deps) {
	r.Route("/groups", func(r chi.Router) {
		r.Get("/types", groupTypes(d))
		r.Get("/", listGroups(d))
		r.Post("/", createGroup(d))
		r.Get("/{name}", getGroup(d))
		r.Patch("/{name}", updateGroup(d))
		r.Delete("/{name}", deleteGroup(d))
		r.Post("/{name}/members", addMember(d))
		r.Delete("/{name}/members/{username}", removeMember(d))
	})
}

func groupTypes(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		var types []map[string]string
		for id, m := range groupTypeMap {
			types = append(types, map[string]string{
				"id": id, "prefix": m.Prefix, "ou": m.OU, "label": m.Label,
			})
		}
		httputil.RespondOK(w, map[string]interface{}{"types": types})
	}
}

func listGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		groups, err := d.Samba.ListGroups()
		if err != nil {
			httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{
			"success": true, "groups": groups, "count": len(groups),
		})
	}
}

func createGroup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			GroupType   string `json:"group_type"`
			ParentOU    string `json:"parent_ou"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		name := req.Name
		targetOU := req.ParentOU

		if m, ok := groupTypeMap[req.GroupType]; ok {
			if !strings.HasPrefix(strings.ToUpper(name), strings.ToUpper(m.Prefix)) {
				name = m.Prefix + name
			}
			if targetOU == "" {
				targetOU = fmt.Sprintf("%s,%s", m.OU, d.Samba.BaseDN())
			}
		}

		result := d.Samba.CreateGroup(name, req.Description, "")
		if !result.Success {
			httputil.RespondError(w, 400, "CREATE_FAILED", result.Error)
			return
		}

		if targetOU != "" {
			moveResult := d.Samba.MoveGroup(name, targetOU)
			if !moveResult.Success {
				d.Store.LogAction("CREATE", "group", name,
					fmt.Sprintf("move_failed=%s", targetOU), auth.GetActor(r), httputil.ClientIP(r))
				httputil.RespondOK(w, map[string]interface{}{
					"success": true, "name": name,
					"warning": fmt.Sprintf("그룹 생성 성공, OU 이동 실패: %s", moveResult.Error),
				})
				return
			}
		}

		// Drive provisioning — 조직 그룹 생성 = 즉시 팀 폴더
		var folderID int
		driveOK := false
		if d.Drive != nil && req.GroupType == "org" {
			folderName := nextcloud.OrgGroupToFolderName(name)
			fid, err := d.Drive.ProvisionOrg(name, folderName, 0)
			if err != nil {
				fmt.Printf("[Drive] WARNING: org folder provision failed (non-fatal): %v\n", err)
			} else {
				folderID = fid
				driveOK = true
			}
		}

		// Service sync — propagate new group to Mattermost
		if d.Sync != nil {
			d.Sync.OnGroupCreated(name, req.GroupType, req.Description)
		}

		d.Store.LogAction("CREATE", "group", name,
			fmt.Sprintf("type=%s, desc=%s, drive=%v", req.GroupType, req.Description, driveOK),
			auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondCreated(w, map[string]interface{}{
			"success":   true,
			"name":      name,
			"drive":     driveOK,
			"folder_id": folderID,
		})
	}
}

func getGroup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		group, err := d.Samba.GetGroup(name)
		if err != nil {
			httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
			return
		}
		if group == nil {
			httputil.RespondError(w, 404, "NOT_FOUND", fmt.Sprintf("Group '%s' not found", name))
			return
		}
		httputil.RespondOK(w, group)
	}
}

func updateGroup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		var attrs map[string]string
		json.NewDecoder(r.Body).Decode(&attrs)
		if len(attrs) == 0 {
			httputil.RespondError(w, 400, "BAD_REQUEST", "No fields to update")
			return
		}
		result := d.Samba.UpdateGroup(name, attrs)
		if !result.Success {
			httputil.RespondError(w, 400, "UPDATE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("UPDATE", "group", name, fmt.Sprintf("%v", attrs), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func deleteGroup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		result := d.Samba.DeleteGroup(name)
		if !result.Success {
			httputil.RespondError(w, 400, "DELETE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("DELETE", "group", name, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func addMember(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		var req struct {
			Username string `json:"username"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.AddMember(name, req.Username)
		if !result.Success {
			httputil.RespondError(w, 400, "ADD_MEMBER_FAILED", result.Error)
			return
		}
		// Drive — 조직에 사용자 추가 시 Drive 계정 확인
		if d.Drive != nil {
			_ = d.Drive.OnUserAddedToOrg(req.Username, name)
		}

		// Service sync — propagate member addition to Mattermost
		if d.Sync != nil {
			d.Sync.OnMemberAdded(name, req.Username)
		}

		d.Store.LogAction("ADD_MEMBER", "group", name,
			fmt.Sprintf("member=%s", req.Username), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func removeMember(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		username := chi.URLParam(r, "username")
		result := d.Samba.RemoveMember(name, username)
		if !result.Success {
			httputil.RespondError(w, 400, "REMOVE_MEMBER_FAILED", result.Error)
			return
		}
		// Service sync — propagate member removal to Mattermost
		if d.Sync != nil {
			d.Sync.OnMemberRemoved(name, username)
		}

		d.Store.LogAction("REMOVE_MEMBER", "group", name,
			fmt.Sprintf("member=%s", username), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}
