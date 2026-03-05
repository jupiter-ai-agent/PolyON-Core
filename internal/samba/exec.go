// Package samba provides the SambaService for AD operations.
package samba

import (
	"fmt"
	"strings"

	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/docker"
	ldapPkg "github.com/triangles/polyon-core/internal/ldap"
)

// Result represents a samba-tool command result.
type Result struct {
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Service provides all PolyON AD DC operations.
type Service struct {
	cfg    *config.Config
	docker *docker.Client
	ldap   *ldapPkg.Client
}

// New creates a new Samba service.
func New(cfg *config.Config, dc *docker.Client, lc *ldapPkg.Client) *Service {
	return &Service{cfg: cfg, docker: dc, ldap: lc}
}

// Realm returns the current realm.
func (s *Service) Realm() string {
	return s.cfg.Realm
}

// BaseDN returns the base DN.
func (s *Service) BaseDN() string {
	return s.cfg.BaseDN()
}

// runTool executes samba-tool in the DC container via Docker SDK.
func (s *Service) runTool(args ...string) Result {
	// Replace samba_host with 127.0.0.1 inside DC container
	for i, a := range args {
		if a == s.cfg.SambaHost {
			args[i] = "127.0.0.1"
		}
	}

	output, err := s.docker.ExecSambaTool(s.cfg.DCContainer, args...)
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	return Result{Success: true, Output: output}
}

// adminAuth returns the -U flag for samba-tool commands requiring auth.
func (s *Service) adminAuth() string {
	return fmt.Sprintf("Administrator%%%s", s.cfg.DCAdminPassword)
}

// parseSambaOutput parses key: value lines from samba-tool output.
func parseSambaOutput(output string) []map[string]string {
	var items []map[string]string
	current := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(current) > 0 {
				items = append(items, current)
				current = map[string]string{}
			}
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			key = strings.ToLower(strings.ReplaceAll(key, " ", "_"))
			current[key] = val
		}
	}
	if len(current) > 0 {
		items = append(items, current)
	}
	return items
}
