package provision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MailSyncer synchronizes AD ML-* groups to Stalwart mailing lists.
type MailSyncer struct {
	StalwartURL   string // e.g. "http://polyon-mail:8080"
	AdminUser     string
	AdminPassword string
	MailDomain    string // e.g. "cmars.com"
	httpClient    *http.Client
}

// NewMailSyncer creates a MailSyncer with the given Stalwart connection details.
func NewMailSyncer(stalwartURL, adminUser, adminPassword, mailDomain string) *MailSyncer {
	return &MailSyncer{
		StalwartURL:   strings.TrimRight(stalwartURL, "/"),
		AdminUser:     adminUser,
		AdminPassword: adminPassword,
		MailDomain:    mailDomain,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
	}
}

// stalwartPrincipal represents a Stalwart principal payload.
type stalwartPrincipal struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Emails      []string `json:"emails,omitempty"`
	Members     []string `json:"members,omitempty"`
}

// SyncMailingLists ensures every ML-* AD group has a corresponding Stalwart
// mailing list principal. SG-ORG-* groups also get a mailing list.
// Incremental: existing lists are skipped (not overwritten).
func (ms *MailSyncer) SyncMailingLists(groups []ADGroup) (*SyncComponentResult, error) {
	result := &SyncComponentResult{}

	// Fetch existing mailing lists from Stalwart
	existing, err := ms.listMailingLists()
	if err != nil {
		return nil, fmt.Errorf("list stalwart mailing lists: %w", err)
	}

	existingSet := map[string]bool{}
	for _, name := range existing {
		existingSet[strings.ToLower(name)] = true
	}

	for _, g := range groups {
		// Only process ML-* and SG-ORG-* groups
		if g.Type != "ML" && g.Type != "SG-ORG" {
			continue
		}

		mlName := mailingListName(g.Name)
		mlEmail := mlName + "@" + ms.MailDomain

		if existingSet[strings.ToLower(mlName)] {
			result.Skipped++
			continue
		}

		// Extract usernames from member DNs (CN=username,...)
		members := extractUsernames(g.Members)

		principal := stalwartPrincipal{
			Type:        "list",
			Name:        mlName,
			Description: g.DisplayName + " 메일링 리스트",
			Emails:      []string{mlEmail},
			Members:     members,
		}

		if err := ms.createMailingList(principal); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("create list %s: %v", mlName, err))
			continue
		}

		result.Created++
	}

	return result, nil
}

// UpdateMailingListMembers updates the member list of an existing mailing list.
func (ms *MailSyncer) UpdateMailingListMembers(mlName string, members []string) error {
	payload := map[string]interface{}{
		"members": members,
	}
	return ms.stalwartPut("/api/principal/"+mlName, payload)
}

// DeleteMailingList removes a mailing list principal from Stalwart.
func (ms *MailSyncer) DeleteMailingList(mlName string) error {
	return ms.stalwartDelete("/api/principal/" + mlName)
}

// ──────────────────────────────────────────────────────────────────────────────
// Stalwart HTTP helpers
// ──────────────────────────────────────────────────────────────────────────────

// listMailingLists returns the names of all Stalwart mailing list principals.
// Stalwart v0.11+ uses GET /api/principal (no /v1/ in path).
func (ms *MailSyncer) listMailingLists() ([]string, error) {
	req, _ := http.NewRequest("GET", ms.StalwartURL+"/api/principal?type=list", nil)
	req.SetBasicAuth(ms.AdminUser, ms.AdminPassword)
	req.Header.Set("Accept", "application/json")

	resp, err := ms.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 {
		return nil, nil // no lists yet
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list principals HTTP %d: %s", resp.StatusCode, string(raw))
	}

	// Stalwart v0.11+ response: {"data": {"items": [...], "total": N}}
	// Items contain principal objects; extract names of type "list".
	var wrapper struct {
		Data struct {
			Items []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Data.Items) > 0 {
		var names []string
		for _, item := range wrapper.Data.Items {
			if item.Type == "list" {
				names = append(names, item.Name)
			}
		}
		return names, nil
	}

	// Fallback: response may be {"data": [...]} with string names
	var wrapperFlat struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapperFlat); err == nil && len(wrapperFlat.Data) > 0 {
		return wrapperFlat.Data, nil
	}

	// Last fallback: plain string array
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("decode principal list: %w", err)
	}
	return names, nil
}

// createMailingList creates a new Stalwart mailing list principal.
// Stalwart v0.11+ uses POST /api/principal (no /v1/ in path).
func (ms *MailSyncer) createMailingList(p stalwartPrincipal) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest("POST", ms.StalwartURL+"/api/principal", bytes.NewReader(body))
	req.SetBasicAuth(ms.AdminUser, ms.AdminPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ms.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create principal HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// stalwartPut sends a PUT to update an existing principal.
func (ms *MailSyncer) stalwartPut(path string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest("PUT", ms.StalwartURL+path, bytes.NewReader(body))
	req.SetBasicAuth(ms.AdminUser, ms.AdminPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ms.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PUT %s HTTP %d: %s", path, resp.StatusCode, string(raw))
	}
	return nil
}

// stalwartDelete sends a DELETE to remove a principal.
func (ms *MailSyncer) stalwartDelete(path string) error {
	req, _ := http.NewRequest("DELETE", ms.StalwartURL+path, nil)
	req.SetBasicAuth(ms.AdminUser, ms.AdminPassword)

	resp, err := ms.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DELETE %s HTTP %d: %s", path, resp.StatusCode, string(raw))
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Naming helpers
// ──────────────────────────────────────────────────────────────────────────────

// mailingListName converts an AD group name to a lowercase Stalwart principal name.
// "ML-Sales" → "ml-sales", "SG-ORG-영업팀" → "sg-org-영업팀"
func mailingListName(groupName string) string {
	return strings.ToLower(groupName)
}

// extractUsernames parses sAMAccountName from a list of AD member DNs.
// DN format: "CN=jdoe,OU=Users,DC=cmars,DC=com" → "jdoe"
func extractUsernames(memberDNs []string) []string {
	var names []string
	for _, dn := range memberDNs {
		if dn == "" {
			continue
		}
		parts := strings.Split(dn, ",")
		if len(parts) == 0 {
			continue
		}
		// First component should be CN=<username>
		cn := parts[0]
		if idx := strings.Index(cn, "="); idx >= 0 {
			username := cn[idx+1:]
			if username != "" {
				names = append(names, username)
			}
		}
	}
	return names
}
