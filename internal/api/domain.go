package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

func RegisterDomain(r chi.Router, d *Deps) {
	r.Route("/domain", func(r chi.Router) {
		r.Get("/info", func(w http.ResponseWriter, _ *http.Request) {
			httputil.RespondOK(w, d.Samba.DomainInfo())
		})
		r.Get("/dcs", func(w http.ResponseWriter, _ *http.Request) {
			dcs, err := d.Samba.DomainDCs()
			if err != nil {
				httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
				return
			}
			httputil.RespondOK(w, map[string]interface{}{"success": true, "dcs": dcs})
		})
		r.Get("/fsmo", func(w http.ResponseWriter, _ *http.Request) {
			httputil.RespondOK(w, d.Samba.FSMOParsed())
		})
		r.Get("/replication", func(w http.ResponseWriter, _ *http.Request) {
			httputil.RespondOK(w, d.Samba.ReplicationStatus())
		})
		r.Get("/computers", func(w http.ResponseWriter, _ *http.Request) {
			computers, err := d.Samba.ListComputers()
			if err != nil {
				httputil.RespondError(w, 500, "LDAP_ERROR", err.Error())
				return
			}
			httputil.RespondOK(w, map[string]interface{}{"success": true, "computers": computers, "count": len(computers)})
		})
	})
}
