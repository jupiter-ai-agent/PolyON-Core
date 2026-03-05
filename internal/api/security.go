package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/samba"
)

func RegisterSecurity(r chi.Router, d *Deps) {
	r.Route("/security", func(r chi.Router) {
		r.Get("/password-policy", getPasswordPolicy(d))
		r.Post("/password-policy", setPasswordPolicy(d))
		r.Get("/gpo", listGPOs(d))
		r.Get("/gpo/{guid}", getGPO(d))
		r.Post("/gpo", createGPO(d))
		r.Delete("/gpo/{guid}", deleteGPO(d))
		r.Post("/gpo/link", linkGPO(d))
		r.Delete("/gpo/link", unlinkGPO(d))
		r.Get("/gpo/{guid}/containers", listGPOContainers(d))
		r.Get("/gpo/links/{containerDN:*}", getGPOLinks(d))
		r.Get("/acl", getACL(d))
		r.Get("/acl/ous", listOUACLs(d))
	})
}

func getPasswordPolicy(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		result := d.Samba.GetPasswordPolicy()
		if !result.Success {
			httputil.RespondError(w, 500, "SAMBA_ERROR", result.Error)
			return
		}
		policy := samba.ParsePasswordPolicy(result.Output)
		httputil.RespondOK(w, map[string]interface{}{"success": true, "policy": policy, "raw": result.Output})
	}
}

func setPasswordPolicy(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var settings map[string]string
		json.NewDecoder(r.Body).Decode(&settings)
		if len(settings) == 0 {
			httputil.RespondError(w, 400, "BAD_REQUEST", "No settings provided")
			return
		}
		result := d.Samba.SetPasswordPolicy(settings)
		if !result.Success {
			httputil.RespondError(w, 400, "UPDATE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("UPDATE", "password_policy", "domain",
			fmt.Sprintf("%v", settings), auth.GetActor(r), httputil.ClientIP(r))

		// ConfigTrack — commit password policy change (non-fatal).
		if d.ConfigTracker != nil {
			content := "# Password policy snapshot\n"
			for k, v := range settings {
				content += fmt.Sprintf("%s=%s\n", k, v)
			}
			if err := d.ConfigTracker.CommitFile("security/password-policy.conf", content,
				"Update password policy", auth.GetActor(r)); err != nil {
				log.Warn().Err(err).Msg("setPasswordPolicy: configtrack commit failed (non-fatal)")
			}
		}

		httputil.RespondOK(w, result)
	}
}

func listGPOs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		gpos, result := d.Samba.ListGPOs()
		if !result.Success {
			httputil.RespondError(w, 500, "SAMBA_ERROR", result.Error)
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "gpos": gpos, "count": len(gpos)})
	}
}

func getGPO(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guid := chi.URLParam(r, "guid")
		result := d.Samba.GetGPO(guid)
		if !result.Success {
			httputil.RespondError(w, 404, "NOT_FOUND", result.Error)
			return
		}
		httputil.RespondOK(w, result)
	}
}

func createGPO(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DisplayName string `json:"display_name"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.CreateGPO(req.DisplayName)
		if !result.Success {
			httputil.RespondError(w, 400, "CREATE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("CREATE", "gpo", req.DisplayName, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func deleteGPO(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guid := chi.URLParam(r, "guid")
		result := d.Samba.DeleteGPO(guid)
		if !result.Success {
			httputil.RespondError(w, 400, "DELETE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("DELETE", "gpo", guid, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func linkGPO(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ContainerDN string `json:"container_dn"`
			GPOGUID     string `json:"gpo_guid"`
			Enforce     bool   `json:"enforce"`
			Disable     bool   `json:"disable"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.LinkGPO(req.ContainerDN, req.GPOGUID, req.Enforce, req.Disable)
		if !result.Success {
			httputil.RespondError(w, 400, "LINK_FAILED", result.Error)
			return
		}
		d.Store.LogAction("LINK", "gpo", req.GPOGUID,
			fmt.Sprintf("to=%s", req.ContainerDN), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func unlinkGPO(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ContainerDN string `json:"container_dn"`
			GPOGUID     string `json:"gpo_guid"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.UnlinkGPO(req.ContainerDN, req.GPOGUID)
		if !result.Success {
			httputil.RespondError(w, 400, "UNLINK_FAILED", result.Error)
			return
		}
		d.Store.LogAction("UNLINK", "gpo", req.GPOGUID,
			fmt.Sprintf("from=%s", req.ContainerDN), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func listGPOContainers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		guid := chi.URLParam(r, "guid")
		containers, result := d.Samba.ListGPOContainers(guid)
		if !result.Success {
			httputil.RespondError(w, 500, "SAMBA_ERROR", result.Error)
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "containers": containers})
	}
}

func getGPOLinks(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerDN := chi.URLParam(r, "containerDN")
		links, result := d.Samba.GetGPOLinks(containerDN)
		if !result.Success {
			httputil.RespondError(w, 500, "SAMBA_ERROR", result.Error)
			return
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "links": links})
	}
}

func getACL(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dn := r.URL.Query().Get("dn")
		if dn == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "DN parameter required")
			return
		}
		result := d.Samba.GetACL(dn)
		if !result.Success {
			httputil.RespondError(w, 500, "SAMBA_ERROR", result.Error)
			return
		}
		httputil.RespondOK(w, result)
	}
}

func listOUACLs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		results := d.Samba.ListOUACLs()
		httputil.RespondOK(w, map[string]interface{}{"success": true, "ou_acls": results})
	}
}
