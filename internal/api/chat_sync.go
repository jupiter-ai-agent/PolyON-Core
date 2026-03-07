package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/engine/mattermost"
)

// ChatSyncResult represents the result of LDAP sync operation.
type ChatSyncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
	Details []string `json:"details,omitempty"`
}

// ChatSyncLDAPUsers reads AD users and syncs them to Mattermost.
func ChatSyncLDAPUsers(d *Deps) (*ChatSyncResult, error) {
	result := &ChatSyncResult{
		Errors:  []string{},
		Details: []string{},
	}

	// Create new Mattermost client with token
	mmURL := mattermostURL(d)
	mmToken := mattermostToken(d)
	mmClient := mattermost.NewClient(mmURL, mmToken)

	log.Info().Msg("Starting custom LDAP sync")

	// 1. Query AD for active users
	baseDN := d.Cfg.BaseDN()
	filter := "(&(objectClass=user)(!(objectClass=computer))(!(userAccountControl:1.2.840.113556.1.4.803:=2)))"
	attrs := []string{"sAMAccountName", "mail", "givenName", "sn", "displayName", "title", "userAccountControl"}

	adUsers, err := d.LDAP.SearchSubtree(baseDN, filter, attrs)
	if err != nil {
		return nil, fmt.Errorf("LDAP search failed: %w", err)
	}

	log.Info().Int("count", len(adUsers)).Msg("Found AD users")

	// 2. Get existing Mattermost users
	mmUsers, err := mmClient.ListAllUsers()
	if err != nil {
		return nil, fmt.Errorf("failed to get Mattermost users: %w", err)
	}

	log.Info().Int("count", len(mmUsers)).Msg("Found Mattermost users")

	// Create username->user map for quick lookup
	mmUserMap := make(map[string]*mattermost.User)
	for i := range mmUsers {
		mmUser := &mmUsers[i]
		// Only include non-deleted users
		if mmUser.DeleteAt == 0 {
			mmUserMap[strings.ToLower(mmUser.Username)] = mmUser
		}
	}

	// System accounts to skip
	systemAccounts := map[string]bool{
		"admin":      true,
		"system":     true,
		"system-bot": true,
		"advisor":    true,
	}

	// 3. Process each AD user
	for _, adUser := range adUsers {
		sAMAccountName := strings.ToLower(adUser.Get("sAMAccountName"))
		if sAMAccountName == "" {
			result.Errors = append(result.Errors, "AD user missing sAMAccountName")
			continue
		}

		// Skip system accounts
		if systemAccounts[sAMAccountName] {
			result.Skipped++
			result.Details = append(result.Details, fmt.Sprintf("Skipped system account: %s", sAMAccountName))
			continue
		}

		email := adUser.Get("mail")
		if email == "" {
			// Generate email if missing
			email = fmt.Sprintf("%s@%s", sAMAccountName, d.Cfg.Realm)
		}

		firstName := adUser.Get("givenName")
		lastName := adUser.Get("sn")
		displayName := adUser.Get("displayName")
		title := adUser.Get("title")

		// Use displayName as nickname if available
		nickname := displayName
		if nickname == "" {
			nickname = firstName + " " + lastName
		}
		nickname = strings.TrimSpace(nickname)

		// Check if user exists in Mattermost
		mmUser, exists := mmUserMap[sAMAccountName]

		if !exists {
			// Create new user
			password, err := generateRandomPassword()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to generate password for %s: %v", sAMAccountName, err))
				continue
			}

			newUser, err := mmClient.CreateUser(sAMAccountName, email, firstName, lastName, nickname, title, password)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to create user %s: %v", sAMAccountName, err))
				continue
			}

			result.Created++
			result.Details = append(result.Details, fmt.Sprintf("Created user: %s (%s)", sAMAccountName, email))
			log.Info().Str("username", sAMAccountName).Str("id", newUser.ID).Msg("Created Mattermost user")

		} else {
			// Update existing user if needed
			needsUpdate := false
			patch := make(map[string]interface{})

			if mmUser.Email != email {
				patch["email"] = email
				needsUpdate = true
			}
			if mmUser.FirstName != firstName {
				patch["first_name"] = firstName
				needsUpdate = true
			}
			if mmUser.LastName != lastName {
				patch["last_name"] = lastName
				needsUpdate = true
			}
			if mmUser.Nickname != nickname {
				patch["nickname"] = nickname
				needsUpdate = true
			}
			if mmUser.Position != title {
				patch["position"] = title
				needsUpdate = true
			}
			// Ensure auth_service is set to ldap
			if mmUser.AuthService != "ldap" {
				patch["auth_service"] = "ldap"
				needsUpdate = true
			}
			// Ensure auth_data is set to sAMAccountName
			if mmUser.AuthData != sAMAccountName {
				patch["auth_data"] = sAMAccountName
				needsUpdate = true
			}

			if needsUpdate {
				err := mmClient.UpdateUser(mmUser.ID, patch)
				if err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("Failed to update user %s: %v", sAMAccountName, err))
					continue
				}

				result.Updated++
				result.Details = append(result.Details, fmt.Sprintf("Updated user: %s", sAMAccountName))
				log.Info().Str("username", sAMAccountName).Str("id", mmUser.ID).Msg("Updated Mattermost user")
			} else {
				result.Skipped++
			}
		}
	}

	// 4. Log Mattermost users that don't exist in AD (but don't delete them)
	adUsernames := make(map[string]bool)
	for _, adUser := range adUsers {
		sAMAccountName := strings.ToLower(adUser.Get("sAMAccountName"))
		if sAMAccountName != "" {
			adUsernames[sAMAccountName] = true
		}
	}

	orphanCount := 0
	for username := range mmUserMap {
		if !adUsernames[username] && !systemAccounts[username] {
			orphanCount++
			result.Details = append(result.Details, fmt.Sprintf("Mattermost user not in AD: %s (not deleted)", username))
		}
	}

	if orphanCount > 0 {
		log.Info().Int("count", orphanCount).Msg("Found Mattermost users not in AD (not deleted)")
	}

	log.Info().
		Int("created", result.Created).
		Int("updated", result.Updated).
		Int("skipped", result.Skipped).
		Int("errors", len(result.Errors)).
		Msg("LDAP sync completed")

	return result, nil
}

// generateRandomPassword generates a random password for new users.
func generateRandomPassword() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}