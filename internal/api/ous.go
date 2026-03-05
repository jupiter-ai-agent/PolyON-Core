package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterOUs(r chi.Router, d *Deps) {
	r.Route("/ous", func(r chi.Router) {
		r.Get("/", listOUs(d))
		r.Get("/tree", ouTree(d))
		r.Get("/contents", ouContents(d))
		r.Post("/", createOU(d))
		r.Delete("/", deleteOU(d))
		r.Post("/move/user", moveUser(d))
		r.Post("/move/group", moveGroupOU(d))
		r.Post("/move/ou", moveOU(d))
	})
}

func listOUs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		ous, err := d.Samba.ListOUs()
		if err != nil {
			httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "ous": ous, "count": len(ous)})
	}
}

func ouTree(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		ous, err := d.Samba.ListOUs()
		if err != nil {
			httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
			return
		}
		baseDN := d.Samba.BaseDN()
		var tree []map[string]interface{}
		for _, ou := range ous {
			contents := d.Samba.ListOUContents(ou.DN)
			depth := strings.Count(ou.DN, "OU=") - 1
			parts := strings.SplitN(ou.DN, ",", 2)
			parentDN := ""
			if len(parts) > 1 {
				parentDN = parts[1]
			}
			tree = append(tree, map[string]interface{}{
				"name": ou.Name, "dn": ou.DN, "description": ou.Description,
				"depth": depth, "parent_dn": parentDN,
				"users": contents.Users, "groups": contents.Groups, "sub_ous": contents.SubOUs,
			})
		}
		baseContents := d.Samba.ListOUContents("CN=Users," + baseDN)
		httputil.RespondOK(w, map[string]interface{}{
			"success": true, "base_dn": baseDN, "base_contents": baseContents, "ous": tree,
		})
	}
}

func ouContents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dn := r.URL.Query().Get("dn")
		if dn == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "dn parameter required")
			return
		}
		httputil.RespondOK(w, d.Samba.ListOUContents(dn))
	}
}

func createOU(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string `json:"name"`
			ParentDN    string `json:"parent_dn"`
			Description string `json:"description"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.CreateOU(req.Name, req.ParentDN, req.Description)
		if !result.Success {
			httputil.RespondError(w, 400, "CREATE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("CREATE", "ou", req.Name,
			fmt.Sprintf("parent=%s", req.ParentDN), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondCreated(w, result)
	}
}

func deleteOU(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dn := r.URL.Query().Get("dn")
		result := d.Samba.DeleteOU(dn)
		if !result.Success {
			httputil.RespondError(w, 400, "DELETE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("DELETE", "ou", dn, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func moveUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		var req struct {
			TargetDN string `json:"target_dn"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.MoveUser(username, req.TargetDN)
		if !result.Success {
			httputil.RespondError(w, 400, "MOVE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("MOVE", "user", username,
			fmt.Sprintf("to=%s", req.TargetDN), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func moveGroupOU(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		group := r.URL.Query().Get("group")
		var req struct {
			TargetDN string `json:"target_dn"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.MoveGroup(group, req.TargetDN)
		if !result.Success {
			httputil.RespondError(w, 400, "MOVE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("MOVE", "group", group,
			fmt.Sprintf("to=%s", req.TargetDN), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func moveOU(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dn := r.URL.Query().Get("dn")
		var req struct {
			TargetDN string `json:"target_dn"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.MoveOU(dn, req.TargetDN)
		if !result.Success {
			httputil.RespondError(w, 400, "MOVE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("MOVE", "ou", dn,
			fmt.Sprintf("to=%s", req.TargetDN), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}
