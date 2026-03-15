package samba

import (
	"fmt"
	"strings"
	"time"
)

func (s *Service) DomainInfo() map[string]interface{} {
	result := s.runTool("domain", "level", "show")
	return map[string]interface{}{
		"success":    true,
		"realm":      s.Realm(),
		"base_dn":    s.BaseDN(),
		"level_info": result.Output,
	}
}

func (s *Service) DomainDCs() ([]map[string]string, error) {
	entries, err := s.ldap.SearchSubtree(
		fmt.Sprintf("OU=Domain Controllers,%s", s.BaseDN()),
		"(objectClass=computer)",
		[]string{"cn", "dNSHostName", "operatingSystem", "whenCreated"},
	)
	if err != nil {
		return nil, err
	}
	var dcs []map[string]string
	for _, e := range entries {
		dcs = append(dcs, map[string]string{
			"name":         e.Get("cn"),
			"dns_hostname": e.Get("dNSHostName"),
			"os":           e.Get("operatingSystem"),
			"when_created": e.Get("whenCreated"),
		})
	}
	return dcs, nil
}

func (s *Service) FSMOShow() Result {
	return s.runTool("fsmo", "show")
}

// FSMOParsed returns parsed FSMO roles as a structured map.
func (s *Service) FSMOParsed() map[string]interface{} {
	result := s.runTool("fsmo", "show")
	if !result.Success {
		return map[string]interface{}{"success": false, "error": result.Error}
	}
	roles := map[string]string{
		"schema":         "",
		"naming":         "",
		"pdc":            "",
		"rid":            "",
		"infrastructure": "",
	}
	keywords := map[string]string{
		"SchemaMasterRole":         "schema",
		"DomainNamingMasterRole":   "naming",
		"PdcEmulationMasterRole":   "pdc",
		"RidAllocationMasterRole":  "rid",
		"InfrastructureMasterRole": "infrastructure",
	}
	for _, line := range strings.Split(result.Output, "\n") {
		for kw, key := range keywords {
			if strings.Contains(line, kw) {
				// Extract short DC name from CN=DC1,CN=Servers,...
				owner := strings.TrimSpace(line)
				if idx := strings.Index(owner, "CN="); idx >= 0 {
					// Find second CN= for the server name
					parts := strings.Split(owner, ",")
					for _, p := range parts {
						p = strings.TrimSpace(p)
						if strings.HasPrefix(p, "CN=") && !strings.HasPrefix(p, "CN=NTDS") {
							roles[key] = strings.TrimPrefix(p, "CN=")
							break
						}
					}
				}
				if roles[key] == "" {
					roles[key] = owner
				}
			}
		}
	}
	return map[string]interface{}{
		"success":        true,
		"schema":         roles["schema"],
		"naming":         roles["naming"],
		"pdc":            roles["pdc"],
		"rid":            roles["rid"],
		"infrastructure": roles["infrastructure"],
		"raw":            result.Output,
	}
}

func (s *Service) ReplicationStatus() Result {
	// drs showrepl은 단일 DC 환경에서 복제 파트너 탐색 타임아웃(60s)이 길어
	// 별도 goroutine + 5초 타임아웃으로 빠르게 처리
	type res struct{ r Result }
	ch := make(chan res, 1)
	go func() {
		ch <- res{s.runTool("drs", "showrepl")}
	}()
	select {
	case r := <-ch:
		return r.r
	case <-time.After(5 * time.Second):
		return Result{Success: false, Error: "단일 DC 환경 — 복제 파트너 없음 (타임아웃)"}
	}
}

func (s *Service) ListComputers() ([]map[string]string, error) {
	entries, err := s.ldap.SearchSubtree(
		s.BaseDN(),
		"(objectClass=computer)",
		[]string{"cn", "dNSHostName", "operatingSystem", "operatingSystemVersion",
			"whenCreated", "lastLogonTimestamp", "distinguishedName"},
	)
	if err != nil {
		return nil, err
	}
	var computers []map[string]string
	for _, e := range entries {
		computers = append(computers, map[string]string{
			"name":         e.Get("cn"),
			"dns_hostname": e.Get("dNSHostName"),
			"os":           e.Get("operatingSystem"),
			"os_version":   e.Get("operatingSystemVersion"),
			"when_created": e.Get("whenCreated"),
			"dn":           e.DN,
		})
	}
	return computers, nil
}
