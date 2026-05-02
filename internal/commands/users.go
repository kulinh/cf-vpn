package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

// MaxUsers is the maximum number of concurrent VPN users.
const MaxUsers = 5

// xrayServiceUnit is the systemd unit restarted after user mutations.
const xrayServiceUnit = "cfvpn-xray.service"

// Package-level path vars that mirror the constants in internal/paths.
// Tests override these to redirect to a temp directory.
var (
	xrayConfigPath     = paths.XrayConfigFile
	hy2ConfigPath      = "/etc/cfvpn/hysteria/config.yaml"
	subscriptionDir    = paths.SubscriptionDir
)

// UserInputs holds inputs for user lifecycle commands.
type UserInputs struct {
	Name   string
	Domain string // for gen-sub
}

// ValidateAddUserInput is a thin wrapper around xray.ValidateUserName.
func ValidateAddUserInput(name string) error {
	return xray.ValidateUserName(name)
}

// generateUUIDv4 returns a canonical RFC 4122 v4 UUID using crypto/rand.
func generateUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Version 4 (random) and variant RFC 4122 (10xxxxxx).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// generatePassword returns a base64.RawURLEncoding-encoded password.
func generatePassword(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// writeSubscriptionFile atomically writes the subscription content for a user.
func writeSubscriptionFile(name, content string) error {
	if err := os.MkdirAll(subscriptionDir, 0o700); err != nil {
		return err
	}
	final := filepath.Join(subscriptionDir, name+".txt")
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// buildSubscriptionFor constructs the subscription string for a user, branching
// on the node's mode.
func buildSubscriptionFor(name, uuid, domain string, env map[string]string) string {
	var uri string
	if env["MODE"] == "direct" && env[state.KeyRealityPub] != "" && env[state.KeyRealityShortID] != "" {
		uri = subscription.BuildVLESSRealityURI(
			name, uuid, domain,
			env[state.KeyRealitySNI],
			env[state.KeyRealityPub],
			env[state.KeyRealityShortID],
		)
	} else {
		uri = subscription.BuildVLESSHTTPUpgradeURI(name, uuid, domain, templates.VLESSPath)
	}
	return subscription.BuildSubscriptionB64(uri)
}

func resolveRunner(r systemd.Runner) systemd.Runner {
	if r == nil {
		return systemd.ExecRunner{}
	}
	return r
}

// RunAddUser creates a new user, persists config, writes the subscription file,
// restarts xray, and prints the subscription on stdout.
func RunAddUser(ctx context.Context, in UserInputs, runner systemd.Runner, stdout, stderr io.Writer) error {
	if err := ValidateAddUserInput(in.Name); err != nil {
		return err
	}
	if in.Domain == "" {
		return fmt.Errorf("DOMAIN is required to issue subscription")
	}

	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("load xray config: %w", err)
	}
	if xray.CountUsers(cfg) >= MaxUsers {
		return fmt.Errorf("user limit reached (max %d)", MaxUsers)
	}
	for _, n := range xray.ListUserNames(cfg) {
		if n == in.Name {
			return fmt.Errorf("user %q already exists", in.Name)
		}
	}

	uuid, err := generateUUIDv4()
	if err != nil {
		return fmt.Errorf("generate uuid: %w", err)
	}

	env, envErr := state.Load(envFilePath)
	if envErr != nil {
		env = map[string]string{}
	}
	flow := ""
	if env["MODE"] == "direct" && env[state.KeyRealityPriv] != "" {
		flow = "xtls-rprx-vision"
	}

	if err := xray.AddUser(&cfg, in.Name, uuid, flow); err != nil {
		return err
	}
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		return fmt.Errorf("save xray config: %w", err)
	}

	sub := buildSubscriptionFor(in.Name, uuid, in.Domain, env)
	if err := writeSubscriptionFile(in.Name, sub+"\n"); err != nil {
		return fmt.Errorf("write subscription: %w", err)
	}

	if err := systemd.Restart(ctx, resolveRunner(runner), xrayServiceUnit); err != nil {
		return fmt.Errorf("restart %s: %w", xrayServiceUnit, err)
	}

	fmt.Fprintln(stdout, sub)
	return nil
}

// RunRemoveUser removes a user from xray config, deletes its subscription
// file (if any), and restarts xray.
func RunRemoveUser(ctx context.Context, in UserInputs, runner systemd.Runner, stdout, stderr io.Writer) error {
	if err := ValidateAddUserInput(in.Name); err != nil {
		return err
	}

	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("load xray config: %w", err)
	}
	if err := xray.RemoveUser(&cfg, in.Name); err != nil {
		return err
	}
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		return fmt.Errorf("save xray config: %w", err)
	}

	subFile := filepath.Join(subscriptionDir, in.Name+".txt")
	if err := os.Remove(subFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove subscription: %w", err)
	}

	if err := systemd.Restart(ctx, resolveRunner(runner), xrayServiceUnit); err != nil {
		return fmt.Errorf("restart %s: %w", xrayServiceUnit, err)
	}

	// Mirror the agent's applyUsers: remove from Hysteria2 config too.
	hy2Users, err := hysteria.ListUsers(hy2ConfigPath)
	if err != nil {
		return fmt.Errorf("load hysteria config: %w", err)
	}
	filtered := hy2Users[:0]
	for _, u := range hy2Users {
		if u.Name == in.Name {
			continue
		}
		filtered = append(filtered, u)
	}
	if len(filtered) < len(hy2Users) {
		if err := hysteria.SetUsers(hy2ConfigPath, filtered); err != nil {
			return fmt.Errorf("remove hysteria user: %w", err)
		}
		if err := hysteria.ReloadService(ctx, resolveRunner(runner)); err != nil {
			return fmt.Errorf("reload hysteria: %w", err)
		}
	}

	return nil
}

// RunGenSub prints the subscription for a single user (when in.Name is set)
// or for all users (when in.Name is empty), separated by blank lines.
// It does not restart any services.
func RunGenSub(ctx context.Context, in UserInputs, stdout, stderr io.Writer) error {
	if in.Domain == "" {
		return fmt.Errorf("DOMAIN is required to issue subscription")
	}
	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("load xray config: %w", err)
	}

	env, envErr := state.Load(envFilePath)
	if envErr != nil {
		env = map[string]string{}
	}

	buildURI := func(name, uuid string) string {
		if env["MODE"] == "direct" && env[state.KeyRealityPub] != "" && env[state.KeyRealityShortID] != "" {
			return subscription.BuildVLESSRealityURI(
				name, uuid, in.Domain,
				env[state.KeyRealitySNI],
				env[state.KeyRealityPub],
				env[state.KeyRealityShortID],
			)
		}
		return subscription.BuildVLESSHTTPUpgradeURI(name, uuid, in.Domain, templates.VLESSPath)
	}

	if in.Name != "" {
		uuid, ok := xray.GetVLESSClient(cfg, in.Name)
		if !ok {
			return fmt.Errorf("user %q not found", in.Name)
		}
		fmt.Fprintln(stdout, buildURI(in.Name, uuid))
		return nil
	}

	names := xray.ListUserNames(cfg)
	for i, name := range names {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			continue
		}
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "# %s\n", name)
		fmt.Fprintln(stdout, buildURI(name, uuid))
	}
	return nil
}
