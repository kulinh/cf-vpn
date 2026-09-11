package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/validate"
	"github.com/kulinh/cf-vpn/internal/xray"
)

// RotateRealityInputs names the new Reality "steal" target. SNI defaults to
// the host part of Dest.
type RotateRealityInputs struct {
	Dest string // host:port the Reality inbound forwards non-client handshakes to
	SNI  string // serverName clients must send; "" = host of Dest
}

// RealityKeygen mints a keypair + shortId; RunRotateReality takes it as a
// parameter so tests do not exec `xray x25519`.
type RealityKeygen func(xray.GenerateRealityOptions) (xray.RealityParams, error)

// RunRotateReality replaces this node's Reality credentials: a fresh x25519
// keypair, a fresh shortId and the given dest/SNI. It re-renders the xray
// config, restarts xray (restoring the previous config if the restart fails),
// persists REALITY_* to cfvpn.env and regenerates the node-side subscription
// files. The caller is responsible for pushing the new public key, shortId,
// SNI and dest to the panel (scripts/d1-set-node.sh <NODE> reality) — until
// then every client keeps the old key and cannot connect.
func RunRotateReality(ctx context.Context, in RotateRealityInputs, runner systemd.Runner, keygen RealityKeygen, stdout, stderr io.Writer) error {
	in.Dest = strings.TrimSpace(in.Dest)
	in.SNI = strings.TrimSpace(in.SNI)
	if in.Dest == "" {
		return fmt.Errorf("--dest host:port is required")
	}
	host, port, err := net.SplitHostPort(in.Dest)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("--dest must be host:port, got %q", in.Dest)
	}
	if err := validate.Hostname(host); err != nil {
		return fmt.Errorf("--dest host: %w", err)
	}
	if in.SNI == "" {
		in.SNI = host
	}
	if err := validate.Hostname(in.SNI); err != nil {
		return fmt.Errorf("--sni: %w", err)
	}
	if keygen == nil {
		keygen = xray.GenerateRealityParams
	}

	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()

	env, err := state.Load(envFilePath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}
	if mode := env[state.KeyMode]; mode != "direct" {
		return fmt.Errorf("rotate-reality needs MODE=direct (this node is MODE=%q)", mode)
	}

	params, err := keygen(xray.GenerateRealityOptions{Dest: in.Dest, SNI: in.SNI})
	if err != nil {
		return fmt.Errorf("generate reality params: %w", err)
	}
	users, err := usersFromCurrentXray()
	if err != nil {
		return err
	}
	rendered, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
		Users:       users,
		PrivateKey:  params.PrivateKey,
		ShortIDs:    []string{params.ShortID},
		Dest:        params.Dest,
		ServerNames: []string{params.SNI},
		DNSServers:  xrayDNSServersFromEnv(env),
	})
	if err != nil {
		return fmt.Errorf("render xray reality config: %w", err)
	}

	oldConfig, oldErr := os.ReadFile(xrayConfigPath)
	if err := writeXrayConfigChecked(ctx, xrayConfigPath, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}
	r := resolveRunner(runner)
	if err := systemd.Restart(ctx, r, "cfvpn-xray.service"); err != nil {
		if oldErr == nil {
			if rerr := writeAtomicFile(xrayConfigPath, oldConfig, 0o600); rerr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: restore previous xray config failed: %v\n", rerr)
			} else if rerr := systemd.Restart(ctx, r, "cfvpn-xray.service"); rerr != nil && stderr != nil {
				fmt.Fprintf(stderr, "warning: restart xray on the restored config failed: %v\n", rerr)
			}
		}
		return fmt.Errorf("restart cfvpn-xray.service (previous config restored): %w", err)
	}

	env[state.KeyRealityPriv] = params.PrivateKey
	env[state.KeyRealityPub] = params.PublicKey
	env[state.KeyRealityShortID] = params.ShortID
	env[state.KeyRealityDest] = params.Dest
	env[state.KeyRealitySNI] = params.SNI
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return fmt.Errorf("save env: %w", err)
	}
	if err := RegenerateSubscriptionsTo(env[state.KeyDomain], stderr); err != nil {
		return fmt.Errorf("regenerate subscriptions: %w", err)
	}
	fmt.Fprintf(stdout, "reality rotated: dest=%s sni=%s pbk=%s sid=%s\n", params.Dest, params.SNI, params.PublicKey, params.ShortID)
	return nil
}
