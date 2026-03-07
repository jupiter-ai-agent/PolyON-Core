package nextcloud

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Client communicates with Nextcloud OCS API and OCC CLI.
type Client struct {
	baseURL    string // e.g. "http://polyon-drive"
	adminUser  string
	adminPass  string
	httpClient *http.Client
	container  string // docker container name for OCC commands
}

func NewClient(baseURL, adminUser, adminPass, container string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		adminUser: adminUser,
		adminPass: adminPass,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		container: container,
	}
}

// dockerExec runs a command inside the Nextcloud container via docker exec.
func (c *Client) dockerExec(cmd []string) (string, error) {
	args := append([]string{"exec", "-u", "www-data", c.container}, cmd...)
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker exec %s: %w (output: %s)", c.container, err, string(out))
	}
	return string(out), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Health
// ──────────────────────────────────────────────────────────────────────────────

type StatusResponse struct {
	Installed     bool   `json:"installed"`
	Maintenance   bool   `json:"maintenance"`
	Version       string `json:"version"`
	VersionString string `json:"versionstring"`
	Edition       string `json:"edition"`
	ProductName   string `json:"productname"`
}

func (c *Client) Status() (*StatusResponse, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/status.php")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var s StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Group Folders (Team Folders) — OCS API
// ──────────────────────────────────────────────────────────────────────────────

type GroupFolder struct {
	ID     int               `json:"id"`
	Name   string            `json:"mount_point"`
	Groups map[string]int    `json:"groups"` // group → permissions bitmask
	Quota  int64             `json:"quota"`  // -3 = unlimited
	Size   int64             `json:"size"`
}

type ocsMeta struct {
	Status     string `json:"status"`
	StatusCode int    `json:"statuscode"`
	Message    string `json:"message"`
}

type ocsResponse struct {
	OCS struct {
		Meta ocsMeta         `json:"meta"`
		Data json.RawMessage `json:"data"`
	} `json:"ocs"`
}

// ListGroupFolders returns all configured team folders.
func (c *Client) ListGroupFolders() ([]GroupFolder, error) {
	body, err := c.groupFoldersGet("/folders")
	if err != nil {
		return nil, err
	}

	var ocs ocsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		return nil, err
	}

	// Data can be a map (id → folder) or empty array when no folders exist.
	// Try map first; if it fails (e.g. "[]"), return empty slice.
	var folders map[string]GroupFolder
	if err := json.Unmarshal(ocs.OCS.Data, &folders); err != nil {
		return nil, nil // empty
	}

	result := make([]GroupFolder, 0, len(folders))
	for _, f := range folders {
		result = append(result, f)
	}
	return result, nil
}

// CreateGroupFolder creates a team folder via OCC CLI and returns its ID.
// Nextcloud 33 groupfolders REST API has CSRF issues; OCC is reliable.
func (c *Client) CreateGroupFolder(name string) (int, error) {
	if c.container == "" {
		return 0, fmt.Errorf("container name not set for OCC exec")
	}

	out, err := c.dockerExec([]string{"php", "occ", "groupfolders:create", name})
	if err != nil {
		return 0, fmt.Errorf("occ groupfolders:create: %w", err)
	}

	// OCC returns the folder ID as a plain integer string
	trimmed := strings.TrimSpace(out)
	id, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse folder ID from OCC output %q: %w", trimmed, err)
	}
	return id, nil
}

// findFolderByName fetches the folder list and returns the ID of the folder
// matching the given name that is NOT in excludeIDs.
func (c *Client) findFolderByName(name string, excludeIDs map[int]bool) (int, error) {
	folders, err := c.ListGroupFolders()
	if err != nil {
		return 0, err
	}
	for _, f := range folders {
		if f.Name == name && !excludeIDs[f.ID] {
			return f.ID, nil
		}
	}
	return 0, fmt.Errorf("created folder %q not found in list", name)
}

