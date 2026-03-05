package samba

import "fmt"

func (s *Service) ListGPOs() ([]map[string]string, Result) {
	result := s.runTool("gpo", "listall", "-H", "ldap://localhost", "-U", s.adminAuth())
	if !result.Success {
		return nil, result
	}
	return parseSambaOutput(result.Output), result
}

func (s *Service) GetGPO(guid string) Result {
	return s.runTool("gpo", "show", guid, "-H", "ldap://localhost", "-U", s.adminAuth())
}

func (s *Service) CreateGPO(displayName string) Result {
	return s.runTool("gpo", "create", displayName, "-H", "ldap://localhost", "-U", s.adminAuth())
}

func (s *Service) DeleteGPO(guid string) Result {
	return s.runTool("gpo", "del", guid, "-H", "ldap://localhost", "-U", s.adminAuth())
}

func (s *Service) LinkGPO(containerDN, guid string, enforce, disable bool) Result {
	args := []string{"gpo", "setlink", containerDN, guid, "-H", "ldap://localhost", "-U", s.adminAuth()}
	if enforce {
		args = append(args, "--enforce")
	}
	if disable {
		args = append(args, "--disable")
	}
	return s.runTool(args...)
}

func (s *Service) UnlinkGPO(containerDN, guid string) Result {
	return s.runTool("gpo", "dellink", containerDN, guid, "-H", "ldap://localhost", "-U", s.adminAuth())
}

func (s *Service) GetGPOLinks(containerDN string) ([]map[string]string, Result) {
	result := s.runTool("gpo", "getlink", containerDN, "-H", "ldap://localhost", "-U", s.adminAuth())
	if !result.Success {
		return nil, result
	}
	return parseSambaOutput(result.Output), result
}

func (s *Service) ListGPOContainers(guid string) ([]string, Result) {
	result := s.runTool("gpo", "listcontainers", guid, "-H", "ldap://localhost", "-U", s.adminAuth())
	if !result.Success {
		return nil, result
	}
	var containers []string
	for _, line := range splitLines(result.Output) {
		if line != "" && !contains(line, "Container") {
			containers = append(containers, line)
		}
	}
	return containers, result
}

// ACL
func (s *Service) GetACL(dn string) Result {
	return s.runTool("dsacl", "get", fmt.Sprintf("--objectdn=%s", dn), "-U", s.adminAuth())
}

func (s *Service) ListOUACLs() []map[string]interface{} {
	ous, err := s.ListOUs()
	if err != nil {
		return nil
	}
	var results []map[string]interface{}
	for _, ou := range ous {
		acl := s.GetACL(ou.DN)
		results = append(results, map[string]interface{}{
			"name":    ou.Name,
			"dn":      ou.DN,
			"acl":     coalesce(acl.Output, acl.Error),
			"success": acl.Success,
		})
	}
	return results
}
