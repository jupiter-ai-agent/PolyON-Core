package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

type firewallService struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Route       string `json:"route"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Port        int    `json:"port"`
	Essential   bool   `json:"essential,omitempty"`
}

var firewallServices = []firewallService{
	// Management HTTP
	{"polyon-console", "PolyON Console", "http", "polyon-console", "management", "관리 콘솔 (UI + API)", 1111, true},
	{"keycloak", "Keycloak Admin", "http", "keycloak", "auth", "Keycloak 관리 콘솔", 1112, false},
	{"stalwart-admin", "Stalwart Admin", "http", "stalwart-admin", "mail", "메일 서버 관리 콘솔", 1113, false},
	{"grafana", "Grafana", "http", "grafana", "monitoring", "모니터링 대시보드", 1114, false},
	{"rustfs", "RustFS Console", "http", "rustfs", "storage", "S3 오브젝트 스토리지", 1115, false},
	{"elasticvue", "Elasticvue", "http", "elasticvue", "monitoring", "Elasticsearch 웹 UI", 1116, false},
	{"pgadmin", "pgAdmin", "http", "pgadmin", "monitoring", "PostgreSQL 관리", 1117, false},
	{"redisinsight", "RedisInsight", "http", "redisinsight", "monitoring", "Redis 관리", 1118, false},
	{"traefik", "Traefik Dashboard", "http", "traefik-dashboard", "management", "게이트웨이 대시보드", 1119, false},
	// TCP mail
	{"smtp", "SMTP", "tcp", "smtp", "mail", "메일 수신 (MTA)", 25, false},
	{"submission", "SMTP Submission", "tcp", "submission", "mail", "메일 발신 (MUA → MTA)", 587, false},
	{"imaps", "IMAPS", "tcp", "imaps", "mail", "메일 클라이언트 접속 (TLS)", 993, false},
	{"managesieve", "ManageSieve", "tcp", "managesieve", "mail", "메일 필터 관리", 4190, false},
	// TCP directory
	{"ldap", "LDAP", "tcp", "ldap", "directory", "AD 디렉토리 조회/인증 (평문)", 389, false},
	{"ldaps", "LDAPS", "tcp", "ldaps", "directory", "AD 디렉토리 조회/인증 (TLS)", 636, false},
	{"kerberos", "Kerberos", "tcp", "kerberos", "directory", "AD Kerberos 인증", 88, false},
	{"dns-ad", "DNS (AD)", "tcp", "dns-ad", "directory", "AD 내장 DNS 서버", 53, false},
}

// firewallRouterMapping holds the Traefik router keys to remove when a service is blocked.
type firewallRouterMapping struct {
	HTTPRouters []string // http.routers keys to remove
	TCPRouters  []string // tcp.routers keys to remove
}

var firewallRouterMap = map[string]firewallRouterMapping{
	"keycloak":       {HTTPRouters: []string{"keycloak", "keycloak-auth-proxy"}},
	"stalwart-admin": {HTTPRouters: []string{"stalwart-admin"}},
	"grafana":        {HTTPRouters: []string{"grafana"}},
	"rustfs":         {HTTPRouters: []string{"rustfs"}},
	"elasticvue":     {HTTPRouters: []string{"elasticvue"}},
	"pgadmin":        {HTTPRouters: []string{"pgadmin"}},
	"redisinsight":   {HTTPRouters: []string{"redisinsight"}},
	"traefik":        {HTTPRouters: []string{"traefik-dashboard", "traefik-api"}},
	"smtp":           {TCPRouters: []string{"smtp"}},
	"submission":     {TCPRouters: []string{"submission"}},
	"imaps":          {TCPRouters: []string{"imaps"}},
	"managesieve":    {TCPRouters: []string{"managesieve"}},
	"ldap":           {TCPRouters: []string{"ldap"}},
	"ldaps":          {TCPRouters: []string{"ldaps"}},
	"kerberos":       {TCPRouters: []string{"kerberos"}},
	"dns-ad":         {TCPRouters: []string{"dns"}},
}

const traefikProxyContainer = "polyon-proxy"
const traefikDynamicPath = "/etc/traefik/dynamic.yml"

var fwMu sync.Mutex

func firewallStatePath(d *Deps) string {
	return filepath.Join(d.Cfg.SharedDir, "firewall-state.json")
}

func firewallBasePath(d *Deps) string {
	return filepath.Join(d.Cfg.SharedDir, "dynamic-base.yml")
}

func readFirewallState(d *Deps) map[string]bool {
	data, err := os.ReadFile(firewallStatePath(d))
	if err != nil {
		// Default: all enabled
		state := make(map[string]bool)
		for _, s := range firewallServices {
			state[s.ID] = true
		}
		return state
	}
	var state map[string]bool
	json.Unmarshal(data, &state)
	return state
}

func writeFirewallState(d *Deps, state map[string]bool) {
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(firewallStatePath(d), data, 0644)
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// applyFirewallState reads the base dynamic.yml, comments out blocked router
// blocks using line-level text manipulation (no YAML marshal — preserves
// original formatting, comments, and key order).
func applyFirewallState(d *Deps) error {
	if d.Docker == nil {
		return fmt.Errorf("docker client not available")
	}

	basePath := firewallBasePath(d)

	// 1. Load base YAML (original full config)
	baseYaml, err := os.ReadFile(basePath)
	if err != nil {
		baseYaml, err = d.Docker.CopyFromContainer(traefikProxyContainer, traefikDynamicPath)
		if err != nil {
			return fmt.Errorf("copy base dynamic.yml from container: %w", err)
		}
		if werr := os.WriteFile(basePath, baseYaml, 0644); werr != nil {
			log.Warn().Err(werr).Msg("firewall: could not save dynamic-base.yml")
		}
	}

	// 1b. Inject portal/console domain routers if not present in base YAML
	if d.Store != nil {
		ctx := context.Background()
		configs, _ := d.Store.GetConfigs(ctx, []string{"portal_domain", "console_domain"})
		portalDomain := configs["portal_domain"]
		consoleDomain := configs["console_domain"]

		baseStr := string(baseYaml)
		injected := false

		if portalDomain != "" && !strings.Contains(baseStr, "portal:") {
			portalBlock := fmt.Sprintf(
				"\n    # ── Portal / Service Domains (HTTPS :443) ──\n"+
					"    portal:\n"+
					"      rule: \"Host(%[1]s%[2]s%[1]s)\"\n"+
					"      entryPoints: [\"websecure\"]\n"+
					"      service: polyon-console\n"+
					"      middlewares: [\"https-headers\"]\n"+
					"      priority: 10\n\n"+
					"    portal-auth:\n"+
					"      rule: \"Host(%[1]s%[2]s%[1]s) && PathPrefix(%[1]s/auth%[1]s)\"\n"+
					"      entryPoints: [\"websecure\"]\n"+
					"      service: keycloak\n"+
					"      middlewares: [\"https-headers\"]\n"+
					"      priority: 100\n\n"+
					"    portal-api:\n"+
					"      rule: \"Host(%[1]s%[2]s%[1]s) && PathPrefix(%[1]s/api%[1]s)\"\n"+
					"      entryPoints: [\"websecure\"]\n"+
					"      service: polyon-core\n"+
					"      middlewares: [\"https-headers\"]\n"+
					"      priority: 50\n\n",
				"`", portalDomain,
			)
			baseStr = strings.Replace(baseStr, "  routers:\n", "  routers:\n"+portalBlock, 1)
			injected = true
		}

		if consoleDomain != "" && !strings.Contains(baseStr, "console-domain:") {
			consolBlock := fmt.Sprintf(
				"    console-domain:\n"+
					"      rule: \"Host(%[1]s%[2]s%[1]s)\"\n"+
					"      entryPoints: [\"websecure\"]\n"+
					"      service: polyon-console\n"+
					"      middlewares: [\"https-headers\"]\n"+
					"      priority: 10\n\n"+
					"    console-domain-auth:\n"+
					"      rule: \"Host(%[1]s%[2]s%[1]s) && PathPrefix(%[1]s/auth%[1]s)\"\n"+
					"      entryPoints: [\"websecure\"]\n"+
					"      service: keycloak\n"+
					"      middlewares: [\"https-headers\"]\n"+
					"      priority: 100\n\n"+
					"    console-domain-api:\n"+
					"      rule: \"Host(%[1]s%[2]s%[1]s) && PathPrefix(%[1]s/api%[1]s)\"\n"+
					"      entryPoints: [\"websecure\"]\n"+
					"      service: polyon-core\n"+
					"      middlewares: [\"https-headers\"]\n"+
					"      priority: 50\n\n",
				"`", consoleDomain,
			)
			baseStr = strings.Replace(baseStr, "  routers:\n", "  routers:\n"+consolBlock, 1)
			injected = true
		}

		if injected {
			baseYaml = []byte(baseStr)
			log.Info().Str("portal", portalDomain).Str("console", consoleDomain).Msg("firewall: injected domain routers")
		}
	}

	// 2. Collect all router keys to block
	state := readFirewallState(d)
	blockKeys := map[string]bool{}
	for svcID, exposed := range state {
		if exposed {
			continue
		}
		mapping, ok := firewallRouterMap[svcID]
		if !ok {
			continue
		}
		for _, k := range mapping.HTTPRouters {
			blockKeys[k] = true
		}
		for _, k := range mapping.TCPRouters {
			blockKeys[k] = true
		}
	}

	if len(blockKeys) == 0 {
		// Nothing to block — restore original via stdin
		writeCmd := fmt.Sprintf("cat > %s", traefikDynamicPath)
		if _, err := d.Docker.ExecWithStdin(traefikProxyContainer, []string{"sh", "-c", writeCmd}, baseYaml); err != nil {
			return fmt.Errorf("restore dynamic.yml: %w", err)
		}
		log.Info().Msg("firewall: all services exposed — original dynamic.yml restored")
		return nil
	}

	// 3. Line-by-line: comment out blocked router blocks in routers sections only
	lines := strings.Split(string(baseYaml), "\n")
	result := make([]string, 0, len(lines))
	inRoutersSection := false // inside http.routers or tcp.routers
	blockingRouter := false  // currently commenting out a router block
	routerIndent := 0        // indent level of router key (e.g. 4 spaces for "    keycloak:")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect routers section (both "  routers:" under http: and tcp:)
		if (trimmed == "routers:" || trimmed == "routers: {}") && !strings.HasPrefix(trimmed, "#") {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent <= 6 { // http.routers or tcp.routers (indent 2-6)
				inRoutersSection = true
				blockingRouter = false
				routerIndent = 0
				result = append(result, line)
				continue
			}
		}

		// If in routers section, detect router key or section end
		if inRoutersSection {
			indent := len(line) - len(strings.TrimLeft(line, " "))

			// Empty line or comment — pass through
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				if blockingRouter {
					result = append(result, "#BLOCKED# "+line)
				} else {
					result = append(result, line)
				}
				continue
			}

			// Top-level section (http:, tcp:, tls:) — exit routers
			if indent == 0 && strings.HasSuffix(trimmed, ":") {
				inRoutersSection = false
				blockingRouter = false
				result = append(result, line)
				continue
			}

			// Sibling section at routers level (e.g. "  services:", "  middlewares:")
			if indent <= 4 && strings.HasSuffix(trimmed, ":") && trimmed != "routers:" {
				inRoutersSection = false
				blockingRouter = false
				result = append(result, line)
				continue
			}

			// Router key line: "    keycloak:" at typical indent (4-8 spaces)
			if strings.HasSuffix(trimmed, ":") && indent >= 4 && indent <= 8 {
				routerName := strings.TrimSuffix(trimmed, ":")
				if blockKeys[routerName] {
					blockingRouter = true
					routerIndent = indent
					result = append(result, "#BLOCKED# "+line)
					continue
				} else {
					blockingRouter = false
					result = append(result, line)
					continue
				}
			}

			// Inside a router block — check if still part of blocked router
			if blockingRouter && indent > routerIndent {
				result = append(result, "#BLOCKED# "+line)
				continue
			}

			// Back to router level but not a key — end blocking
			if blockingRouter {
				blockingRouter = false
			}
		}

		result = append(result, line)
	}

	// 4. Write to Traefik container via stdin pipe
	newYaml := strings.Join(result, "\n")
	writeCmd := fmt.Sprintf("cat > %s", traefikDynamicPath)
	if _, err := d.Docker.ExecWithStdin(traefikProxyContainer, []string{"sh", "-c", writeCmd}, []byte(newYaml)); err != nil {
		// Fallback: try base64 approach
		encoded := base64Encode([]byte(newYaml))
		// Split into chunks to avoid shell arg length limit
		chunkSize := 60000
		if len(encoded) <= chunkSize {
			fallbackCmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d > %s", encoded, traefikDynamicPath)
			if _, err2 := d.Docker.ExecInContainer(traefikProxyContainer, []string{"sh", "-c", fallbackCmd}); err2 != nil {
				return fmt.Errorf("write dynamic.yml (both methods failed): stdin=%w, base64=%w", err, err2)
			}
		} else {
			return fmt.Errorf("write dynamic.yml via stdin: %w", err)
		}
	}

	log.Info().Int("blocked_routers", len(blockKeys)).Msg("firewall: Traefik dynamic.yml updated (line-level)")
	return nil
}

