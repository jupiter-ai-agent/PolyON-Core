package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/store"
	"github.com/triangles/polyon-core/internal/traefik"
)

// triggerTraefikRegenerate reads the current DB state and regenerates all
// Traefik dynamic config files. Safe to call from a goroutine.
// Uses background context since the HTTP request context may be canceled.
func triggerTraefikRegenerate(d *Deps, _ context.Context) {
	ctx := context.Background()
	if d.Store == nil {
		log.Warn().Msg("traefik regen: store is nil, skipping")
		return
	}

	configs, err := d.Store.GetConfigs(ctx, []string{
		"service_base_domain", "console_domain", "portal_domain", "mail_domain",
	})
	if err != nil {
		log.Error().Err(err).Msg("traefik regen: failed to read domain settings")
		return
	}

	baseDomain := configs["service_base_domain"]
	consoleDomain := configs["console_domain"]
	portalDomain := configs["portal_domain"]

	if baseDomain == "" && d.Cfg.Realm != "" {
		baseDomain = strings.ToLower(d.Cfg.Realm)
	}
	if consoleDomain == "" && baseDomain != "" {
		consoleDomain = "console." + baseDomain
	}
	if portalDomain == "" && baseDomain != "" {
		portalDomain = "portal." + baseDomain
	}

	// Read apps
	apps, err := d.Store.ListApps(ctx)
	if err != nil {
		log.Error().Err(err).Msg("traefik regen: failed to list apps")
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
	infraSvcs, err := d.Store.ListInfraServices(ctx)
	if err != nil {
		log.Error().Err(err).Msg("traefik regen: failed to list infra services")
		return
	}

	// Convert store.InfraService → traefik.InfraService (parse PathRules JSON)
	traefikInfra := StoreInfraToTraefik(infraSvcs)

	// Build ForwardAuth URL from core service
	forwardAuthURL := BuildForwardAuthURL(traefikInfra)

	// Check wildcard cert
	outputDir := os.Getenv("TRAEFIK_DYNAMIC_DIR")
	if outputDir == "" {
		outputDir = "/traefik-dynamic"
	}

	// Core writes to outputDir, but Traefik sees it as /etc/traefik/dynamic/
	// TRAEFIK_CERT_PREFIX is the path prefix from Traefik's perspective
	traefikCertPrefix := os.Getenv("TRAEFIK_CERT_PREFIX")
	if traefikCertPrefix == "" {
		traefikCertPrefix = "/etc/traefik/dynamic"
	}

	certFileLocal := outputDir + "/certs/wildcard.crt"
	certFile := traefikCertPrefix + "/certs/wildcard.crt"
	keyFile := traefikCertPrefix + "/certs/wildcard.key"
	if _, err := os.Stat(certFileLocal); os.IsNotExist(err) {
		certFile = ""
		keyFile = ""
	}

	mailEnabled := len(FilterInfraByCategory(traefikInfra, "mail-tcp")) > 0

	genCfg := traefik.GeneratorConfig{
		OutputDir:      outputDir,
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
		log.Error().Err(err).Msg("traefik regen: generation failed")
		return
	}

	log.Info().
		Str("baseDomain", baseDomain).
		Int("apps", len(appRoutes)).
		Int("infraServices", len(traefikInfra)).
		Msg("Traefik dynamic config regenerated")
}

// StoreInfraToTraefik converts store.InfraService slice to traefik.InfraService slice,
// parsing PathRules JSON along the way.
// Exported for use by server.go.
func StoreInfraToTraefik(svcs []store.InfraService) []traefik.InfraService {
	result := make([]traefik.InfraService, 0, len(svcs))
	for _, svc := range svcs {
		var rules []traefik.PathRule
		if svc.PathRules != "" {
			if err := json.Unmarshal([]byte(svc.PathRules), &rules); err != nil {
				log.Warn().Err(err).Str("service", svc.ID).Msg("traefik regen: failed to parse PathRules, skipping rules")
				rules = nil
			}
		}
		result = append(result, traefik.InfraService{
			ID:         svc.ID,
			Name:       svc.Name,
			Host:       svc.Host,
			Port:       svc.Port,
			Protocol:   svc.Protocol,
			Category:   svc.Category,
			EntryPoint: svc.EntryPoint,
			PathRules:  rules,
			Enabled:    svc.Enabled,
		})
	}
	return result
}

// BuildForwardAuthURL constructs the ForwardAuth URL from the "core" infra service.
// Falls back to a hardcoded default if not found.
// Exported for use by server.go.
func BuildForwardAuthURL(svcs []traefik.InfraService) string {
	for _, s := range svcs {
		if s.ID == "core" {
			return fmt.Sprintf("http://%s:%d/api/internal/auth/verify", s.Host, s.Port)
		}
	}
	// Fallback from config (populated from env or DB)
	if cfg := config.Get(); cfg != nil && cfg.CoreURL != "" {
		log.Warn().Msg("traefik regen: core infra service not found in list, falling back to config CoreURL")
		return cfg.CoreURL + "/api/internal/auth/verify"
	}
	log.Warn().Msg("traefik regen: core infra service not found, using hardcoded fallback ForwardAuth URL")
	return "http://polyon-core:8000/api/internal/auth/verify"
}

// FilterInfraByCategory returns traefik.InfraService entries matching the category.
// Exported for use by server.go.
func FilterInfraByCategory(svcs []traefik.InfraService, category string) []traefik.InfraService {
	var result []traefik.InfraService
	for _, s := range svcs {
		if s.Category == category && s.Enabled {
			result = append(result, s)
		}
	}
	return result
}
