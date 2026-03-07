// sync_api.go extends the Mattermost client with methods used by the service
// sync dispatcher. Methods already present in provision.go are not duplicated.
package mattermost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// deleteReq sends an authenticated DELETE request and returns the response.
func (c *Client) deleteReq(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http delete %s: %w", path, err)
	}
	return resp, nil
}

// postJSON sends an authenticated POST request with a JSON body and returns the response.
func (c *Client) postJSON(path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post %s: %w", path, err)
	}
	return resp, nil
}

// checkResp reads response body and returns an error if status is not 2xx.
func checkResp(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

// GetUserByEmail returns a Mattermost User by email address.
// GET /api/v4/users/email/{email}
func (c *Client) GetUserByEmail(email string) (*User, error) {
	resp, err := c.get("/api/v4/users/email/" + email)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}
	return &u, nil
}

// GetUserByUsernameTyped returns a typed User struct by username.
// (provision.go has GetUserByUsername returning map[string]interface{})
// GET /api/v4/users/username/{username}
func (c *Client) GetUserByUsernameTyped(username string) (*User, error) {
	resp, err := c.get("/api/v4/users/username/" + username)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}
	return &u, nil
}

// GetTeamByNameTyped returns a typed Team struct by name.
// (provision.go has GetTeamByName returning map[string]interface{})
// GET /api/v4/teams/name/{name}
func (c *Client) GetTeamByNameTyped(name string) (*Team, error) {
	resp, err := c.get("/api/v4/teams/name/" + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return nil, fmt.Errorf("get team by name: %w", err)
	}
	var t Team
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("decode team: %w", err)
	}
	return &t, nil
}

// AddTeamMember adds a user to a team.
// POST /api/v4/teams/{id}/members
func (c *Client) AddTeamMember(teamID, userID string) error {
	body := map[string]string{
		"team_id": teamID,
		"user_id": userID,
	}
	resp, err := c.postJSON("/api/v4/teams/"+teamID+"/members", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return fmt.Errorf("add team member: %w", err)
	}
	return nil
}

// CreateChannelFull creates a channel with a purpose field.
// (provision.go CreateChannel omits purpose; this variant adds it)
// POST /api/v4/channels
func (c *Client) CreateChannelFull(teamID, name, displayName, purpose, channelType string) (*Channel, error) {
	body := map[string]string{
		"team_id":      teamID,
		"name":         name,
		"display_name": displayName,
		"purpose":      purpose,
		"type":         channelType,
	}
	resp, err := c.postJSON("/api/v4/channels", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return nil, fmt.Errorf("create channel: %w", err)
	}
	var ch Channel
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return nil, fmt.Errorf("decode channel: %w", err)
	}
	return &ch, nil
}

// GetChannelByName returns a channel by name within a team.
// GET /api/v4/teams/{id}/channels/name/{name}
func (c *Client) GetChannelByName(teamID, name string) (*Channel, error) {
	resp, err := c.get("/api/v4/teams/" + teamID + "/channels/name/" + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return nil, fmt.Errorf("get channel by name: %w", err)
	}
	var ch Channel
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return nil, fmt.Errorf("decode channel: %w", err)
	}
	return &ch, nil
}

// AddChannelMember adds a user to a channel.
// POST /api/v4/channels/{id}/members
func (c *Client) AddChannelMember(channelID, userID string) error {
	body := map[string]string{
		"user_id": userID,
	}
	resp, err := c.postJSON("/api/v4/channels/"+channelID+"/members", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return fmt.Errorf("add channel member: %w", err)
	}
	return nil
}

// RemoveChannelMember removes a user from a channel.
// DELETE /api/v4/channels/{id}/members/{user_id}
func (c *Client) RemoveChannelMember(channelID, userID string) error {
	resp, err := c.deleteReq("/api/v4/channels/" + channelID + "/members/" + userID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return fmt.Errorf("remove channel member: %w", err)
	}
	return nil
}

// DeactivateUser deactivates (soft-deletes) a user.
// DELETE /api/v4/users/{id}
func (c *Client) DeactivateUser(userID string) error {
	resp, err := c.deleteReq("/api/v4/users/" + userID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}
	return nil
}

// SendPost sends a message to a channel.
// POST /api/v4/posts
func (c *Client) SendPost(channelID, message string) error {
	body := map[string]string{
		"channel_id": channelID,
		"message":    message,
	}
	resp, err := c.postJSON("/api/v4/posts", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return fmt.Errorf("send post: %w", err)
	}
	return nil
}

// CreateUser creates a Mattermost user via POST /api/v4/users.
// For LDAP auth users, password must NOT be set (auth_data + auth_service only).
func (c *Client) CreateUser(username, email, firstName, lastName, nickname, position, password string) (*User, error) {
	body := map[string]interface{}{
		"username":     username,
		"email":        email,
		"first_name":   firstName,
		"last_name":    lastName,
		"nickname":     nickname,
		"position":     position,
		"auth_service": "ldap",
		"auth_data":    username,
	}
	resp, err := c.postJSON("/api/v4/users", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("decode created user: %w", err)
	}
	return &u, nil
}

// putJSON sends an authenticated PUT request with a JSON body and returns the response.
func (c *Client) putJSON(path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http put %s: %w", path, err)
	}
	return resp, nil
}

// UpdateUser updates user fields via PUT /api/v4/users/{id}/patch
func (c *Client) UpdateUser(userID string, patch map[string]interface{}) error {
	resp, err := c.putJSON("/api/v4/users/"+userID+"/patch", patch)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResp(resp); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

// ListAllUsers returns all users with pagination
func (c *Client) ListAllUsers() ([]User, error) {
	var allUsers []User
	page := 0
	perPage := 200

	for {
		path := fmt.Sprintf("/api/v4/users?page=%d&per_page=%d", page, perPage)
		resp, err := c.get(path)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if err := checkResp(resp); err != nil {
			return nil, fmt.Errorf("list users page %d: %w", page, err)
		}

		var users []User
		if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
			return nil, fmt.Errorf("decode users page %d: %w", page, err)
		}

		allUsers = append(allUsers, users...)

		// If we got fewer than perPage users, we're done
		if len(users) < perPage {
			break
		}
		page++
	}

	return allUsers, nil
}
