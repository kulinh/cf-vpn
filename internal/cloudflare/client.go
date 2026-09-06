package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/validate"
)

// DefaultClient returns a Client wired with a sane HTTP timeout (60s).
// The Cloudflare API rarely takes more than a few seconds; without a timeout
// a stuck connection wedges every install/rotate flow indefinitely.
func DefaultClient(token, accountID string) *Client {
	return &Client{
		BaseURL:   "https://api.cloudflare.com/client/v4",
		Token:     token,
		AccountID: accountID,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
	}
}

type Client struct {
	BaseURL   string
	Token     string
	AccountID string
	HTTP      *http.Client
}

type apiResp struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (c Client) do(ctx context.Context, method, path string, body []byte) (apiResp, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return apiResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return apiResp{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiResp{}, err
	}
	var out apiResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return apiResp{}, err
	}
	if !out.Success {
		if len(out.Errors) > 0 {
			return apiResp{}, fmt.Errorf("cf api error: %d: %s", out.Errors[0].Code, out.Errors[0].Message)
		}
		return apiResp{}, fmt.Errorf("cf api error: unknown")
	}
	return out, nil
}

// zonePath builds "/zones/<id>" for a caller-supplied zone id.
//
// C2/LOW: zone, tunnel and record ids used to be concatenated into the API path
// unchecked. The tunnel id in particular arrives from an agent's status
// response, so a value like "../accounts/<id>/tokens" reached a different
// Cloudflare endpoint entirely. Ids are validated against their documented
// shape and path-escaped; both, because escaping alone still lets a nonsense id
// reach the API and validation alone is one refactor away from being bypassed.
func zonePath(zoneID string) (string, error) {
	if err := validate.HexID32(zoneID); err != nil {
		return "", fmt.Errorf("cloudflare: zone id: %w", err)
	}
	return "/zones/" + url.PathEscape(zoneID), nil
}

// recordPath builds "/zones/<id>/dns_records/<record id>".
func recordPath(zoneID, recordID string) (string, error) {
	zp, err := zonePath(zoneID)
	if err != nil {
		return "", err
	}
	if err := validate.HexID32(recordID); err != nil {
		return "", fmt.Errorf("cloudflare: dns record id: %w", err)
	}
	return zp + "/dns_records/" + url.PathEscape(recordID), nil
}

// accountPath builds "/accounts/<id>" from the client's configured account.
func (c Client) accountPath() (string, error) {
	if err := validate.HexID32(c.AccountID); err != nil {
		return "", fmt.Errorf("cloudflare: account id: %w", err)
	}
	return "/accounts/" + url.PathEscape(c.AccountID), nil
}

func (c Client) GetZoneID(ctx context.Context, domain string) (string, error) {
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		q := url.Values{"name": {candidate}}.Encode()
		resp, err := c.do(ctx, http.MethodGet, "/zones?"+q, nil)
		if err != nil {
			return "", err
		}
		var zones []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(resp.Result, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("zone not found for domain: %s", domain)
}

func (c Client) CreateTunnel(ctx context.Context, name string) (string, []byte, error) {
	secretRaw := make([]byte, 32)
	if _, err := rand.Read(secretRaw); err != nil {
		return "", nil, err
	}
	secret := base64.StdEncoding.EncodeToString(secretRaw)
	body, _ := json.Marshal(map[string]any{"name": name, "tunnel_secret": secret, "config_src": "local"})
	ap, err := c.accountPath()
	if err != nil {
		return "", nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, ap+"/cfd_tunnel", body)
	if err != nil {
		return "", nil, err
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", nil, err
	}
	cred, _ := json.Marshal(map[string]string{
		"AccountTag":   c.AccountID,
		"TunnelID":     result.ID,
		"TunnelSecret": secret,
	})
	return result.ID, cred, nil
}

func (c Client) UpsertCNAME(ctx context.Context, zoneID, name, target string) error {
	// name.exact + match=all so a partial-name match can't return a different
	// record and cause the PUT below to overwrite the wrong CNAME (consistent
	// with UpsertARecord / deleteRecordsByName).
	zp, err := zonePath(zoneID)
	if err != nil {
		return err
	}
	q := url.Values{"type": {"CNAME"}, "name.exact": {name}, "match": {"all"}}.Encode()
	get, err := c.do(ctx, http.MethodGet, zp+"/dns_records?"+q, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(get.Result, &records); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"type": "CNAME", "name": name, "content": target, "proxied": true, "ttl": 1})
	if len(records) > 0 {
		rp, err := recordPath(zoneID, records[0].ID)
		if err != nil {
			return err
		}
		_, err = c.do(ctx, http.MethodPut, rp, payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, zp+"/dns_records", payload)
	return err
}

func (c Client) DeleteTunnel(ctx context.Context, tunnelID string) error {
	ap, err := c.accountPath()
	if err != nil {
		return err
	}
	if err := validate.UUID(tunnelID); err != nil {
		return fmt.Errorf("cloudflare: tunnel id: %w", err)
	}
	_, err = c.do(ctx, http.MethodDelete, ap+"/cfd_tunnel/"+url.PathEscape(tunnelID), nil)
	return err
}

func (c Client) UpsertARecord(ctx context.Context, zoneID, name, ip string) error {
	zp, err := zonePath(zoneID)
	if err != nil {
		return err
	}
	q := url.Values{
		"type":       {"A"},
		"name.exact": {name},
		"match":      {"all"},
	}.Encode()
	get, err := c.do(ctx, http.MethodGet, zp+"/dns_records?"+q, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(get.Result, &records); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"type":    "A",
		"name":    name,
		"content": ip,
		"proxied": false,
		"ttl":     60,
	})
	if len(records) > 0 {
		rp, err := recordPath(zoneID, records[0].ID)
		if err != nil {
			return err
		}
		_, err = c.do(ctx, http.MethodPut, rp, payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, zp+"/dns_records", payload)
	return err
}

func (c Client) DeleteARecordByName(ctx context.Context, zoneID, name string) error {
	return c.deleteRecordsByName(ctx, zoneID, "A", name)
}

func (c Client) DeleteCNAMEByName(ctx context.Context, zoneID, name string) error {
	return c.deleteRecordsByName(ctx, zoneID, "CNAME", name)
}

func (c Client) deleteRecordsByName(ctx context.Context, zoneID, recordType, name string) error {
	zp, err := zonePath(zoneID)
	if err != nil {
		return err
	}
	q := url.Values{
		"type":       {recordType},
		"name.exact": {name},
		"match":      {"all"},
	}.Encode()
	get, err := c.do(ctx, http.MethodGet, zp+"/dns_records?"+q, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(get.Result, &records); err != nil {
		return err
	}
	for _, r := range records {
		rp, err := recordPath(zoneID, r.ID)
		if err != nil {
			return err
		}
		if _, err := c.do(ctx, http.MethodDelete, rp, nil); err != nil {
			return err
		}
	}
	return nil
}
