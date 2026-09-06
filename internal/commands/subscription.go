package commands

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/templates"
)

// buildUserURIs returns the client URIs for one user on this node. It is the Go
// mirror of buildSubscriptionURIs() in panel/worker/src/lib/subscription.ts and
// must keep the same branching, or the same user gets different links depending
// on whether they were provisioned from the panel or from `cfvpnctl`.
//
// H10 — the branch that matters: a direct node serves REALITY on :443 with
// dest www.apple.com. Handing such a node's user a security=tls&type=httpupgrade
// URI (what the old `else` branch did whenever any Reality field was missing)
// makes the client perform an ordinary TLS handshake, which REALITY forwards to
// Apple; the client then gets Apple's certificate for the VPN hostname and
// fails. REALITY_SNI was not even checked, so an empty SNI produced "&sni=&pbk="
// which cannot match realitySettings.serverNames and hangs the client with no
// error. Emitting nothing — and saying so on stderr — is the only honest
// outcome for a direct node with incomplete Reality params.
//
// hy2PW is the user's Hysteria2 password as it exists in the node's hysteria
// config; an empty value simply suppresses the HY2 line.
func buildUserURIs(name, uuid, domain, hy2PW string, env map[string]string, warn io.Writer) []string {
	var lines []string

	if env[state.KeyMode] == "direct" {
		pub := env[state.KeyRealityPub]
		sid := env[state.KeyRealityShortID]
		sni := env[state.KeyRealitySNI]
		if pub == "" || sid == "" || sni == "" {
			warnf(warn, "warning: node is MODE=direct but Reality params are incomplete "+
				"(REALITY_PUBLIC_KEY=%q REALITY_SHORT_ID=%q REALITY_SNI=%q); emitting no VLESS URI for %q — "+
				"an HTTPUpgrade URI would be served by nothing on this node",
				pub, sid, sni, name)
		} else {
			lines = append(lines, subscription.BuildVLESSRealityURI(name, uuid, domain, sni, pub, sid))
		}
	} else {
		path := env[state.KeyXHTTPPath]
		if path == "" {
			path = templates.VLESSPath
		}
		lines = append(lines, subscription.BuildVLESSHTTPUpgradeURI(name, uuid, domain, path))
	}

	if uri, ok := buildHy2Line(name, hy2PW, env, warn); ok {
		lines = append(lines, uri)
	}
	return lines
}

// buildHy2Line renders the Hysteria2 URI when the node has a complete HY2
// configuration and the user has a password in the hysteria config. Anything
// missing is reported rather than silently dropped (the Worker drops it
// silently; on the node we can at least say why).
func buildHy2Line(name, hy2PW string, env map[string]string, warn io.Writer) (string, bool) {
	host := env[state.KeyHy2Host]
	if host == "" {
		return "", false
	}
	port, err := strconv.Atoi(env[state.KeyHy2Port])
	if err != nil || port <= 0 {
		warnf(warn, "warning: HY2_HOST is set but HY2_PORT=%q is not a port; no HY2 line for %q", env[state.KeyHy2Port], name)
		return "", false
	}
	obfs := env[state.KeyHy2ObfsPW]
	if obfs == "" {
		warnf(warn, "warning: HY2_HOST is set but HY2_OBFS_PW is empty; no HY2 line for %q", name)
		return "", false
	}
	if hy2PW == "" {
		warnf(warn, "warning: user %q has no password in the hysteria config; no HY2 line "+
			"(run `cfvpnctl add-user` or a panel sync to provision it)", name)
		return "", false
	}
	return subscription.BuildHy2URI(name, name, hy2PW, host, port, obfs), true
}

// hy2PasswordsByName reads the node's hysteria config and returns password by
// user name. A missing config is not an error — the node simply has no HY2 —
// but an unreadable one is reported so the caller can warn.
func hy2PasswordsByName() (map[string]string, error) {
	users, err := hysteria.ListUsers(hysteriaConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return map[string]string{}, err
	}
	out := make(map[string]string, len(users))
	for _, u := range users {
		out[u.Name] = u.Password
	}
	return out, nil
}

func warnf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format+"\n", args...)
}
