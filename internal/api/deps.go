// Package api provides HTTP handlers for all PolyON API endpoints.
package api

import (
	"github.com/triangles/polyon-core/internal/builder"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/configtrack"
	"github.com/triangles/polyon-core/internal/docker"
	"github.com/triangles/polyon-core/internal/engine"
	affineEngine "github.com/triangles/polyon-core/internal/engine/affine"
	litellmEngine "github.com/triangles/polyon-core/internal/engine/litellm"
	"github.com/triangles/polyon-core/internal/engine/mattermost"
	mem0Engine "github.com/triangles/polyon-core/internal/engine/mem0"
	"github.com/triangles/polyon-core/internal/engine/n8n"
	"github.com/triangles/polyon-core/internal/engine/nextcloud"
	"github.com/triangles/polyon-core/internal/engine/odoo"
	onlyofficeEngine "github.com/triangles/polyon-core/internal/engine/onlyoffice"
	openclawEngine "github.com/triangles/polyon-core/internal/engine/openclaw"
	"github.com/triangles/polyon-core/internal/engine/operaton"
	strapiEngine "github.com/triangles/polyon-core/internal/engine/strapi"
	"github.com/triangles/polyon-core/internal/gitea"
	"github.com/triangles/polyon-core/internal/kube"
	ldapPkg "github.com/triangles/polyon-core/internal/ldap"
	"github.com/triangles/polyon-core/internal/samba"
	"github.com/triangles/polyon-core/internal/servicesync"
	"github.com/triangles/polyon-core/internal/store"
	"github.com/triangles/polyon-core/internal/traefik"
)

// Deps holds all shared dependencies for API handlers.
type Deps struct {
	Cfg     *config.Config
	Docker  *docker.Client
	Kube    *kube.Client
	LDAP    *ldapPkg.Client
	Samba   *samba.Service
	Store   *store.Store
	Builder *builder.Builder
	Gitea   *gitea.Client
	Traefik *traefik.Manager
	Engines *engine.Registry
	Drive   *nextcloud.Provisioner // PolyON Drive auto-provisioning

	// Engine clients for direct provisioning access
	MattermostClient  *mattermost.Client
	OdooClient        *odoo.Client
	OperatonClient    *operaton.Client
	N8nClient         *n8n.Client
	StrapiClient      *strapiEngine.Client
	AffineClient      *affineEngine.Client      // AFFiNE Wiki — Keycloak OIDC
	OnlyOfficeClient  *onlyofficeEngine.Client  // OnlyOffice — JWT internal
	LiteLLMClient     *litellmEngine.Client     // LiteLLM AI Gateway — Forward Auth
	Mem0Client        *mem0Engine.Client        // Mem0 — AI Memory Layer
	OpenClawClient    *openclawEngine.Client    // OpenClaw — AI Agent Runtime

	// Service sync dispatcher — propagates AD changes to downstream services
	Sync *servicesync.Dispatcher

	// ConfigTracker writes configuration changes to the polyon-config Gitea repo.
	ConfigTracker *configtrack.ConfigTracker
}
