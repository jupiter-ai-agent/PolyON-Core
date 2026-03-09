package server

import (
	"context"
	"fmt"
	stdlog "log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/rs/zerolog/log"

	"github.com/triangles/polyon-core/internal/api"
	"github.com/triangles/polyon-core/internal/auth"
	"github.com/triangles/polyon-core/internal/prc"
	"github.com/triangles/polyon-core/internal/builder"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/docker"
	"github.com/triangles/polyon-core/internal/engine"
	affineEngine "github.com/triangles/polyon-core/internal/engine/affine"
	litellmEngine "github.com/triangles/polyon-core/internal/engine/litellm"
	mattermostEngine "github.com/triangles/polyon-core/internal/engine/mattermost"
	mem0Engine "github.com/triangles/polyon-core/internal/engine/mem0"
	n8nEngine "github.com/triangles/polyon-core/internal/engine/n8n"
	nextcloudEngine "github.com/triangles/polyon-core/internal/engine/nextcloud"
	odooEngine "github.com/triangles/polyon-core/internal/engine/odoo"
	onlyofficeEngine "github.com/triangles/polyon-core/internal/engine/onlyoffice"
	openclawEngine "github.com/triangles/polyon-core/internal/engine/openclaw"
	operatonEngine "github.com/triangles/polyon-core/internal/engine/operaton"
	strapiEngine "github.com/triangles/polyon-core/internal/engine/strapi"
	"github.com/triangles/polyon-core/internal/gitea"
	"github.com/triangles/polyon-core/internal/httputil"
	"github.com/triangles/polyon-core/internal/kube"
	ldapPkg "github.com/triangles/polyon-core/internal/ldap"
	"github.com/triangles/polyon-core/internal/proxy"
	"github.com/triangles/polyon-core/internal/samba"
	"github.com/triangles/polyon-core/internal/servicesync"
	"github.com/triangles/polyon-core/internal/store"
	"github.com/triangles/polyon-core/internal/traefik"
)

// Server holds all dependencies and the HTTP server.
type Server struct {
	cfg     *config.Config
	router  chi.Router
	http    *http.Server
	docker  *docker.Client
	kube    *kube.Client
	ldap    *ldapPkg.Client
	samba   *samba.Service
	store   *store.Store
	builder *builder.Builder
	gitea   *gitea.Client
	traefik *traefik.Manager
	engines          *engine.Registry
	driveProvisioner *nextcloudEngine.Provisioner
	mattermostClient *mattermostEngine.Client
	syncDispatcher   *servicesync.Dispatcher
	odooClient       *odooEngine.Client
	operatonClient   *operatonEngine.Client
	n8nClient        *n8nEngine.Client
	strapiClient     *strapiEngine.Client
	affineClient     *affineEngine.Client
	onlyofficeClient *onlyofficeEngine.Client
	litellmClient    *litellmEngine.Client
	mem0Client       *mem0Engine.Client
	openclawClient   *openclawEngine.Client
	deps             *api.Deps
}

