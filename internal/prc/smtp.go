package prc

import (
	"context"

	"github.com/rs/zerolog/log"
)

// SmtpProvider provisions Stalwart Mail service accounts for modules.
type SmtpProvider struct {
	Host       string // e.g., "polyon-mail"
	Port       string // e.g., "587"
	BaseDomain string // e.g., "cmars.com"
	AdminURL   string // Stalwart admin API URL
	AdminToken string // admin bearer token
}

func (p *SmtpProvider) Type() string        { return "smtp" }
func (p *SmtpProvider) DependsOn() []string { return []string{"directory"} }

func (p *SmtpProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	domain := claim.ConfigString("domain", claim.ModuleID)
	user := domain + "@" + p.BaseDomain
	password := generatePassword(24)

	// Phase 2: Stalwart admin API로 서비스 계정 생성
	// POST /api/account { type: "individual", name: user, password: ... }
	log.Info().Str("user", user).Msg("PRC: SMTP account provisioned (Phase 2 - credential-only)")

	return Credentials{
		"host":     p.Host,
		"port":     p.Port,
		"user":     user,
		"password": password,
	}, nil
}

func (p *SmtpProvider) Deprovision(ctx context.Context, claim Claim) error {
	// Phase 2: Stalwart admin API로 삭제
	return nil
}

func (p *SmtpProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	return StatusProvisioned, nil // Phase 2
}
