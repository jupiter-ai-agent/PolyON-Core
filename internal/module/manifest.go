package module

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseManifest parses module.yaml content (YAML or JSON) into a Manifest struct.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	// Try YAML first (superset of JSON)
	if err := yaml.Unmarshal(data, &m); err != nil {
		// Try JSON
		if err2 := json.Unmarshal(data, &m); err2 != nil {
			return nil, fmt.Errorf("parse manifest: yaml=%w, json=%w", err, err2)
		}
	}
	if m.Metadata.ID == "" {
		return nil, fmt.Errorf("manifest metadata.id is required")
	}
	if m.Kind != "Module" {
		return nil, fmt.Errorf("manifest kind must be 'Module', got '%s'", m.Kind)
	}
	return &m, nil
}

// Manifest represents the module.yaml structure according to PP (PolyON Platform) specification.
type Manifest struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

// Metadata contains basic module identification.
type Metadata struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Version     string `yaml:"version" json:"version"`
	Category    string `yaml:"category" json:"category"`
	Icon        string `yaml:"icon" json:"icon"`
	Accent      string `yaml:"accent" json:"accent"`
	Description string `yaml:"description" json:"description"`
	Vendor      string `yaml:"vendor,omitempty" json:"vendor,omitempty"`
	License     string `yaml:"license,omitempty" json:"license,omitempty"`
}

// ClaimSpec represents a Platform Resource Claim in module.yaml.
type ClaimSpec struct {
	Type   string         `yaml:"type" json:"type"`
	Config map[string]any `yaml:"config" json:"config"`
}

// Spec contains the module specification.
type Spec struct {
	Engine    string           `yaml:"engine" json:"engine"`
	Requires  []Dependency     `yaml:"requires,omitempty" json:"requires,omitempty"`
	Optional  []Dependency     `yaml:"optional,omitempty" json:"optional,omitempty"`
	Resources ResourcesSpec    `yaml:"resources" json:"resources"`
	Ingress   IngressSpec      `yaml:"ingress,omitempty" json:"ingress,omitempty"`
	Database  DatabaseSpec     `yaml:"database,omitempty" json:"database,omitempty"`
	LDAP      LDAPBindSpec     `yaml:"ldap,omitempty" json:"ldap,omitempty"`
	OIDC      OIDCSpec         `yaml:"oidc,omitempty" json:"oidc,omitempty"`
	Admin     AdminSpec        `yaml:"admin" json:"admin"`
	Uninstall UninstallSpec    `yaml:"uninstall,omitempty" json:"uninstall,omitempty"`
	// PRC: Platform Resource Claims (replaces legacy Database/LDAP/OIDC)
	Claims    []ClaimSpec      `yaml:"claims,omitempty" json:"claims,omitempty"`
	// PRC: env template mapping ({{ claims.TYPE.KEY }} → env var)
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// PRC: data retention policy on uninstall
	DataPolicy string          `yaml:"dataPolicy,omitempty" json:"dataPolicy,omitempty"`
}

// LDAPBindSpec defines automatic AD/LDAP binding on install.
type LDAPBindSpec struct {
	Bind     bool              `yaml:"bind" json:"bind"`
	Engine   string            `yaml:"engine" json:"engine"`
	Settings map[string]string `yaml:"settings,omitempty" json:"settings,omitempty"`
}

// Dependency represents a module dependency.
type Dependency struct {
	ID      string `yaml:"id" json:"id"`
	Reason  string `yaml:"reason,omitempty" json:"reason,omitempty"`
	Feature string `yaml:"feature,omitempty" json:"feature,omitempty"`
}

// ResourcesSpec defines K8s resource configuration.
type ResourcesSpec struct {
	StatefulSet bool                    `yaml:"statefulset,omitempty" json:"statefulset,omitempty"`
	Image       string                  `yaml:"image" json:"image"`
	Replicas    int                     `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Command     []string                `yaml:"command,omitempty" json:"command,omitempty"`
	Args        []string                `yaml:"args,omitempty" json:"args,omitempty"`
	Ports       []PortSpec              `yaml:"ports" json:"ports"`
	Env         []EnvSpec               `yaml:"env,omitempty" json:"env,omitempty"`
	Health      HealthSpec              `yaml:"health" json:"health"`
	Resources   ResourceLimitsSpec      `yaml:"resources,omitempty" json:"resources,omitempty"`
	PVC         []PVCSpec               `yaml:"pvc,omitempty" json:"pvc,omitempty"`
	ConfigMaps  []string                `yaml:"configMaps,omitempty" json:"configMaps,omitempty"`
}

// PortSpec defines container port configuration.
type PortSpec struct {
	Name          string `yaml:"name" json:"name"`
	ContainerPort int    `yaml:"containerPort" json:"containerPort"`
	Protocol      string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
}

// EnvSpec defines environment variable configuration.
type EnvSpec struct {
	Name      string          `yaml:"name" json:"name"`
	Value     string          `yaml:"value,omitempty" json:"value,omitempty"`
	ValueFrom *EnvFromSpec    `yaml:"valueFrom,omitempty" json:"valueFrom,omitempty"`
}

// EnvFromSpec defines environment variable source.
type EnvFromSpec struct {
	SecretKeyRef *SecretKeyRef `yaml:"secretKeyRef,omitempty" json:"secretKeyRef,omitempty"`
}

// SecretKeyRef references a key in a secret.
type SecretKeyRef struct {
	Name string `yaml:"name" json:"name"`
	Key  string `yaml:"key" json:"key"`
}

// HealthSpec defines health check configuration.
type HealthSpec struct {
	Path         string `yaml:"path" json:"path"`
	Port         int    `yaml:"port" json:"port"`
	InitialDelay int    `yaml:"initialDelay,omitempty" json:"initialDelay,omitempty"`
	Period       int    `yaml:"period,omitempty" json:"period,omitempty"`
}

// ResourceLimitsSpec defines resource requests and limits.
type ResourceLimitsSpec struct {
	Requests ResourceValues `yaml:"requests,omitempty" json:"requests,omitempty"`
	Limits   ResourceValues `yaml:"limits,omitempty" json:"limits,omitempty"`
}

// ResourceValues defines CPU and memory values.
type ResourceValues struct {
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"`
}

