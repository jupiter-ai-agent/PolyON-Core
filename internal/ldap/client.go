// Package ldap provides AD LDAP operations for HELIOS.
package ldap

import (
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/triangles/polyon-core/internal/config"
)

// Client provides LDAP operations against PolyON AD DC.
type Client struct {
	cfg *config.Config
}

// New creates a new LDAP client.
func New(cfg *config.Config) *Client {
	return &Client{cfg: cfg}
}

// connect creates an authenticated LDAP connection.
func (c *Client) connect() (*goldap.Conn, error) {
	conn, err := goldap.DialURL(c.cfg.LDAPURL)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	err = conn.Bind(c.cfg.AdminDN(), c.cfg.DCAdminPassword)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ldap bind: %w", err)
	}
	return conn, nil
}

// Entry represents a parsed LDAP entry.
type Entry struct {
	DN         string
	Attributes map[string][]string
}

// Get returns the first value of an attribute, or empty string.
func (e *Entry) Get(attr string) string {
	if vals, ok := e.Attributes[attr]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// GetAll returns all values of an attribute.
func (e *Entry) GetAll(attr string) []string {
	if vals, ok := e.Attributes[attr]; ok {
		return vals
	}
	return nil
}

// GetInt returns the first value as int, or 0.
func (e *Entry) GetInt(attr string) int {
	s := e.Get(attr)
	if s == "" {
		return 0
	}
	var v int
	fmt.Sscanf(s, "%d", &v)
	return v
}

// Search performs an LDAP search.
// scope: 0=Base, 1=OneLevel, 2=Subtree
func (c *Client) Search(baseDN, filter string, attrs []string, scope int) ([]Entry, error) {
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	ldapScope := goldap.ScopeWholeSubtree
	switch scope {
	case 0:
		ldapScope = goldap.ScopeBaseObject
	case 1:
		ldapScope = goldap.ScopeSingleLevel
	case 2:
		ldapScope = goldap.ScopeWholeSubtree
	}

	req := goldap.NewSearchRequest(
		baseDN, ldapScope, goldap.NeverDerefAliases, 0, 0, false,
		filter, attrs, nil,
	)

	result, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}

	var entries []Entry
	for _, e := range result.Entries {
		entry := Entry{
			DN:         e.DN,
			Attributes: make(map[string][]string),
		}
		for _, a := range e.Attributes {
			vals := make([]string, len(a.Values))
			copy(vals, a.Values)
			entry.Attributes[a.Name] = vals
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// SearchSubtree is a convenience for subtree search.
func (c *Client) SearchSubtree(baseDN, filter string, attrs []string) ([]Entry, error) {
	return c.Search(baseDN, filter, attrs, 2)
}

// SearchOneLevel is a convenience for one-level search.
func (c *Client) SearchOneLevel(baseDN, filter string, attrs []string) ([]Entry, error) {
	return c.Search(baseDN, filter, attrs, 1)
}

// Modify replaces attributes on a DN.
func (c *Client) Modify(dn string, mods map[string]string) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	req := goldap.NewModifyRequest(dn, nil)
	for attr, val := range mods {
		req.Replace(attr, []string{val})
	}
	return conn.Modify(req)
}

// ModifyBytes replaces a binary attribute.
func (c *Client) ModifyBytes(dn, attr string, data []byte) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	req := goldap.NewModifyRequest(dn, nil)
	req.Replace(attr, []string{string(data)})
	return conn.Modify(req)
}

// AddBytes adds a binary attribute.
func (c *Client) AddBytes(dn, attr string, data []byte) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	req := goldap.NewModifyRequest(dn, nil)
	req.Add(attr, []string{string(data)})
	return conn.Modify(req)
}

// DeleteAttr removes an attribute from a DN.
func (c *Client) DeleteAttr(dn, attr string) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	req := goldap.NewModifyRequest(dn, nil)
	req.Delete(attr, nil)
	return conn.Modify(req)
}

// VerifyBind attempts to bind with the given credentials. Returns nil on success.
func (c *Client) VerifyBind(dn, password string) error {
	conn, err := goldap.DialURL(c.cfg.LDAPURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Bind(dn, password)
}

// GetPhoto reads thumbnailPhoto binary from a user entry.
func (c *Client) GetPhoto(baseDN, username string) ([]byte, error) {
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	req := goldap.NewSearchRequest(
		baseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(&(objectClass=user)(sAMAccountName=%s))", goldap.EscapeFilter(username)),
		[]string{"thumbnailPhoto"}, nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		return nil, err
	}
	for _, e := range result.Entries {
		for _, a := range e.Attributes {
			if strings.EqualFold(a.Name, "thumbnailPhoto") && len(a.ByteValues) > 0 {
				return a.ByteValues[0], nil
			}
		}
	}
	return nil, nil
}