func findFirewallService(id string) *firewallService {
	for i := range firewallServices {
		if firewallServices[i].ID == id {
			return &firewallServices[i]
		}
	}
	return nil
}

// InitFirewall applies the persisted firewall state to Traefik on startup.
// Should be called once after the server is ready.
func InitFirewall(d *Deps) {
	if err := applyFirewallState(d); err != nil {
		log.Warn().Err(err).Msg("firewall: startup apply failed (Traefik may not be ready yet)")
	} else {
		log.Info().Msg("firewall: startup state applied to Traefik")
	}
}

func RegisterFirewall(r chi.Router, d *Deps) {
	r.Route("/firewall", func(r chi.Router) {
		r.Get("/services", listFirewallServices(d))
		r.Put("/toggle", toggleFirewallService(d))
		r.Put("/bulk-toggle", bulkToggleFirewall(d))
		r.Post("/apply", applyFirewall(d))
	})
}

func listFirewallServices(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		state := readFirewallState(d)
		var services []map[string]interface{}
		for _, svc := range firewallServices {
			services = append(services, map[string]interface{}{
				"id": svc.ID, "name": svc.Name, "type": svc.Type,
				"route": svc.Route, "category": svc.Category,
				"description": svc.Description, "port": svc.Port,
				"essential": svc.Essential, "exposed": state[svc.ID],
				"method": "traefik",
			})
		}
		httputil.RespondOK(w, map[string]interface{}{"success": true, "services": services})
	}
}

