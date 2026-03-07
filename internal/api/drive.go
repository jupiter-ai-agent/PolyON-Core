package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/engine/nextcloud"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterDrive(r chi.Router, d *Deps) {
	r.Route("/drive", func(r chi.Router) {
		r.Get("/status", driveStatus(d))
		r.Get("/folders", listFolders(d))
		r.Post("/folders", createFolder(d))
		r.Put("/folders/{id}/quota", setFolderQuota(d))
		r.Delete("/folders/{id}", deleteFolder(d))
		r.Post("/ldap-sync", driveLDAPSync(d))
	})
}

func driveClient(d *Deps) *nextcloud.Client {
	if d.Drive != nil {
		return d.Drive.Client()
	}
	return nil
}

func driveStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nc := nextcloud.NewClient(
			d.Cfg.NextcloudURL,
			"admin",
			d.Cfg.NextcloudAdminPassword,
			"polyon-drive",
		)
		status, err := nc.Status()
		if err != nil {
			httputil.RespondError(w, 503, "DRIVE_UNREACHABLE", err.Error())
			return
		}

		folders, _ := nc.ListGroupFolders()
		totalSize := int64(0)
		for _, f := range folders {
			totalSize += f.Size
		}

		httputil.RespondOK(w, map[string]interface{}{
			"installed":    status.Installed,
			"version":      status.VersionString,
			"maintenance":  status.Maintenance,
			"product":      status.ProductName,
			"team_folders": len(folders),
			"total_size":   totalSize,
		})
	}
}

func listFolders(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nc := nextcloud.NewClient(
			d.Cfg.NextcloudURL,
			"admin",
			d.Cfg.NextcloudAdminPassword,
			"polyon-drive",
		)
		folders, err := nc.ListGroupFolders()
		if err != nil {
			httputil.RespondError(w, 500, "DRIVE_ERROR", err.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{
			"success": true,
			"folders": folders,
			"count":   len(folders),
		})
	}
}

func createFolder(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name    string `json:"name"`
			GroupID string `json:"group_id"`
			Quota   int64  `json:"quota_bytes"` // -3 unlimited, 0 default 10GB
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}

		if d.Drive == nil {
			httputil.RespondError(w, 503, "DRIVE_NOT_CONFIGURED", "Drive provisioner not available")
			return
		}

		folderID, err := d.Drive.ProvisionOrg(req.GroupID, req.Name, req.Quota)
		if err != nil {
			httputil.RespondError(w, 500, "CREATE_FAILED", err.Error())
			return
		}

		d.Store.LogAction("CREATE", "drive_folder", req.Name,
			fmt.Sprintf("group=%s, folder_id=%d", req.GroupID, folderID),
			auth.GetActor(r), httputil.ClientIP(r))

		httputil.RespondCreated(w, map[string]interface{}{
			"success":   true,
			"folder_id": folderID,
			"name":      req.Name,
		})
	}
}

func setFolderQuota(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		folderID, _ := strconv.Atoi(idStr)

		var req struct {
			QuotaBytes int64 `json:"quota_bytes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}

		nc := nextcloud.NewClient(
			d.Cfg.NextcloudURL,
			"admin",
			d.Cfg.NextcloudAdminPassword,
			"polyon-drive",
		)
		if err := nc.SetFolderQuota(folderID, req.QuotaBytes); err != nil {
			httputil.RespondError(w, 500, "QUOTA_FAILED", err.Error())
			return
		}

		d.Store.LogAction("UPDATE", "drive_folder", idStr,
			fmt.Sprintf("quota=%d", req.QuotaBytes),
			auth.GetActor(r), httputil.ClientIP(r))

		httputil.RespondOK(w, map[string]interface{}{"success": true})
	}
}

func deleteFolder(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		folderID, _ := strconv.Atoi(idStr)

		nc := nextcloud.NewClient(
			d.Cfg.NextcloudURL,
			"admin",
			d.Cfg.NextcloudAdminPassword,
			"polyon-drive",
		)
		if err := nc.DeleteGroupFolder(folderID); err != nil {
			httputil.RespondError(w, 500, "DELETE_FAILED", err.Error())
			return
		}

		d.Store.LogAction("DELETE", "drive_folder", idStr, "",
			auth.GetActor(r), httputil.ClientIP(r))

		httputil.RespondOK(w, map[string]interface{}{"success": true})
	}
}

func driveLDAPSync(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := DriveSyncLDAPUsers(d)
		if err != nil {
			httputil.RespondError(w, 500, "SYNC_ERROR", err.Error())
			return
		}
		httputil.RespondJSON(w, 200, result)
	}
}
