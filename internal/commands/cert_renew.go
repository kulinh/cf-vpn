package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/state"
)

// CertRenewDeps lets tests inject a fake cert manager.
type CertRenewDeps struct {
	Cert cert.Manager
}

// RunCertRenew renews every cfvpn-managed certificate on this node. Today
// only the Hysteria2 host has a real Let's Encrypt cert; Reality direct nodes
// expose no public TLS, and cloudflare-mode nodes ride cloudflared's edge
// cert. The function is a no-op when no cert hosts are configured so the
// daily systemd timer is safe on every node.
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

	hy2CertPath, hy2KeyPath := HysteriaCertPaths()
	fmt.Fprintf(stdout, "cert-renew: renewing %s\n", hy2Host)
	if err := mgr.Renew(ctx, hy2Host, hy2CertPath, hy2KeyPath, token, 30); err != nil {
		return fmt.Errorf("renew %s: %w", hy2Host, err)
	}
	fmt.Fprintf(stdout, "cert-renew: ok %s\n", hy2Host)
	return nil
}
