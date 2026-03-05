package samba

import (
	"fmt"

	goldap "github.com/go-ldap/ldap/v3"
)

type GroupInfo struct {
	Name        string   `json:"name"`
	CN          string   `json:"cn"`
	Description string   `json:"description"`
	DN          string   `json:"dn"`
	MemberCount int      `json:"member_count"`
	GroupType   string   `json:"group_type"`
	Members     []string `json:"members,omitempty"`
}

func (s *Service) ListGroups() ([]GroupInfo, error) {
	entries, err := s.ldap.SearchSubtree(
		s.BaseDN(),
		"(objectClass=group)",
		[]string{"sAMAccountName", "cn", "description", "member", "groupType", "distinguishedName"},
	)
	if err != nil {
		return nil, err
	}

	var groups []GroupInfo
	for _, e := range entries {
		members := e.GetAll("member")
		count := len(members)
		if count == 1 && members[0] == "" {
			count = 0
		}
		groups = append(groups, GroupInfo{
			Name:        coalesce(e.Get("sAMAccountName"), e.Get("cn")),
			CN:          e.Get("cn"),
			Description: e.Get("description"),
			DN:          e.DN,
			MemberCount: count,
			GroupType:   e.Get("groupType"),
		})
	}
	return groups, nil
}

func (s *Service) GetGroup(name string) (*GroupInfo, error) {
	entries, err := s.ldap.SearchSubtree(
		s.BaseDN(),
		fmt.Sprintf("(&(objectClass=group)(sAMAccountName=%s))", goldap.EscapeFilter(name)),
		nil,
	)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	e := entries[0]
	members := e.GetAll("member")
	return &GroupInfo{
		Name:        e.Get("sAMAccountName"),
		CN:          e.Get("cn"),
		Description: e.Get("description"),
		DN:          e.DN,
		Members:     members,
	}, nil
}

func (s *Service) CreateGroup(name, description, groupType string) Result {
	args := []string{"group", "add", name}
	if description != "" {
		args = append(args, fmt.Sprintf("--description=%s", description))
	}
	if groupType != "" {
		args = append(args, fmt.Sprintf("--group-type=%s", groupType))
	}
	return s.runTool(args...)
}

func (s *Service) UpdateGroup(name string, attrs map[string]string) Result {
	group, err := s.GetGroup(name)
	if err != nil || group == nil {
		return Result{Success: false, Error: fmt.Sprintf("Group '%s' not found", name)}
	}

	// Rename
	if newName, ok := attrs["name"]; ok && newName != name {
		result := s.runTool("group", "rename", name,
			fmt.Sprintf("--samaccountname=%s", newName),
			"-H", "ldap://localhost")
		if !result.Success {
			return result
		}
		name = newName
	}

	// Description
	if desc, ok := attrs["description"]; ok {
		g, _ := s.GetGroup(name)
		if g != nil {
			mods := map[string]string{"description": desc}
			if err := s.ldap.Modify(g.DN, mods); err != nil {
				return Result{Success: false, Error: fmt.Sprintf("Description update failed: %v", err)}
			}
		}
	}

	return Result{Success: true, Output: fmt.Sprintf("Group '%s' updated", name)}
}

func (s *Service) DeleteGroup(name string) Result {
	return s.runTool("group", "delete", name)
}

func (s *Service) AddMember(group, member string) Result {
	return s.runTool("group", "addmembers", group, member)
}

func (s *Service) RemoveMember(group, member string) Result {
	return s.runTool("group", "removemembers", group, member)
}

func (s *Service) MoveGroup(group, newParentDN string) Result {
	return s.runTool("group", "move", group, newParentDN)
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
