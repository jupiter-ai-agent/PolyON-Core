package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// DirectoryConnectInfo — PP 환경의 AD DC 접속 정보.
// 모든 모듈이 이 API를 통해 LDAP/AD 연동 정보를 동적으로 획득한다.
type DirectoryConnectInfo struct {
	Host     string `json:"host"`      // K8s service name (e.g. polyon-dc)
	FQDN     string `json:"fqdn"`      // cluster-internal FQDN
	Port     int    `json:"port"`      // LDAP port (389)
	TLSPort  int    `json:"tls_port"`  // LDAPS port (636)
	BaseDN   string `json:"base_dn"`   // e.g. DC=CMARS,DC=COM
	AdminDN  string `json:"admin_dn"`  // e.g. CN=Administrator,CN=Users,DC=CMARS,DC=COM
	UsersDN  string `json:"users_dn"`  // e.g. CN=Users,DC=CMARS,DC=COM
	Realm    string `json:"realm"`     // e.g. CMARS.COM
	Domain   string `json:"domain"`    // NetBIOS e.g. CMARS
	LDAPURL  string `json:"ldap_url"`  // e.g. ldap://polyon-dc:389

	// Credential 참조 — 직접 비밀번호를 노출하지 않고 Secret 이름/키 제공
	BindCredential BindCredentialRef `json:"bind_credential"`
}

type BindCredentialRef struct {
	SecretName string `json:"secret_name"` // K8s Secret name
	SecretKey  string `json:"secret_key"`  // Key in Secret
}

func RegisterDirectory(r chi.Router, d *Deps) {
	r.Route("/directory", func(r chi.Router) {
		// GET /api/v1/directory/connect-info
		// PP 환경의 AD DC 접속 정보를 반환한다.
		// 모듈은 이 API로 LDAP 연동을 동적으로 구성한다.
		r.Get("/connect-info", directoryConnectInfo(d))
	})
}

func directoryConnectInfo(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg := d.Cfg
		ns := cfg.Namespace
		host := cfg.SambaHost

		info := DirectoryConnectInfo{
			Host:    host,
			FQDN:    host + "." + ns + ".svc.cluster.local",
			Port:    389,
			TLSPort: 636,
			BaseDN:  cfg.BaseDN(),
			AdminDN: cfg.AdminDN(),
			UsersDN: "CN=Users," + cfg.BaseDN(),
			Realm:   cfg.Realm,
			Domain:  cfg.Domain,
			LDAPURL: cfg.LDAPURL,
			BindCredential: BindCredentialRef{
				SecretName: "polyon-dc-admin",
				SecretKey:  "password",
			},
		}

		httputil.RespondOK(w, info)
	}
}
