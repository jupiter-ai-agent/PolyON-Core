package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/engine/nextcloud"
)

// DriveSyncResult represents the result of LDAP sync operation.
type DriveSyncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
	Details []string `json:"details,omitempty"`
}

// DriveSyncLDAPUsers reads AD users and syncs them to Nextcloud Drive.
func DriveSyncLDAPUsers(d *Deps) (*DriveSyncResult, error) {
	result := &DriveSyncResult{
		Errors:  []string{},
		Details: []string{},
	}

	// Create Nextcloud client
	nc := nextcloud.NewClient(d.Cfg.NextcloudURL, "admin", d.Cfg.NextcloudAdminPassword, "")

	log.Info().Msg("Starting custom LDAP sync for Nextcloud Drive")

	// 1. Query AD for active users
	baseDN := d.Cfg.BaseDN()
	filter := "(&(objectClass=user)(!(objectClass=computer))(!(userAccountControl:1.2.840.113556.1.4.803:=2)))"
	attrs := []string{"sAMAccountName", "mail", "givenName", "sn", "displayName", "userAccountControl"}

	adUsers, err := d.LDAP.SearchSubtree(baseDN, filter, attrs)
	if err != nil {
		return nil, fmt.Errorf("LDAP search failed: %w", err)
	}

	log.Info().Int("count", len(adUsers)).Msg("Found AD users")

	// 2. Get existing Nextcloud users
	ncUsernames, err := nc.ListOCSUsers()
	if err != nil {
		return nil, fmt.Errorf("failed to get Nextcloud users: %w", err)
	}

	log.Info().Int("count", len(ncUsernames)).Msg("Found Nextcloud users")

	// Build map of existing users with details
	ncUserMap := make(map[string]*nextcloud.OCSUser)
	for _, username := range ncUsernames {
		user, err := nc.GetOCSUser(username)
		if err != nil {
			log.Warn().Str("username", username).Err(err).Msg("Failed to get user details")
			continue
		}
		ncUserMap[strings.ToLower(username)] = user
	}

	// System accounts to skip
	systemAccounts := map[string]bool{
		"admin":   true,
		"system":  true,
		"guest":   true,
		"polyon":  true,
		"advisor": true,
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

		// Use displayName if available, otherwise build from first + last name
		if displayName == "" {
			displayName = strings.TrimSpace(firstName + " " + lastName)
		}

		// Check if user exists in Nextcloud
		ncUser, exists := ncUserMap[sAMAccountName]

		if !exists {
			// Generate random password (32 bytes hex)
			passwordBytes := make([]byte, 32)
			if _, err := rand.Read(passwordBytes); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to generate password for %s: %v", sAMAccountName, err))
				continue
			}
			password := hex.EncodeToString(passwordBytes)

			// Create new user
			err := nc.CreateOCSUser(sAMAccountName, password, displayName, email)
			if err != nil {
				if strings.Contains(err.Error(), "already exists") {
					result.Skipped++
					result.Details = append(result.Details, fmt.Sprintf("User already exists: %s", sAMAccountName))
					continue
				}
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to create user %s: %v", sAMAccountName, err))
				continue
			}

			result.Created++
			result.Details = append(result.Details, fmt.Sprintf("Created user: %s (%s)", sAMAccountName, email))
			log.Info().Str("username", sAMAccountName).Msg("Created Nextcloud user")

		} else {
			// Update existing user if needed
			needsUpdate := false

			// Check if email needs update
			if ncUser.Email != email {
				err := nc.UpdateOCSUser(sAMAccountName, "email", email)
				if err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("Failed to update email for %s: %v", sAMAccountName, err))
					continue
				}
				needsUpdate = true
			}

			// Check if display name needs update
			if ncUser.DisplayName != displayName {
				err := nc.UpdateOCSUser(sAMAccountName, "displayname", displayName)
				if err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("Failed to update displayname for %s: %v", sAMAccountName, err))
					continue
				}
				needsUpdate = true
			}

			if needsUpdate {
				result.Updated++
				result.Details = append(result.Details, fmt.Sprintf("Updated user: %s", sAMAccountName))
				log.Info().Str("username", sAMAccountName).Msg("Updated Nextcloud user")
			} else {
				result.Skipped++
			}
		}
	}

	// 4. Log Nextcloud users that don't exist in AD (but don't delete them)
	adUsernames := make(map[string]bool)
	for _, adUser := range adUsers {
		sAMAccountName := strings.ToLower(adUser.Get("sAMAccountName"))
		if sAMAccountName != "" {
			adUsernames[sAMAccountName] = true
		}
	}

	orphanCount := 0
	for username := range ncUserMap {
		if !adUsernames[username] && !systemAccounts[username] {
			orphanCount++
			result.Details = append(result.Details, fmt.Sprintf("Nextcloud user not in AD: %s (not deleted)", username))
		}
	}

	if orphanCount > 0 {
		log.Info().Int("count", orphanCount).Msg("Found Nextcloud users not in AD (not deleted)")
	}

	log.Info().
		Int("created", result.Created).
		Int("updated", result.Updated).
		Int("skipped", result.Skipped).
		Int("errors", len(result.Errors)).
		Msg("Nextcloud LDAP sync completed")

	return result, nil
}