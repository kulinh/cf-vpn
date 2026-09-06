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

func BuildVLESSRealityURI(name, uuid, host, sni, pbk, sid string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=%s&pbk=%s&sid=%s&fp=chrome#%s-Reality",
		uuid, host, enc(sni), enc(pbk), enc(sid), enc(name),
	)
}

func BuildVLESSHTTPUpgradeURI(name, uuid, domain, path string) string {
	enc := EncodeURIComponent
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=httpupgrade&host=%s&path=%s&sni=%s#%s-HTTPUpgrade",
		uuid, domain, enc(domain), encodeVLESSPath(path), enc(domain), enc(name),
	)
}

// BuildHy2URI builds the Hysteria2 client URI. Mirrors buildHy2URI() in
// panel/worker/src/lib/subscription.ts.
//
// The server runs `auth.type: userpass`, so the URI must carry
// "username:password@" — password alone gets a 404 auth error from the server.
// The host in the authority is deliberately NOT escaped (it is a hostname, and
// the Worker leaves it raw); it IS escaped in the sni= parameter.
func BuildHy2URI(tag, username, password, host string, port int, obfsPw string) string {
	enc := EncodeURIComponent
	return "hysteria2://" + enc(username) + ":" + enc(password) + "@" + host + ":" + strconv.Itoa(port) +
		"/?obfs=salamander&obfs-password=" + enc(obfsPw) +
		"&sni=" + enc(host) + "&insecure=0#" + enc(tag) + "-HY2"
}

func BuildSubscriptionB64(uris ...string) string {
	payload := strings.Join(uris, "\n")
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
