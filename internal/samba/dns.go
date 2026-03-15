package samba

import (
	"fmt"
	"time"
)

func (s *Service) ListDNSZones() Result {
	return cachedResult("dns:zones", 60*time.Second, func() Result {
		return s.runTool("dns", "zonelist", s.cfg.SambaHost, "-U", s.adminAuth())
	})
}

func (s *Service) ListDNSRecords(zone string) Result {
	return cachedResult("dns:records:"+zone, 30*time.Second, func() Result {
		return s.runTool("dns", "query", s.cfg.SambaHost, zone, "@", "ALL", "-U", s.adminAuth())
	})
}

func (s *Service) AddDNSRecord(zone, name, rtype, data string) Result {
	r := s.runTool("dns", "add", s.cfg.SambaHost, zone, name, rtype, data, "-U", s.adminAuth())
	if r.Success {
		// 캐시 무효화
		globalCache.invalidate("dns:records:" + zone)
	}
	return r
}

func (s *Service) DeleteDNSRecord(zone, name, rtype, data string) Result {
	r := s.runTool("dns", "delete", s.cfg.SambaHost, zone, name, rtype, data, "-U", s.adminAuth())
	if r.Success {
		globalCache.invalidate("dns:records:" + zone)
	}
	return r
}

func (s *Service) GetDomainLevel() Result {
	return s.runTool("domain", "level", "show", "-U", s.adminAuth())
}

func (s *Service) RaiseDomainLevel(domainLevel, forestLevel string) Result {
	args := []string{"domain", "level", "raise"}
	if domainLevel != "" {
		args = append(args, "--domain-level", domainLevel)
	}
	if forestLevel != "" {
		args = append(args, "--forest-level", forestLevel)
	}
	args = append(args, "-U", s.adminAuth())
	return s.runTool(args...)
}

// ParseDomainLevel parses domain level output into structured fields.
func ParseDomainLevel(output string) map[string]string {
	result := map[string]string{"raw": output}
	for _, line := range splitLines(output) {
		switch {
		case contains(line, "Forest function level"):
			result["forest_level"] = afterColon(line)
		case contains(line, "Domain function level"):
			result["domain_level"] = afterColon(line)
		case contains(line, "Lowest function level"):
			result["lowest_dc_level"] = afterColon(line)
		}
	}
	return result
}

// PasswordPolicy
func (s *Service) GetPasswordPolicy() Result {
	return s.runTool("domain", "passwordsettings", "show", "-U", s.adminAuth())
}

func (s *Service) SetPasswordPolicy(settings map[string]string) Result {
	args := []string{"domain", "passwordsettings", "set"}
	paramMap := map[string]string{
		"complexity":        "--complexity",
		"history_length":    "--history-length",
		"min_length":        "--min-pwd-length",
		"min_age_days":      "--min-pwd-age",
		"max_age_days":      "--max-pwd-age",
		"lockout_duration":  "--account-lockout-duration",
		"lockout_threshold": "--account-lockout-threshold",
		"lockout_reset_after": "--reset-account-lockout-after",
	}
	for key, flag := range paramMap {
		if val, ok := settings[key]; ok && val != "" {
			args = append(args, fmt.Sprintf("%s=%s", flag, val))
		}
	}
	args = append(args, "-U", s.adminAuth())
	return s.runTool(args...)
}

// ParsePasswordPolicy parses password policy output.
func ParsePasswordPolicy(output string) map[string]string {
	fieldMap := map[string]string{
		"Password complexity":               "complexity",
		"Store plaintext passwords":          "store_plaintext",
		"Password history length":            "history_length",
		"Minimum password length":            "min_length",
		"Minimum password age (days)":        "min_age_days",
		"Maximum password age (days)":        "max_age_days",
		"Account lockout duration (mins)":    "lockout_duration",
		"Account lockout threshold (attempts)": "lockout_threshold",
		"Reset account lockout after (mins)": "lockout_reset_after",
	}
	policy := map[string]string{}
	for _, line := range splitLines(output) {
		for prefix, mapped := range fieldMap {
			if contains(line, prefix) {
				policy[mapped] = afterColon(line)
			}
		}
	}
	return policy
}