func toggleFirewallService(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ServiceID string `json:"service_id"`
			Exposed   bool   `json:"exposed"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		svc := findFirewallService(req.ServiceID)
		if svc == nil {
			httputil.RespondError(w, 404, "NOT_FOUND", "서비스를 찾을 수 없습니다")
			return
		}
		if svc.Essential && !req.Exposed {
			httputil.RespondError(w, 400, "ESSENTIAL", svc.Name+"은(는) 필수 서비스입니다.")
			return
		}

		fwMu.Lock()
		state := readFirewallState(d)
		state[svc.ID] = req.Exposed
		writeFirewallState(d, state)
		applyErr := applyFirewallState(d)
		fwMu.Unlock()

		label := "외부 노출"
		if !req.Exposed {
			label = "차단"
		}

		if applyErr != nil {
			log.Error().Err(applyErr).Str("service", svc.ID).Msg("firewall: failed to apply state")
			httputil.RespondError(w, 500, "APPLY_FAILED",
				svc.Name+" → "+label+" (상태 저장됨, Traefik 적용 실패: "+applyErr.Error()+")")
			return
		}

		httputil.RespondOK(w, map[string]interface{}{
			"success": true, "instant": true,
			"message": svc.Name + " → " + label,
		})
	}
}

func bulkToggleFirewall(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Services map[string]bool `json:"services"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		fwMu.Lock()
		state := readFirewallState(d)
		var results []map[string]interface{}
		for sid, exposed := range req.Services {
			svc := findFirewallService(sid)
			if svc == nil {
				continue
			}
			if svc.Essential && !exposed {
				results = append(results, map[string]interface{}{
					"id": sid, "skipped": true, "reason": "필수 서비스",
				})
				continue
			}
			state[sid] = exposed
			results = append(results, map[string]interface{}{
				"id": sid, "exposed": exposed,
			})
		}
		writeFirewallState(d, state)
		applyErr := applyFirewallState(d)
		fwMu.Unlock()

		resp := map[string]interface{}{
			"success": true, "results": results, "instant": true,
		}
		if applyErr != nil {
			log.Error().Err(applyErr).Msg("firewall: bulk toggle apply failed")
			resp["warning"] = "상태 저장됨, Traefik 적용 실패: " + applyErr.Error()
		}
		httputil.RespondOK(w, resp)
	}
}

func applyFirewall(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		fwMu.Lock()
		applyErr := applyFirewallState(d)
		fwMu.Unlock()

		if applyErr != nil {
			log.Error().Err(applyErr).Msg("firewall: apply failed")
			httputil.RespondError(w, 500, "APPLY_FAILED", "Traefik 적용 실패: "+applyErr.Error())
			return
		}
		httputil.RespondOK(w, map[string]interface{}{
			"success": true, "instant": true,
			"message": "Traefik 설정이 즉시 반영되었습니다. 컨테이너 재시작이 필요하지 않습니다.",
		})
	}
}
