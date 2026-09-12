// internal/templates/render.go
package templates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/validate"
)

type XrayUser struct {
	Name string
	UUID string
}

type XrayDirectRealityInputs struct {
	Users       []XrayUser
	PrivateKey  string
	ShortIDs    []string
	Dest        string
	ServerNames []string
	// DNSServers overrides the DoH resolvers forced on node traffic. Empty
	// means the international DoH default (dohServers) — CHN nodes pass domestic
	// resolvers here instead.
	DNSServers []string
	// H3Host/H3Path/H3Cert/H3Key describe the optional XHTTP-over-H3 inbound
	// that sits next to REALITY on a direct-mode node. All four empty (the
	// fleet default) renders exactly the config it rendered before this
	// inbound existed. H3Host is the hostname the certificate was issued for;
	// it doubles as the TLS serverName and the XHTTP Host header.
	H3Host string
	H3Path string
	H3Cert string
	H3Key  string
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
{{if .Protocol}}protocol: {{.Protocol}}
{{end}}ingress:
  - hostname: {{.AdminHost}}
    service: http://127.0.0.1:6788
  - service: http_status:404
`

const cloudflaredWithAdminTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
{{if .Protocol}}protocol: {{.Protocol}}
{{end}}ingress:
{{if .XHTTP}}  - hostname: {{.Domain}}
    path: ^/api/v2/stream
    service: http://127.0.0.1:10002
{{end}}  - hostname: {{.Domain}}
    path: ^/api/v1/sync
    service: http://127.0.0.1:10001
  - hostname: {{.AdminHost}}
    service: http://127.0.0.1:6788
  - service: http_status:404
`

// validateCloudflaredInputs guards the only text/template-rendered config in
// the tree. Every other renderer builds a map and json.Marshal()s it, which is
// injection-proof; this one interpolates straight into unquoted YAML.
//
// Proven before this check existed: a Domain carrying a newline plus
// "    service: http://127.0.0.1:22" rendered with err == nil and produced a
// valid ingress whose FIRST matching rule published the node's SSH port through
// the Cloudflare tunnel (cloudflared takes the first match). TunnelUUID
// additionally lands inside a filesystem path (credentials-file), so it is
// pinned to the UUID shape.
func validateCloudflaredInputs(tunnelUUID string, hosts map[string]string) error {
	if err := validate.UUID(tunnelUUID); err != nil {
		return fmt.Errorf("cloudflared config: tunnel uuid: %w", err)
	}
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := validate.Hostname(hosts[name]); err != nil {
			return fmt.Errorf("cloudflared config: %s: %w", name, err)
		}
	}
	return nil
}

// validateCloudflaredProtocol pins the optional transport override to the two
// values cloudflared accepts. The value lands unquoted in YAML, so anything
// else (a newline, an extra key) is rejected the same way hostnames are.
func validateCloudflaredProtocol(p string) error {
	switch p {
	case "", "quic", "http2":
		return nil
	}
	return fmt.Errorf("cloudflared config: protocol %q is not one of quic, http2", p)
}

// RenderCloudflaredAdmin renders the direct-mode tunnel config (admin ingress
// only). protocol is "" for cloudflared's default, or "quic"/"http2".
func RenderCloudflaredAdmin(tunnelUUID, adminHost, protocol string) (string, error) {
	if err := validateCloudflaredInputs(tunnelUUID, map[string]string{"admin host": adminHost}); err != nil {
		return "", err
	}
	if err := validateCloudflaredProtocol(protocol); err != nil {
		return "", err
	}
	t, err := template.New("cloudflared-admin").Parse(cloudflaredAdminTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"TunnelUUID": tunnelUUID, "AdminHost": adminHost, "Protocol": protocol})
	return b.String(), err
}

// CloudflaredOptions are the per-node knobs of the cloudflare-mode tunnel
// config: the transport (see RenderCloudflaredAdmin) and whether the XHTTP
// ingress rule (XHTTPPath -> XHTTPPort) is emitted ahead of HTTPUpgrade.
type CloudflaredOptions struct {
	Protocol string
	XHTTP    bool
}

// RenderCloudflaredWithAdmin renders the cloudflare-mode tunnel config (VPN +
// admin ingress). protocol as in RenderCloudflaredAdmin; no XHTTP rule.
func RenderCloudflaredWithAdmin(tunnelUUID, domain, adminHost, protocol string) (string, error) {
	return RenderCloudflaredWithAdminOpts(tunnelUUID, domain, adminHost, CloudflaredOptions{Protocol: protocol})
}

