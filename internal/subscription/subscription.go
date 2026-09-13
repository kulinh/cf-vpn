// Package subscription builds the client URIs a node hands out.
//
// The Cloudflare Worker builds the same wire format in
// panel/worker/src/lib/subscription.ts. The two implementations MUST stay
// byte-for-byte identical for the same inputs — a user can be provisioned from
// either side — so every escape here mirrors the Worker's encodeURIComponent
// exactly (see EncodeURIComponent) and every builder below has a golden test
// pinning the literal string the Worker produces.
//
// The URI lines are byte-identical; the Worker additionally prefixes
// REMARKS=RWL8899 as the first line of the subscription payload it serves —
// the node side does not add that line to the files/output it generates.
package subscription

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// EncodeURIComponent percent-encodes s exactly like JavaScript's
// encodeURIComponent: ASCII letters, digits and -_.!~*'() pass through, every
// other byte becomes %XX (uppercase hex) over the UTF-8 encoding.
//
// Do NOT substitute url.QueryEscape (encodes space as "+", wrong inside a path
// or a fragment) or url.PathEscape (leaves $&+,;=:@ unescaped, so it would
// diverge from the Worker on any value containing them).
func EncodeURIComponent(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isURIComponentUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

func isURIComponentUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

// encodeVLESSPath escapes a transport path the way the Worker does:
// split on "/", encode each segment, re-join with the literal "%2F". The path
// is a *query parameter value*, so its slashes must stay escaped;
// "/api/v1/sync" therefore renders as "%2Fapi%2Fv1%2Fsync".
//
// Encoding only "/" (what this used to do) let any ?, #, & or space in the path
// through untouched, which truncates the URI's query string at the client.
func encodeVLESSPath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = EncodeURIComponent(seg)
	}
	return strings.Join(segments, "%2F")
}

// V6Suffix names the IPv6 twin of a Reality or HY2 route. Mirrors V6_SUFFIX
// in panel/worker/src/lib/subscription.ts.
const V6Suffix = "-v6"

// URIHost brackets an IPv6 literal for the authority part of a URI; hostnames
// and IPv4 pass through. Mirrors uriHost() in the Worker.
func URIHost(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

func BuildVLESSRealityURI(name, uuid, host, sni, pbk, sid string) string {
	return BuildVLESSRealityURISuffix(name, uuid, host, sni, pbk, sid, "")
}

// BuildVLESSRealityURISuffix is BuildVLESSRealityURI with suffix appended to
// the route name (V6Suffix for the IPv6 twin).
func BuildVLESSRealityURISuffix(name, uuid, host, sni, pbk, sid, suffix string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=%s&pbk=%s&sid=%s&fp=chrome#%s-Reality%s",
		uuid, URIHost(host), enc(sni), enc(pbk), enc(sid), enc(name), suffix,
	)
}

// alpn=http/1.1 is explicit: behind Cloudflare the Upgrade only works on
// HTTP/1.1, and a client that offers h2 (Shadowrocket does) gets h2 from the
// edge and hangs. xray picks http/1.1 by itself, other clients do not. XHTTP
// is the opposite and carries alpn=h2,http/1.1 (XTLS/Xray-core#6024).
func BuildVLESSHTTPUpgradeURI(name, uuid, domain, path string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=httpupgrade&host=%s&path=%s&alpn=http%%2F1.1&sni=%s#%s-HTTPUpgrade",
		uuid, domain, enc(domain), encodeVLESSPath(path), enc(domain), enc(name),
	)
}

// BuildVLESSXHTTPURI builds the XHTTP client URI for a cloudflare-mode node
// whose XHTTP inbound is enabled. Mirrors buildVLESSXHTTPURI() in
// panel/worker/src/lib/subscription.ts.
func BuildVLESSXHTTPURI(name, uuid, domain, path, mode string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=xhttp&host=%s&path=%s&mode=%s&alpn=h2%%2Chttp%%2F1.1&sni=%s#%s-XHTTP",
		uuid, domain, enc(domain), encodeVLESSPath(path), enc(mode), enc(domain), enc(name),
	)
}

// BuildVLESSXHTTPDirectURI builds the URI of the direct (not via Cloudflare)
// XHTTP route: real TLS on the node's own hostname, so the address MUST be the
// hostname. Mirrors buildVLESSXHTTPDirectURI() in
// panel/worker/src/lib/subscription.ts.
func BuildVLESSXHTTPDirectURI(name, uuid, host, path, mode string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=xhttp&host=%s&path=%s&mode=%s&alpn=h2%%2Chttp%%2F1.1&sni=%s#%s-XHTTP-Direct",
		uuid, host, enc(host), encodeVLESSPath(path), enc(mode), enc(host), enc(name),
	)
}

// BuildVLESSXHTTPH3URI builds the URI of the H3 (QUIC) XHTTP route on a
// direct-mode node: real TLS on the node's own hostname, served over UDP by
// quic-go, so the address MUST be the hostname the certificate was issued for.
// Mirrors buildVLESSXHTTPH3URI() in panel/worker/src/lib/subscription.ts.
//
// alpn=h3 is load-bearing, not decoration: xray only binds the UDP port when
// the TLS alpn list is exactly ["h3"], and a client that omits it dials TCP,
// where nothing is listening on this route.
func BuildVLESSXHTTPH3URI(name, uuid, host, path, mode string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=xhttp&host=%s&path=%s&mode=%s&alpn=h3&sni=%s#%s-XHTTP-H3",
		uuid, host, enc(host), encodeVLESSPath(path), enc(mode), enc(host), enc(name),
	)
}

// BuildHy2URI builds the Hysteria2 client URI. Mirrors buildHy2URI() in
// panel/worker/src/lib/subscription.ts.
//
// The server runs `auth.type: userpass`, so the URI must carry
// "username:password@" — password alone gets a 404 auth error from the server.
// address is what the client dials (the node's public IP, so no DNS lookup of
// the HY2 hostname is needed from China); sniHost is the hostname the HY2
// certificate was issued for and goes into sni=. The address in the authority
// is deliberately NOT escaped (an IP or hostname; the Worker leaves it raw);
// sniHost IS escaped in the sni= parameter.
func BuildHy2URI(tag, username, password, address, sniHost string, port int, obfsPw string) string {
	return BuildHy2URISuffix(tag, username, password, address, sniHost, port, obfsPw, "")
}

// BuildHy2URISuffix is BuildHy2URI with suffix appended to the route name
// (V6Suffix for the IPv6 twin). An IPv6 address is bracketed.
func BuildHy2URISuffix(tag, username, password, address, sniHost string, port int, obfsPw, suffix string) string {
	enc := EncodeURIComponent
	return "hysteria2://" + enc(username) + ":" + enc(password) + "@" + URIHost(address) + ":" + strconv.Itoa(port) +
		"/?obfs=salamander&obfs-password=" + enc(obfsPw) +
		"&sni=" + enc(sniHost) + "&insecure=0#" + enc(tag) + "-HY2" + suffix
}

func BuildSubscriptionB64(uris ...string) string {
	payload := strings.Join(uris, "\n")
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
