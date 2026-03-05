package odoo

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// ADUser represents an Active Directory user synced from LDAP.
type ADUser struct {
	Username    string
	Email       string
	DisplayName string
	FirstName   string
	LastName    string
	Department  string
	Title       string
	Enabled     bool
}

// SyncResult summarises the outcome of a SyncUsers call.
type SyncResult struct {
	Created     int
	Updated     int
	Deactivated int
	Errors      []string
}

// SyncUsers synchronises AD users into Odoo res.users.
//
// Rules:
//   - AD user present, not in Odoo → create
//   - AD user present, already in Odoo → update (name, email, department, title)
//   - AD user disabled → deactivate in Odoo
func (c *Client) SyncUsers(users []ADUser) (*SyncResult, error) {
	result := &SyncResult{}

	// Fetch all existing Odoo users (login → id/active map)
	existing, err := c.fetchExistingUsers()
	if err != nil {
		return nil, fmt.Errorf("fetch existing users: %w", err)
	}

	for _, u := range users {
		if u.Username == "" {
			continue
		}

		odooUser, exists := existing[u.Username]

		if !u.Enabled {
			// Deactivate in Odoo if present and currently active
			if exists {
				id := int(odooUser["id"].(float64))
				active, _ := odooUser["active"].(bool)
				if active {
					if err := c.deactivateUser(id); err != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("deactivate %s: %v", u.Username, err))
					} else {
						result.Deactivated++
					}
				}
			}
			continue
		}

		// Build Odoo values
		name := u.DisplayName
		if name == "" {
			name = u.FirstName + " " + u.LastName
		}
		if name == " " || name == "" {
			name = u.Username
		}

		vals := map[string]interface{}{
			"name":  name,
			"email": u.Email,
		}
		if u.Department != "" {
			vals["department_id"] = false // will be matched separately if needed
		}
		if u.Title != "" {
			vals["function"] = u.Title
		}

		if exists {
			id := int(odooUser["id"].(float64))
			// Re-activate if currently inactive
			if active, _ := odooUser["active"].(bool); !active {
				vals["active"] = true
			}
			if err := c.updateUser(id, vals); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("update %s: %v", u.Username, err))
			} else {
				result.Updated++
			}
		} else {
			// Generate a random password — AD auth is used in practice, this is just a placeholder
			pass, err := randomPassword(24)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("gen password for %s: %v", u.Username, err))
				continue
			}

			createVals := map[string]interface{}{
				"name":     name,
				"login":    u.Username,
				"email":    u.Email,
				"active":   true,
				"password": pass,
			}
			if u.Title != "" {
				createVals["function"] = u.Title
			}

			if err := c.createUser(createVals); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("create %s: %v", u.Username, err))
			} else {
				result.Created++
			}
		}
	}

	return result, nil
}

// fetchExistingUsers returns a map of login → record for all non-system Odoo users.
func (c *Client) fetchExistingUsers() (map[string]map[string]interface{}, error) {
	// active_test=False to include deactivated users
	records, err := c.SearchRead(
		"res.users",
		[]interface{}{[]interface{}{"share", "=", false}}, // internal users only
		[]string{"id", "login", "name", "email", "active"},
		0, 0,
	)
	if err != nil {
		return nil, err
	}

	m := make(map[string]map[string]interface{}, len(records))
	for _, r := range records {
		login, _ := r["login"].(string)
		if login != "" {
			m[login] = r
		}
	}
	return m, nil
}

// createUser creates a new res.users record.
func (c *Client) createUser(vals map[string]interface{}) error {
	_, err := c.Call("res.users", "create", []interface{}{vals}, nil)
	return err
}

// updateUser writes vals into an existing res.users record.
func (c *Client) updateUser(id int, vals map[string]interface{}) error {
	_, err := c.Call("res.users", "write", []interface{}{[]int{id}, vals}, nil)
	return err
}

// deactivateUser sets active=false on a res.users record.
func (c *Client) deactivateUser(id int) error {
	return c.updateUser(id, map[string]interface{}{"active": false})
}

// randomPassword generates a cryptographically random password of n characters
// using only a-zA-Z0-9-_ (HELIOS password charset rule).
func randomPassword(n int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		buf[i] = charset[idx.Int64()]
	}
	return string(buf), nil
}