// RenderCloudflaredWithAdminOpts is RenderCloudflaredWithAdmin with every knob.
func RenderCloudflaredWithAdminOpts(tunnelUUID, domain, adminHost string, opts CloudflaredOptions) (string, error) {
	if err := validateCloudflaredInputs(tunnelUUID, map[string]string{"domain": domain, "admin host": adminHost}); err != nil {
		return "", err
	}
	if err := validateCloudflaredProtocol(opts.Protocol); err != nil {
		return "", err
	}
	t, err := template.New("cloudflared-with-admin").Parse(cloudflaredWithAdminTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]any{"TunnelUUID": tunnelUUID, "Domain": domain, "AdminHost": adminHost, "Protocol": opts.Protocol, "XHTTP": opts.XHTTP})
	return b.String(), err
}

// dohServers is the DoH resolver list forced on all node traffic. IP-form URLs
// carry the resolver's address in the URL, so xray needs no plaintext :53
// bootstrap to reach them — the point behind the GFW, where plaintext :53
// lookups (to any provider, Google included) are poisoned.
var dohServers = []string{
	"https://1.1.1.1/dns-query",
	"https://9.9.9.9/dns-query",
}

// dnsBlock is the top-level xray `dns` object: every name xray resolves goes
// through the given servers, or the international DoH default when none are
// supplied.
func dnsBlock(servers []string) map[string]any {
	if len(servers) == 0 {
		servers = dohServers
	}
	return map[string]any{"servers": servers}
}

// sniffingBlock recovers the real domain from TLS/HTTP/QUIC even when the
// client dialed a pre-resolved IP, so those connections are re-resolved via
// our DNS instead of bypassing it.
func sniffingBlock() map[string]any {
	return map[string]any{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic"},
	}
}

