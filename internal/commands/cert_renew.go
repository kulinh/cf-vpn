package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

// CertRenewDeps lets tests inject a fake cert manager and systemd runner.
type CertRenewDeps struct {
	Cert   cert.Manager
	Runner systemd.Runner
}

// RunCertRenew renews every cfvpn-managed certificate on this node. Today
// only the Hysteria2 host has a real Let's Encrypt cert; Reality direct nodes
// expose no public TLS, and cloudflare-mode nodes ride cloudflared's edge
// cert. The function is a no-op when no cert hosts are configured so the
// daily systemd timer is safe on every node.
//
// M-G2: when the certificate actually changes on disk, cfvpn-hysteria is
// restarted. hysteria loads the leaf at startup, and nothing else in the tree
// restarts it on renewal (CertRenewService() is a bare ExecStart with no
// ExecStartPost), so before this the node could serve an expired certificate
// until some unrelated restart. This fleet has already been bitten by a broken
// acme.sh reload hook, so the reload is made explicit here.
//
// H11: it takes the config lock like every other writer — it replaces files
// under /etc/cfvpn and restarts a service.
func RunCertRenew(ctx context.Context, env map[string]string, deps CertRenewDeps, stdout, stderr io.Writer) error {
	mgr := deps.Cert
	if mgr == nil {
		mgr = cert.NewDefault()
	}
	token := strings.TrimSpace(env["CF_API_TOKEN"])
	if token == "" {
		return fmt.Errorf("CF_API_TOKEN is required for cert renewal")
	}

	hy2Host := strings.TrimSpace(env[state.KeyHy2Host])
	if hy2Host == "" {
		fmt.Fprintln(stdout, "cert-renew: no cert hosts configured (HY2_HOST empty); nothing to do")
		return nil
	}

	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()

	hy2CertPath, hy2KeyPath := HysteriaCertPaths()
	beforeCert := readFileOrNil(hy2CertPath)
	beforeKey := readFileOrNil(hy2KeyPath)

	fmt.Fprintf(stdout, "cert-renew: renewing %s\n", hy2Host)
	if err := mgr.Renew(ctx, hy2Host, hy2CertPath, hy2KeyPath, token, 30); err != nil {
		return fmt.Errorf("renew %s: %w", hy2Host, err)
	}

	changed := !bytes.Equal(beforeCert, readFileOrNil(hy2CertPath)) ||
		!bytes.Equal(beforeKey, readFileOrNil(hy2KeyPath))
	if changed {
		if err := hysteria.ReloadService(ctx, resolveRunner(deps.Runner)); err != nil {
			return fmt.Errorf("restart cfvpn-hysteria.service after cert change: %w", err)
		}
		fmt.Fprintf(stdout, "cert-renew: ok %s (certificate changed, cfvpn-hysteria restarted)\n", hy2Host)
		return nil
	}
	fmt.Fprintf(stdout, "cert-renew: ok %s (unchanged)\n", hy2Host)
	return nil
}

// readFileOrNil returns the file's bytes, or nil when it cannot be read. A
// missing file and an unreadable one are both "no previous content", which is
// exactly what the change comparison needs.
func readFileOrNil(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}
