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

	// Hysteria2 on/off. Absent = on. "0"/"false"/"no"/"off" = the node runs no
	// HY2 (unit disabled, no HY2 line in subscriptions, no HY2 cert renewal).
	KeyHy2Enabled = "HY2_ENABLED"

	// Hysteria (existing)
	KeyHy2Host   = "HY2_HOST"
	KeyHy2Port   = "HY2_PORT"
	KeyHy2ObfsPW = "HY2_OBFS_PW"
)