// standardOutbounds forces name resolution through the DoH `dns` block:
//   - freedom uses domainStrategy UseIP so it resolves via that block, not the
//     node's /etc/resolv.conf;
//   - dns-out answers hijacked client DNS queries (see standardRouting) from the
//     same block.
func standardOutbounds() []map[string]any {
	return []map[string]any{
		{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIP"}},
		{"tag": "dns-out", "protocol": "dns"},
		{"tag": "block", "protocol": "blackhole"},
	}
}

// standardRouting hijacks all client DNS traffic (port 53) to dns-out so it is
// answered via DoH, then blocks connections to private IP ranges.
func standardRouting() map[string]any {
	return map[string]any{
		"rules": []any{
			map[string]any{"type": "field", "port": 53, "outboundTag": "dns-out"},
			map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
		},
	}
}

// RenderXrayCloudflareHTTPUpgrade renders the cloudflare-mode xray config
// with the HTTPUpgrade inbound only.
func RenderXrayCloudflareHTTPUpgrade(users []XrayUser, vpnHost string, dnsServers []string) (string, error) {
	return RenderXrayCloudflare(users, vpnHost, dnsServers, false)
}

// XrayCloudflareOptions are the optional inbounds of a cloudflare-mode node.
type XrayCloudflareOptions struct {
	XHTTP      bool   // second inbound on XHTTPPort behind the Cloudflare tunnel
	DirectHost string // direct XHTTP route: TLS hostname served by the local TLS front
	DirectPath string // direct XHTTP route: the one path the front proxies to XHTTPDirectPort
}

// RenderXrayCloudflare renders the cloudflare-mode xray config: the
// HTTPUpgrade inbound on 10001 and, when xhttp is set, a second VLESS inbound
// on XHTTPPort with network xhttp / XHTTPPath / XHTTPMode for the same users.
func RenderXrayCloudflare(users []XrayUser, vpnHost string, dnsServers []string, xhttp bool) (string, error) {
	return RenderXrayCloudflareOpts(users, vpnHost, dnsServers, XrayCloudflareOptions{XHTTP: xhttp})
}

// RenderXrayCloudflareOpts is RenderXrayCloudflare with every optional inbound.
func RenderXrayCloudflareOpts(users []XrayUser, vpnHost string, dnsServers []string, opts XrayCloudflareOptions) (string, error) {
	xhttp := opts.XHTTP
	if (opts.DirectHost == "") != (opts.DirectPath == "") {
		return "", errors.New("direct xhttp route needs both host and path")
	}
	if opts.DirectPath != "" && !strings.HasPrefix(opts.DirectPath, "/") {
		return "", errors.New("direct xhttp path must start with /")
	}
	clients := make([]map[string]string, 0, len(users))
	for _, u := range users {
		clients = append(clients, map[string]string{"id": u.UUID, "email": u.Name + "@vpn"})
	}
	inbounds := []any{
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
			"sniffing": sniffingBlock(),
		},
	}
	if xhttp {
		inbounds = append(inbounds, map[string]any{
			"tag":      "vless-xhttp",
			"listen":   "127.0.0.1",
			"port":     XHTTPPort,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    clients,
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network": "xhttp",
				"xhttpSettings": map[string]any{
					"path": XHTTPPath,
					"host": vpnHost,
					"mode": XHTTPMode,
				},
			},
			"sniffing": sniffingBlock(),
		})
	}
	if opts.DirectHost != "" {
		inbounds = append(inbounds, map[string]any{
			"tag":      "vless-xhttp-direct",
			"listen":   "127.0.0.1",
			"port":     XHTTPDirectPort,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    clients,
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network": "xhttp",
				"xhttpSettings": map[string]any{
					"path": opts.DirectPath,
					"host": opts.DirectHost,
					"mode": XHTTPDirectMode,
				},
			},
			"sniffing": sniffingBlock(),
		})
	}
	cfg := map[string]any{
		"log":       map[string]string{"loglevel": "warning"},
		"dns":       dnsBlock(dnsServers),
		"inbounds":  inbounds,
		"outbounds": standardOutbounds(),
		"routing":   standardRouting(),
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
	// The H3 inbound is all-or-nothing. A half-filled set would render an
	// inbound with an empty path or certificate path, xray would reject the
	// config, and the caller's restart would take down a node that was fine a
	// moment earlier.
	h3Set := in.H3Host != "" || in.H3Path != "" || in.H3Cert != "" || in.H3Key != ""
	if h3Set {
		if in.H3Host == "" || in.H3Path == "" || in.H3Cert == "" || in.H3Key == "" {
			return "", errors.New("h3 route needs host, path, cert and key together")
		}
		if !strings.HasPrefix(in.H3Path, "/") {
			return "", errors.New("h3 path must start with /")
		}
	}

	clients := make([]map[string]string, 0, len(in.Users))
	for _, u := range in.Users {
		clients = append(clients, map[string]string{
			"id":    u.UUID,
			"email": u.Name + "@vpn",
			"flow":  "xtls-rprx-vision",
		})
	}

	inbounds := []any{
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
			// NB: no sniffing on the Reality inbound. Sniffing with
			// destOverride rewrites the connection destination, which breaks
			// the xtls-rprx-vision splice and hangs the connection. DNS is
			// still forced via dns-out (port-53 hijack) + freedom UseIP.
		},
	}

	if in.H3Host != "" {
		// Deliberately NOT the `clients` slice above: that one carries
		// flow=xtls-rprx-vision, which only works over raw TCP. An H3 client
		// with a flow is rejected by xray.
		h3Clients := make([]map[string]string, 0, len(in.Users))
		for _, u := range in.Users {
			h3Clients = append(h3Clients, map[string]string{
				"id":    u.UUID,
				"email": u.Name + "@vpn",
			})
		}
		inbounds = append(inbounds, map[string]any{
			"tag":      "vless-xhttp-h3",
			"listen":   "0.0.0.0",
			"port":     XHTTPH3Port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    h3Clients,
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network":  "xhttp",
				"security": "tls",
				"tlsSettings": map[string]any{
					"serverName": in.H3Host,
					"alpn":       XHTTPH3ALPN,
					"certificates": []any{
						map[string]any{
							"certificateFile": in.H3Cert,
							"keyFile":         in.H3Key,
						},
					},
				},
				"xhttpSettings": map[string]any{
					"host": in.H3Host,
					"path": in.H3Path,
					"mode": XHTTPH3Mode,
				},
			},
			// Unlike REALITY above, sniffing is safe here: there is no
			// xtls-rprx-vision splice on this inbound to break.
			"sniffing": sniffingBlock(),
		})
	}

	cfg := map[string]any{
		"log":       map[string]string{"loglevel": "warning"},
		"dns":       dnsBlock(in.DNSServers),
		"inbounds":  inbounds,
		"outbounds": standardOutbounds(),
		"routing":   standardRouting(),
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
