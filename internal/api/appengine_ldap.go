package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	goldap "github.com/go-ldap/ldap/v3"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterAppEngineLDAP registers LDAP diagnostic routes for AppEngine.
func RegisterAppEngineLDAP(r chi.Router, d *Deps) {
	r.Route("/appengine/ldap", func(r chi.Router) {
		r.Get("/config", getAppEngineLDAPConfig(d))
		r.Post("/test-connection", testAppEngineLDAPConnection(d))
		r.Get("/users", listAppEngineLDAPUsers(d))
		r.Get("/groups", listAppEngineLDAPGroups(d))
	})
}

// ldapConfigResponse returns the PP-fixed LDAP configuration for AppEngine.
type ldapConfigResponse struct {
	// Server Information
	ServerAddress string `json:"server_address"`
	ServerPort    int    `json:"server_port"`
	UseTLS        bool   `json:"use_tls"`

	// Login Information
	BindDN   string `json:"bind_dn"`
	Password string `json:"password"` // always masked

	// Process Parameters
	LDAPBase   string `json:"ldap_base"`
	LDAPFilter string `json:"ldap_filter"`

	// User Information
	CreateUser   bool   `json:"create_user"`
	TemplateUser string `json:"template_user"`

	// Users
	UsersDN          string `json:"users_dn"`
	AuthSearchFilter string `json:"auth_search_filter"`
	UserSearchFilter string `json:"user_search_filter"`

	// User Mapping
	MappingScreenName string `json:"mapping_screen_name"`
	MappingEmail      string `json:"mapping_email"`
	MappingFirstName  string `json:"mapping_first_name"`
	MappingMiddleName string `json:"mapping_middle_name"`
	MappingLastName   string `json:"mapping_last_name"`
	MappingFullName   string `json:"mapping_full_name"`
	MappingJobTitle   string `json:"mapping_job_title"`
	MappingGroup      string `json:"mapping_group"`

	// Groups
	GroupsDN           string `json:"groups_dn"`
	GroupFilter        string `json:"group_filter"`
	SyncADGroups       bool   `json:"sync_ad_groups"`
	CreateRolePerGroup bool   `json:"create_role_per_group"`
}

func getAppEngineLDAPConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := d.Cfg
		baseDN := cfg.BaseDN()
		sambaHost := cfg.SambaHost
		if sambaHost == "" {
			sambaHost = "polyon-dc"
		}

		resp := ldapConfigResponse{
			// Server
			ServerAddress: sambaHost,
			ServerPort:    389,
			UseTLS:        false,

			// Login
			BindDN:   cfg.AdminDN(),
			Password: "••••••••••••",

			// Process
			LDAPBase:   baseDN,
			LDAPFilter: "(&(objectClass=person)(objectClass=user)(|(sAMAccountName=%s)(userPrincipalName=%s@*)))",

			// User Info
			CreateUser:   true,
			TemplateUser: "Administrator",

			// Users
			UsersDN:          baseDN,
			AuthSearchFilter: "(&(objectClass=person)(userPrincipalName=%(user)s))",
			UserSearchFilter: "(&(objectClass=person)(!isCriticalSystemObject=TRUE)(!userPrincipalName=*sys@*))",

			// Mapping
			MappingScreenName: "sAMAccountName",
			MappingEmail:      "userPrincipalName",
			MappingFirstName:  "givenName",
			MappingMiddleName: "middleName",
			MappingLastName:   "sn",
			MappingFullName:   "displayName",
			MappingJobTitle:   "title",
			MappingGroup:      "memberOf",

			// Groups
			GroupsDN:           baseDN,
			GroupFilter:        "(&(objectClass=group)(!isCriticalSystemObject=TRUE)(!cn=Dns*))",
			SyncADGroups:       true,
			CreateRolePerGroup: true,
		}

		httputil.RespondJSON(w, http.StatusOK, resp)
	}
}

// ldapTestConnectionResponse is returned by POST /appengine/ldap/test-connection.
type ldapTestConnectionResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	LatencyMs int64  `json:"latency_ms"`
}

