package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// API is the small slice of the Tailscale v2 API cf-vpn needs: read and write
// the policy file with an OAuth client that has the policy_file:write scope.
type API interface {
	GetPolicy(ctx context.Context) (policy []byte, etag string, err error)
	ValidatePolicy(ctx context.Context, policy []byte) error
	SetPolicy(ctx context.Context, policy []byte, ifMatch string) ([]byte, error)
}

// Client talks to api.tailscale.com (or BaseURL in tests) with a client
// credentials OAuth flow; the access token is fetched lazily and reused.
type Client struct {
	ClientID     string
	ClientSecret string
	BaseURL      string // default https://api.tailscale.com
	Tailnet      string // default "-" (the client's own tailnet)
	HTTP         *http.Client

	token string
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return "https://api.tailscale.com"
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) tailnet() string {
	if c.Tailnet == "" {
		return "-"
	}
	return c.Tailnet
}

func (c *Client) http() *http.Client {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTP
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", fmt.Errorf("tailscale oauth: client id and secret are required")
	}
	form := url.Values{"client_id": {c.ClientID}, "client_secret": {c.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/api/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("tailscale oauth token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tailscale oauth token: HTTP %d: %s", resp.StatusCode, trim(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("tailscale oauth token: no access_token in response")
	}
	c.token = tok.AccessToken
	return c.token, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, headers map[string]string) (int, []byte, http.Header, error) {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return 0, nil, nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("tailscale api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, out, resp.Header, nil
}

// GetPolicy fetches the policy file as HuJSON together with its ETag.
func (c *Client) GetPolicy(ctx context.Context) ([]byte, string, error) {
	code, body, hdr, err := c.do(ctx, http.MethodGet, "/api/v2/tailnet/"+c.tailnet()+"/acl", nil, map[string]string{"Accept": "application/hujson"})
	if err != nil {
		return nil, "", err
	}
	if code != http.StatusOK {
		return nil, "", fmt.Errorf("get policy: HTTP %d: %s", code, trim(body))
	}
	return body, hdr.Get("ETag"), nil
}

// ValidatePolicy runs the API's dry-run validation; a non-empty error body
// (the API answers 200 with {"message": ...} on failure) is returned as error.
func (c *Client) ValidatePolicy(ctx context.Context, policy []byte) error {
	code, body, _, err := c.do(ctx, http.MethodPost, "/api/v2/tailnet/"+c.tailnet()+"/acl/validate", policy, map[string]string{"Content-Type": "application/hujson"})
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("validate policy: HTTP %d: %s", code, trim(body))
	}
	var msg struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &msg) == nil && msg.Message != "" {
		return fmt.Errorf("validate policy: %s", msg.Message)
	}
	return nil
}

// SetPolicy writes the policy; ifMatch is the ETag from GetPolicy so a
// concurrent edit in the admin console is refused (HTTP 412) instead of
// overwritten. Returns the policy as stored.
func (c *Client) SetPolicy(ctx context.Context, policy []byte, ifMatch string) ([]byte, error) {
	h := map[string]string{"Content-Type": "application/hujson", "Accept": "application/hujson"}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	code, body, _, err := c.do(ctx, http.MethodPost, "/api/v2/tailnet/"+c.tailnet()+"/acl", policy, h)
	if err != nil {
		return nil, err
	}
	if code == http.StatusPreconditionFailed {
		return nil, fmt.Errorf("set policy: the policy changed since it was read (HTTP 412); re-run the command")
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("set policy: HTTP %d: %s", code, trim(body))
	}
	return body, nil
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
