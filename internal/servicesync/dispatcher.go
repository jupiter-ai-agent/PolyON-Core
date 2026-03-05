// Package servicesync provides synchronization dispatchers that propagate
// AD/LDAP changes to downstream services such as Mattermost.
package servicesync

import (
	"fmt"
	"strings"

	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/engine/mattermost"
	"github.com/triangles/polyon-core/internal/store"
)

const (
	defaultTeamName = "triangles"
	botUserID       = "ndtqp6sexinubp1etw8ibouz3c"
	townSquareName  = "town-square"
)

// SyncResult holds the outcome of a sync operation across services.
type SyncResult struct {
	ChatOK  bool   `json:"chat_ok"`
	ChatErr string `json:"chat_error,omitempty"`
	MailOK  bool   `json:"mail_ok"`
	MailErr string `json:"mail_error,omitempty"`
}

// Dispatcher dispatches sync events to downstream services.
type Dispatcher struct {
	mm    *mattermost.Client
	store *store.Store
	cfg   *config.Config
}

// New creates a new Dispatcher.
func New(mm *mattermost.Client, st *store.Store, cfg *config.Config) *Dispatcher {
	return &Dispatcher{mm: mm, store: st, cfg: cfg}
}

// OnUserCreated is called after an AD user is successfully created.
// It adds the user to the default Mattermost team and sends a welcome message.
func (d *Dispatcher) OnUserCreated(username, email string) SyncResult {
	result := SyncResult{}

	if d.mm == nil {
		result.ChatErr = "mattermost client not configured"
		return result
	}

	// 1. Find user in Mattermost by email (LDAP sync should have created them)
	mmUser, err := d.mm.GetUserByEmail(email)
	if err != nil {
		result.ChatErr = fmt.Sprintf("user not found in MM (LDAP sync pending?): %v", err)
		d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=false email=%s err=%s", email, result.ChatErr))
		return result
	}

	// 2. Get default team
	team, err := d.mm.GetTeamByNameTyped(defaultTeamName)
	if err != nil {
		result.ChatErr = fmt.Sprintf("team '%s' not found: %v", defaultTeamName, err)
		d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=false err=%s", result.ChatErr))
		return result
	}

	// 3. Add user to team
	if err := d.mm.AddTeamMember(team.ID, mmUser.ID); err != nil {
		result.ChatErr = fmt.Sprintf("add team member failed: %v", err)
		d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=false team=%s err=%s", team.ID, result.ChatErr))
		return result
	}

	// 4. Send welcome message to Town Square
	townSquare, err := d.mm.GetChannelByName(team.ID, townSquareName)
	if err != nil {
		// Non-fatal — user was added to team, just skip welcome message
		result.ChatOK = true
		result.ChatErr = fmt.Sprintf("town-square not found (welcome skipped): %v", err)
		d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=true town_square_err=%v", err))
		return result
	}

	welcome := fmt.Sprintf("👋 **%s** 님이 팀에 합류했습니다! 환영합니다!", username)
	if err := d.mm.SendPost(townSquare.ID, welcome); err != nil {
		// Non-fatal — user added to team, welcome message failed
		result.ChatOK = true
		result.ChatErr = fmt.Sprintf("welcome message failed: %v", err)
	} else {
		result.ChatOK = true
	}

	d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=%v team=%s", result.ChatOK, team.Name))
	return result
}

// OnUserDisabled is called after an AD user is disabled.
// It deactivates the user in Mattermost.
func (d *Dispatcher) OnUserDisabled(username, email string) SyncResult {
	result := SyncResult{}

	if d.mm == nil {
		result.ChatErr = "mattermost client not configured"
		return result
	}

	mmUser, err := d.mm.GetUserByEmail(email)
	if err != nil {
		result.ChatErr = fmt.Sprintf("user not found in MM: %v", err)
		d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=false err=%s", result.ChatErr))
		return result
	}

	if err := d.mm.DeactivateUser(mmUser.ID); err != nil {
		result.ChatErr = fmt.Sprintf("deactivate user failed: %v", err)
		d.logSync("SYNC_CHAT", "user", username, fmt.Sprintf("chat_ok=false err=%s", result.ChatErr))
		return result
	}

	result.ChatOK = true
	d.logSync("SYNC_CHAT", "user", username, "chat_ok=true action=deactivate")
	return result
}