func testAppEngineLDAPConnection(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ldapURL := d.Cfg.LDAPURL
		if ldapURL == "" {
			sambaHost := d.Cfg.SambaHost
			if sambaHost == "" {
				sambaHost = "polyon-dc"
			}
			ldapURL = fmt.Sprintf("ldap://%s:389", sambaHost)
		}

		conn, err := goldap.DialURL(ldapURL)
		if err != nil {
			httputil.RespondJSON(w, http.StatusOK, ldapTestConnectionResponse{
				Success:   false,
				Message:   fmt.Sprintf("연결 실패: %v", err),
				LatencyMs: time.Since(start).Milliseconds(),
			})
			return
		}
		defer conn.Close()

		if err := conn.Bind(d.Cfg.AdminDN(), d.Cfg.DCAdminPassword); err != nil {
			httputil.RespondJSON(w, http.StatusOK, ldapTestConnectionResponse{
				Success:   false,
				Message:   fmt.Sprintf("인증 실패: %v", err),
				LatencyMs: time.Since(start).Milliseconds(),
			})
			return
		}

		httputil.RespondJSON(w, http.StatusOK, ldapTestConnectionResponse{
			Success:   true,
			Message:   fmt.Sprintf("연결 성공 (%s)", ldapURL),
			LatencyMs: time.Since(start).Milliseconds(),
		})
	}
}

// ldapUserEntry represents a single AD user returned from LDAP.
type ldapUserEntry struct {
	ScreenName string   `json:"screen_name"`
	Email      string   `json:"email"`
	FirstName  string   `json:"first_name"`
	LastName   string   `json:"last_name"`
	JobTitle   string   `json:"job_title"`
	Groups     []string `json:"groups"`
	GroupCount int      `json:"group_count"`
}

func listAppEngineLDAPUsers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}

		ldapClient := d.LDAP
		baseDN := d.Cfg.BaseDN()
		filter := "(&(objectClass=person)(!isCriticalSystemObject=TRUE)(!userPrincipalName=*sys@*))"
		attrs := []string{"sAMAccountName", "userPrincipalName", "givenName", "sn", "displayName", "title", "memberOf"}

		entries, err := ldapClient.SearchSubtree(baseDN, filter, attrs)
		if err != nil {
			httputil.RespondJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("LDAP 조회 실패: %v", err),
			})
			return
		}

		users := make([]ldapUserEntry, 0, len(entries))
		for i, e := range entries {
			if i >= limit {
				break
			}
			memberOf := e.GetAll("memberOf")
			groupNames := extractCNs(memberOf)
			users = append(users, ldapUserEntry{
				ScreenName: e.Get("sAMAccountName"),
				Email:      e.Get("userPrincipalName"),
				FirstName:  e.Get("givenName"),
				LastName:   e.Get("sn"),
				JobTitle:   e.Get("title"),
				Groups:     groupNames,
				GroupCount: len(groupNames),
			})
		}

		httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"users":     users,
			"total":     len(users),
			"truncated": len(entries) > limit,
		})
	}
}

// ldapGroupEntry represents a single AD group returned from LDAP.
type ldapGroupEntry struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count"`
	DN          string `json:"dn"`
}

func listAppEngineLDAPGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ldapClient := d.LDAP
		baseDN := d.Cfg.BaseDN()
		filter := "(&(objectClass=group)(!isCriticalSystemObject=TRUE)(!cn=Dns*))"
		attrs := []string{"cn", "description", "member"}

		entries, err := ldapClient.SearchSubtree(baseDN, filter, attrs)
		if err != nil {
			httputil.RespondJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("LDAP 그룹 조회 실패: %v", err),
			})
			return
		}

		groups := make([]ldapGroupEntry, 0, len(entries))
		for i, e := range entries {
			desc := e.Get("description")
			members := e.GetAll("member")
			groups = append(groups, ldapGroupEntry{
				Index:       i + 1,
				Name:        e.Get("cn"),
				Description: desc,
				MemberCount: len(members),
				DN:          e.DN,
			})
		}

		httputil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"groups": groups,
			"total":  len(groups),
		})
	}
}

// extractCNs extracts the CN value from a list of LDAP DNs.
func extractCNs(dns []string) []string {
	result := make([]string, 0, len(dns))
	for _, dn := range dns {
		if dn == "" {
			continue
		}
		for _, part := range splitDN(dn) {
			k, v := splitRDN(part)
			if k == "cn" || k == "CN" {
				result = append(result, v)
				break
			}
		}
	}
	return result
}

func splitDN(dn string) []string {
	var parts []string
	current := ""
	escaped := false
	for _, ch := range dn {
		if escaped {
			current += string(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			current += string(ch)
			continue
		}
		if ch == ',' {
			parts = append(parts, current)
			current = ""
			continue
		}
		current += string(ch)
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func splitRDN(rdn string) (string, string) {
	for i, ch := range rdn {
		if ch == '=' {
			return rdn[:i], rdn[i+1:]
		}
	}
	return rdn, ""
}