// New creates a new Server with all dependencies wired.
func New(cfg *config.Config) (*Server, error) {
	// Docker client (gracefully handle K8s environment where Docker socket is not available)
	dc, err := docker.New()
	if err != nil {
		log.Warn().Err(err).Msg("Docker client init failed (will retry on demand)")
		dc = nil // Ensure nil client in case of error
	} else if dc == nil {
		log.Info().Msg("Docker client not available (K8s environment)")
	}

	// Kubernetes client (for K8s health checks)
	kc, _ := kube.New()

	// LDAP client
	lc := ldapPkg.New(cfg)

	// Samba service (uses docker exec instead of subprocess)
	sb := samba.New(cfg, dc, lc)

	// PostgreSQL store (retries up to 15 times with backoff)
	st, err := store.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Store init failed after all retries — cannot start without DB")
	}

	// Apply DB-backed infra service URLs to config (overrides env/default fallbacks)
	if st != nil {
		if infraSvcs, ierr := st.ListInfraServices(context.Background()); ierr == nil {
			entries := make([]config.InfraServiceEntry, len(infraSvcs))
			for i, s := range infraSvcs {
				entries[i] = config.InfraServiceEntry{ID: s.ID, Host: s.Host, Port: s.Port}
			}
			cfg.ApplyInfraServices(entries)
			log.Info().Int("count", len(entries)).Msg("Config: applied DB infra services")
		} else {
			log.Warn().Err(ierr).Msg("Config: failed to load infra services from DB, using env/default URLs")
		}
	}

	// Ensure Strapi DB exists (non-blocking background call)
	go store.EnsureStrapiDB(cfg.DatabaseURL)

	// Auto-populate polyon_config with domain defaults
	// Trigger: POLYON_DOMAIN env is set (Operator always provides this via ConfigMap)
	if st != nil && cfg.Realm != "" {
		go func() {
			ctx := context.Background()
			realm := strings.ToLower(cfg.Realm)
			existing, _ := st.GetConfigs(ctx, []string{"dc_domain"})
			if existing["dc_domain"] == "" {
				kvs := map[string]string{
					"dc_domain":           realm,
					"mail_domain":         "mail." + realm,
					"service_base_domain": realm,
				}
				// auth_domain from env var
				if ad := os.Getenv("POLYON_AUTH_DOMAIN"); ad != "" {
					kvs["auth_domain"] = ad
				} else {
					kvs["auth_domain"] = "auth." + realm
				}
				// Only set console/portal if not already set
				ex2, _ := st.GetConfigs(ctx, []string{"console_domain", "portal_domain"})
				if ex2["console_domain"] == "" {
					kvs["console_domain"] = "console." + realm
				}
				if ex2["portal_domain"] == "" {
					kvs["portal_domain"] = "portal." + realm
				}
				if ex2["cert_mode"] == "" {
					kvs["cert_mode"] = "self-signed"
				}
				if ex2["external_access"] == "" {
					kvs["external_access"] = "internal"
				}
				_ = st.SetConfigs(ctx, kvs)
				log.Info().Str("dc_domain", realm).Msg("Auto-populated polyon_config domain defaults")
			}
		}()
	}

	// Site builder
	bldr := builder.New(dc, st)

	// Gitea client
	giteaToken := os.Getenv("GITEA_TOKEN")
	var gc *gitea.Client
	if giteaToken != "" {
		gc = gitea.New(cfg.GiteaURL, giteaToken)
		log.Info().Str("url", cfg.GiteaURL).Msg("Gitea client initialized")
	}

	// Traefik dynamic config manager
	// TRAEFIK_DYNAMIC_DIR is the directory shared with Traefik (file provider directory mode)
	traefikDynDir := os.Getenv("TRAEFIK_DYNAMIC_DIR")
	if traefikDynDir == "" {
		traefikDynDir = "/traefik-dynamic"
	}
	tm := traefik.NewManager(traefikDynDir)

	// Engine registry
	odooPassword := os.Getenv("ODOO_ADMIN_PASSWORD")
	if odooPassword == "" {
		odooPassword = "admin"
	}
	odooCl := odooEngine.NewClient(cfg.OdooURL, "odoo", "admin", odooPassword)
	reg := engine.NewRegistry()
	reg.Register(odooEngine.NewEngine(odooCl))
	log.Info().Str("url", cfg.OdooURL).Msg("Odoo engine registered")

	// Mattermost engine — URL sourced from cfg (populated from DB or env)
	mattermostCl := mattermostEngine.NewClient(cfg.MattermostURL, os.Getenv("MATTERMOST_TOKEN"))
	reg.Register(mattermostEngine.NewEngine(mattermostCl))
	log.Info().Str("url", cfg.MattermostURL).Msg("Mattermost engine registered")

	// AFFiNE engine (Knowledge/Wiki)
	affineCl := affineEngine.NewClient(cfg.AffineURL)
	reg.Register(affineEngine.NewEngine(affineCl))
	log.Info().Str("url", cfg.AffineURL).Msg("AFFiNE engine registered")

	// LiteLLM engine (AI Gateway)
	litellmCl := litellmEngine.NewClient(cfg.LiteLLMURL, os.Getenv("LITELLM_MASTER_KEY"))
	reg.Register(litellmEngine.NewEngine(litellmCl))
	log.Info().Str("url", cfg.LiteLLMURL).Msg("LiteLLM engine registered")

	// Nextcloud (PolyON Drive) engine + provisioner
	ncCl := nextcloudEngine.NewClient(cfg.NextcloudURL, cfg.NextcloudAdminUser, cfg.NextcloudAdminPassword, "polyon-drive")
	ncEngine := nextcloudEngine.NewEngine(cfg.NextcloudURL, cfg.NextcloudAdminUser, cfg.NextcloudAdminPassword, "polyon-drive")
	reg.Register(ncEngine)
	driveProvisioner := nextcloudEngine.NewProvisioner(ncCl, stdlog.New(os.Stderr, "[drive] ", stdlog.LstdFlags))
	log.Info().Str("url", cfg.NextcloudURL).Msg("Nextcloud Drive engine registered")

	// OnlyOffice (PolyON Office) engine
	ooCl := onlyofficeEngine.NewClient(cfg.OnlyOfficeURL)
	reg.Register(onlyofficeEngine.NewEngine(ooCl))
	log.Info().Str("url", cfg.OnlyOfficeURL).Msg("OnlyOffice engine registered")

	// Operaton client
	operatonCl := operatonEngine.NewClient(cfg.OperatonURL)
	log.Info().Str("url", cfg.OperatonURL).Msg("Operaton client initialized")

	// n8n client
	n8nCl := n8nEngine.NewClient(cfg.N8nURL)
	log.Info().Str("url", cfg.N8nURL).Msg("n8n client initialized")

	// Strapi client (engine)
	strapiCl := strapiEngine.NewClient(cfg.StrapiURL)
	log.Info().Str("url", cfg.StrapiURL).Msg("Strapi client initialized")

	// Mem0 client (AI Memory Layer)
	mem0Cl := mem0Engine.NewClient(cfg.Mem0URL)
	reg.Register(mem0Engine.NewEngine(mem0Cl))
	log.Info().Str("url", cfg.Mem0URL).Msg("Mem0 engine registered")

	// OpenClaw client (AI Agent Runtime)
	openclawCl := openclawEngine.NewClient(cfg.OpenClawURL)
	reg.Register(openclawEngine.NewEngine(openclawCl))
	log.Info().Str("url", cfg.OpenClawURL).Msg("OpenClaw engine registered")

	// Service sync dispatcher
	syncDisp := servicesync.New(mattermostCl, st, cfg)

	s := &Server{
		cfg:              cfg,
		docker:           dc,
		kube:             kc,
		ldap:             lc,
		samba:            sb,
		store:            st,
		builder:          bldr,
		gitea:            gc,
		traefik:          tm,
		engines:          reg,
		driveProvisioner: driveProvisioner,
		mattermostClient: mattermostCl,
		odooClient:       odooCl,
		operatonClient:   operatonCl,
		n8nClient:        n8nCl,
		strapiClient:     strapiCl,
		affineClient:     affineCl,
		onlyofficeClient: ooCl,
		litellmClient:    litellmCl,
		mem0Client:       mem0Cl,
		openclawClient:   openclawCl,
		syncDispatcher:   syncDisp,
	}

	s.buildRouter()
	return s, nil
}

