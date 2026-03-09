package prc

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

// DirectoryProvider provisions Samba AD DC service accounts for modules.
type DirectoryProvider struct {
	Host          string // e.g., "polyon-dc"
	Port          string // e.g., "389"
	BaseDN        string // e.g., "DC=cmars,DC=com"
	AdminUser     string // e.g., "Administrator"
	AdminPassword string
	// ExecFn executes samba-tool commands inside the DC pod.
	// Signature: func(ctx, args) (stdout, error)
	ExecFn func(ctx context.Context, args []string) (string, error)
}

func (p *DirectoryProvider) Type() string        { return "directory" }
func (p *DirectoryProvider) DependsOn() []string { return nil }

func (p *DirectoryProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	ou := claim.ConfigString("ou", "Services")
	svcName := fmt.Sprintf("svc-%s", claim.ModuleID)
	password := generatePassword(24)

	// 1. Ensure OU exists
	ouDN := fmt.Sprintf("OU=%s,%s", ou, p.BaseDN)
	if p.ExecFn != nil {
		// Create OU (ignore error if exists)
		p.ExecFn(ctx, []string{
			"samba-tool", "ou", "create", ouDN,
			"-U", p.AdminUser, "--password=" + p.AdminPassword,
		})

		// 2. Create service account
		_, err := p.ExecFn(ctx, []string{
			"samba-tool", "user", "create", svcName, password,
			"--userou=" + ouDN,
			"--description=PolyON Module: " + claim.ModuleID,
			"-U", p.AdminUser, "--password=" + p.AdminPassword,
		})
		if err != nil {
			// Check if user already exists
			if !strings.Contains(fmt.Sprint(err), "already exists") {
				return nil, fmt.Errorf("create service account %s: %w", svcName, err)
			}
			log.Info().Str("user", svcName).Msg("PRC: AD service account already exists")
		}

		// 3. Set password never expires
		p.ExecFn(ctx, []string{
			"samba-tool", "user", "setexpiry", svcName, "--noexpiry",
			"-U", p.AdminUser, "--password=" + p.AdminPassword,
		})
	}

	bindDN := fmt.Sprintf("CN=%s,%s", svcName, ouDN)

	return Credentials{
		"url":          fmt.Sprintf("ldap://%s:%s", p.Host, p.Port),
		"bindDN":       bindDN,
		"bindPassword": password,
		"baseDN":       p.BaseDN,
		"host":         p.Host,
		"port":         p.Port,
	}, nil
}

func (p *DirectoryProvider) Deprovision(ctx context.Context, claim Claim) error {
	svcName := fmt.Sprintf("svc-%s", claim.ModuleID)

	if p.ExecFn != nil {
		_, err := p.ExecFn(ctx, []string{
			"samba-tool", "user", "delete", svcName,
			"-U", p.AdminUser, "--password=" + p.AdminPassword,
		})
		if err != nil {
			log.Warn().Err(err).Str("user", svcName).Msg("PRC: AD user deletion failed")
		}
	}
	return nil
}

func (p *DirectoryProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	svcName := fmt.Sprintf("svc-%s", claim.ModuleID)
	if p.ExecFn != nil {
		_, err := p.ExecFn(ctx, []string{
			"samba-tool", "user", "show", svcName,
			"-U", p.AdminUser, "--password=" + p.AdminPassword,
		})
		if err != nil {
			return StatusNotFound, nil
		}
		return StatusProvisioned, nil
	}
	return StatusNotFound, nil
}
