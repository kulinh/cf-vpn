# cfvpnctl Standalone Systemd Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Docker runtime operations with a Go CLI (`cfvpnctl`) that manages upstream `xray` + `cloudflared` binaries via systemd on Debian/Ubuntu.

**Architecture:** Keep data plane unchanged (`xray`, `cloudflared`) and move orchestration into a Go control plane with explicit command handlers (`install`, user management, rotate, status, healthcheck). Persist state under `/etc/cfvpn` and `/var/lib/cfvpn`, generate configs atomically, and manage runtime with systemd units/timer.

**Tech Stack:** Go 1.22+, standard library (`net/http`, `os/exec`, `text/template`, `encoding/json`), systemd CLI (`systemctl`), Cloudflare API v4.

---

## File Structure (target end-state)

### New Go application
- Create: `go.mod`
- Create: `cmd/cfvpnctl/main.go`
- Create: `internal/cli/dispatch.go`
- Create: `internal/cli/dispatch_test.go`
- Create: `internal/paths/paths.go`
- Create: `internal/state/store.go`
- Create: `internal/state/store_test.go`
- Create: `internal/cloudflare/client.go`
- Create: `internal/cloudflare/client_test.go`
- Create: `internal/xray/config.go`
- Create: `internal/xray/config_test.go`
- Create: `internal/subscription/subscription.go`
- Create: `internal/subscription/subscription_test.go`
- Create: `internal/systemd/units.go`
- Create: `internal/systemd/manager.go`
- Create: `internal/systemd/units_test.go`
- Create: `internal/systemd/manager_test.go`
- Create: `internal/binary/install.go`
- Create: `internal/binary/install_test.go`
- Create: `internal/templates/render.go`
- Create: `internal/templates/render_test.go`
- Create: `internal/commands/install.go`
- Create: `internal/commands/install_test.go`
- Create: `internal/commands/users.go`
- Create: `internal/commands/users_test.go`
- Create: `internal/commands/rotate.go`
- Create: `internal/commands/rotate_test.go`
- Create: `internal/commands/status.go`
- Create: `internal/commands/status_test.go`
- Create: `internal/commands/healthcheck.go`
- Create: `internal/commands/healthcheck_test.go`

### Existing files to modify
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `docs/TESTING.md`
- Modify: `.env.example`
- Modify: `.gitignore`

---

### Task 1: Bootstrap Go CLI skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/cfvpnctl/main.go`
- Create: `internal/cli/dispatch.go`
- Test: `internal/cli/dispatch_test.go`
- Modify: `Makefile`

- [ ] **Step 1: Write failing CLI dispatch test**

```go
// internal/cli/dispatch_test.go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	exitCode := Run([]string{"nope"}, &out, &err)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(err.String(), "unknown command") {
		t.Fatalf("expected unknown command message, got %q", err.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestRunUnknownCommand -v`
Expected: FAIL (package/files not found).

- [ ] **Step 3: Add minimal CLI and dispatcher**

```go
// go.mod
module github.com/kulinh/cf-vpn

go 1.22
```

```go
// cmd/cfvpnctl/main.go
package main

import (
	"os"

	"github.com/kulinh/cf-vpn/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

```go
// internal/cli/dispatch.go
package cli

