package templates

// VLESSPath is the neutral request path used by both XHTTP (cloudflare mode)
// and any legacy WS endpoints during transition. Was "/vless"; renamed to
// avoid being a GFW signature.
const VLESSPath = "/api/v1/sync"
