package samba

import (
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

type UserInfo struct {
	Username    string            `json:"username"`
	CN          string            `json:"cn"`
	GivenName   string            `json:"given_name"`
	Surname     string            `json:"surname"`
	Mail        string            `json:"mail"`
	DN          string            `json:"dn"`
	Enabled     bool              `json:"enabled"`
	WhenCreated string            `json:"when_created"`
	Description string            `json:"description,omitempty"`
	MemberOf    []string          `json:"member_of"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

func (s *Service) ListUsers() ([]UserInfo, error) {
	entries, err := s.ldap.SearchSubtree(
		s.BaseDN(),
		"(&(objectClass=user)(!(objectClass=computer))(!(isCriticalSystemObject=TRUE))(!(sAMAccountName=krbtgt))(!(sAMAccountName=Guest))(!(sAMAccountName=Administrator))(!(sAMAccountName=admin)))",
		[]string{"sAMAccountName", "cn", "givenName", "sn", "mail",
			"userAccountControl", "whenCreated", "memberOf", "distinguishedName"},
	)
	if err != nil {
		return nil, err
	}

	var users []UserInfo
	for _, e := range entries {
		uac := e.GetInt("userAccountControl")
		memberOf := e.GetAll("memberOf")
		if memberOf == nil {
			memberOf = []string{}
		}
		users = append(users, UserInfo{
			Username:    e.Get("sAMAccountName"),
			CN:          e.Get("cn"),
			GivenName:   e.Get("givenName"),
			Surname:     e.Get("sn"),
			Mail:        e.Get("mail"),
			DN:          e.DN,
			Enabled:     uac&0x2 == 0,
			WhenCreated: e.Get("whenCreated"),
			MemberOf:    memberOf,
		})
	}
	return users, nil
}

func (s *Service) GetUser(username string) (*UserInfo, error) {
	entries, err := s.ldap.SearchSubtree(
		s.BaseDN(),
		fmt.Sprintf("(&(objectClass=user)(sAMAccountName=%s))", goldap.EscapeFilter(username)),
		nil,
	)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	e := entries[0]
	uac := e.GetInt("userAccountControl")
	memberOf := e.GetAll("memberOf")
	if memberOf == nil {
		memberOf = []string{}
	}
	// Build attributes map from LDAP entry
	attrs := make(map[string]string)
	skipAttrs := map[string]bool{"thumbnailPhoto": true, "unicodePwd": true, "supplementalCredentials": true, "dBCSPwd": true, "lmPwdHistory": true, "ntPwdHistory": true}
	for k, vals := range e.Attributes {
		if skipAttrs[k] || len(vals) == 0 {
			continue
		}
		if len(vals) == 1 {
			attrs[k] = vals[0]
		} else {
			attrs[k] = strings.Join(vals, "; ")
		}
	}

	return &UserInfo{
		Username:    e.Get("sAMAccountName"),
		CN:          e.Get("cn"),
		GivenName:   e.Get("givenName"),
		Surname:     e.Get("sn"),
		Mail:        e.Get("mail"),
		DN:          e.DN,
		Enabled:     uac&0x2 == 0,
		WhenCreated: e.Get("whenCreated"),
		Description: e.Get("description"),
		MemberOf:    memberOf,
		Attributes:  attrs,
	}, nil
}

func (s *Service) CreateUser(username, password string, givenName, surname, mail, ou string) Result {
	args := []string{"user", "create", username, password, "--use-username-as-cn"}
	if givenName != "" {
		args = append(args, fmt.Sprintf("--given-name=%s", givenName))
	}
	if surname != "" {
		args = append(args, fmt.Sprintf("--surname=%s", surname))
	}
	if mail != "" {
		args = append(args, fmt.Sprintf("--mail-address=%s", mail))
	}
	if ou != "" {
		args = append(args, fmt.Sprintf("--userou=%s", ou))
	}
	return s.runTool(args...)
}

func (s *Service) UpdateUser(username string, attrs map[string]string) Result {
	user, err := s.GetUser(username)
	if err != nil || user == nil {
		return Result{Success: false, Error: fmt.Sprintf("User '%s' not found", username)}
	}

	attrMap := map[string]string{
		"given_name":   "givenName",
		"surname":      "sn",
		"mail":         "mail",
		"description":  "description",
		"display_name": "displayName",
	}

	mods := map[string]string{}
	for key, val := range attrs {
		if ldapAttr, ok := attrMap[key]; ok {
			mods[ldapAttr] = val
		}
	}

	if len(mods) > 0 {
		if err := s.ldap.Modify(user.DN, mods); err != nil {
			return Result{Success: false, Error: err.Error()}
		}
	}
	return Result{Success: true, Output: fmt.Sprintf("User '%s' updated", username)}
}

func (s *Service) DeleteUser(username string) Result {
	return s.runTool("user", "delete", username)
}

func (s *Service) ResetPassword(username, newPassword string) Result {
	return s.runTool("user", "setpassword", username, fmt.Sprintf("--newpassword=%s", newPassword))
}

func (s *Service) EnableUser(username string) Result {
	return s.runTool("user", "enable", username)
}

func (s *Service) DisableUser(username string) Result {
	return s.runTool("user", "disable", username)
}

// Photo operations

func (s *Service) GetUserPhoto(username string) ([]byte, error) {
	return s.ldap.GetPhoto(s.BaseDN(), username)
}

func (s *Service) SetUserPhoto(username string, data []byte) Result {
	user, err := s.GetUser(username)
	if err != nil || user == nil {
		return Result{Success: false, Error: fmt.Sprintf("User '%s' not found", username)}
	}
	existing, _ := s.GetUserPhoto(username)
	if existing != nil {
		err = s.ldap.ModifyBytes(user.DN, "thumbnailPhoto", data)
	} else {
		err = s.ldap.AddBytes(user.DN, "thumbnailPhoto", data)
	}
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	return Result{Success: true, Output: fmt.Sprintf("Photo set for '%s' (%d bytes)", username, len(data))}
}

func (s *Service) DeleteUserPhoto(username string) Result {
	user, err := s.GetUser(username)
	if err != nil || user == nil {
		return Result{Success: false, Error: fmt.Sprintf("User '%s' not found", username)}
	}
	if err := s.ldap.DeleteAttr(user.DN, "thumbnailPhoto"); err != nil {
		// Ignore "no such attribute"
		return Result{Success: true, Output: "No photo to remove"}
	}
	return Result{Success: true, Output: fmt.Sprintf("Photo removed for '%s'", username)}
}

// LDAPModify exposes the internal LDAP Modify to API layer.
func (s *Service) LDAPModify(dn string, mods map[string]string) error {
	return s.ldap.Modify(dn, mods)
}
