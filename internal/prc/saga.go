package prc

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
)

// SagaExecutor orchestrates claim provisioning with topological sort
// and compensation rollback on failure.
type SagaExecutor struct {
	providers map[string]ResourceProvider
}

// NewSagaExecutor creates a SagaExecutor with the given providers.
func NewSagaExecutor(providers []ResourceProvider) *SagaExecutor {
	m := make(map[string]ResourceProvider, len(providers))
	for _, p := range providers {
		m[p.Type()] = p
	}
	return &SagaExecutor{providers: m}
}

// Execute provisions all claims in dependency order.
// On failure, previously provisioned claims are compensated in reverse.
func (s *SagaExecutor) Execute(ctx context.Context, claims []Claim) (map[string]Credentials, []ProvisionResult, error) {
	if len(claims) == 0 {
		return map[string]Credentials{}, nil, nil
	}

	// 1. Topological sort
	sorted, err := s.topologicalSort(claims)
	if err != nil {
		return nil, nil, fmt.Errorf("dependency resolution failed: %w", err)
	}

	// 2. Provision in order
	results := make(map[string]Credentials)
	var completed []Claim
	var logs []ProvisionResult

	for _, claim := range sorted {
		provider, ok := s.providers[claim.Type]
		if !ok {
			s.compensate(ctx, completed)
			return nil, logs, fmt.Errorf("unsupported claim type: %s", claim.Type)
		}

		start := time.Now()
		creds, err := provider.Provision(ctx, claim)
		dur := time.Since(start).Milliseconds()

		pr := ProvisionResult{
			ClaimType:  claim.Type,
			DurationMs: dur,
		}

		if err != nil {
			pr.Status = "failed"
			pr.Error = err.Error()
			logs = append(logs, pr)

			log.Error().Err(err).
				Str("claim", claim.Type).
				Str("module", claim.ModuleID).
				Msg("PRC: provisioning failed, starting compensation")

			s.compensate(ctx, completed)
			return nil, logs, fmt.Errorf("claim %s provisioning failed: %w", claim.Type, err)
		}

		pr.Status = "provisioned"
		pr.Credentials = creds
		logs = append(logs, pr)

		results[claim.Type] = creds
		completed = append(completed, claim)

		log.Info().
			Str("claim", claim.Type).
			Str("module", claim.ModuleID).
			Int64("ms", dur).
			Msg("PRC: provisioned")
	}

	return results, logs, nil
}

// Compensate deprovisions all claims in reverse order (for uninstall or failure).
func (s *SagaExecutor) Compensate(ctx context.Context, claims []Claim) {
	s.compensate(ctx, claims)
}

func (s *SagaExecutor) compensate(ctx context.Context, completed []Claim) {
	for i := len(completed) - 1; i >= 0; i-- {
		claim := completed[i]
		provider, ok := s.providers[claim.Type]
		if !ok {
			continue
		}
		if err := provider.Deprovision(ctx, claim); err != nil {
			log.Error().Err(err).
				Str("claim", claim.Type).
				Str("module", claim.ModuleID).
				Msg("PRC: compensation failed (manual cleanup required)")
		} else {
			log.Info().
				Str("claim", claim.Type).
				Str("module", claim.ModuleID).
				Msg("PRC: compensated")
		}
	}
}

// topologicalSort orders claims by dependency using Kahn's algorithm.
func (s *SagaExecutor) topologicalSort(claims []Claim) ([]Claim, error) {
	// Active types in this request
	active := make(map[string]bool)
	for _, c := range claims {
		active[c.Type] = true
	}

	// Build adjacency and in-degree
	inDegree := make(map[string]int)
	adj := make(map[string][]string)
	for _, c := range claims {
		if _, ok := inDegree[c.Type]; !ok {
			inDegree[c.Type] = 0
		}
		provider, ok := s.providers[c.Type]
		if !ok {
			continue
		}
		for _, dep := range provider.DependsOn() {
			if active[dep] {
				adj[dep] = append(adj[dep], c.Type)
				inDegree[c.Type]++
			}
		}
	}

	// BFS — Kahn's algorithm
	var queue []string
	for t, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, t)
		}
	}
	sort.Strings(queue) // deterministic order for same-level

	var order []string
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		order = append(order, curr)
		for _, next := range adj[curr] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(order) != len(claims) {
		return nil, fmt.Errorf("circular dependency detected")
	}

	// Reorder claims by topology
	typeIndex := make(map[string]int)
	for i, t := range order {
		typeIndex[t] = i
	}
	sorted := make([]Claim, len(claims))
	copy(sorted, claims)
	sort.SliceStable(sorted, func(i, j int) bool {
		return typeIndex[sorted[i].Type] < typeIndex[sorted[j].Type]
	})

	return sorted, nil
}
