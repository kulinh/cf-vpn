package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/xray"
)

// userRestartRunner fails the systemctl invocations whose joined form contains
// `failOn`, and records everything.
type userRestartRunner struct {
	calls  [][]string
	failOn string
}

func (r *userRestartRunner) Run(_ context.Context, name string, args ...string) error {
	inv := append([]string{name}, args...)
	r.calls = append(r.calls, inv)
	if r.failOn != "" && strings.Contains(strings.Join(inv, " "), r.failOn) {
		return fmt.Errorf("stub restart failure")
	}
	return nil
}

func (r *userRestartRunner) joined() string {
	var b strings.Builder
	for _, c := range r.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func seedUsersConfig(t *testing.T, cfgPath string) {
	t.Helper()
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Item 4: the xray write must go through the validating writer, so a config
// xray refuses never reaches the live file.
func TestRunAddUserRejectsInvalidXrayConfigWithoutTouchingLiveFile(t *testing.T) {
	cfgPath, subDir := withTempPaths(t)
	seedUsersConfig(t, cfgPath)
	writeTestEnv(t, directEnv())
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	old := validateXrayConfig
	validateXrayConfig = func(context.Context, []byte) error { return fmt.Errorf("xray rejected the new config") }
	t.Cleanup(func() { validateXrayConfig = old })

	r := &userRestartRunner{}
	var out, errBuf bytes.Buffer
	err = RunAddUser(context.Background(), UserInputs{Name: "bob", Domain: "cdn-a1b2.rwl.one"}, r, &out, &errBuf)
	if err == nil {
		t.Fatal("expected the add to fail validation")
	}
	after, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatalf("live xray config was replaced by the rejected one:\n%s", after)
	}
	if strings.Contains(r.joined(), "restart cfvpn-xray.service") {
		t.Errorf("xray was restarted despite the rejected config:\n%s", r.joined())
	}
	if _, err := os.Stat(filepath.Join(subDir, "bob.txt")); !os.IsNotExist(err) {
		t.Errorf("subscription written for a user that was never added: %v", err)
	}
}

// Item 4: a failed restart must restore the previous config and restart on it.
func TestRunAddUserRestoresPreviousConfigWhenRestartFails(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	seedUsersConfig(t, cfgPath)
	writeTestEnv(t, directEnv())
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	r := &userRestartRunner{failOn: "restart cfvpn-xray.service"}
	var out, errBuf bytes.Buffer
	if err := RunAddUser(context.Background(), UserInputs{Name: "bob", Domain: "cdn-a1b2.rwl.one"}, r, &out, &errBuf); err == nil {
		t.Fatal("expected the restart failure to surface")
	}

	after, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatalf("previous xray config was not restored:\n%s", after)
	}
	// It must also try to bring xray back up on the restored config.
	if strings.Count(r.joined(), "restart cfvpn-xray.service") < 2 {
		t.Errorf("no restart attempt on the restored config:\n%s", r.joined())
	}
}

// Item 5: a hysteria failure must not leave a half-added user in xray.
func TestRunAddUserLeavesXrayUntouchedWhenHysteriaFails(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	seedUsersConfig(t, cfgPath)
	writeTestEnv(t, directEnv())
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// A hysteria config with no auth.userpass block makes SetUsers fail (H9).
	if err := os.WriteFile(hysteriaConfigPath, []byte("listen: \":24430\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &userRestartRunner{}
	var out, errBuf bytes.Buffer
	if err := RunAddUser(context.Background(), UserInputs{Name: "bob", Domain: "cdn-a1b2.rwl.one"}, r, &out, &errBuf); err == nil {
		t.Fatal("expected the hysteria failure to surface")
	}

	after, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatalf("xray config was mutated despite the hysteria failure:\n%s", after)
	}
	var cfg map[string]any
	if err := json.Unmarshal(after, &cfg); err != nil {
		t.Fatalf("live config is not valid JSON after the failure: %v", err)
	}
	loaded, _ := xray.Load(cfgPath)
	for _, n := range xray.ListUserNames(loaded) {
		if n == "bob" {
			t.Fatal("bob exists in xray but not in hysteria — the exact drift this ordering prevents")
		}
	}
}

func TestRunRemoveUserRejectsInvalidXrayConfigWithoutTouchingLiveFile(t *testing.T) {
	cfgPath, subDir := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.AddUser(&cfg, "alice", "uuid-a", ""); err != nil {
		t.Fatal(err)
	}
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	subFile := filepath.Join(subDir, "alice.txt")
	if err := os.WriteFile(subFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)

	old := validateXrayConfig
	validateXrayConfig = func(context.Context, []byte) error { return fmt.Errorf("xray rejected the new config") }
	t.Cleanup(func() { validateXrayConfig = old })

	r := &userRestartRunner{}
	var out, errBuf bytes.Buffer
	if err := RunRemoveUser(context.Background(), UserInputs{Name: "alice"}, r, &out, &errBuf); err == nil {
		t.Fatal("expected the removal to fail validation")
	}
	after, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatalf("live xray config was replaced by the rejected one:\n%s", after)
	}
	if _, err := os.Stat(subFile); err != nil {
		t.Errorf("subscription file removed even though the removal failed: %v", err)
	}
}

// The happy path still provisions both sides and prints the subscription.
func TestRunAddUserAddsToBothConfigs(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	seedUsersConfig(t, cfgPath)
	writeTestEnv(t, directEnv())

	r := &userRestartRunner{}
	var out, errBuf bytes.Buffer
	if err := RunAddUser(context.Background(), UserInputs{Name: "bob", Domain: "cdn-a1b2.rwl.one"}, r, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	loaded, _ := xray.Load(cfgPath)
	found := false
	for _, n := range xray.ListUserNames(loaded) {
		if n == "bob" {
			found = true
		}
	}
	if !found {
		t.Error("bob missing from the xray config")
	}
	hy2, err := hysteria.ListUsers(hysteriaConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	hasBob := false
	for _, u := range hy2 {
		if u.Name == "bob" {
			hasBob = u.Password != ""
		}
	}
	if !hasBob {
		t.Errorf("bob missing from the hysteria config: %#v", hy2)
	}
	if out.Len() == 0 {
		t.Error("no subscription printed")
	}
}
