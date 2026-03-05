package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/samba"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterDNS(r chi.Router, d *Deps) {
	r.Route("/dns", func(r chi.Router) {
		r.Get("/domain/level", getDomainLevel(d))
		r.Post("/domain/level", raiseDomainLevel(d))
		r.Get("/zones", listZones(d))
		r.Get("/zones/{zone}", zoneInfo(d))
		r.Get("/zones/{zone}/records", listRecords(d))
		r.Post("/zones/{zone}/records", addRecord(d))
		r.Delete("/zones/{zone}/records", deleteRecord(d))
	})
}

func getDomainLevel(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		result := d.Samba.GetDomainLevel()
		if !result.Success {
			httputil.RespondError(w, 500, "SAMBA_ERROR", result.Error)
			return
		}
		levels := samba.ParseDomainLevel(result.Output)
		levels["success"] = "true"
		httputil.RespondOK(w, levels)
	}
}

func raiseDomainLevel(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DomainLevel string `json:"domain_level"`
			ForestLevel string `json:"forest_level"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.RaiseDomainLevel(req.DomainLevel, req.ForestLevel)
		if !result.Success {
			httputil.RespondError(w, 400, "RAISE_FAILED", result.Error)
			return
		}
		httputil.RespondOK(w, result)
	}
}

func listZones(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httputil.RespondOK(w, d.Samba.ListDNSZones())
	}
}

func zoneInfo(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zone := chi.URLParam(r, "zone")
		httputil.RespondOK(w, d.Samba.ListDNSRecords(zone))
	}
}

func listRecords(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zone := chi.URLParam(r, "zone")
		httputil.RespondOK(w, d.Samba.ListDNSRecords(zone))
	}
}

func addRecord(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zone := chi.URLParam(r, "zone")
		var req struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Data string `json:"data"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.AddDNSRecord(zone, req.Name, req.Type, req.Data)
		if !result.Success {
			httputil.RespondError(w, 400, "ADD_FAILED", result.Error)
			return
		}
		httputil.RespondOK(w, result)
	}
}

func deleteRecord(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zone := chi.URLParam(r, "zone")
		var req struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Data string `json:"data"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := d.Samba.DeleteDNSRecord(zone, req.Name, req.Type, req.Data)
		if !result.Success {
			httputil.RespondError(w, 400, "DELETE_FAILED", result.Error)
			return
		}
		httputil.RespondOK(w, result)
	}
}