// AddGroupToFolder grants a group access to a team folder.
// Permissions: 1=read, 2=update, 4=create, 8=delete, 16=share. 31=all.
func (c *Client) AddGroupToFolder(folderID int, groupID string, permissions int) error {
	path := fmt.Sprintf("/folders/%d/groups", folderID)
	data := url.Values{"group": {groupID}}
	if _, err := c.groupFoldersPost(path, data); err != nil {
		return err
	}

	// Set permissions
	if permissions > 0 {
		permPath := fmt.Sprintf("/folders/%d/groups/%s", folderID, url.PathEscape(groupID))
		permData := url.Values{"permissions": {fmt.Sprintf("%d", permissions)}}
		if _, err := c.groupFoldersPost(permPath, permData); err != nil {
			return fmt.Errorf("set permissions: %w", err)
		}
	}
	return nil
}

// SetFolderQuota sets the quota for a team folder (-3 = unlimited).
func (c *Client) SetFolderQuota(folderID int, quotaBytes int64) error {
	path := fmt.Sprintf("/folders/%d/quota", folderID)
	data := url.Values{"quota": {fmt.Sprintf("%d", quotaBytes)}}
	_, err := c.groupFoldersPost(path, data)
	return err
}

// RemoveGroupFromFolder removes a group's access to a team folder.
func (c *Client) RemoveGroupFromFolder(folderID int, groupID string) error {
	path := fmt.Sprintf("/folders/%d/groups/%s", folderID, url.PathEscape(groupID))
	_, err := c.groupFoldersDelete(path)
	return err
}

// DeleteGroupFolder deletes a team folder.
func (c *Client) DeleteGroupFolder(folderID int) error {
	path := fmt.Sprintf("/folders/%d", folderID)
	_, err := c.groupFoldersDelete(path)
	return err
}

// ──────────────────────────────────────────────────────────────────────────────
// User provisioning
// ──────────────────────────────────────────────────────────────────────────────

// EnsureUserProvisioned triggers a user lookup which causes LDAP-backed user
// to be provisioned (home directory created).
func (c *Client) EnsureUserProvisioned(userID string) error {
	path := fmt.Sprintf("/cloud/users/%s", url.PathEscape(userID))
	_, err := c.ocsGet(path)
	return err
}

// ──────────────────────────────────────────────────────────────────────────────
// OCS HTTP helpers
// ──────────────────────────────────────────────────────────────────────────────