// PVCSpec defines persistent volume claim configuration.
type PVCSpec struct {
	Name      string `yaml:"name" json:"name"`
	Size      string `yaml:"size" json:"size"`
	MountPath string `yaml:"mountPath" json:"mountPath"`
}

// IngressSpec defines ingress configuration.
type IngressSpec struct {
	Subdomain   string            `yaml:"subdomain" json:"subdomain"`
	PathPrefix  string            `yaml:"pathPrefix,omitempty" json:"path_prefix,omitempty"`
	Port        int               `yaml:"port" json:"port"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// DatabaseSpec defines database configuration.
type DatabaseSpec struct {
	Create     bool   `yaml:"create" json:"create"`
	Name       string `yaml:"name" json:"name"`
	User       string `yaml:"user" json:"user"`
	Migrations string `yaml:"migrations,omitempty" json:"migrations,omitempty"`
}

// OIDCSpec defines OIDC client configuration.
type OIDCSpec struct {
	Create       bool     `yaml:"create" json:"create"`
	Realm        string   `yaml:"realm,omitempty" json:"realm,omitempty"`
	ClientID     string   `yaml:"clientId" json:"clientId"`
	PublicClient bool     `yaml:"publicClient,omitempty" json:"publicClient,omitempty"`
	RedirectURIs []string `yaml:"redirectUris,omitempty" json:"redirectUris,omitempty"`
	WebOrigins   []string `yaml:"webOrigins,omitempty" json:"webOrigins,omitempty"`
}

// AdminSpec defines Admin Console UI configuration.
type AdminSpec struct {
	Nav NavSpec `yaml:"nav" json:"nav"`
	UI  UISpec  `yaml:"ui" json:"ui"`
}

// NavSpec defines navigation menu configuration.
type NavSpec struct {
	Title       string    `yaml:"title" json:"title"`
	Section     string    `yaml:"section" json:"section"`
	Icon        string    `yaml:"icon" json:"icon"`
	DefaultPath string    `yaml:"defaultPath" json:"defaultPath"`
	SortOrder   int       `yaml:"sortOrder,omitempty" json:"sortOrder,omitempty"`
	Items       []NavItem `yaml:"items,omitempty" json:"items,omitempty"`
}

// NavItem defines a navigation menu item.
type NavItem struct {
	Type  string `yaml:"type,omitempty" json:"type,omitempty"`
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
	Path  string `yaml:"path,omitempty" json:"path,omitempty"`
	Icon  string `yaml:"icon,omitempty" json:"icon,omitempty"`
}

// UISpec defines UI bundle configuration.
type UISpec struct {
	Entry string     `yaml:"entry" json:"entry"`
	Pages []PageSpec `yaml:"pages" json:"pages"`
}

// PageSpec defines a UI page configuration.
type PageSpec struct {
	Path      string `yaml:"path" json:"path"`
	Component string `yaml:"component" json:"component"`
}

// UninstallSpec defines uninstall behavior.
type UninstallSpec struct {
	Confirm    bool                    `yaml:"confirm,omitempty" json:"confirm,omitempty"`
	DataPolicy string                  `yaml:"dataPolicy,omitempty" json:"dataPolicy,omitempty"`
	Resources  UninstallResourcesSpec  `yaml:"resources,omitempty" json:"resources,omitempty"`
}

// UninstallResourcesSpec defines what to delete on uninstall.
type UninstallResourcesSpec struct {
	Databases   []string `yaml:"databases,omitempty" json:"databases,omitempty"`
	PVCs        []string `yaml:"pvcs,omitempty" json:"pvcs,omitempty"`
	OIDCClients []string `yaml:"oidcClients,omitempty" json:"oidcClients,omitempty"`
	Secrets     []string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// RouteInfo represents UI routing information for the Console.
type RouteInfo struct {
	Path      string `json:"path"`
	Component string `json:"component"`
}

// ConvertToRoutes converts PageSpec slice to RouteInfo slice for Console routing.
func (spec *AdminSpec) ConvertToRoutes() []RouteInfo {
	routes := make([]RouteInfo, len(spec.UI.Pages))
	for i, page := range spec.UI.Pages {
		routes[i] = RouteInfo{
			Path:      page.Path,
			Component: page.Component,
		}
	}
	return routes
}

// MarshalJSON helper for nav items.
func MarshalNavItems(items []NavItem) (json.RawMessage, error) {
	if items == nil {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(items)
}

// MarshalRoutes helper for routes.
func MarshalRoutes(routes []RouteInfo) (json.RawMessage, error) {
	if routes == nil {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(routes)
}