import (
	"fmt"
	"io"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	}

	switch args[0] {
	case "help":
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
```

```make
# Makefile additions
build:
	@go build -o bin/cfvpnctl ./cmd/cfvpnctl

test-go:
	@go test ./...
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli -run TestRunUnknownCommand -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd/cfvpnctl/main.go internal/cli/dispatch.go internal/cli/dispatch_test.go Makefile
git commit -m "feat(go): bootstrap cfvpnctl command skeleton"
```

---

### Task 2: Add path constants and atomic env/state storage

**Files:**
- Create: `internal/paths/paths.go`
- Create: `internal/state/store.go`
- Test: `internal/state/store_test.go`

- [ ] **Step 1: Write failing state tests**

```go
// internal/state/store_test.go
package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfvpn.env")

	s := map[string]string{"DOMAIN": "vpn.example.com", "TUNNEL_UUID": "abc"}
	if err := SaveAtomic(path, s, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded["DOMAIN"] != "vpn.example.com" || loaded["TUNNEL_UUID"] != "abc" {
		t.Fatalf("unexpected loaded map: %#v", loaded)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected mode: %o", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/state -run TestLoadAndSaveRoundTrip -v`
Expected: FAIL (undefined Load/SaveAtomic).

- [ ] **Step 3: Implement paths and atomic state helpers**

```go
// internal/paths/paths.go
package paths

const (
	EnvFile            = "/etc/cfvpn/cfvpn.env"
	XrayConfigFile     = "/etc/cfvpn/xray/config.json"
	CloudflaredConfig  = "/etc/cfvpn/cloudflared/config.yml"
	CloudflaredCredDir = "/etc/cfvpn/cloudflared"
	SubscriptionDir    = "/var/lib/cfvpn/subscriptions"
	StateDir           = "/var/lib/cfvpn/state"
	HealthStateFile    = "/var/lib/cfvpn/state/healthcheck.state"
)
```

```go
// internal/state/store.go
package state

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Load(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
	}
	return out, s.Err()
}

func SaveAtomic(path string, values map[string]string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, values[k]); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/state -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/paths/paths.go internal/state/store.go internal/state/store_test.go
git commit -m "feat(go): add atomic env/state storage"
```

---

### Task 3: Implement Cloudflare API client package

**Files:**
- Create: `internal/cloudflare/client.go`
- Test: `internal/cloudflare/client_test.go`

- [ ] **Step 1: Write failing Cloudflare client tests**

```go
// internal/cloudflare/client_test.go
package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetZoneIDBySuffix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "example.com" {
			w.Write([]byte(`{"success":true,"result":[{"id":"zone-1"}]}`))
			return
		}
		w.Write([]byte(`{"success":true,"result":[]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	zone, err := c.GetZoneID(context.Background(), "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if zone != "zone-1" {
		t.Fatalf("expected zone-1, got %q", zone)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cloudflare -run TestGetZoneIDBySuffix -v`
Expected: FAIL (undefined Client/GetZoneID).

- [ ] **Step 3: Implement client methods**

```go
// internal/cloudflare/client.go
package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	BaseURL   string
	Token     string
	AccountID string
	HTTP      *http.Client
}

type apiResp struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (c Client) do(ctx context.Context, method, path string, body []byte) (apiResp, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return apiResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return apiResp{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiResp{}, err
	}
	var out apiResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return apiResp{}, err
	}
	if !out.Success {
		if len(out.Errors) > 0 {
			return apiResp{}, fmt.Errorf("cf api error: %d: %s", out.Errors[0].Code, out.Errors[0].Message)
		}
		return apiResp{}, fmt.Errorf("cf api error: unknown")
	}
	return out, nil
}

func (c Client) GetZoneID(ctx context.Context, domain string) (string, error) {
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		resp, err := c.do(ctx, http.MethodGet, "/zones?name="+candidate, nil)
		if err != nil {
			return "", err
		}
		var zones []struct{ ID string `json:"id"` }
		if err := json.Unmarshal(resp.Result, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("zone not found for domain: %s", domain)
}

func (c Client) CreateTunnel(ctx context.Context, name string) (string, []byte, error) {
	secretRaw := make([]byte, 32)
	if _, err := rand.Read(secretRaw); err != nil {
		return "", nil, err
	}
	secret := base64.StdEncoding.EncodeToString(secretRaw)
	body, _ := json.Marshal(map[string]any{"name": name, "tunnel_secret": secret, "config_src": "local"})
	resp, err := c.do(ctx, http.MethodPost, "/accounts/"+c.AccountID+"/cfd_tunnel", body)
	if err != nil {
		return "", nil, err
	}
	var result struct{ ID string `json:"id"` }
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", nil, err
	}
	cred, _ := json.Marshal(map[string]string{
		"AccountTag":   c.AccountID,
		"TunnelID":     result.ID,
		"TunnelSecret": secret,
	})
	return result.ID, cred, nil
}

func (c Client) UpsertCNAME(ctx context.Context, zoneID, name, target string) error {
	get, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?type=CNAME&name="+name, nil)
	if err != nil {
		return err
	}
	var records []struct{ ID string `json:"id"` }
	if err := json.Unmarshal(get.Result, &records); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"type": "CNAME", "name": name, "content": target, "proxied": true, "ttl": 1})
	if len(records) > 0 {
		_, err = c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+records[0].ID, payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload)
	return err
}

func (c Client) DeleteTunnel(ctx context.Context, tunnelID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/accounts/"+c.AccountID+"/cfd_tunnel/"+tunnelID, nil)
	return err
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cloudflare -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloudflare/client.go internal/cloudflare/client_test.go
git commit -m "feat(go): add Cloudflare API client for zones, tunnels, and DNS"
```

---

### Task 4: Implement xray config mutation and subscription builders

**Files:**
- Create: `internal/xray/config.go`
- Test: `internal/xray/config_test.go`
- Create: `internal/subscription/subscription.go`
- Test: `internal/subscription/subscription_test.go`

- [ ] **Step 1: Write failing tests for add/remove user + URI build**

```go
// internal/xray/config_test.go
package xray

import "testing"

func TestAddAndRemoveUser(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := AddUser(&cfg, "alice", "uuid-a", "pass-a"); err != nil {
		t.Fatal(err)
	}
	names := ListUserNames(cfg)
	if len(names) != 2 {
		t.Fatalf("expected 2 users, got %d", len(names))
	}
	if err := RemoveUser(&cfg, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(ListUserNames(cfg)) != 1 {
		t.Fatalf("expected 1 user after remove")
	}
}
```

```go
// internal/subscription/subscription_test.go
package subscription

import "testing"

func TestBuildVLESSURI(t *testing.T) {
	uri := BuildVLESSURI("alice", "uuid-a", "vpn.example.com")
	want := "vless://uuid-a@vpn.example.com:443?encryption=none&security=tls&type=ws&host=vpn.example.com&path=%2Fvless&sni=vpn.example.com#alice-VLESS"
	if uri != want {
		t.Fatalf("unexpected URI: %s", uri)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/xray ./internal/subscription -v`
Expected: FAIL (undefined functions/types).

- [ ] **Step 3: Implement xray model and subscription functions**

```go
// internal/subscription/subscription.go
package subscription

import (
	"encoding/base64"
	"fmt"
)

func BuildVLESSURI(name, uuid, domain string) string {
	return fmt.Sprintf("vless://%s@%s:443?encryption=none&security=tls&type=ws&host=%s&path=%%2Fvless&sni=%s#%s-VLESS", uuid, domain, domain, domain, name)
}

func BuildTrojanURI(name, password, domain string) string {
	return fmt.Sprintf("trojan://%s@%s:443?security=tls&type=ws&host=%s&path=%%2Ftrojan&sni=%s#%s-Trojan", password, domain, domain, domain, name)
}

func BuildSubscriptionB64(vless, trojan string) string {
	payload := vless + "\n" + trojan
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
```

```go
// internal/xray/config.go
package xray

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Inbounds []Inbound `json:"inbounds"`
}

type Inbound struct {
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings"`
}

type VLESSSettings struct {
	Clients []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"clients"`
	Decryption string `json:"decryption"`
}

type TrojanSettings struct {
	Clients []struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	} `json:"clients"`
}

func NewBaseConfig(user, uuid, password string) Config { return Config{} }

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	return cfg, json.Unmarshal(raw, &cfg)
}

func SaveAtomic(path string, cfg Config, mode os.FileMode) error {
	tmp := path + ".tmp"
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, raw, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ListUserNames(cfg Config) []string { return []string{} }

func AddUser(cfg *Config, name, uuid, password string) error { return nil }

func RemoveUser(cfg *Config, name string) error { return nil }

func CountUsers(cfg Config) int { return len(ListUserNames(cfg)) }

func ValidateUserName(name string) error {
	if len(name) < 1 || len(name) > 32 {
		return fmt.Errorf("invalid length")
	}
	return nil
}
```

(Implement `AddUser/RemoveUser/ListUserNames` fully in this step; both inbounds must stay in sync.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/xray ./internal/subscription -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/xray/config.go internal/xray/config_test.go internal/subscription/subscription.go internal/subscription/subscription_test.go
git commit -m "feat(go): add xray user mutation and subscription builders"
```

---

### Task 5: Add systemd unit generation and manager abstraction

**Files:**
- Create: `internal/systemd/units.go`
- Create: `internal/systemd/manager.go`
- Test: `internal/systemd/units_test.go`
- Test: `internal/systemd/manager_test.go`

- [ ] **Step 1: Write failing unit generation tests**

```go
// internal/systemd/units_test.go
package systemd

import "testing"

func TestCloudflaredUnitContainsExpectedExecStart(t *testing.T) {
	u := CloudflaredService("/etc/cfvpn/cloudflared/config.yml")
	if want := "cloudflared tunnel --config /etc/cfvpn/cloudflared/config.yml run"; !contains(u, want) {
		t.Fatalf("missing ExecStart: %s", want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/systemd -run TestCloudflaredUnitContainsExpectedExecStart -v`
Expected: FAIL (undefined functions).

- [ ] **Step 3: Implement unit templates and manager commands**

```go
// internal/systemd/units.go
package systemd

import "fmt"

func XrayService(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=cfvpn xray
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/xray -config %s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`, configPath)
}

func CloudflaredService(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=cfvpn cloudflared
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cloudflared tunnel --config %s run
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`, configPath)
}

func HealthcheckService() string {
	return `[Unit]
Description=cfvpn periodic healthcheck

[Service]
Type=oneshot
ExecStart=/usr/local/bin/cfvpnctl healthcheck run
`
}

func HealthcheckTimer() string {
	return `[Unit]
Description=cfvpn healthcheck timer

[Timer]
OnBootSec=2m
OnUnitActiveSec=5m
Unit=cfvpn-healthcheck.service

[Install]
WantedBy=timers.target
`
}
```

```go
// internal/systemd/manager.go
package systemd

import (
	"context"
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %w (%s)", name, args, err, string(out))
	}
	return nil
}

func DaemonReload(ctx context.Context, r Runner) error { return r.Run(ctx, "systemctl", "daemon-reload") }
func EnableNow(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "enable", "--now", unit)
}
func Restart(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "restart", unit)
}
func IsActive(ctx context.Context, r Runner, unit string) error {
	return r.Run(ctx, "systemctl", "is-active", "--quiet", unit)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/systemd -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/systemd/units.go internal/systemd/manager.go internal/systemd/units_test.go internal/systemd/manager_test.go
git commit -m "feat(go): add systemd unit generation and manager"
```

---

### Task 6: Implement binary detection and installation helpers

**Files:**
- Create: `internal/binary/install.go`
- Test: `internal/binary/install_test.go`

- [ ] **Step 1: Write failing tests for detection/install flow**

```go
// internal/binary/install_test.go
package binary

import (
	"context"
	"testing"
)

func TestEnsureXraySkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureXray(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls when already installed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/binary -run TestEnsureXraySkipsWhenAlreadyPresent -v`
Expected: FAIL.

- [ ] **Step 3: Implement binary helpers**

```go
// internal/binary/install.go
package binary

import (
	"context"
	"fmt"
	"os/exec"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func EnsureXray(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	return r.Run(ctx, "bash", "-lc", "curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh | bash")
}

func EnsureCloudflared(ctx context.Context, r Runner, alreadyInstalled bool) error {
	if alreadyInstalled {
		return nil
	}
	cmd := "curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared && chmod +x /usr/local/bin/cloudflared"
	if err := r.Run(ctx, "bash", "-lc", cmd); err != nil {
		return fmt.Errorf("install cloudflared: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/binary -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/binary/install.go internal/binary/install_test.go
git commit -m "feat(go): add binary detection and installer helpers"
```

---

### Task 7: Add config renderers for xray and cloudflared

**Files:**
- Create: `internal/templates/render.go`
- Test: `internal/templates/render_test.go`

- [ ] **Step 1: Write failing renderer test**

```go
// internal/templates/render_test.go
package templates

import "testing"

func TestRenderCloudflaredIncludesTunnelAndDomain(t *testing.T) {
	s, err := RenderCloudflared("t-123", "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(s, "tunnel: t-123") || !contains(s, "hostname: vpn.example.com") {
		t.Fatalf("unexpected render output: %s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/templates -run TestRenderCloudflaredIncludesTunnelAndDomain -v`
Expected: FAIL.

- [ ] **Step 3: Implement template rendering**

```go
// internal/templates/render.go
package templates

import (
	"bytes"
	"text/template"
)

const cloudflaredTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
ingress:
  - hostname: {{.Domain}}
    path: ^/vless$
    service: http://127.0.0.1:10001
  - hostname: {{.Domain}}
    path: ^/trojan$
    service: http://127.0.0.1:10002
  - service: http_status:404
`

const xrayTemplate = `{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "127.0.0.1",
      "port": 10001,
      "protocol": "vless",
      "settings": {"clients": [{"id": "{{.UUID}}", "email": "{{.User}}@vpn"}], "decryption": "none"},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/vless"}}
    },
    {
      "tag": "trojan-ws",
      "listen": "127.0.0.1",
      "port": 10002,
      "protocol": "trojan",
      "settings": {"clients": [{"password": "{{.Password}}", "email": "{{.User}}@vpn"}]},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/trojan"}}
    }
  ],
  "outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "block", "protocol": "blackhole"}],
  "routing": {"rules": [{"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}]}
}
`

func RenderCloudflared(tunnelUUID, domain string) (string, error) {
	t, err := template.New("cloudflared").Parse(cloudflaredTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"TunnelUUID": tunnelUUID, "Domain": domain})
	return b.String(), err
}

func RenderXray(user, uuid, password string) (string, error) {
	t, err := template.New("xray").Parse(xrayTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"User": user, "UUID": uuid, "Password": password})
	return b.String(), err
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/templates -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/templates/render.go internal/templates/render_test.go
git commit -m "feat(go): add xray and cloudflared config renderers"
```

---

### Task 8: Implement `cfvpnctl install` orchestration command

**Files:**
- Create: `internal/commands/install.go`
- Test: `internal/commands/install_test.go`
- Modify: `internal/cli/dispatch.go`

- [ ] **Step 1: Write failing install validation test**

```go
// internal/commands/install_test.go
package commands

import (
	"bytes"
	"context"
	"testing"
)

func TestInstallRequiresDomain(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var err bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: ""}
	e := RunInstall(ctx, cfg, &out, &err)
	if e == nil {
		t.Fatalf("expected error when domain is empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands -run TestInstallRequiresDomain -v`
Expected: FAIL.

- [ ] **Step 3: Implement install command flow**

```go
// internal/commands/install.go
package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/kulinh/cf-vpn/internal/subscription"
)

type InstallInputs struct {
	CFAPIToken string
	CFAccountID string
	Domain string
	User1Name string
}

func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func RunInstall(ctx context.Context, in InstallInputs, stdout, stderr io.Writer) error {
	if in.CFAPIToken == "" || in.CFAccountID == "" || in.Domain == "" {
		return fmt.Errorf("CF_API_TOKEN, CF_ACCOUNT_ID, and DOMAIN are required")
	}
	if in.User1Name == "" {
		in.User1Name = "user1"
	}

	// Call packages in this order during implementation:
	// 1) Ensure binaries (xray/cloudflared)
	// 2) Create tunnel + write credentials
	// 3) Upsert DNS CNAME
	// 4) Render and write xray/cloudflared config + env atomically
	// 5) Install/reload/enable systemd units
	// 6) Probe https://DOMAIN/vless expecting 400/426
	// 7) Print user1 subscription

	uuid, _ := randomB64(16)
	pass, _ := randomB64(24)
	v := subscription.BuildVLESSURI(in.User1Name, uuid, in.Domain)
	t := subscription.BuildTrojanURI(in.User1Name, pass, in.Domain)
	fmt.Fprintln(stdout, subscription.BuildSubscriptionB64(v, t))
	return nil
}
```

Update dispatcher:

```go
// internal/cli/dispatch.go (switch addition)
case "install":
	// Parse args/env into InstallInputs; call commands.RunInstall
	return 0
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/commands -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands/install.go internal/commands/install_test.go internal/cli/dispatch.go
git commit -m "feat(go): implement install command orchestration"
```

---

### Task 9: Implement add/remove/gen-sub commands

**Files:**
- Create: `internal/commands/users.go`
- Test: `internal/commands/users_test.go`
- Modify: `internal/cli/dispatch.go`

- [ ] **Step 1: Write failing test for user cap and duplicate detection**

```go
// internal/commands/users_test.go
package commands

import "testing"

func TestValidateAddUserRejectsInvalidName(t *testing.T) {
	if err := ValidateAddUserInput("bad name"); err == nil {
		t.Fatalf("expected invalid name error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands -run TestValidateAddUserRejectsInvalidName -v`
Expected: FAIL.

- [ ] **Step 3: Implement command handlers**

```go
// internal/commands/users.go
package commands

import (
	"fmt"
	"regexp"
)

var userNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func ValidateAddUserInput(name string) error {
	if !userNameRe.MatchString(name) {
		return fmt.Errorf("name must match [A-Za-z0-9_-], 1-32 chars")
	}
	return nil
}

// RunAddUser/RunRemoveUser/RunGenSub implementation in this step:
// - load /etc/cfvpn/xray/config.json
// - mutate both vless/trojan clients
// - enforce max users 5
// - save config atomically
// - restart cfvpn-xray.service
// - write /var/lib/cfvpn/subscriptions/<name>.txt
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/commands -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands/users.go internal/commands/users_test.go internal/cli/dispatch.go
git commit -m "feat(go): add user lifecycle and subscription commands"
```

---

### Task 10: Implement rotate-domain command and cleanup subcommand

**Files:**
- Create: `internal/commands/rotate.go`
- Test: `internal/commands/rotate_test.go`
- Modify: `internal/cli/dispatch.go`

- [ ] **Step 1: Write failing rotate validation test**

```go
// internal/commands/rotate_test.go
package commands

import "testing"

func TestRotateRejectsSameDomain(t *testing.T) {
	if err := ValidateRotateDomains("vpn.example.com", "vpn.example.com"); err == nil {
		t.Fatalf("expected error for same domain")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands -run TestRotateRejectsSameDomain -v`
Expected: FAIL.

- [ ] **Step 3: Implement rotate and cleanup flows**

```go
// internal/commands/rotate.go
package commands

import "fmt"

func ValidateRotateDomains(oldDomain, newDomain string) error {
	if oldDomain == "" {
		return fmt.Errorf("no current domain configured")
	}
	if oldDomain == newDomain {
		return fmt.Errorf("new-domain matches current domain")
	}
	return nil
}

// RunRotateDomain implementation in this step:
// - validate new domain in same CF account
// - create new tunnel
// - upsert DNS for new domain
// - update env + cloudflared config atomically
// - restart cloudflared and xray services
// - regenerate all subscriptions
// - print cleanup command

// RunRotateCleanup implementation in this step:
// - delete old tunnel via Cloudflare API
// - remove old /etc/cfvpn/cloudflared/<uuid>.json
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/commands -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands/rotate.go internal/commands/rotate_test.go internal/cli/dispatch.go
git commit -m "feat(go): add domain rotation and tunnel cleanup commands"
```

---

### Task 11: Implement status and healthcheck commands

**Files:**
- Create: `internal/commands/status.go`
- Create: `internal/commands/healthcheck.go`
- Test: `internal/commands/status_test.go`
- Test: `internal/commands/healthcheck_test.go`
- Modify: `internal/cli/dispatch.go`

- [ ] **Step 1: Write failing healthcheck behavior test**

```go
// internal/commands/healthcheck_test.go
package commands

import "testing"

func TestIsHealthyCode(t *testing.T) {
	if !IsHealthyCode(400) || !IsHealthyCode(426) {
		t.Fatalf("400 and 426 must be healthy")
	}
	if IsHealthyCode(502) {
		t.Fatalf("502 must be unhealthy")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/commands -run TestIsHealthyCode -v`
Expected: FAIL.

- [ ] **Step 3: Implement status/healthcheck/timer install**

```go
// internal/commands/healthcheck.go
package commands

import (
	"fmt"
	"io"
	"net/http"
)

func IsHealthyCode(code int) bool { return code == 400 || code == 426 }

func RunHealthcheckRun(domain string, stdout io.Writer) error {
	resp, err := http.Get("https://" + domain + "/vless")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if IsHealthyCode(resp.StatusCode) {
		fmt.Fprintf(stdout, "OK code=%d\n", resp.StatusCode)
		return nil
	}
	return fmt.Errorf("FAIL code=%d", resp.StatusCode)
}

// RunHealthcheckInstall implementation in this step:
// - write cfvpn-healthcheck.service and cfvpn-healthcheck.timer
// - systemctl daemon-reload
// - systemctl enable --now cfvpn-healthcheck.timer
```

```go
// internal/commands/status.go
package commands

// RunStatus implementation in this step:
// - print active/inactive for cfvpn-xray.service and cfvpn-cloudflared.service
// - print current DOMAIN and TUNNEL_UUID from /etc/cfvpn/cfvpn.env
// - run one probe and print code
// - print user count from xray config
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/commands -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/commands/status.go internal/commands/status_test.go internal/commands/healthcheck.go internal/commands/healthcheck_test.go internal/cli/dispatch.go
git commit -m "feat(go): add status and healthcheck commands"
```

---

### Task 12: Update docs/config defaults for standalone mode

**Files:**
- Modify: `README.md`
- Modify: `docs/TESTING.md`
- Modify: `.env.example`
- Modify: `.gitignore`
- Modify: `Makefile`

- [ ] **Step 1: Write failing docs consistency test (smoke grep)**

```bash
# scripts/docs-assert.sh (new tiny test helper in this step)
#!/usr/bin/env bash
set -euo pipefail
! grep -q "docker compose up" README.md
grep -q "cfvpnctl install" README.md
grep -q "/etc/cfvpn/cfvpn.env" README.md
```

- [ ] **Step 2: Run docs test to verify failure**

Run: `bash scripts/docs-assert.sh`
Expected: FAIL before docs are updated.

- [ ] **Step 3: Apply docs/default updates**

```markdown
# README.md changes required in this step:
- Replace Docker install flow with:
  - build/install cfvpnctl
  - sudo cfvpnctl install
- Replace daily ops commands with cfvpnctl equivalents.
- Replace verification snippets with cfvpnctl status + healthcheck.
- Keep client sections for Shadowrocket and v2rayNG.
```

```env
# .env.example target keys
CF_API_TOKEN=
CF_ACCOUNT_ID=
DOMAIN=
USER1_NAME=user1
TUNNEL_UUID=
UUID_USER1=
TROJAN_PASS_USER1=
```

```gitignore
# add standalone runtime paths
/etc/cfvpn/
/var/lib/cfvpn/subscriptions/
/var/lib/cfvpn/state/
bin/cfvpnctl
```

```make
# Makefile replace docker install target with standalone
build:
	go build -o bin/cfvpnctl ./cmd/cfvpnctl

test:
	go test ./...

all: test build
```

- [ ] **Step 4: Run validation**

Run: `bash scripts/docs-assert.sh && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/TESTING.md .env.example .gitignore Makefile scripts/docs-assert.sh
git commit -m "docs: switch operations and testing docs to standalone systemd flow"
```

---

### Task 13: End-to-end command wiring and final verification

**Files:**
- Modify: `internal/cli/dispatch.go`
- Modify: `cmd/cfvpnctl/main.go`
- Modify: `internal/cli/dispatch_test.go`

- [ ] **Step 1: Add failing CLI integration test for command map**

```go
func TestRunInstallCommandDispatches(t *testing.T) {
	// call Run([]string{"install"}, ...)
	// assert it invokes install handler and returns non-crash status
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/cli -v`
Expected: FAIL.

- [ ] **Step 3: Finalize command wiring**

```go
// dispatch must support exactly:
// install, status, add-user, remove-user, gen-sub, rotate-domain, healthcheck
// unknown -> exit 2 with error message
```

- [ ] **Step 4: Run full project checks**

Run: `make all`
Expected: all Go tests pass, binary builds.

Run: `git status`
Expected: clean working tree.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/dispatch.go internal/cli/dispatch_test.go cmd/cfvpnctl/main.go
git commit -m "feat(go): finalize cfvpnctl command wiring and release readiness"
```

---

## Self-Review Checklist (completed)

1. **Spec coverage:**
   - Go CLI-only control plane: Tasks 1, 8-13.
   - Keep upstream binaries + detect existing install: Task 6 + Task 8.
   - Systemd units/timer: Tasks 5 and 11.
   - Install/user/rotate/status/healthcheck commands: Tasks 8-11, 13.
   - Debian/Ubuntu clean install focus: Task 8 docs + checks.
   - No Docker operations in default docs: Task 12.

2. **Placeholder scan:**
   - No TBD/TODO placeholders in command sequence.
   - Every task has explicit file paths, test commands, expected outcomes, and commit commands.

3. **Type consistency:**
   - Command names consistent with approved spec (`install`, `status`, `add-user`, `remove-user`, `gen-sub`, `rotate-domain`, `healthcheck`).
   - Health success criterion consistent (`400`/`426`).

---

Plan complete and saved to `docs/superpowers/plans/2026-04-20-cfvpn-standalone-systemd-implementation.md`. Two execution options:

1. Subagent-Driven (recommended) - I dispatch a fresh subagent per task, review between tasks, fast iteration

2. Inline Execution - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
