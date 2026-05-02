// internal/templates/render.go
package templates

import (
	"bytes"
	"encoding/json"
	"errors"
	"text/template"

	"github.com/kulinh/cf-vpn/internal/hysteria"
)

type XrayUser struct {
	Name string
	UUID string
}

type XrayCert struct {
	Zone     string
	CertFile string
	KeyFile  string
}

type XrayDirectInputs struct {
	Users []XrayUser
	Certs []XrayCert
}

type XrayDirectRealityInputs struct {
	Users       []XrayUser
	PrivateKey  string
	ShortIDs    []string
	Dest        string
	ServerNames []string
}

type HysteriaUser struct{ Name, Password string }

type HysteriaInputs struct {
	Listen   string
	TLSCert  string
	TLSKey   string
	ObfsPW   string
	UpMbps   int
	DownMbps int
	Users    []HysteriaUser
}

const cloudflaredAdminTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
ingress:
  - hostname: {{.AdminHost}}
    service: http://127.0.0.1:6788
  - service: http_status:404
`

const cloudflaredWithAdminTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
ingress:
  - hostname: {{.Domain}}
    path: ^/api/v1/sync
    service: http://127.0.0.1:10001
  - hostname: {{.AdminHost}}
    service: http://127.0.0.1:6788
  - service: http_status:404
`

const xrayTemplate = `{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "127.0.0.1",
      "port": 10001,
      "protocol": "vless",
      "settings": {"clients": [{"id": "{{.UUID}}", "email": "{{.User}}@vpn"}], "decryption": "none"},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/api/v1/sync"}}
    }
  ],
  "outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "block", "protocol": "blackhole"}],
  "routing": {"rules": [{"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}]}
}
`

func RenderCloudflaredAdmin(tunnelUUID, adminHost string) (string, error) {
	t, err := template.New("cloudflared-admin").Parse(cloudflaredAdminTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"TunnelUUID": tunnelUUID, "AdminHost": adminHost})
	return b.String(), err
}

func RenderCloudflaredWithAdmin(tunnelUUID, domain, adminHost string) (string, error) {
	t, err := template.New("cloudflared-with-admin").Parse(cloudflaredWithAdminTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"TunnelUUID": tunnelUUID, "Domain": domain, "AdminHost": adminHost})
	return b.String(), err
}

func RenderXrayCloudflareHTTPUpgrade(users []XrayUser, vpnHost string) (string, error) {
	clients := make([]map[string]string, 0, len(users))
	for _, u := range users {
		clients = append(clients, map[string]string{"id": u.UUID, "email": u.Name + "@vpn"})
	}
	cfg := map[string]any{
		"log": map[string]string{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-httpupgrade",
				"listen":   "127.0.0.1",
				"port":     10001,
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    clients,
					"decryption": "none",
				},
				"streamSettings": map[string]any{
					"network": "httpupgrade",
					"httpupgradeSettings": map[string]any{
						"path": VLESSPath,
						"host": vpnHost,
					},
				},
			},
		},
		"outbounds": []map[string]any{
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func RenderHysteriaConfig(in HysteriaInputs) ([]byte, error) {
	users := make([]hysteria.User, 0, len(in.Users))
	for _, u := range in.Users {
		users = append(users, hysteria.User{Name: u.Name, Password: u.Password})
	}
	return hysteria.Render(hysteria.Config{
		Listen:   in.Listen,
		TLSCert:  in.TLSCert,
		TLSKey:   in.TLSKey,
		ObfsPW:   in.ObfsPW,
		UpMbps:   in.UpMbps,
		DownMbps: in.DownMbps,
		Users:    users,
	})
}

// Deprecated: WS-path renderer. Use RenderXrayDirectReality for direct mode or
// RenderXrayCloudflareXHTTP for cloudflare mode once all nodes are migrated.
func RenderXrayDirect(in XrayDirectInputs) (string, error) {
	if len(in.Certs) == 0 {
		return "", errors.New("at least one certificate is required")
	}

	vlessClients := make([]map[string]string, 0, len(in.Users))
	for _, u := range in.Users {
		vlessClients = append(vlessClients, map[string]string{"id": u.UUID, "email": u.Name + "@vpn"})
	}

	var certs []map[string]string
	for _, c := range in.Certs {
		certs = append(certs, map[string]string{"certificateFile": c.CertFile, "keyFile": c.KeyFile})
	}

	cfg := map[string]any{
		"log": map[string]string{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-ws",
				"listen":   "0.0.0.0",
				"port":     443,
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    vlessClients,
					"decryption": "none",
				},
				"streamSettings": map[string]any{
					"network":  "ws",
					"security": "tls",
					"wsSettings": map[string]any{
						"path": "/api/v1/sync",
					},
					"tlsSettings": map[string]any{
						"certificates": certs,
					},
				},
			},
		},
		"outbounds": []map[string]any{
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func RenderXrayDirectReality(in XrayDirectRealityInputs) (string, error) {
	if in.PrivateKey == "" {
		return "", errors.New("privateKey is required")
	}
	if in.Dest == "" {
		return "", errors.New("dest is required")
	}
	if len(in.ServerNames) == 0 {
		return "", errors.New("at least one serverName is required")
	}
	if len(in.ShortIDs) == 0 {
		return "", errors.New("at least one shortId is required")
	}

	clients := make([]map[string]string, 0, len(in.Users))
	for _, u := range in.Users {
		clients = append(clients, map[string]string{
			"id":    u.UUID,
			"email": u.Name + "@vpn",
			"flow":  "xtls-rprx-vision",
		})
	}

	cfg := map[string]any{
		"log": map[string]string{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-reality",
				"listen":   "0.0.0.0",
				"port":     443,
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    clients,
					"decryption": "none",
				},
				"streamSettings": map[string]any{
					"network":  "tcp",
					"security": "reality",
					"realitySettings": map[string]any{
						"show":        false,
						"dest":        in.Dest,
						"xver":        0,
						"serverNames": in.ServerNames,
						"privateKey":  in.PrivateKey,
						"shortIds":    in.ShortIDs,
					},
				},
			},
		},
		"outbounds": []map[string]any{
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
