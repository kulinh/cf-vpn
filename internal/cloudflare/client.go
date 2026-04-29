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
)

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

func (c Client) GetZoneID(ctx context.Context, domain string) (string, error) {
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		resp, err := c.do(ctx, http.MethodGet, "/zones?name="+candidate, nil)
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
	resp, err := c.do(ctx, http.MethodPost, "/accounts/"+c.AccountID+"/cfd_tunnel", body)
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
	get, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?type=CNAME&name="+name, nil)
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
		_, err = c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+records[0].ID, payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload)
	return err
}

func (c Client) DeleteTunnel(ctx context.Context, tunnelID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/accounts/"+c.AccountID+"/cfd_tunnel/"+tunnelID, nil)
	return err
}

func (c Client) UpsertARecord(ctx context.Context, zoneID, name, ip string) error {
	q := url.Values{
		"type":       {"A"},
		"name.exact": {name},
		"match":      {"all"},
	}.Encode()
	get, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q, nil)
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
		_, err = c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+records[0].ID, payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload)
	return err
}

func (c Client) DeleteARecordByName(ctx context.Context, zoneID, name string) error {
	q := url.Values{
		"type":       {"A"},
		"name.exact": {name},
		"match":      {"all"},
	}.Encode()
	get, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q, nil)
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
		if _, err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+r.ID, nil); err != nil {
			return err
		}
	}
	return nil
}
