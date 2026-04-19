package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/xray"
)

// MaxUsers is the maximum number of concurrent VPN users.
const MaxUsers = 5

// xrayServiceUnit is the systemd unit restarted after user mutations.
const xrayServiceUnit = "cfvpn-xray.service"

// Package-level path vars that mirror the constants in internal/paths.
// Tests override these to redirect to a temp directory.
var (
	xrayConfigPath  = paths.XrayConfigFile
	subscriptionDir = paths.SubscriptionDir
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
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// buildSubscriptionFor constructs the base64 subscription string for a user.
func buildSubscriptionFor(name, uuid, password, domain string) string {
	v := subscription.BuildVLESSURI(name, uuid, domain)
	t := subscription.BuildTrojanURI(name, password, domain)
	return subscription.BuildSubscriptionB64(v, t)
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
	password, err := generatePassword(24)
	if err != nil {
		return fmt.Errorf("generate password: %w", err)
	}

	if err := xray.AddUser(&cfg, in.Name, uuid, password); err != nil {
		return err
	}
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		return fmt.Errorf("save xray config: %w", err)
	}

	sub := buildSubscriptionFor(in.Name, uuid, password, in.Domain)
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

	if in.Name != "" {
		uuid, ok := xray.GetVLESSClient(cfg, in.Name)
		if !ok {
			return fmt.Errorf("user %q not found", in.Name)
		}
		password, ok := xray.GetTrojanClient(cfg, in.Name)
		if !ok {
			return fmt.Errorf("user %q has no trojan client", in.Name)
		}
		fmt.Fprintln(stdout, buildSubscriptionFor(in.Name, uuid, password, in.Domain))
		return nil
	}

	names := xray.ListUserNames(cfg)
	for i, name := range names {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			continue
		}
		password, ok := xray.GetTrojanClient(cfg, name)
		if !ok {
			continue
		}
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "# %s\n", name)
		fmt.Fprintln(stdout, buildSubscriptionFor(name, uuid, password, in.Domain))
	}
	return nil
}
