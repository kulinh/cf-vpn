package templates

// VLESSPath is the neutral request path used by both XHTTP (cloudflare mode)
// and any legacy WS endpoints during transition. Was "/vless"; renamed to
// avoid being a GFW signature.
const VLESSPath = "/api/v1/sync"

// XHTTPPath is the request path of the optional XHTTP inbound that sits next
// to HTTPUpgrade on cloudflare-mode nodes (XHTTP_ENABLED=1). Distinct from
// VLESSPath so cloudflared can route the two to different xray ports.
const XHTTPPath = "/api/v2/stream"

// XHTTPMode is the only XHTTP mode that works through the Cloudflare tunnel
// (stream-up/stream-one are rejected at the edge; verified 2026-09-12).
const XHTTPMode = "packet-up"

// XHTTPPort is the local port of the XHTTP inbound (HTTPUpgrade is 10001).
const XHTTPPort = 10002

// XHTTPDirectPort is the local port of the direct (not via Cloudflare) XHTTP
// inbound; a TLS front on :443 reverse-proxies one secret path to it.
const XHTTPDirectPort = 10003

// XHTTPDirectMode is the client mode for the direct route: no CDN in the way,
// so a single bidirectional stream is fine.
const XHTTPDirectMode = "stream-one"
