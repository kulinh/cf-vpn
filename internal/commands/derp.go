package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/tailscale"
)

// Where the OAuth client lives and where policy snapshots go. Package-level so
// tests can redirect them.
var (
	derpOAuthEnvPath = "/etc/cfvpn/tailscale-oauth.env"
	derpBackupDir    = "/root/cfvpn-backups/acl"
)

// DerpDeps lets tests inject a fake API and a fake netcheck.
type DerpDeps struct {
	API      tailscale.API                             // nil = real client from derpOAuthEnvPath
	Netcheck func(ctx context.Context) ([]byte, error) // nil = `tailscale netcheck`
	Now      func() time.Time                          // nil = time.Now
	// Settle is how long to wait after the write before running netcheck so
	// the new DERP map has reached this client. nil = 10 s; tests pass 0.
	Settle *time.Duration
}

func (d *DerpDeps) settle() time.Duration {
	if d.Settle != nil {
		return *d.Settle
	}
	return 10 * time.Second
}

func (d *DerpDeps) api() (tailscale.API, error) {
	if d.API != nil {
		return d.API, nil
	}
	env, err := state.Load(derpOAuthEnvPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (create it with TS_OAUTH_CLIENT_ID and TS_OAUTH_CLIENT_SECRET, mode 600)", derpOAuthEnvPath, err)
	}
	id, secret := strings.TrimSpace(env["TS_OAUTH_CLIENT_ID"]), strings.TrimSpace(env["TS_OAUTH_CLIENT_SECRET"])
	if id == "" || secret == "" {
		return nil, fmt.Errorf("%s: TS_OAUTH_CLIENT_ID and TS_OAUTH_CLIENT_SECRET must both be set", derpOAuthEnvPath)
	}
	return &tailscale.Client{ClientID: id, ClientSecret: secret}, nil
}

func (d *DerpDeps) netcheck(ctx context.Context) ([]byte, error) {
	if d.Netcheck != nil {
		return d.Netcheck(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tailscale", "netcheck").CombinedOutput()
}

func (d *DerpDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// snapshot writes the policy to derpBackupDir/<ts>.<suffix>.json (mode 600).
func snapshot(ts, suffix string, policy []byte) (string, error) {
	if err := os.MkdirAll(derpBackupDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", derpBackupDir, err)
	}
	p := filepath.Join(derpBackupDir, ts+"."+suffix+".json")
	if err := os.WriteFile(p, policy, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	return p, nil
}

// editPolicy is the shared read → snapshot → patch → validate → write →
// snapshot flow. mutate returns the new policy; a nil result means "nothing to
// change" and the write is skipped.
func editPolicy(ctx context.Context, deps DerpDeps, label string, mutate func(policy []byte) ([]byte, error), stdout io.Writer) error {
	api, err := deps.api()
	if err != nil {
		return err
	}
	before, etag, err := api.GetPolicy(ctx)
	if err != nil {
		return err
	}
	ts := deps.now().UTC().Format("20060102T150405Z")
	beforePath, err := snapshot(ts, "before", before)
	if err != nil {
		return err
	}
	after, err := mutate(before)
	if err != nil {
		return err
	}
	if after == nil {
		fmt.Fprintf(stdout, "%s: already in the requested state; policy untouched (snapshot %s)\n", label, beforePath)
		return nil
	}
	if err := api.ValidatePolicy(ctx, after); err != nil {
		return err
	}
	stored, err := api.SetPolicy(ctx, after, etag)
	if err != nil {
		return err
	}
	if len(stored) == 0 {
		stored = after
	}
	afterPath, err := snapshot(ts, "after", stored)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s: policy updated (before: %s, after: %s)\n", label, beforePath, afterPath)
	return nil
}

// RunDerpChinaMode flips derpMap.OmitDefaultRegions: on = devices use only
// the custom regions (HKG-01/JPY-01) — for travel inside China, where the
// public relays are unreachable; off = public relays plus the custom regions.
// After the write it runs `tailscale netcheck` on this machine and prints it.
func RunDerpChinaMode(ctx context.Context, on bool, deps DerpDeps, stdout, stderr io.Writer) error {
	label := "china-mode " + map[bool]string{true: "on", false: "off"}[on]
	err := editPolicy(ctx, deps, label, func(policy []byte) ([]byte, error) {
		s, err := tailscale.Summarize(policy)
		if err != nil {
			return nil, err
		}
		if s.OmitDefaultRegions == on {
			return nil, nil
		}
		return tailscale.SetOmitDefaultRegions(policy, on)
	}, stdout)
	if err != nil {
		return err
	}
	// The DERP map reaches clients within seconds; give it a moment so the
	// netcheck below reflects the new state.
	if wait := deps.settle(); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	out, nerr := deps.netcheck(ctx)
	fmt.Fprintln(stdout, "--- tailscale netcheck ---")
	fmt.Fprint(stdout, string(out))
	if nerr != nil && stderr != nil {
		fmt.Fprintf(stderr, "warning: tailscale netcheck: %v\n", nerr)
	}
	return nil
}

// RunDerpRegionAdd adds (or replaces) a custom region.
func RunDerpRegionAdd(ctx context.Context, r tailscale.Region, deps DerpDeps, stdout io.Writer) error {
	return editPolicy(ctx, deps, fmt.Sprintf("region add %d", r.ID), func(policy []byte) ([]byte, error) {
		return tailscale.AddRegion(policy, r)
	}, stdout)
}

// RunDerpRegionRemove removes a custom region.
func RunDerpRegionRemove(ctx context.Context, id int, deps DerpDeps, stdout io.Writer) error {
	return editPolicy(ctx, deps, fmt.Sprintf("region remove %d", id), func(policy []byte) ([]byte, error) {
		return tailscale.RemoveRegion(policy, id)
	}, stdout)
}

// RunDerpShow prints the derpMap as the policy declares it.
func RunDerpShow(ctx context.Context, deps DerpDeps, stdout io.Writer) error {
	api, err := deps.api()
	if err != nil {
		return err
	}
	policy, _, err := api.GetPolicy(ctx)
	if err != nil {
		return err
	}
	s, err := tailscale.Summarize(policy)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "OmitDefaultRegions: %t (china-mode %s)\n", s.OmitDefaultRegions, map[bool]string{true: "on", false: "off"}[s.OmitDefaultRegions])
	if len(s.Regions) == 0 {
		fmt.Fprintln(stdout, "regions: none")
	}
	for _, r := range s.Regions {
		fmt.Fprintf(stdout, "region %d %s (%s): %s derp=%d stun=%d\n", r.ID, r.Code, r.Name, r.HostName, r.DERPPort, r.STUNPort)
	}
	return nil
}
