package module

import (
	"embed"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

//go:embed catalog/*
var catalogFS embed.FS

// FindCatalogManifest checks if the given image matches a built-in catalog module.
// Returns nil if not found (caller should fall back to image extraction).
func FindCatalogManifest(image string) *Manifest {
	// Extract module name from image: "jupitertriangles/polyon-chat:v1.0.0" → "chat"
	name := extractModuleName(image)
	if name == "" {
		return nil
	}

	// Try catalog/{name}/module.yaml
	path := filepath.Join("catalog", name, "module.yaml")
	data, err := catalogFS.ReadFile(path)
	if err != nil {
		return nil // not in catalog
	}

	m, err := ParseManifest(data)
	if err != nil {
		log.Warn().Err(err).Str("path", path).Msg("catalog manifest parse failed")
		return nil
	}

	log.Info().Str("module", m.Metadata.ID).Str("name", m.Metadata.Name).Msg("Found catalog manifest")
	return m
}

// FindCatalogManifestByID searches catalog by module ID (e.g., "mattermost").
func FindCatalogManifestByID(moduleID string) *Manifest {
	// Try each catalog directory
	entries, err := catalogFS.ReadDir("catalog")
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := "catalog/" + entry.Name() + "/module.yaml"
		data, err := catalogFS.ReadFile(path)
		if err != nil {
			continue
		}
		m, err := ParseManifest(data)
		if err != nil {
			continue
		}
		if m.Metadata.ID == moduleID {
			return m
		}
	}
	return nil
}

// extractModuleName extracts the module name from a Docker image reference.
// "jupitertriangles/polyon-chat:v1.0.0" → "chat"
// "jupitertriangles/polyon-wiki:latest" → "wiki"
// "myrepo/custom-app:1.0" → "custom-app" (no polyon- prefix)
func extractModuleName(image string) string {
	// Remove tag
	img := image
	if idx := strings.LastIndex(img, ":"); idx > 0 {
		img = img[:idx]
	}
	// Get last part (after /)
	if idx := strings.LastIndex(img, "/"); idx >= 0 {
		img = img[idx+1:]
	}
	// Remove "polyon-" prefix
	img = strings.TrimPrefix(img, "polyon-")
	return img
}