// OnGroupCreated is called after an AD group is successfully created.
// For project groups, it creates a corresponding Mattermost channel.
func (d *Dispatcher) OnGroupCreated(groupName, groupType, description string) SyncResult {
	result := SyncResult{}

	if groupType != "project" {
		// Only sync project groups
		result.ChatOK = true
		return result
	}

	if d.mm == nil {
		result.ChatErr = "mattermost client not configured"
		return result
	}

	// Extract channel name from group name: SG-PROJ-Prism → prism
	channelName := extractChannelName(groupName)
	if channelName == "" {
		result.ChatErr = fmt.Sprintf("cannot derive channel name from group: %s", groupName)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false err=%s", result.ChatErr))
		return result
	}

	// Get default team
	team, err := d.mm.GetTeamByNameTyped(defaultTeamName)
	if err != nil {
		result.ChatErr = fmt.Sprintf("team '%s' not found: %v", defaultTeamName, err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false err=%s", result.ChatErr))
		return result
	}

	// Create public channel
	displayName := channelDisplayName(groupName)
	ch, err := d.mm.CreateChannelFull(team.ID, channelName, displayName, description, "O")
	if err != nil {
		result.ChatErr = fmt.Sprintf("create channel failed: %v", err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false channel=%s err=%s", channelName, result.ChatErr))
		return result
	}

	// Invite polyon-agent bot
	if err := d.mm.AddChannelMember(ch.ID, botUserID); err != nil {
		// Non-fatal — channel created, bot invite failed
		result.ChatOK = true
		result.ChatErr = fmt.Sprintf("bot invite failed: %v", err)
	} else {
		result.ChatOK = true
	}

	d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=%v channel=%s team=%s", result.ChatOK, channelName, team.Name))
	return result
}

// OnMemberAdded is called after a member is added to an AD group.
// For project groups, it adds the member to the corresponding Mattermost channel.
func (d *Dispatcher) OnMemberAdded(groupName, username string) SyncResult {
	result := SyncResult{}

	if !isProjectGroup(groupName) {
		result.ChatOK = true
		return result
	}

	if d.mm == nil {
		result.ChatErr = "mattermost client not configured"
		return result
	}

	// Resolve username → MM user ID
	mmUser, err := d.mm.GetUserByUsernameTyped(username)
	if err != nil {
		result.ChatErr = fmt.Sprintf("user '%s' not found in MM: %v", username, err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false member=%s err=%s", username, result.ChatErr))
		return result
	}

	// Get channel
	ch, err := d.getProjectChannel(groupName)
	if err != nil {
		result.ChatErr = fmt.Sprintf("channel not found for group '%s': %v", groupName, err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false member=%s err=%s", username, result.ChatErr))
		return result
	}

	if err := d.mm.AddChannelMember(ch.ID, mmUser.ID); err != nil {
		result.ChatErr = fmt.Sprintf("add channel member failed: %v", err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false member=%s err=%s", username, result.ChatErr))
		return result
	}

	result.ChatOK = true
	d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=true action=add_member member=%s channel=%s", username, ch.Name))
	return result
}

// OnMemberRemoved is called after a member is removed from an AD group.
// For project groups, it removes the member from the corresponding Mattermost channel.
func (d *Dispatcher) OnMemberRemoved(groupName, username string) SyncResult {
	result := SyncResult{}

	if !isProjectGroup(groupName) {
		result.ChatOK = true
		return result
	}

	if d.mm == nil {
		result.ChatErr = "mattermost client not configured"
		return result
	}

	// Resolve username → MM user ID
	mmUser, err := d.mm.GetUserByUsernameTyped(username)
	if err != nil {
		result.ChatErr = fmt.Sprintf("user '%s' not found in MM: %v", username, err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false member=%s err=%s", username, result.ChatErr))
		return result
	}

	// Get channel
	ch, err := d.getProjectChannel(groupName)
	if err != nil {
		result.ChatErr = fmt.Sprintf("channel not found for group '%s': %v", groupName, err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false member=%s err=%s", username, result.ChatErr))
		return result
	}

	if err := d.mm.RemoveChannelMember(ch.ID, mmUser.ID); err != nil {
		result.ChatErr = fmt.Sprintf("remove channel member failed: %v", err)
		d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=false member=%s err=%s", username, result.ChatErr))
		return result
	}

	result.ChatOK = true
	d.logSync("SYNC_CHAT", "group", groupName, fmt.Sprintf("chat_ok=true action=remove_member member=%s channel=%s", username, ch.Name))
	return result
}

// --- helpers ---

// extractChannelName derives a Mattermost channel name from an AD group name.
// SG-PROJ-Prism → prism, SG-PROJ-My-Project → my-project
func extractChannelName(groupName string) string {
	upper := strings.ToUpper(groupName)
	const prefix = "SG-PROJ-"
	if strings.HasPrefix(upper, prefix) {
		return strings.ToLower(groupName[len(prefix):])
	}
	return ""
}

// channelDisplayName derives a human-readable display name from the group name.
// SG-PROJ-Prism → Prism
func channelDisplayName(groupName string) string {
	upper := strings.ToUpper(groupName)
	const prefix = "SG-PROJ-"
	if strings.HasPrefix(upper, prefix) {
		return groupName[len(prefix):]
	}
	return groupName
}

// isProjectGroup returns true if the group name matches the project prefix.
func isProjectGroup(groupName string) bool {
	return strings.HasPrefix(strings.ToUpper(groupName), "SG-PROJ-")
}

// getProjectChannel resolves the Mattermost channel for a project group.
func (d *Dispatcher) getProjectChannel(groupName string) (*mattermost.Channel, error) {
	channelName := extractChannelName(groupName)
	if channelName == "" {
		return nil, fmt.Errorf("cannot derive channel name from %s", groupName)
	}

	team, err := d.mm.GetTeamByNameTyped(defaultTeamName)
	if err != nil {
		return nil, fmt.Errorf("team '%s' not found: %w", defaultTeamName, err)
	}

	return d.mm.GetChannelByName(team.ID, channelName)
}

// logSync writes an audit log entry (non-fatal if store is nil).
func (d *Dispatcher) logSync(action, objectType, objectName, details string) {
	if d.store == nil {
		return
	}
	d.store.LogAction(action, objectType, objectName, details, "system", "")
}
