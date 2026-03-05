// Package provision implements cross-app organization synchronization.
// AD groups → Mattermost teams/channels + Nextcloud group folders + Stalwart mailing lists.
package provision

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/engine/mattermost"
	"github.com/triangles/polyon-core/internal/engine/nextcloud"
	"github.com/triangles/polyon-core/internal/samba"
)

// ADGroup represents an Active Directory group with its metadata.
type ADGroup struct {
	Name        string   // sAMAccountName / CN
	DisplayName string   // human-readable display name
	DN          string   // distinguished name
	Type        string   // "SG-ORG", "ML", "SG"
	Members     []string // member DNs
}

// SyncComponentResult holds per-component statistics from a sync run.
type SyncComponentResult struct {
	Created int
	Updated int
	Skipped int
	Errors  []string
}

// SyncResult aggregates results across all provisioned apps.
type SyncResult struct {
	Mattermost SyncComponentResult
	Nextcloud  SyncComponentResult
	Mail       SyncComponentResult
}

// OrgSyncer is the central engine that reads AD groups and provisions them
// across Mattermost, Nextcloud, and Stalwart Mail.
type OrgSyncer struct {
	Samba    *samba.Service
	Cfg      *config.Config
	MM       *mattermost.Client
	NC       *nextcloud.Provisioner
	Mail     *MailSyncer
	Logger   *log.Logger
}

// NewOrgSyncer creates an OrgSyncer with the provided dependencies.
// MM, NC, and Mail are optional — nil clients are silently skipped.
func NewOrgSyncer(
	svc *samba.Service,
	cfg *config.Config,
	mm *mattermost.Client,
	nc *nextcloud.Provisioner,
	mail *MailSyncer,
	logger *log.Logger,
) *OrgSyncer {
	if logger == nil {
		logger = log.Default()
	}
	return &OrgSyncer{
		Samba:  svc,
		Cfg:    cfg,
		MM:     mm,
		NC:     nc,
		Mail:   mail,
		Logger: logger,
	}
}

