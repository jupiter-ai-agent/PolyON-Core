package samba

import "fmt"

type OUInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DN          string `json:"dn"`
}

type OUContents struct {
	Success bool             `json:"success"`
	Users   []OUContentUser  `json:"users"`
	Groups  []OUContentGroup `json:"groups"`
	SubOUs  []OUContentOU    `json:"sub_ous"`
}

type OUContentUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Mail        string `json:"mail"`
	UPN         string `json:"upn"`
	Enabled     bool   `json:"enabled"`
}

type OUContentGroup struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type OUContentOU struct {
	Name        string `json:"name"`
	DN          string `json:"dn"`
	Description string `json:"description"`
}

func (s *Service) ListOUs() ([]OUInfo, error) {
	entries, err := s.ldap.SearchSubtree(
		s.BaseDN(),
		"(objectClass=organizationalUnit)",
		[]string{"ou", "description", "distinguishedName"},
	)
	if err != nil {
		return nil, err
	}
	var ous []OUInfo
	for _, e := range entries {
		ous = append(ous, OUInfo{
			Name:        e.Get("ou"),
			Description: e.Get("description"),
			DN:          e.DN,
		})
	}
	return ous, nil
}

func (s *Service) ListOUContents(ouDN string) OUContents {
	result := OUContents{Success: true}

	// Users
	users, _ := s.ldap.SearchOneLevel(ouDN,
		"(&(objectClass=user)(!(objectClass=computer)))",
		[]string{"sAMAccountName", "displayName", "mail", "userAccountControl", "userPrincipalName"})
	for _, u := range users {
		uac := u.GetInt("userAccountControl")
		result.Users = append(result.Users, OUContentUser{
			Username:    u.Get("sAMAccountName"),
			DisplayName: u.Get("displayName"),
			Mail:        u.Get("mail"),
			UPN:         u.Get("userPrincipalName"),
			Enabled:     uac&2 == 0,
		})
	}

	// Groups
	groups, _ := s.ldap.SearchOneLevel(ouDN, "(objectClass=group)",
		[]string{"sAMAccountName", "description", "groupType"})
	for _, g := range groups {
		result.Groups = append(result.Groups, OUContentGroup{
			Name:        g.Get("sAMAccountName"),
			Description: g.Get("description"),
		})
	}

	// Sub-OUs
	subOUs, _ := s.ldap.SearchOneLevel(ouDN, "(objectClass=organizationalUnit)",
		[]string{"ou", "description", "distinguishedName"})
	for _, o := range subOUs {
		result.SubOUs = append(result.SubOUs, OUContentOU{
			Name:        o.Get("ou"),
			DN:          o.DN,
			Description: o.Get("description"),
		})
	}

	return result
}

func (s *Service) CreateOU(name, parentDN, description string) Result {
	ouDN := fmt.Sprintf("OU=%s", name)
	if parentDN != "" {
		ouDN = fmt.Sprintf("OU=%s,%s", name, parentDN)
	}
	args := []string{"ou", "create", ouDN}
	if description != "" {
		args = append(args, fmt.Sprintf("--description=%s", description))
	}
	return s.runTool(args...)
}

func (s *Service) DeleteOU(dn string) Result {
	return s.runTool("ou", "delete", dn)
}

func (s *Service) MoveOU(ouDN, newParentDN string) Result {
	return s.runTool("ou", "move", ouDN, newParentDN)
}

func (s *Service) MoveUser(username, newParentDN string) Result {
	return s.runTool("user", "move", username, newParentDN)
}
