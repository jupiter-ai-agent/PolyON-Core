package samba

import "fmt"

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

func (s *Service) ReplicationStatus() Result {
	return s.runTool("drs", "showrepl")
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
