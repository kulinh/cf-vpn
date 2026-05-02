package state

// Keys persisted in /etc/cfvpn/cfvpn.env. Adding a key here does NOT migrate
// existing files; callers must default-on-missing.
const (
	KeyMode             = "MODE"
	KeyDomain           = "DOMAIN"
	KeyPublicIP         = "PUBLIC_IP"
	KeyAdminHost        = "ADMIN_HOST"
	KeyAdminTunnelUUID  = "ADMIN_TUNNEL_UUID"
	KeyNodeID           = "NODE_ID"

	// Reality (direct mode)
	KeyRealityPriv      = "REALITY_PRIVATE_KEY"
	KeyRealityPub       = "REALITY_PUBLIC_KEY"
	KeyRealityShortID   = "REALITY_SHORT_ID"
	KeyRealityDest      = "REALITY_DEST"
	KeyRealitySNI       = "REALITY_SNI"

	// XHTTP (cloudflare mode)
	KeyXHTTPPath        = "XHTTP_PATH"

	// Hysteria (existing)
	KeyHy2Host          = "HY2_HOST"
	KeyHy2Port          = "HY2_PORT"
	KeyHy2ObfsPW        = "HY2_OBFS_PW"
)