// SyncOrganizations reads all AD org groups and mailing lists, then provisions
// them across Mattermost, Nextcloud, and Stalwart.
//
// Behavior:
//   - SG-ORG-* groups → Mattermost team + Nextcloud group folder
//   - ML-* groups      → Stalwart mailing list
//   - Already-existing resources are skipped (incremental sync)
func (s *OrgSyncer) SyncOrganizations(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	// 1. Load all groups from AD via samba.Service
	orgGroups, mlGroups, err := s.loadADGroups()
	if err != nil {
		return nil, fmt.Errorf("load AD groups: %w", err)
	}

	s.Logger.Printf("[OrgSync] Found %d SG-ORG groups, %d ML groups", len(orgGroups), len(mlGroups))

	// 2. Sync org groups → Mattermost
	if s.MM != nil {
		result.Mattermost = s.syncMattermost(ctx, orgGroups)
	}

	// 3. Sync org groups → Nextcloud group folders
	if s.NC != nil {
		result.Nextcloud = s.syncNextcloud(ctx, orgGroups)
	}

	// 4. Sync ML groups → Stalwart mailing lists
	if s.Mail != nil {
		allGroups := append(orgGroups, mlGroups...)
		r, err := s.Mail.SyncMailingLists(allGroups)
		if err != nil {
			result.Mail.Errors = append(result.Mail.Errors, err.Error())
		} else {
			result.Mail = *r
		}
	}

	return result, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// AD Group loading
// ──────────────────────────────────────────────────────────────────────────────

// loadADGroups retrieves SG-ORG-* and ML-* groups from AD using samba.Service.
func (s *OrgSyncer) loadADGroups() (orgGroups []ADGroup, mlGroups []ADGroup, err error) {
	groups, err := s.Samba.ListGroups()
	if err != nil {
		return nil, nil, err
	}

	for _, g := range groups {
		name := g.Name
		if name == "" {
			name = g.CN
		}

		adg := ADGroup{
			Name:        name,
			DisplayName: groupDisplayName(name),
			DN:          g.DN,
			Type:        groupType(name),
		}

		// Load members for the group
		detail, err := s.Samba.GetGroup(name)
		if err == nil && detail != nil {
			adg.Members = detail.Members
		}

		switch adg.Type {
		case "SG-ORG":
			orgGroups = append(orgGroups, adg)
		case "ML":
			mlGroups = append(mlGroups, adg)
		}
	}
	return orgGroups, mlGroups, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Mattermost sync
// ──────────────────────────────────────────────────────────────────────────────

// syncMattermost ensures each SG-ORG group has a Mattermost team + default channel.
func (s *OrgSyncer) syncMattermost(_ context.Context, groups []ADGroup) SyncComponentResult {
	result := SyncComponentResult{}

	// Get existing teams for dedup
	existingTeams, err := s.MM.ListTeams()
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list teams: %v", err))
		return result
	}

	// Build set of existing teams by display name AND by URL name for dedup.
	existingByDisplay := map[string]string{} // displayName → teamID
	existingByName := map[string]string{}    // name (slug) → teamID
	for _, t := range existingTeams {
		dn, _ := t["display_name"].(string)
		id, _ := t["id"].(string)
		nm, _ := t["name"].(string)
		existingByDisplay[dn] = id
		existingByName[nm] = id
	}

	for _, g := range groups {
		displayName := g.DisplayName
		teamName := mattermost.TeamNameFromDisplay(displayName)

		// Skip if team already exists (by display name or URL slug)
		if _, exists := existingByDisplay[displayName]; exists {
			s.Logger.Printf("[MM] Team already exists (display): %s — skipping", displayName)
			result.Skipped++
			continue
		}
		if _, exists := existingByName[teamName]; exists {
			s.Logger.Printf("[MM] Team already exists (name): %s — skipping", teamName)
			result.Skipped++
			continue
		}

		teamID, err := s.MM.CreateTeam(teamName, displayName)
		if err != nil {
			// Handle "already exists" as idempotent (race condition or previous partial run)
			if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "URL already exists") {
				s.Logger.Printf("[MM] Team already exists (conflict): %s — skipping", displayName)
				result.Skipped++
				continue
			}
			s.Logger.Printf("[MM] ERROR creating team %s: %v", displayName, err)
			result.Errors = append(result.Errors, fmt.Sprintf("create team %s: %v", displayName, err))
			continue
		}

		// Create a default "general" channel in the new team
		_, chErr := s.MM.CreateChannel(teamID, "general", "일반", "O")
		if chErr != nil {
			// Non-fatal — Mattermost may already create a default channel
			s.Logger.Printf("[MM] WARNING: create channel for team %s: %v", displayName, chErr)
		}

		s.Logger.Printf("[MM] Created team: %s (id=%s)", displayName, teamID)
		result.Created++
	}

	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Nextcloud sync
// ──────────────────────────────────────────────────────────────────────────────

// syncNextcloud ensures each SG-ORG group has a Nextcloud team folder.
func (s *OrgSyncer) syncNextcloud(_ context.Context, groups []ADGroup) SyncComponentResult {
	result := SyncComponentResult{}

	for _, g := range groups {
		_, err := s.NC.ProvisionOrg(g.Name, g.DisplayName, 0)
		if err != nil {
			s.Logger.Printf("[NC] ERROR provisioning org %s: %v", g.Name, err)
			result.Errors = append(result.Errors, fmt.Sprintf("provision org %s: %v", g.Name, err))
			continue
		}
		// ProvisionOrg already handles the "already exists" case internally
		s.Logger.Printf("[NC] Provisioned org folder: %s", g.DisplayName)
		result.Created++
	}

	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// groupType infers the group type from its naming convention.
// Returns "SG-ORG", "ML", or "SG".
func groupType(name string) string {
	switch {
	case strings.HasPrefix(name, "SG-ORG-"):
		return "SG-ORG"
	case strings.HasPrefix(name, "ML-"):
		return "ML"
	case strings.HasPrefix(name, "SG-"):
		return "SG"
	default:
		return ""
	}
}

// groupDisplayName strips the standard prefix to produce a human-readable name.
// "SG-ORG-영업팀" → "영업팀", "ML-Sales" → "Sales"
func groupDisplayName(name string) string {
	for _, prefix := range []string{"SG-ORG-", "ML-", "SG-"} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}
