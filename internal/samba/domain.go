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

// FSMOParsed returns FSMO roles via direct LDAP attribute lookup (fSMORoleOwner).
// LDAP 직접 조회 → samba-tool 프로세스 없이 수 ms 수준.
func (s *Service) FSMOParsed() map[string]interface{} {
	return cachedMap("fsmo", 60*time.Second, func() map[string]interface{} {
		baseDN := s.BaseDN()
		confDN := "CN=Configuration," + baseDN

		// (DN, role key) 매핑
		targets := []struct {
			dn  string
			key string
		}{
			{baseDN, "pdc"},
			{"CN=Infrastructure," + baseDN, "infrastructure"},
			{"CN=RID Manager$,CN=System," + baseDN, "rid"},
			{"CN=Partitions," + confDN, "naming"},
			{"CN=Schema," + confDN, "schema"},
		}

		roles := map[string]string{}
		for _, t := range targets {
			entries, err := s.ldap.Search(t.dn, "(objectClass=*)", []string{"fSMORoleOwner"}, 0 /* base */)
			if err != nil || len(entries) == 0 {
				roles[t.key] = "—"
				continue
			}
			owner := entries[0].Get("fSMORoleOwner")
			// CN=NTDS Settings,CN=DC1,... → DC1
			dcName := extractDCName(owner)
			roles[t.key] = dcName
		}

		return map[string]interface{}{
			"success":        true,
			"schema":         roles["schema"],
			"naming":         roles["naming"],
			"pdc":            roles["pdc"],
			"rid":            roles["rid"],
			"infrastructure": roles["infrastructure"],
		}
	})
}

// extractDCName extracts the server name from an NTDS Settings DN.
// e.g. "CN=NTDS Settings,CN=DC1,CN=Servers,..." → "DC1"
func extractDCName(dn string) string {
	parts := strings.Split(dn, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "CN=") && !strings.HasPrefix(p, "CN=NTDS") {
			return strings.TrimPrefix(p, "CN=")
		}
	}
	return dn
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
