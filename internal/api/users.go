package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/httputil"
)

// 1x1 transparent PNG (67 bytes) — 사진 없는 사용자에게 반환
var emptyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x62, 0x00, 0x00, 0x00, 0x02,
	0x00, 0x01, 0xe5, 0x27, 0xde, 0xfc, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func RegisterUsers(r chi.Router, d *Deps) {
	r.Route("/users", func(r chi.Router) {
		r.Get("/", listUsers(d))
		r.Post("/", createUser(d))
		r.Get("/{username}", getUser(d))
		r.Put("/{username}", updateUser(d))
		r.Delete("/{username}", deleteUser(d))
		r.Post("/{username}/password", changePassword(d))
		r.Post("/{username}/enable", enableUser(d))
		r.Post("/{username}/disable", disableUser(d))
		r.Get("/{username}/photo", getUserPhoto(d))
		r.Put("/{username}/photo", uploadUserPhoto(d))
		r.Delete("/{username}/photo", deleteUserPhoto(d))
	})
}

func listUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := d.Samba.ListUsers()
		if err != nil {
			httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{
			"success": true, "users": users, "count": len(users),
		})
	}
}

func createUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username  string `json:"username"`
			Password  string `json:"password"`
			GivenName string `json:"given_name"`
			Surname   string `json:"surname"`
			Mail      string `json:"mail"`
			OU        string `json:"ou"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}

		// Auto-set mail to username@domain if not provided
		domain := strings.ToLower(d.Samba.Realm())
		userMail := req.Mail
		if userMail == "" {
			userMail = fmt.Sprintf("%s@%s", strings.ToLower(req.Username), domain)
		}

		result := d.Samba.CreateUser(req.Username, req.Password, req.GivenName, req.Surname, userMail, req.OU)
		if !result.Success {
			httputil.RespondError(w, 400, "CREATE_FAILED", result.Error)
			return
		}

		// Ensure AD mail attribute is set (samba-tool --mail-address may not work on all versions)
		mailSet := ensureADMailAttr(d, req.Username, userMail)

		// Drive provisioning — 사용자 생성 = 즉시 개인 Drive 폴더
		driveOK := false
		if d.Drive != nil {
			if err := d.Drive.ProvisionUser(req.Username); err != nil {
				// Non-fatal — user exists in AD, Drive will provision on first login
				fmt.Printf("[Drive] WARNING: user provision failed (non-fatal): %v\n", err)
			} else {
				driveOK = true
			}
		}

		// Service sync — propagate new user to Mattermost
		syncResult := map[string]interface{}{"chat_ok": false, "mail_ok": false}
		if d.Sync != nil {
			sr := d.Sync.OnUserCreated(req.Username, userMail)
			syncResult["chat_ok"] = sr.ChatOK
			syncResult["mail_ok"] = sr.MailOK
			if sr.ChatErr != "" {
				syncResult["chat_error"] = sr.ChatErr
			}
			if sr.MailErr != "" {
				syncResult["mail_error"] = sr.MailErr
			}
		}

		resp := map[string]interface{}{
			"success":    true,
			"output":     result.Output,
			"mail_set":   mailSet,
			"mail_email": userMail,
			"drive":      driveOK,
			"sync":       syncResult,
		}

		d.Store.LogAction("CREATE", "user", req.Username,
			fmt.Sprintf("email=%s drive=%v", userMail, driveOK), auth.GetActor(r), httputil.ClientIP(r))

		httputil.RespondCreated(w, resp)
	}
}

func getUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		user, err := d.Samba.GetUser(username)
		if err != nil {
			httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
			return
		}
		if user == nil {
			httputil.RespondError(w, 404, "NOT_FOUND", fmt.Sprintf("User '%s' not found", username))
			return
		}
		httputil.RespondOK(w, user)
	}
}

func updateUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		var attrs map[string]string
		if err := json.NewDecoder(r.Body).Decode(&attrs); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}

		result := d.Samba.UpdateUser(username, attrs)
		if !result.Success {
			httputil.RespondError(w, 400, "UPDATE_FAILED", result.Error)
			return
		}

		d.Store.LogAction("UPDATE", "user", username,
			fmt.Sprintf("%v", attrs), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func deleteUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		result := d.Samba.DeleteUser(username)
		if !result.Success {
			httputil.RespondError(w, 400, "DELETE_FAILED", result.Error)
			return
		}
		deleteMailAccount(d, username)
		d.Store.LogAction("DELETE", "user", username, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func changePassword(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		var req struct {
			NewPassword string `json:"new_password"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		result := d.Samba.ResetPassword(username, req.NewPassword)
		if !result.Success {
			httputil.RespondError(w, 400, "PASSWORD_FAILED", result.Error)
			return
		}
		d.Store.LogAction("PASSWORD_RESET", "user", username, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func enableUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		result := d.Samba.EnableUser(username)
		if !result.Success {
			httputil.RespondError(w, 400, "ENABLE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("ENABLE", "user", username, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func disableUser(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		result := d.Samba.DisableUser(username)
		if !result.Success {
			httputil.RespondError(w, 400, "DISABLE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("DISABLE", "user", username, "", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func getUserPhoto(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		photo, err := d.Samba.GetUserPhoto(username)
		if err != nil || photo == nil {
			// 1x1 transparent PNG — 브라우저 콘솔 404 에러 방지
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "public, max-age=60")
			w.Header().Set("X-Photo-Status", "empty")
			w.Write(emptyPNG)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(photo)
	}
}

func uploadUserPhoto(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		data, err := io.ReadAll(io.LimitReader(r.Body, 100*1024+1))
		if err != nil {
			httputil.RespondError(w, 400, "READ_ERROR", err.Error())
			return
		}
		if len(data) > 100*1024 {
			httputil.RespondError(w, 413, "TOO_LARGE", "Photo too large. Max 100KB.")
			return
		}
		result := d.Samba.SetUserPhoto(username, data)
		if !result.Success {
			httputil.RespondError(w, 400, "UPLOAD_FAILED", result.Error)
			return
		}
		d.Store.LogAction("UPDATE", "user", username,
			fmt.Sprintf("photo uploaded (%d bytes)", len(data)), auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

func deleteUserPhoto(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := chi.URLParam(r, "username")
		result := d.Samba.DeleteUserPhoto(username)
		if !result.Success {
			httputil.RespondError(w, 400, "DELETE_FAILED", result.Error)
			return
		}
		d.Store.LogAction("UPDATE", "user", username, "photo removed", auth.GetActor(r), httputil.ClientIP(r))
		httputil.RespondOK(w, result)
	}
}

// Mail helpers — AD LDAP mail attribute management
// With LDAP directory mode, Stalwart reads users from AD directly.
// No need to create Stalwart principals — just set the mail attribute in AD.

func ensureADMailAttr(d *Deps, username, email string) bool {
	mods := map[string]string{"mail": email}
	user, err := d.Samba.GetUser(username)
	if err != nil || user == nil {
		return false
	}
	if err := d.Samba.LDAPModify(user.DN, mods); err != nil {
		return false
	}
	return true
}

func deleteMailAccount(d *Deps, username string) {
	// In LDAP directory mode, deleting the AD user is sufficient.
	// Stalwart will no longer see the user in LDAP queries.
	// No separate Stalwart API call needed.
}
