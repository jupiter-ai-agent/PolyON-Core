package nextcloud

import (
	"fmt"
	"log"
	"strings"
)

// Provisioner handles automatic Drive provisioning when users/orgs are created.
// This is the core of "생성 = 즉시 폴더" principle.
type Provisioner struct {
	client *Client
	logger *log.Logger
}

func NewProvisioner(client *Client, logger *log.Logger) *Provisioner {
	return &Provisioner{client: client, logger: logger}
}

// Client returns the underlying Nextcloud API client.
func (p *Provisioner) Client() *Client {
	return p.client
}

// ──────────────────────────────────────────────────────────────────────────────
// User Provisioning
// ──────────────────────────────────────────────────────────────────────────────

// ProvisionUser ensures a user has a personal Drive folder.
// Called immediately after AD DC user creation.
// Nextcloud auto-creates the home directory on first access via LDAP backend.
func (p *Provisioner) ProvisionUser(username string) error {
	p.logger.Printf("[Drive] Provisioning user: %s", username)

	// Trigger user lookup → Nextcloud creates home dir for LDAP user
	if err := p.client.EnsureUserProvisioned(username); err != nil {
		return fmt.Errorf("provision user %s: %w", username, err)
	}

	p.logger.Printf("[Drive] User provisioned: %s", username)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Organization (Team Folder) Provisioning
// ──────────────────────────────────────────────────────────────────────────────

// ProvisionOrg creates a team folder for an AD organizational group.
// Called immediately after AD DC group creation.
//
// Parameters:
//   - adGroupName: AD group name (e.g. "SG-ORG-경영지원본부")
//   - folderDisplayName: Human-readable folder name (e.g. "경영지원본부")
//   - quotaBytes: Folder quota in bytes (-3 = unlimited, 0 = default 10GB)
//
// Returns the created folder ID.
func (p *Provisioner) ProvisionOrg(adGroupName, folderDisplayName string, quotaBytes int64) (int, error) {
	p.logger.Printf("[Drive] Provisioning org folder: %s → %s", adGroupName, folderDisplayName)

	// Check if folder already exists
	folders, err := p.client.ListGroupFolders()
	if err != nil {
		return 0, fmt.Errorf("list folders: %w", err)
	}
	for _, f := range folders {
		if f.Name == folderDisplayName {
			p.logger.Printf("[Drive] Folder already exists: %s (id=%d)", folderDisplayName, f.ID)
			// Ensure group mapping exists
			if _, ok := f.Groups[adGroupName]; !ok {
				_ = p.client.AddGroupToFolder(f.ID, adGroupName, 31) // full permissions
			}
			return f.ID, nil
		}
	}

	// Create team folder
	folderID, err := p.client.CreateGroupFolder(folderDisplayName)
	if err != nil {
		return 0, fmt.Errorf("create folder %s: %w", folderDisplayName, err)
	}

	// Map AD group with full permissions (31 = read+update+create+delete+share)
	if err := p.client.AddGroupToFolder(folderID, adGroupName, 31); err != nil {
		return folderID, fmt.Errorf("add group %s to folder %d: %w", adGroupName, folderID, err)
	}

	// Set quota
	if quotaBytes == 0 {
		quotaBytes = 10 * 1024 * 1024 * 1024 // default 10GB
	}
	if err := p.client.SetFolderQuota(folderID, quotaBytes); err != nil {
		p.logger.Printf("[Drive] WARNING: quota set failed for folder %d: %v", folderID, err)
	}

	p.logger.Printf("[Drive] Org folder provisioned: %s (id=%d, group=%s)", folderDisplayName, folderID, adGroupName)
	return folderID, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Membership Changes
// ──────────────────────────────────────────────────────────────────────────────

// OnUserAddedToOrg is called when a user is added to an AD organizational group.
// Since Nextcloud team folders use LDAP group membership, the user automatically
// gets access. This method just ensures the user's Nextcloud account exists.
func (p *Provisioner) OnUserAddedToOrg(username, adGroupName string) error {
	p.logger.Printf("[Drive] User %s added to org %s", username, adGroupName)
	return p.ProvisionUser(username)
}

// ──────────────────────────────────────────────────────────────────────────────
// Deprovisioning (soft — never delete data)
// ──────────────────────────────────────────────────────────────────────────────

// DeprovisionOrg removes group access from a team folder but does NOT delete it.
// Data preservation is critical.
func (p *Provisioner) DeprovisionOrg(adGroupName, folderDisplayName string) error {
	p.logger.Printf("[Drive] Deprovisioning org: %s", adGroupName)

	folders, err := p.client.ListGroupFolders()
	if err != nil {
		return err
	}

	for _, f := range folders {
		if f.Name == folderDisplayName {
			return p.client.RemoveGroupFromFolder(f.ID, adGroupName)
		}
	}

	return nil // folder not found, nothing to do
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// OrgGroupToFolderName converts AD group naming convention to display name.
// Examples:
//   - "SG-ORG-경영지원본부" → "경영지원본부"
//   - "SG-ORG-DevOps팀"   → "DevOps팀"
//   - "ML-AdminOffice"     → "AdminOffice"
//   - "ML-Research"        → "Research"
func OrgGroupToFolderName(adGroupName string) string {
	// Remove common prefixes
	for _, prefix := range []string{"SG-ORG-", "ML-"} {
		if strings.HasPrefix(adGroupName, prefix) {
			return strings.TrimPrefix(adGroupName, prefix)
		}
	}
	return adGroupName
}
