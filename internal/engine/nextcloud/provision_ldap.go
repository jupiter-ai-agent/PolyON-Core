package nextcloud

import (
	"fmt"
	"strconv"

	"github.com/triangles/polyon-core/internal/docker"
)

// LDAPConfig holds the parameters for configuring Nextcloud LDAP backend via occ.
type LDAPConfig struct {
	Host          string
	Port          int
	BaseDN        string
	AdminDN       string
	AdminPassword string
	Container     string // Docker container name, e.g. "polyon-drive"
}

// ConfigureLDAP sets up the Nextcloud LDAP backend by running occ commands
// inside the Nextcloud Docker container.
//
// It creates an empty LDAP config (s01) if one doesn't exist, then applies
// all AD-compatible settings for sAMAccountName-based authentication.
func ConfigureLDAP(d *docker.Client, cfg LDAPConfig) error {
	container := cfg.Container
	if container == "" {
		container = "polyon-drive"
	}

	host := cfg.Host
	if host == "" {
		host = "polyon-dc"
	}

	port := cfg.Port
	if port == 0 {
		port = 389
	}

	// Step 1: create an empty LDAP config slot (idempotent — ok if already exists)
	_, _ = occ(d, container, "ldap:create-empty-config")

	// Step 2: apply all settings
	settings := []struct {
		key   string
		value string
	}{
		{"ldapHost", host},
		{"ldapPort", strconv.Itoa(port)},
		{"ldapBase", cfg.BaseDN},
		{"ldapAgentName", cfg.AdminDN},
		{"ldapAgentPassword", cfg.AdminPassword},
		{"ldapLoginFilter", "(&(objectClass=user)(|(sAMAccountName=%uid)(mail=%uid)))"},
		{"ldapUserFilter", "(&(objectClass=user)(!(objectClass=computer))(!(userAccountControl:1.2.840.113556.1.4.803:=2)))"},
		{"ldapGroupFilter", "(&(objectClass=group))"},
		{"ldapEmailAttribute", "mail"},
		{"ldapUserDisplayName", "displayName"},
		{"ldapGroupDisplayName", "cn"},
		{"ldapGroupMemberAssocAttr", "member"},
		{"ldapUserUUID", "objectGUID"},
		{"ldapGroupUUID", "objectGUID"},
		{"ldapUserName", "sAMAccountName"},
		{"ldapFirstNameAttribute", "givenName"},
		{"ldapLastNameAttribute", "sn"},
		{"turnOnPasswordChange", "0"},
		{"ldapConfigurationActive", "1"},
	}

	for _, s := range settings {
		if _, err := occ(d, container, "ldap:set-config", "s01", s.key, s.value); err != nil {
			return fmt.Errorf("nextcloud ldap set-config %s: %w", s.key, err)
		}
	}

	return nil
}

// TestLDAP runs occ ldap:test-config to verify the LDAP connection.
func TestLDAP(d *docker.Client, container string) error {
	if container == "" {
		container = "polyon-drive"
	}
	out, err := occ(d, container, "ldap:test-config", "s01")
	if err != nil {
		return fmt.Errorf("ldap:test-config: %w (output: %s)", err, out)
	}
	return nil
}

// occ runs a php occ command inside the given container and returns stdout.
func occ(d *docker.Client, container string, args ...string) (string, error) {
	cmd := append([]string{"php", "occ"}, args...)
	return d.ExecInContainer(container, cmd)
}
