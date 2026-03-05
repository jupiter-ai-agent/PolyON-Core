// Package odoo provides a JSON-RPC client for Odoo 18.0.
package odoo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is an Odoo JSON-RPC client.
type Client struct {
	baseURL    string
	db         string
	username   string
	password   string
	uid        int
	httpClient *http.Client
}

// NewClient creates a new Odoo JSON-RPC client.
func NewClient(url, db, username, password string) *Client {
	return &Client{
		baseURL:  url,
		db:       db,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// jsonrpcRequest is the standard Odoo JSON-RPC request envelope.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	ID      int         `json:"id"`
	Params  interface{} `json:"params"`
}

// jsonrpcResponse is the standard Odoo JSON-RPC response envelope.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonrpcError   `json:"error"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Name    string `json:"name"`
		Debug   string `json:"debug"`
		Message string `json:"message"`
	} `json:"data"`
}

func (e *jsonrpcError) Error() string {
	if e.Data.Message != "" {
		return fmt.Sprintf("odoo rpc error %d: %s — %s", e.Code, e.Message, e.Data.Message)
	}
	return fmt.Sprintf("odoo rpc error %d: %s", e.Code, e.Message)
}

// call sends a raw JSON-RPC request to /jsonrpc and returns the decoded result.
func (c *Client) call(service, method string, args interface{}) (json.RawMessage, error) {
	payload := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "call",
		ID:      1,
		Params: map[string]interface{}{
			"service": service,
			"method":  method,
			"args":    args,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(c.baseURL+"/jsonrpc", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var rpcResp jsonrpcResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	return rpcResp.Result, nil
}

// Authenticate authenticates with Odoo and stores the user id (uid).
// Returns the uid on success.
func (c *Client) Authenticate() (int, error) {
	args := []interface{}{c.db, c.username, c.password, map[string]interface{}{}}
	raw, err := c.call("common", "authenticate", args)
	if err != nil {
		return 0, fmt.Errorf("authenticate: %w", err)
	}

	var uid interface{}
	if err := json.Unmarshal(raw, &uid); err != nil {
		return 0, fmt.Errorf("decode uid: %w", err)
	}

	switch v := uid.(type) {
	case float64:
		if v == 0 {
			return 0, fmt.Errorf("authentication failed: invalid credentials")
		}
		c.uid = int(v)
		return c.uid, nil
	case bool:
		return 0, fmt.Errorf("authentication failed: invalid credentials")
	default:
		return 0, fmt.Errorf("unexpected uid type: %T", uid)
	}
}

// Call invokes model.method via execute_kw on the object service.
// Authenticate() must be called before using this method.
func (c *Client) Call(model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	if c.uid == 0 {
		if _, err := c.Authenticate(); err != nil {
			return nil, err
		}
	}
	if kwargs == nil {
		kwargs = map[string]interface{}{}
	}
	rpcArgs := []interface{}{c.db, c.uid, c.password, model, method, args, kwargs}
	raw, err := c.call("object", "execute_kw", rpcArgs)
	if err != nil {
		return nil, err
	}
	var result interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode result: %w", err)
	}
	return result, nil
}

// SearchRead searches model records matching domain and returns the given fields.
func (c *Client) SearchRead(model string, domain []interface{}, fields []string, offset, limit int) ([]map[string]interface{}, error) {
	kwargs := map[string]interface{}{
		"fields": fields,
		"offset": offset,
		"limit":  limit,
	}
	result, err := c.Call(model, "search_read", []interface{}{domain}, kwargs)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("re-marshal result: %w", err)
	}
	var records []map[string]interface{}
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode records: %w", err)
	}
	return records, nil
}

// SearchCount returns the number of model records matching domain.
func (c *Client) SearchCount(model string, domain []interface{}) (int, error) {
	result, err := c.Call(model, "search_count", []interface{}{domain}, nil)
	if err != nil {
		return 0, err
	}
	switch v := result.(type) {
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unexpected count type: %T", result)
	}
}

// Version calls the common.version endpoint and returns version info.
func (c *Client) Version() (map[string]interface{}, error) {
	raw, err := c.call("common", "version", []interface{}{})
	if err != nil {
		return nil, fmt.Errorf("version: %w", err)
	}
	var info map[string]interface{}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode version: %w", err)
	}
	return info, nil
}

// Health calls GET /web/health and returns true if Odoo is responsive.
func (c *Client) Health() (bool, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/web/health")
	if err != nil {
		return false, fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return true, nil
}
