package kube

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VersionSyncer watches K8s Deployments and StatefulSets and updates
// the version field of polyon_components from the container image tags.
type VersionSyncer struct {
	client    *Client
	store     *store.Store
}

// NewVersionSyncer creates a VersionSyncer using an existing kube.Client.
// Returns nil, nil when the kube client is unavailable (non-K8s environment).
func NewVersionSyncer(kc *Client, s *store.Store) (*VersionSyncer, error) {
	if kc == nil || kc.cs == nil {
		return nil, nil
	}
	return &VersionSyncer{client: kc, store: s}, nil
}

// Run executes an immediate sync then repeats every 5 minutes.
func (vs *VersionSyncer) Run(ctx context.Context) {
	vs.sync(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			vs.sync(ctx)
		}
	}
}

func (vs *VersionSyncer) sync(ctx context.Context) {
	imageMap := map[string]string{}
	ns := vs.client.namespace

	// Deployment 이름 기준으로 매핑 (DB container_name과 일치)
	// e.g. Deployment "polyon-core" → imageMap["polyon-core"] = "v1.14.6"
	deps, err := vs.client.cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, d := range deps.Items {
			if len(d.Spec.Template.Spec.Containers) > 0 {
				imageMap[d.Name] = extractImageTag(d.Spec.Template.Spec.Containers[0].Image)
			}
		}
	} else {
		log.Warn().Err(err).Str("ns", ns).Msg("version_sync: list deployments failed")
	}

	ssets, err := vs.client.cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, s := range ssets.Items {
			if len(s.Spec.Template.Spec.Containers) > 0 {
				imageMap[s.Name] = extractImageTag(s.Spec.Template.Spec.Containers[0].Image)
			}
		}
	} else {
		log.Warn().Err(err).Str("ns", ns).Msg("version_sync: list statefulsets failed")
	}

	components, err := vs.store.ListComponents(ctx, "", "")
	if err != nil {
		log.Warn().Err(err).Msg("version_sync: list components failed")
		return
	}

	updated := 0
	for _, comp := range components {
		if comp.ContainerName == "" {
			continue
		}
		ver, ok := imageMap[comp.ContainerName]
		if !ok || ver == "" || ver == comp.Version {
			continue
		}
		if err := vs.store.UpdateComponentVersion(ctx, comp.ID, ver); err != nil {
			log.Warn().Err(err).Str("id", comp.ID).Msg("version_sync: update failed")
		} else {
			log.Info().Str("id", comp.ID).Str("version", ver).Msg("version_sync: updated")
			updated++
		}
	}
	if updated > 0 {
		log.Info().Int("count", updated).Msg("version_sync: sync complete")
	}
}

// extractImageTag strips the digest and returns the image tag.
// e.g. "registry/image:v1.2.3@sha256:abc" → "v1.2.3"
//      "registry/image:latest" → "latest"
//      "registry/image" → "latest"
func extractImageTag(image string) string {
	// Strip digest
	if idx := strings.Index(image, "@"); idx != -1 {
		image = image[:idx]
	}
	parts := strings.Split(image, ":")
	if len(parts) < 2 {
		return "latest"
	}
	return parts[len(parts)-1]
}