func (s *Server) buildRouter() {
	r := chi.NewRouter()

	// Global middleware
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(Recoverer)
	r.Use(Logger)

	// Auth middleware (skips if setup not complete)
	r.Use(auth.Middleware(s.cfg))

	// Health endpoint
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		httputil.RespondOK(w, map[string]string{"status": "ok", "service": "polyon-core"})
	})

	// Status alias (used by monitoring / validation scripts)
	r.Get("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		httputil.RespondOK(w, map[string]string{
			"status":    "ok",
			"service":   "polyon-core",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	// API routes
	deps := &api.Deps{
		Cfg:              s.cfg,
		Docker:           s.docker,
		Kube:             s.kube,
		LDAP:             s.ldap,
		Samba:            s.samba,
		Store:            s.store,
		Builder:          s.builder,
		Gitea:            s.gitea,
		Traefik:          s.traefik,
		Engines:          s.engines,
		Drive:            s.driveProvisioner,
		MattermostClient: s.mattermostClient,
		OdooClient:       s.odooClient,
		OperatonClient:   s.operatonClient,
		N8nClient:        s.n8nClient,
		StrapiClient:     s.strapiClient,
		AffineClient:     s.affineClient,
		OnlyOfficeClient: s.onlyofficeClient,
		LiteLLMClient:    s.litellmClient,
		Mem0Client:       s.mem0Client,
		OpenClawClient:   s.openclawClient,
		Sync:             s.syncDispatcher,
	}

	// PRC (Platform Resource Claim) engine initialization
	if s.store != nil {
		prcEngine := prc.NewEngine(s.store.Pool())
		prc.MigrateSchema(context.Background(), s.store.Pool())
		deps.PRC = prcEngine
		log.Info().Msg("PRC engine initialized with 8 Foundation providers")
	}

	s.deps = deps

	r.Route("/api/v1", func(r chi.Router) {
		api.RegisterUsers(r, deps)
		api.RegisterGroups(r, deps)
		api.RegisterOUs(r, deps)
		api.RegisterDNS(r, deps)
		api.RegisterDomain(r, deps)
		api.RegisterDirectory(r, deps)
		api.RegisterSecurity(r, deps)
		api.RegisterPolicy(r, deps)
		api.RegisterSystem(r, deps)
		api.RegisterDatabases(r, deps)
		api.RegisterContainers(r, deps)
		api.RegisterMail(r, deps)
		api.RegisterMailHistory(r, deps)
		api.RegisterSMTP(r, deps)
		api.RegisterCredentials(r, deps)
		api.RegisterAccount(r, deps)
		api.RegisterFirewall(r, deps)
		api.RegisterSentinel(r, deps)
		api.RegisterApps(r, deps)
		api.RegisterSettings(r, deps)
		api.RegisterSSO(r, deps)
		api.RegisterSites(r, deps)
		api.RegisterStrapi(r, deps)
		api.RegisterEngines(r, deps)
		api.RegisterChat(r, deps)
		api.RegisterBPMN(r, deps)
		api.RegisterAutomation(r, deps)
		api.RegisterAI(r, deps)
		api.RegisterDrive(r, deps)
		api.RegisterWiki(r, deps)
		api.RegisterProvision(r, deps)
		api.RegisterInfraServices(r, deps)
		api.RegisterComponents(r, deps)
		api.RegisterModules(r, deps)
		api.RegisterMirrors(r, deps)
		api.RegisterSettingsDomain(r, deps)
		api.RegisterBackup(r, deps)
		api.RegisterWorkstream(r, deps)
		r.Route("/alert-rules", func(r chi.Router) {
			api.RegisterAlertRules(r, deps)
		})
		r.Route("/platform", func(r chi.Router) {
			api.RegisterPlatform(r, deps)
		})
	})

	// Setup/Reset lifecycle APIs
	r.Route("/api/setup", func(r chi.Router) {
		api.RegisterSetup(r, deps)
	})
	r.Route("/api/reset", func(r chi.Router) {
		api.RegisterReset(r, deps)
	})
	// Forward Auth endpoint — called by Traefik for SSO token verification
	// Wire the DB-backed base domain resolver so Forward Auth uses service_base_domain
	if s.store != nil {
		auth.BaseDomainResolver = func() string {
			configs, err := s.store.GetConfigs(context.Background(), []string{"service_base_domain"})
			if err != nil {
				return ""
			}
			return configs["service_base_domain"]
		}
	}
	r.Route("/api/internal/auth", func(r chi.Router) {
		auth.RegisterForwardAuth(r, s.cfg)
	})

	r.Route("/api/alerts", func(r chi.Router) {
		api.RegisterAlerts(r, deps)
	})

	// Camunda Modeler compatible endpoint — proxies /engine-rest/* to Operaton
	// Allows modeler to deploy BPMN via Traefik → Core (5th principle: single gateway)
	r.Route("/engine-rest", func(r chi.Router) {
		api.RegisterEngineRestProxy(r, deps)
	})

	// Proxy routes
	r.Route("/mail-proxy", func(r chi.Router) {
		proxy.RegisterStalwart(r, s.cfg)
	})
	r.Route("/es-proxy", func(r chi.Router) {
		proxy.RegisterElasticsearch(r, s.cfg)
	})

	s.router = r
}