func (c *Client) ocsGet(path string) ([]byte, error) {
	reqURL := c.baseURL + "/ocs/v2.php" + path + "?format=json"
	req, _ := http.NewRequest("GET", reqURL, nil)
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Client) ocsPost(path string, data url.Values) ([]byte, error) {
	reqURL := c.baseURL + "/ocs/v2.php" + path + "?format=json"
	req, _ := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Client) ocsDelete(path string) ([]byte, error) {
	reqURL := c.baseURL + "/ocs/v2.php" + path + "?format=json"
	req, _ := http.NewRequest("DELETE", reqURL, nil)
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ──────────────────────────────────────────────────────────────────────────────
// GroupFolders app HTTP helpers (Nextcloud 21+)
// Nextcloud 21+ groupfolders app uses /apps/groupfolders/ (not OCS path).
// Requires Accept: application/json to get JSON instead of XML.
// ──────────────────────────────────────────────────────────────────────────────

func (c *Client) groupFoldersGet(path string) ([]byte, error) {
	reqURL := c.baseURL + "/apps/groupfolders" + path
	req, _ := http.NewRequest("GET", reqURL, nil)
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Client) groupFoldersPost(path string, data url.Values) ([]byte, error) {
	reqURL := c.baseURL + "/apps/groupfolders" + path
	req, _ := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Client) groupFoldersDelete(path string) ([]byte, error) {
	reqURL := c.baseURL + "/apps/groupfolders" + path
	req, _ := http.NewRequest("DELETE", reqURL, nil)
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Client) ocsPut(path string, data url.Values) ([]byte, error) {
	reqURL := c.baseURL + "/ocs/v2.php" + path + "?format=json"
	req, _ := http.NewRequest("PUT", reqURL, strings.NewReader(data.Encode()))
	req.SetBasicAuth(c.adminUser, c.adminPass)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ──────────────────────────────────────────────────────────────────────────────
// OCS User Provisioning API
// ──────────────────────────────────────────────────────────────────────────────

// OCSUser represents a Nextcloud user from OCS API.
type OCSUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayname"`
	Email       string `json:"email"`
	Enabled     bool   `json:"enabled"`
	Backend     string `json:"backend"` // "Database" or "LDAP"
}

// ListOCSUsers returns all user IDs from Nextcloud.
// GET /ocs/v2.php/cloud/users
func (c *Client) ListOCSUsers() ([]string, error) {
	body, err := c.ocsGet("/cloud/users")
	if err != nil {
		return nil, err
	}

	var ocs ocsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		return nil, err
	}

	if ocs.OCS.Meta.StatusCode >= 300 {
		return nil, fmt.Errorf("OCS error %d: %s", ocs.OCS.Meta.StatusCode, ocs.OCS.Meta.Message)
	}

	var data struct {
		Users []string `json:"users"`
	}
	if err := json.Unmarshal(ocs.OCS.Data, &data); err != nil {
		return nil, err
	}

	return data.Users, nil
}

// GetOCSUser returns user details.
// GET /ocs/v2.php/cloud/users/{userid}
func (c *Client) GetOCSUser(userID string) (*OCSUser, error) {
	path := fmt.Sprintf("/cloud/users/%s", url.PathEscape(userID))
	body, err := c.ocsGet(path)
	if err != nil {
		return nil, err
	}

	var ocs ocsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		return nil, err
	}

	if ocs.OCS.Meta.StatusCode >= 300 {
		return nil, fmt.Errorf("OCS error %d: %s", ocs.OCS.Meta.StatusCode, ocs.OCS.Meta.Message)
	}

	var user OCSUser
	if err := json.Unmarshal(ocs.OCS.Data, &user); err != nil {
		return nil, err
	}

	return &user, nil
}

// CreateOCSUser creates a local user via OCS API.
// POST /ocs/v2.php/cloud/users (form: userid, password, displayName, email)
func (c *Client) CreateOCSUser(userID, password, displayName, email string) error {
	data := url.Values{
		"userid":      {userID},
		"password":    {password},
		"displayName": {displayName},
		"email":       {email},
	}

	body, err := c.ocsPost("/cloud/users", data)
	if err != nil {
		return err
	}

	var ocs ocsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		return err
	}

	if ocs.OCS.Meta.StatusCode == 102 {
		return fmt.Errorf("user already exists")
	}
	if ocs.OCS.Meta.StatusCode >= 300 {
		return fmt.Errorf("OCS error %d: %s", ocs.OCS.Meta.StatusCode, ocs.OCS.Meta.Message)
	}

	return nil
}

// UpdateOCSUser updates a single user field.
// PUT /ocs/v2.php/cloud/users/{userid} (form: key, value)
func (c *Client) UpdateOCSUser(userID, key, value string) error {
	path := fmt.Sprintf("/cloud/users/%s", url.PathEscape(userID))
	data := url.Values{
		"key":   {key},
		"value": {value},
	}

	body, err := c.ocsPut(path, data)
	if err != nil {
		return err
	}

	var ocs ocsResponse
	if err := json.Unmarshal(body, &ocs); err != nil {
		return err
	}

	if ocs.OCS.Meta.StatusCode >= 300 {
		return fmt.Errorf("OCS error %d: %s", ocs.OCS.Meta.StatusCode, ocs.OCS.Meta.Message)
	}

	return nil
}
