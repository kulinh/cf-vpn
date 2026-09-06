package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kulinh/cf-vpn/internal/fsutil"
	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
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

// GenerateUUIDv4 returns a canonical RFC 4122 v4 UUID using crypto/rand.
func GenerateUUIDv4() (string, error) {
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

// GeneratePassword returns a base64.RawURLEncoding-encoded password.
func GeneratePassword(nBytes int) (string, error) {
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
	return fsutil.WriteFile(filepath.Join(subscriptionDir, name+".txt"), []byte(content), 0o600)
}

// buildSubscriptionFor constructs the base64 subscription payload for a user:
// the VLESS line for the node's mode plus the HY2 line when the node and the
// user both have Hysteria2 credentials.
func buildSubscriptionFor(name, uuid, domain, hy2PW string, env map[string]string, warn io.Writer) string {
	return subscription.BuildSubscriptionB64(buildUserURIs(name, uuid, domain, hy2PW, env, warn)...)
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

	// Serialize the whole load→mutate→save→restart against the agent and any
	// concurrent CLI invocation so the user-count check and the write can't race.
	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()

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

	uuid, err := GenerateUUIDv4()
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

	// M-G5: mirror RunRemoveUser and add the user to Hysteria2 as well.
	// Without this the CLI created users that existed in xray but not in
	// hysteria, and the agent's next currentSyncUsers() minted a Hy2 password
	// that does not match the one the panel/D1 holds — the exact drift the
	// previous audit round could only warn about.
	//
	// Hysteria goes FIRST on purpose. If it fails after the xray config were
	// already written, the node would be left with exactly that drift (an
	// xray-only user); in the other order a failure leaves an unused hysteria
	// entry, which the next sync simply overwrites.
	hy2PW, err := addHysteriaUser(ctx, in.Name, resolveRunner(runner), stderr)
	if err != nil {
		return err
	}

	if err := xray.AddUser(&cfg, in.Name, uuid, flow); err != nil {
		return err
	}
	rendered, err := xray.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("render xray config: %w", err)
	}
	// Validate, publish, restart, and restore the previous config if the
	// restart fails — the same cycle every other xray writer performs.
	if err := applyXrayConfig(ctx, rendered, resolveRunner(runner), stderr); err != nil {
		return err
	}

	sub := buildSubscriptionFor(in.Name, uuid, in.Domain, hy2PW, env, stderr)
	if err := writeSubscriptionFile(in.Name, sub+"\n"); err != nil {
		return fmt.Errorf("write subscription: %w", err)
	}

	fmt.Fprintln(stdout, sub)
	return nil
}

// addHysteriaUser adds name to the node's hysteria config with a fresh
// password and restarts hysteria, returning the password so it can go into the
// subscription. A node with no hysteria config at all (nothing to join) is not
// an error: the caller just gets an empty password and no HY2 line.
func addHysteriaUser(ctx context.Context, name string, runner systemd.Runner, stderr io.Writer) (string, error) {
	users, err := hysteria.ListUsers(hysteriaConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			warnf(stderr, "warning: no hysteria config at %s; user %q gets no Hy2 credential", hysteriaConfigPath, name)
			return "", nil
		}
		return "", fmt.Errorf("load hysteria config: %w", err)
	}
	for _, u := range users {
		if u.Name == name {
			// Already present (e.g. a retry): keep the existing password so the
			// user's client keeps working.
			return u.Password, nil
		}
	}
	pw, err := GeneratePassword(24)
	if err != nil {
		return "", fmt.Errorf("generate hy2 password: %w", err)
	}
	if err := hysteria.SetUsers(hysteriaConfigPath, append(users, hysteria.User{Name: name, Password: pw})); err != nil {
		return "", fmt.Errorf("add hysteria user: %w", err)
	}
	if err := hysteria.ReloadService(ctx, runner); err != nil {
		return "", fmt.Errorf("reload hysteria: %w", err)
	}
	return pw, nil
}

// RunRemoveUser removes a user from xray config, deletes its subscription
// file (if any), and restarts xray.
func RunRemoveUser(ctx context.Context, in UserInputs, runner systemd.Runner, stdout, stderr io.Writer) error {
	if err := ValidateAddUserInput(in.Name); err != nil {
		return err
	}

	unlock, err := AcquireConfigLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire config lock: %w", err)
	}
	defer unlock()

	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("load xray config: %w", err)
	}
	if err := xray.RemoveUser(&cfg, in.Name); err != nil {
		return err
	}
	rendered, err := xray.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("render xray config: %w", err)
	}
	// Validate, publish, restart, restore-on-failed-restart. Revoking access is
	// the point of this command, so xray goes first here: a later hysteria
	// failure leaves the user unable to reach VLESS, not still on it.
	if err := applyXrayConfig(ctx, rendered, resolveRunner(runner), stderr); err != nil {
		return err
	}

	subFile := filepath.Join(subscriptionDir, in.Name+".txt")
	if err := os.Remove(subFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove subscription: %w", err)
	}

	// Mirror the agent's applyUsers: remove from Hysteria2 config too.
	hy2Users, err := hysteria.ListUsers(hysteriaConfigPath)
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
		if err := hysteria.SetUsers(hysteriaConfigPath, filtered); err != nil {
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

	hy2PW, hy2Err := hy2PasswordsByName()
	if hy2Err != nil {
		warnf(stderr, "warning: cannot read hysteria config (%v); no HY2 lines will be emitted", hy2Err)
	}

	printURIs := func(name, uuid string) {
		for _, uri := range buildUserURIs(name, uuid, in.Domain, hy2PW[name], env, stderr) {
			fmt.Fprintln(stdout, uri)
		}
	}

	if in.Name != "" {
		uuid, ok := xray.GetVLESSClient(cfg, in.Name)
		if !ok {
			return fmt.Errorf("user %q not found", in.Name)
		}
		printURIs(in.Name, uuid)
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
		printURIs(name, uuid)
	}
	return nil
}