// Store returns the underlying store (may be nil if DB init failed).
func (s *Server) Store() *store.Store {
	return s.store
}

// Config returns the server config.
func (s *Server) Config() *config.Config {
	return s.cfg
}

// Start begins listening.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	s.http = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Info().Str("addr", addr).Msg("PolyON Core starting")

	// Regenerate Traefik dynamic config from DB state on startup
	if s.store != nil && s.traefik != nil {
		go func() {
			time.Sleep(3 * time.Second) // let DB settle
			s.regenerateTraefikConfig()
		}()
	}

	// Apply persisted firewall state to Traefik after a brief delay
	// (Traefik container may not be ready immediately at startup)
	if s.deps != nil {
		go func() {
			time.Sleep(10 * time.Second)
			api.InitFirewall(s.deps)
		}()
	}

	return s.http.ListenAndServe()
}

// regenerateTraefikConfig reads DB state and regenerates all Traefik dynamic config files.
func (s *Server) regenerateTraefikConfig() {
	ctx := context.Background()

	// Read domain settings from DB
	configs, err := s.store.GetConfigs(ctx, []string{
		"service_base_domain", "console_domain", "portal_domain", "mail_domain",
	})
	if err != nil {
		log.Error().Err(err).Msg("traefik regenerate: failed to read domain settings")
		return
	}

	baseDomain := configs["service_base_domain"]
	consoleDomain := configs["console_domain"]
	portalDomain := configs["portal_domain"]

	if baseDomain == "" {
		// Fallback to Realm
		baseDomain = strings.ToLower(s.cfg.Realm)
	}
	if consoleDomain == "" && baseDomain != "" {
		consoleDomain = "console." + baseDomain
	}
	if portalDomain == "" && baseDomain != "" {
		portalDomain = "portal." + baseDomain
	}

	// Read apps with subdomain + backend_url
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		log.Error().Err(err).Msg("traefik regenerate: failed to list apps")
		return
	}

	var appRoutes []traefik.AppRoute
	for _, a := range apps {
		if a.Subdomain != "" && a.BackendURL != "" {
			appRoutes = append(appRoutes, traefik.AppRoute{
				ID:         a.ID,
				Subdomain:  a.Subdomain,
				BackendURL: a.BackendURL,
			})
		}
	}

	// Read infra services from DB
	infraSvcs, err := s.store.ListInfraServices(ctx)
	if err != nil {
		log.Error().Err(err).Msg("traefik regenerate: failed to list infra services")
		return
	}

	// Convert store.InfraService → traefik.InfraService (parse PathRules JSON)
	traefikInfra := api.StoreInfraToTraefik(infraSvcs)

	// Build ForwardAuth URL from core service
	forwardAuthURL := api.BuildForwardAuthURL(traefikInfra)

	// Check if wildcard cert exists
	// Core sees /traefik-dynamic/certs/, Traefik sees /etc/traefik/dynamic/certs/
	traefikDynDir := os.Getenv("TRAEFIK_DYNAMIC_DIR")
	if traefikDynDir == "" {
		traefikDynDir = "/traefik-dynamic"
	}
	traefikCertPrefix := os.Getenv("TRAEFIK_CERT_PREFIX")
	if traefikCertPrefix == "" {
		traefikCertPrefix = "/etc/traefik/dynamic"
	}
	certFileLocal := traefikDynDir + "/certs/wildcard.crt"
	certFile := traefikCertPrefix + "/certs/wildcard.crt"
	keyFile := traefikCertPrefix + "/certs/wildcard.key"
	if _, err := os.Stat(certFileLocal); os.IsNotExist(err) {
		certFile = ""
		keyFile = ""
	}

	mailEnabled := len(api.FilterInfraByCategory(traefikInfra, "mail-tcp")) > 0

	genCfg := traefik.GeneratorConfig{
		BaseDomain:     baseDomain,
		ConsoleDomain:  consoleDomain,
		PortalDomain:   portalDomain,
		MailDomain:     configs["mail_domain"],
		CertFile:       certFile,
		KeyFile:        keyFile,
		ForwardAuthURL: forwardAuthURL,
		Apps:           appRoutes,
		InfraServices:  traefikInfra,
		MailEnabled:    mailEnabled,
	}

	if err := traefik.Generate(genCfg); err != nil {
		log.Error().Err(err).Msg("traefik regenerate: failed")
		return
	}

	log.Info().
		Str("baseDomain", baseDomain).
		Int("apps", len(appRoutes)).
		Int("infraServices", len(traefikInfra)).
		Msg("Traefik dynamic config regenerated from DB")
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.store != nil {
		s.store.Close()
	}
	return s.http.Shutdown(ctx)
}
