package state

// Keys persisted in /etc/cfvpn/cfvpn.env. Adding a key here does NOT migrate
// existing files; callers must default-on-missing.
const (
	KeyMode            = "MODE"
	KeyDomain          = "DOMAIN"
	KeyPublicIP        = "PUBLIC_IP"
	KeyAdminHost       = "ADMIN_HOST"
	KeyAdminTunnelUUID = "ADMIN_TUNNEL_UUID"
	KeyNodeID          = "NODE_ID"

	// Reality (direct mode)
	KeyRealityPriv    = "REALITY_PRIVATE_KEY"
	KeyRealityPub     = "REALITY_PUBLIC_KEY"
	KeyRealityShortID = "REALITY_SHORT_ID"
	KeyRealityDest    = "REALITY_DEST"
	KeyRealitySNI     = "REALITY_SNI"

	// XHTTP (cloudflare mode)
	KeyXHTTPPath = "XHTTP_PATH"

	// cloudflared transport: "" (cloudflared default), "quic" or "http2". Set
	// to http2 on nodes whose UDP path to the Cloudflare edge keeps dropping.
	KeyCloudflaredProtocol = "CLOUDFLARED_PROTOCOL"

	// XHTTP second inbound (cloudflare mode) on/off. Absent = off.
	KeyXHTTPEnabled = "XHTTP_ENABLED"

	// Direct XHTTP route (cloudflare-mode nodes): a TLS front (Caddy) on :443
	// reverse-proxies exactly XHTTP_DIRECT_PATH to a second xhttp inbound on
	// templates.XHTTPDirectPort. Both empty = no direct route.
	KeyXHTTPDirectHost = "XHTTP_DIRECT_HOST"
	KeyXHTTPDirectPath = "XHTTP_DIRECT_PATH"

	// XHTTP-over-H3 route (direct-mode nodes): a second xray inbound on UDP
	// templates.XHTTPH3Port serving XHTTP_H3_PATH under real TLS for
	// XHTTP_H3_HOST. Both empty = no H3 route. The certificate is the HY2 one
	// (same host), which xray hot-reloads from disk, so nothing extra has to
	// run on renewal.
	KeyXHTTPH3Host = "XHTTP_H3_HOST"
	KeyXHTTPH3Path = "XHTTP_H3_PATH"

	// NaiveProxy route: a Caddy forward_proxy (probe_resistance) on TCP 443
	// under the real certificate for NAIVE_HOST, with one shared basic-auth
	// pair. Caddy is configured by hand on the node (JPY-01, 2026-09-13) and
	// reads NAIVE_USER/NAIVE_PASS from this file; cf-vpn only reports them so
	// the panel can hand out the route. All three set = route on.
	KeyNaiveHost = "NAIVE_HOST"
	KeyNaiveUser = "NAIVE_USER"
	KeyNaivePass = "NAIVE_PASS"

	// Hysteria2 on/off. Absent = on. "0"/"false"/"no"/"off" = the node runs no
	// HY2 (unit disabled, no HY2 line in subscriptions, no HY2 cert renewal).
	KeyHy2Enabled = "HY2_ENABLED"

	// Hysteria (existing)
	KeyHy2Host   = "HY2_HOST"
	KeyHy2Port   = "HY2_PORT"
	KeyHy2ObfsPW = "HY2_OBFS_PW"
)
