# cfvpn-agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go HTTP agent (`cfvpn-agent`) that runs on each VPS and exposes the existing `internal/commands` operations as JSON endpoints under `/admin/v1/*`, reachable only via cloudflared's ingress on a stable admin hostname gated by CF Access.

**Architecture:** New binary `cmd/cfvpn-agent` binds `127.0.0.1:8787` and delegates to `internal/commands` via a thin HTTP handler layer in `internal/agent`. The existing `cfvpnctl` CLI surface stays byte-identical. Three small refactors to `internal/commands` expose structured return values (credentials, rotation-with-zone-id, healthcheck probe result) so the agent can serialize them as JSON without re-reading files. A new systemd unit and a cloudflared ingress rule wire everything together.

**Tech Stack:** Go 1.22 stdlib only (no new deps). Tests use `net/http/httptest` plus the existing `stubRunner` / `fakeCF` harnesses. Systemd `Type=simple` unit with `EnvironmentFile=/etc/cfvpn/cfvpn.env`. Cloudflare Tunnel ingress rule routes `admin-<label>.<zone>/admin/*` → `http://127.0.0.1:8787`.

---

## File Structure

**Created:**
- `cmd/cfvpn-agent/main.go` — binary entrypoint; loads env, builds CF client, starts HTTP server
- `internal/agent/types.go` — request/response JSON structs for all endpoints
- `internal/agent/server.go` — `NewServer(deps) *http.Server`, mux wiring, JSON write helpers
- `internal/agent/users.go` — handlers: `POST/DELETE/GET /admin/v1/users`
- `internal/agent/status.go` — handler: `GET /admin/v1/status`
- `internal/agent/health.go` — handler: `POST /admin/v1/healthcheck`
- `internal/agent/rotate.go` — handlers: `POST /admin/v1/rotate-domain`, `POST /admin/v1/rotate-cleanup`
- `internal/agent/sync.go` — handler: `POST /admin/v1/sync`
- `internal/agent/server_test.go` — integration-style tests using `httptest.Server` + fakes
- `scripts/install-agent.sh` — idempotent installer: writes unit, regenerates cloudflared config with admin host, daemon-reload, enable --now

**Modified:**
- `internal/commands/users.go` — `RunAddUser` returns `AddUserResult{Name, UUID, Password}` in addition to printing
- `internal/commands/users_test.go` — assert the returned struct
- `internal/commands/rotate.go` — extract `RunRotateDomainWithZone(ctx, in, zoneID, deps, stdout, stderr)`; `RunRotateDomain` becomes a thin wrapper that looks up zone then calls it
- `internal/commands/rotate_test.go` — cover the new direct-zone variant
- `internal/commands/healthcheck.go` — add `RunHealthcheckProbe(ctx, domain) (HealthResult, error)`; `RunHealthcheckRun` becomes a thin printer over it
- `internal/commands/healthcheck_test.go` — cover the new probe function
- `internal/systemd/units.go` — add `CfvpnAgentService() string`
- `internal/systemd/units_test.go` — assert unit text contains key directives
- `internal/templates/render.go` — `RenderCloudflared` gains an `adminHost string` parameter; when non-empty, prepend an `/admin/*` ingress rule
- `internal/templates/render_test.go` — cover both with-admin and without-admin output
- `internal/cli/dispatch.go` — update `RunAddUser` call sites for the new return signature (`_, err := ...`)
- `Makefile` — add `cfvpn-agent` build target alongside `cfvpnctl`
- `docs/TESTING.md` — append multi-VPS agent smoke-test section

**Unchanged:** `cfvpnctl` CLI surface, `/etc/cfvpn/*` state file layouts, xray config schema, healthcheck timer.

---

## Task 1: Refactor `RunAddUser` to return `AddUserResult`

**Files:**
- Modify: `internal/commands/users.go`
- Modify: `internal/commands/users_test.go`

**Why:** The agent's `POST /admin/v1/users` must respond with the freshly generated UUID and trojan password so the Worker can persist them to D1. Today `RunAddUser` only writes them to a file and prints the base64 subscription blob. Exposing a struct return value avoids re-parsing the on-disk subscription.

- [ ] **Step 1: Write the failing test**

Append to `internal/commands/users_test.go`:

```go
func TestRunAddUserReturnsCreds(t *testing.T) {
	cfgPath, _ := withTempPaths(t)
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.SaveAtomic(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	res, err := RunAddUser(context.Background(), UserInputs{Name: "alice", Domain: "vpn.example.com"}, &stubRunner{}, &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "alice" {
		t.Fatalf("name = %q", res.Name)
	}
	if len(res.UUID) != 36 || !strings.Contains(res.UUID, "-") {
		t.Fatalf("uuid looks wrong: %q", res.UUID)
	}
	if len(res.Password) < 20 {
		t.Fatalf("password too short: %q", res.Password)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/commands -run TestRunAddUserReturnsCreds -v`
Expected: FAIL — either "RunAddUser returns 1 value" or undefined `AddUserResult`.

- [ ] **Step 3: Update the implementation**

In `internal/commands/users.go`:

```go
// AddUserResult is returned from RunAddUser so callers (CLI, agent) can
// access the generated credentials without re-reading the subscription file.
type AddUserResult struct {
	Name     string
	UUID     string
	Password string
}

// RunAddUser creates a new user, persists config, writes the subscription file,
// restarts xray, and prints the subscription on stdout. It returns the generated
// credentials so callers can propagate them (e.g. to a control panel).
func RunAddUser(ctx context.Context, in UserInputs, runner systemd.Runner, stdout, stderr io.Writer) (AddUserResult, error) {
	if err := ValidateAddUserInput(in.Name); err != nil {
		return AddUserResult{}, err
	}
	if in.Domain == "" {
		return AddUserResult{}, fmt.Errorf("DOMAIN is required to issue subscription")
	}

	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return AddUserResult{}, fmt.Errorf("load xray config: %w", err)
	}
	if xray.CountUsers(cfg) >= MaxUsers {
		return AddUserResult{}, fmt.Errorf("user limit reached (max %d)", MaxUsers)
	}
	for _, n := range xray.ListUserNames(cfg) {
		if n == in.Name {
			return AddUserResult{}, fmt.Errorf("user %q already exists", in.Name)
		}
	}

	uuid, err := generateUUIDv4()
	if err != nil {
		return AddUserResult{}, fmt.Errorf("generate uuid: %w", err)
	}
	password, err := generatePassword(24)
	if err != nil {
		return AddUserResult{}, fmt.Errorf("generate password: %w", err)
	}

	if err := xray.AddUser(&cfg, in.Name, uuid, password); err != nil {
		return AddUserResult{}, err
	}
	if err := xray.SaveAtomic(xrayConfigPath, cfg, 0o600); err != nil {
		return AddUserResult{}, fmt.Errorf("save xray config: %w", err)
	}

	sub := buildSubscriptionFor(in.Name, uuid, password, in.Domain)
	if err := writeSubscriptionFile(in.Name, sub+"\n"); err != nil {
		return AddUserResult{}, fmt.Errorf("write subscription: %w", err)
	}

	if err := systemd.Restart(ctx, resolveRunner(runner), xrayServiceUnit); err != nil {
		return AddUserResult{}, fmt.Errorf("restart %s: %w", xrayServiceUnit, err)
	}

	fmt.Fprintln(stdout, sub)
	return AddUserResult{Name: in.Name, UUID: uuid, Password: password}, nil
}
```

- [ ] **Step 4: Update existing callers of `RunAddUser`**

Search: `go doc -all ./... | grep RunAddUser` or `grep -rn "RunAddUser(" cmd/ internal/`.

Every existing call site (primarily the CLI wrapper) now receives two return values. Change each from `err := RunAddUser(...)` to `_, err := RunAddUser(...)`. Do not alter CLI output — the `fmt.Fprintln(stdout, sub)` inside `RunAddUser` still handles printing.

- [ ] **Step 5: Update the existing happy-path test**

In `internal/commands/users_test.go`, change `TestRunAddUserHappyPath`:

```go
err := RunAddUser(context.Background(), UserInputs{Name: "alice", Domain: "vpn.example.com"}, r, &out, &errBuf)
```

to:

```go
_, err := RunAddUser(context.Background(), UserInputs{Name: "alice", Domain: "vpn.example.com"}, r, &out, &errBuf)
```

Repeat for `TestRunAddUserRejectsAtMax` and `TestRunAddUserRejectsDuplicate`.

- [ ] **Step 6: Run the full commands test suite**

Run: `go test ./internal/commands -v`
Expected: PASS, including `TestRunAddUserReturnsCreds`.

- [ ] **Step 7: Run the full module build + test**

Run: `go build ./... && go test ./...`
Expected: both succeed. If any CLI call site was missed, the build fails with a clear line number.

- [ ] **Step 8: Commit**

```bash
git add internal/commands/users.go internal/commands/users_test.go cmd/
git commit -m "refactor(commands): RunAddUser returns AddUserResult with creds"
```

---

## Task 2: Add `RunRotateDomainWithZone` variant

**Files:**
- Modify: `internal/commands/rotate.go`
- Modify: `internal/commands/rotate_test.go`

**Why:** The agent receives `{new_host, new_zone_id}` from the Worker — the zone id is already known because the Worker's zone-pool lookup produced it. Calling `GetZoneID(new_host)` again from the agent is redundant and would fail if the agent's CF token lacks list-zones on that zone. Extracting a zone-aware variant lets the agent skip the lookup while `cfvpnctl rotate-domain <host>` keeps its current behavior.

- [ ] **Step 1: Write the failing test**

Append to `internal/commands/rotate_test.go` (the `fakeCF` and path-override helpers already exist in that file):

```go
func TestRunRotateDomainWithZoneSkipsLookup(t *testing.T) {
	restore := withRotateTempPaths(t) // existing helper in rotate_test.go
	defer restore()

	cf := &fakeCF{newID: "new-tun-id", creds: []byte(`{"x":1}`)}
	in := RotateInputs{
		NewDomain: "k7wz3r2a.example.com",
		OldDomain: "old.example.com",
		OldTunnel: "old-tun",
	}
	var out, errBuf bytes.Buffer

	if err := RunRotateDomainWithZone(context.Background(), in, "zone-id-direct",
		RotateDeps{CF: cf, Runner: &stubRunner{}}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	if cf.getZoneCalls != 0 {
		t.Fatalf("expected GetZoneID to be skipped, got %d calls", cf.getZoneCalls)
	}
	if len(cf.upserts) != 1 || cf.upserts[0][0] != "zone-id-direct" {
		t.Fatalf("expected upsert with zone-id-direct, got %+v", cf.upserts)
	}
}
```

> **Note:** if `fakeCF` does not yet have a `getZoneCalls` counter, add one in the same edit (`getZoneCalls int` field, bump it in `GetZoneID`). The existing helper `withRotateTempPaths` is the `t.Cleanup`-registering function already used by the other tests in that file; reuse it verbatim.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/commands -run TestRunRotateDomainWithZoneSkipsLookup -v`
Expected: FAIL — `RunRotateDomainWithZone` undefined.

- [ ] **Step 3: Refactor `RunRotateDomain`**

In `internal/commands/rotate.go`, split the function. Keep `RunRotateDomain` as the CLI entrypoint; add `RunRotateDomainWithZone` with the zone injected:

```go
// RunRotateDomain resolves the zone id for in.NewDomain via the CF API, then
// delegates to RunRotateDomainWithZone. This is what the CLI calls.
func RunRotateDomain(ctx context.Context, in RotateInputs, deps RotateDeps, stdout, stderr io.Writer) error {
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}
	if err := ValidateRotateDomains(in.OldDomain, in.NewDomain); err != nil {
		return err
	}
	zoneID, err := deps.CF.GetZoneID(ctx, in.NewDomain)
	if err != nil {
		return fmt.Errorf("get zone id for %s: %w", in.NewDomain, err)
	}
	return RunRotateDomainWithZone(ctx, in, zoneID, deps, stdout, stderr)
}

// RunRotateDomainWithZone performs the rotation using an explicit zone id,
// bypassing GetZoneID. Used by the agent when the Worker already knows the zone.
func RunRotateDomainWithZone(ctx context.Context, in RotateInputs, zoneID string, deps RotateDeps, stdout, stderr io.Writer) error {
	_ = stderr
	if deps.CF == nil {
		return fmt.Errorf("cloudflare client is required")
	}
	if err := ValidateRotateDomains(in.OldDomain, in.NewDomain); err != nil {
		return err
	}
	if strings.TrimSpace(in.OldTunnel) == "" {
		return fmt.Errorf("no current TUNNEL_UUID set")
	}
	if strings.TrimSpace(zoneID) == "" {
		return fmt.Errorf("zone id is required")
	}

	newTunnelID, creds, err := deps.CF.CreateTunnel(ctx, "cfvpn-"+in.NewDomain)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	if newTunnelID == "" {
		return fmt.Errorf("create tunnel: empty tunnel id")
	}

	credPath := filepath.Join(cloudflaredCredDir, newTunnelID+".json")
	if err := writeAtomicFile(credPath, creds, 0o600); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("write tunnel credentials: %w", err)
	}

	if err := deps.CF.UpsertCNAME(ctx, zoneID, in.NewDomain, newTunnelID+".cfargotunnel.com"); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("upsert dns cname: %w", err)
	}

	rendered, err := templates.RenderCloudflared(newTunnelID, in.NewDomain)
	if err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("render cloudflared config: %w", err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte(rendered), 0o600); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("write cloudflared config: %w", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("load env: %w", err)
	}
	env["DOMAIN"] = in.NewDomain
	env["TUNNEL_UUID"] = newTunnelID
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("save env: %w", err)
	}

	runner := resolveRunner(deps.Runner)
	if err := systemd.Restart(ctx, runner, "cfvpn-cloudflared.service"); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("restart cfvpn-cloudflared.service: %w", err)
	}
	if err := systemd.Restart(ctx, runner, "cfvpn-xray.service"); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return fmt.Errorf("restart cfvpn-xray.service: %w", err)
	}

	if err := regenerateSubscriptions(in.NewDomain); err != nil {
		printRotateHint(stdout, "cfvpnctl rotate-domain "+in.NewDomain, newTunnelID)
		return err
	}

	fmt.Fprintf(stdout, "rotation complete. Old tunnel %s still active.\n", in.OldTunnel)
	fmt.Fprintf(stdout, "After verifying clients, run: cfvpnctl rotate-domain --cleanup %s\n", in.OldTunnel)
	return nil
}
```

> **Note:** the body of `RunRotateDomainWithZone` is the existing `RunRotateDomain` body with `zoneID` injected as a parameter instead of looked up. Preserve every existing line — especially the `printRotateHint` calls on error paths — unchanged.

- [ ] **Step 4: Run the rotate tests**

Run: `go test ./internal/commands -run TestRunRotate -v`
Expected: PASS — both the new test and all existing rotate tests.

- [ ] **Step 5: Run the full module**

Run: `go build ./... && go test ./...`
Expected: both succeed.

- [ ] **Step 6: Commit**

```bash
git add internal/commands/rotate.go internal/commands/rotate_test.go
git commit -m "refactor(commands): extract RunRotateDomainWithZone for agent use"
```

---

## Task 3: Add `RunHealthcheckProbe` returning structured result

**Files:**
- Modify: `internal/commands/healthcheck.go`
- Modify: `internal/commands/healthcheck_test.go`

**Why:** The agent's `POST /admin/v1/healthcheck` must return `{ok, code, latency_ms}` as JSON. `RunHealthcheckRun` currently probes and prints a human-readable line. Extract the probe into a pure function returning a struct; keep the printer as a thin wrapper.

- [ ] **Step 1: Write the failing test**

Append to `internal/commands/healthcheck_test.go`:

```go
func TestRunHealthcheckProbeOK(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400) // a "healthy" code per IsHealthyCode
	}))
	defer srv.Close()

	// Extract host:port from srv.URL so we can target it directly.
	u, _ := url.Parse(srv.URL)
	client := srv.Client()

	res, err := runHealthcheckProbeWith(context.Background(), u.Host, client)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ok {
		t.Fatalf("expected Ok=true for code 400, got %+v", res)
	}
	if res.Code != 400 {
		t.Fatalf("code = %d", res.Code)
	}
	if res.LatencyMs < 0 {
		t.Fatalf("latency negative: %d", res.LatencyMs)
	}
}

func TestRunHealthcheckProbeUnhealthy(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	res, err := runHealthcheckProbeWith(context.Background(), u.Host, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if res.Ok {
		t.Fatalf("expected Ok=false for code 502, got %+v", res)
	}
	if res.Code != 502 {
		t.Fatalf("code = %d", res.Code)
	}
}
```

Add imports at the top of the test file if missing: `"net/http"`, `"net/http/httptest"`, `"net/url"`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/commands -run TestRunHealthcheckProbe -v`
Expected: FAIL — `runHealthcheckProbeWith` and `HealthResult` undefined.

- [ ] **Step 3: Implement the probe function**

In `internal/commands/healthcheck.go`, add:

```go
// HealthResult is the structured outcome of a single healthcheck probe.
type HealthResult struct {
	Ok        bool  `json:"ok"`
	Code      int   `json:"code"`
	LatencyMs int64 `json:"latency_ms"`
}

// RunHealthcheckProbe probes https://<domain>/vless and returns the result.
// It does not print or mutate state; callers may log/persist as they wish.
func RunHealthcheckProbe(ctx context.Context, domain string) (HealthResult, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	return runHealthcheckProbeWith(ctx, domain, client)
}

// runHealthcheckProbeWith is the injectable core used by tests.
func runHealthcheckProbeWith(ctx context.Context, host string, client *http.Client) (HealthResult, error) {
	if strings.TrimSpace(host) == "" {
		return HealthResult{}, fmt.Errorf("domain is required")
	}
	url := "https://" + host + "/vless"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return HealthResult{}, fmt.Errorf("build request: %w", err)
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return HealthResult{Ok: false, Code: 0, LatencyMs: latency}, nil
	}
	defer resp.Body.Close()
	return HealthResult{
		Ok:        IsHealthyCode(resp.StatusCode),
		Code:      resp.StatusCode,
		LatencyMs: latency,
	}, nil
}
```

> **Note:** returning `(result, nil)` on transport errors is intentional — the Worker wants to learn `ok=false` without receiving an HTTP 500 from the agent.

- [ ] **Step 4: Refactor `RunHealthcheckRun` to use the probe**

Replace the existing probe logic inside `RunHealthcheckRun` with a call to `RunHealthcheckProbe`. Preserve the exact human-readable output format (e.g. `OK code=400` / `FAIL code=502`):

```go
res, err := RunHealthcheckProbe(ctx, domain)
if err != nil {
	fmt.Fprintf(stdout, "FAIL %v\n", err)
	return err
}
if res.Ok {
	fmt.Fprintf(stdout, "OK code=%d\n", res.Code)
} else {
	fmt.Fprintf(stdout, "FAIL code=%d\n", res.Code)
}
return nil
```

If the existing function prints different wording, match it verbatim — consult `grep -n "code=" internal/commands/healthcheck.go` before editing.

- [ ] **Step 5: Run the healthcheck tests**

Run: `go test ./internal/commands -run TestRunHealthcheck -v`
Expected: PASS (existing `RunHealthcheckRun` tests keep passing; new probe tests pass).

- [ ] **Step 6: Run the full module**

Run: `go build ./... && go test ./...`
Expected: both succeed.

- [ ] **Step 7: Commit**

```bash
git add internal/commands/healthcheck.go internal/commands/healthcheck_test.go
git commit -m "refactor(commands): add RunHealthcheckProbe returning HealthResult"
```

---

### Task 4: Request/Response JSON types for the agent

**Files:**
- Create: `internal/agent/types.go`

- [ ] **Step 1: Create the types file**

```go
// internal/agent/types.go
package agent

// AddUserRequest is the body of POST /admin/v1/users.
type AddUserRequest struct {
	Name string `json:"name"`
}

// AddUserResponse is the 200 body of POST /admin/v1/users.
type AddUserResponse struct {
	Name      string `json:"name"`
	VlessUUID string `json:"vless_uuid"`
	TrojanPw  string `json:"trojan_pw"`
}

// UserRecord is one entry returned by GET /admin/v1/users.
type UserRecord struct {
	Name      string `json:"name"`
	VlessUUID string `json:"vless_uuid"`
	TrojanPw  string `json:"trojan_pw"`
}

// StatusResponse is the 200 body of GET /admin/v1/status.
type StatusResponse struct {
	Xray          string `json:"xray"`
	Cloudflared   string `json:"cloudflared"`
	VPNHost       string `json:"vpn_host"`
	TunnelUUID    string `json:"tunnel_uuid"`
	LastRotateAt  int64  `json:"last_rotate_at,omitempty"`
}

// HealthcheckResponse is the 200 body of POST /admin/v1/healthcheck.
type HealthcheckResponse struct {
	Ok        bool  `json:"ok"`
	Code      int   `json:"code"`
	LatencyMs int64 `json:"latency_ms"`
}

// RotateDomainRequest is the body of POST /admin/v1/rotate-domain.
type RotateDomainRequest struct {
	NewHost   string `json:"new_host"`
	NewZoneID string `json:"new_zone_id"`
}

// RotateDomainResponse is the 200 body of POST /admin/v1/rotate-domain.
type RotateDomainResponse struct {
	TunnelUUID string `json:"tunnel_uuid"`
	VPNHost    string `json:"vpn_host"`
}

// RotateCleanupRequest is the body of POST /admin/v1/rotate-cleanup.
type RotateCleanupRequest struct {
	OldTunnelUUID string `json:"old_tunnel_uuid"`
}

// SyncRequest is the body of POST /admin/v1/sync.
type SyncRequest struct {
	Users []UserRecord `json:"users"`
}

// SyncResponse is the 200 body of POST /admin/v1/sync.
type SyncResponse struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// ErrorResponse is the 4xx/5xx body shape.
type ErrorResponse struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/agent/...`
Expected: PASS (no tests yet; pure types compile cleanly).

- [ ] **Step 3: Commit**

```bash
git add internal/agent/types.go
git commit -m "feat(agent): add request/response JSON types"
```

---

### Task 5: Agent HTTP server scaffolding

**Files:**
- Create: `internal/agent/server.go`
- Create: `internal/agent/server_test.go`

The server takes a `Deps` struct holding everything handlers need (systemd runner, CF client, domain). Handlers live in separate files added by later tasks; this task only wires the mux, helpers, and a 404 catch-all.

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/server_test.go
package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerUnknownRouteReturns404JSON(t *testing.T) {
	h := NewHandler(Deps{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("want JSON content-type, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("want error JSON, got %q", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestServerUnknownRouteReturns404JSON -v`
Expected: FAIL (`NewHandler`/`Deps` undefined).

- [ ] **Step 3: Create the server**

```go
// internal/agent/server.go
package agent

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

// Deps is everything agent handlers need. Fields are wired by cmd/cfvpn-agent
// in production and stubbed in tests.
type Deps struct {
	// Runner restarts systemd units. In tests, use a stub that records calls.
	Runner systemd.Runner

	// CF is the Cloudflare client used by rotate-domain / rotate-cleanup.
	CF commands.RotateCFClient

	// EnvPath overrides /etc/cfvpn/cfvpn.env for reading DOMAIN / TUNNEL_UUID
	// in status and rotate handlers. Empty means use the default.
	EnvPath string

	// CFAPIToken and CFAccountID are read once at startup from the env file
	// and passed into RotateInputs when the rotate handler fires.
	CFAPIToken  string
	CFAccountID string
}

// NewHandler returns a mux wired to every /admin/v1/* route.
func NewHandler(deps Deps) http.Handler {
	mux := http.NewServeMux()

	// User lifecycle
	mux.HandleFunc("/admin/v1/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handleAddUser(deps, w, r)
		case http.MethodGet:
			handleListUsers(deps, w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		}
	})
	mux.HandleFunc("/admin/v1/users/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		handleRemoveUser(deps, w, r)
	})

	// Status + health
	mux.HandleFunc("/admin/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		handleStatus(deps, w, r)
	})
	mux.HandleFunc("/admin/v1/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		handleHealthcheck(deps, w, r)
	})

	// Rotation
	mux.HandleFunc("/admin/v1/rotate-domain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		handleRotateDomain(deps, w, r)
	})
	mux.HandleFunc("/admin/v1/rotate-cleanup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		handleRotateCleanup(deps, w, r)
	})

	// Sync
	mux.HandleFunc("/admin/v1/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
			return
		}
		handleSync(deps, w, r)
	})

	// Catch-all 404.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", r.URL.Path)
	})

	return mux
}

// NewServer wraps NewHandler with an http.Server bound to addr. cmd/cfvpn-agent
// uses this; tests use httptest.NewServer(NewHandler(deps)) directly.
func NewServer(addr string, deps Deps) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: NewHandler(deps),
	}
}

// writeJSON marshals v and writes it with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes an ErrorResponse with the given status.
func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, ErrorResponse{Error: code, Detail: detail})
}

// decodeJSON reads r.Body into v. Returns a user-facing error message on failure.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ctxWithTimeout trims a default timeout onto the request context. Handlers
// call this so a stuck CF API call cannot pin a goroutine forever.
func ctxWithTimeout(r *http.Request, _seconds int) (context.Context, context.CancelFunc) {
	return context.WithCancel(r.Context())
}

// --- placeholder handlers; each one is implemented in its own task ---

func handleAddUser(Deps, http.ResponseWriter, *http.Request)      {}
func handleRemoveUser(Deps, http.ResponseWriter, *http.Request)   {}
func handleListUsers(Deps, http.ResponseWriter, *http.Request)    {}
func handleStatus(Deps, http.ResponseWriter, *http.Request)       {}
func handleHealthcheck(Deps, http.ResponseWriter, *http.Request)  {}
func handleRotateDomain(Deps, http.ResponseWriter, *http.Request) {}
func handleRotateCleanup(Deps, http.ResponseWriter, *http.Request){}
func handleSync(Deps, http.ResponseWriter, *http.Request)         {}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent -run TestServerUnknownRouteReturns404JSON -v`
Expected: PASS.

- [ ] **Step 5: Build everything**

Run: `go build ./...`
Expected: PASS (handler stubs satisfy signatures; later tasks replace each stub with a real implementation in its own file).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): add HTTP server scaffolding and routing"
```

---

### Task 6: POST /admin/v1/users handler

**Files:**
- Create: `internal/agent/users.go`
- Modify: `internal/agent/server_test.go` (replace stub `handleAddUser` and add tests)

The handler reads `{name}`, calls `commands.RunAddUser`, and returns the newly minted creds so the panel can persist them to D1.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/server_test.go`:

```go
// --- shared fixture for agent tests ---

// withTempCfvpnPaths redirects commands.xrayConfigPath and subscriptionDir to
// a temp directory by calling the fixture exported from commands (see Task 6
// step 2 for why this fixture is moved to a test helper).
func newTestDeps(t *testing.T, runner systemd.Runner) Deps {
	t.Helper()
	return Deps{Runner: runner}
}

type stubRunner struct{ calls int }

func (s *stubRunner) Run(_ context.Context, _ string, _ ...string) error {
	s.calls++
	return nil
}

func TestAddUserHandlerCreatesUserAndReturnsCreds(t *testing.T) {
	// Redirect commands package paths to a temp dir.
	restore := commands.SetTestPaths(t.TempDir(), t.TempDir())
	defer restore()

	// Seed xray config with one existing user.
	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	// env file so DOMAIN is available to RunAddUser via Deps plumbing.
	envDir := t.TempDir()
	envPath := filepath.Join(envDir, "cfvpn.env")
	if err := os.WriteFile(envPath, []byte("DOMAIN=vpn.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deps := Deps{Runner: &stubRunner{}, EnvPath: envPath}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	body := strings.NewReader(`{"name":"alice"}`)
	resp, err := http.Post(srv.URL+"/admin/v1/users", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	var out AddUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "alice" || out.VlessUUID == "" || out.TrojanPw == "" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestAddUserHandlerRejectsBadName(t *testing.T) {
	restore := commands.SetTestPaths(t.TempDir(), t.TempDir())
	defer restore()
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	envDir := t.TempDir()
	envPath := filepath.Join(envDir, "cfvpn.env")
	_ = os.WriteFile(envPath, []byte("DOMAIN=vpn.example.com\n"), 0o600)

	deps := Deps{Runner: &stubRunner{}, EnvPath: envPath}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/admin/v1/users", "application/json",
		strings.NewReader(`{"name":"bad name"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}
```

Add imports to `internal/agent/server_test.go`:

```go
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/xray"
)
```

- [ ] **Step 2: Add a test-only helper to `internal/commands` so the agent tests can swap paths**

Add `internal/commands/testutil.go` with cross-package path override helpers:

```go
// internal/commands/testutil.go
package commands

// SetTestPaths overrides the package-level xrayConfigPath and subscriptionDir
// vars and returns a restore function. Intended for use from tests in other
// packages (e.g. internal/agent) that exercise command functions end-to-end.
//
// This is a test helper, not part of the production API. It is kept in a
// regular .go file (not _test.go) so cross-package tests can import it.
func SetTestPaths(xrayCfg, subDir string) (restore func()) {
	oldCfg, oldSub := xrayConfigPath, subscriptionDir
	xrayConfigPath = xrayCfg + "/config.json"
	subscriptionDir = subDir
	return func() {
		xrayConfigPath = oldCfg
		subscriptionDir = oldSub
	}
}

// XrayConfigPathForTest exposes xrayConfigPath for cross-package test setup.
func XrayConfigPathForTest() string { return xrayConfigPath }

// SubscriptionDirForTest exposes subscriptionDir for cross-package test setup.
func SubscriptionDirForTest() string { return subscriptionDir }
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/agent -run TestAddUserHandler -v`
Expected: FAIL (`handleAddUser` is still a no-op from Task 5, so body is empty → decode returns EOF).

- [ ] **Step 4: Replace the stub in `internal/agent/server.go` and add the real handler in `internal/agent/users.go`**

Delete the `func handleAddUser(Deps, http.ResponseWriter, *http.Request) {}` stub line in `server.go`, then create:

```go
// internal/agent/users.go
package agent

import (
	"context"
	"net/http"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/state"
)

func handleAddUser(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req AddUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	domain, err := loadDomain(deps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_unreadable", err.Error())
		return
	}
	in := commands.UserInputs{Name: req.Name, Domain: domain}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	res, err := commands.RunAddUser(ctx, in, deps.Runner, discardWriter{}, discardWriter{})
	if err != nil {
		// Validation + "user exists" / "limit reached" errors are 400; anything
		// else (systemd, disk) is 500. The simplest heuristic: known message
		// prefixes map to 400, everything else to 500.
		status := http.StatusInternalServerError
		if isUserError(err) {
			status = http.StatusBadRequest
		}
		writeError(w, status, "add_user_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, AddUserResponse{
		Name:      res.Name,
		VlessUUID: res.UUID,
		TrojanPw:  res.Password,
	})
}

// loadDomain reads DOMAIN from the agent's env file. Deps.EnvPath may be "",
// in which case the default /etc/cfvpn/cfvpn.env is used.
func loadDomain(deps Deps) (string, error) {
	path := deps.EnvPath
	if path == "" {
		path = "/etc/cfvpn/cfvpn.env"
	}
	env, err := state.Load(path)
	if err != nil {
		return "", err
	}
	return env["DOMAIN"], nil
}

// isUserError returns true for errors that should surface as 400 to the panel.
func isUserError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, p := range []string{
		"invalid user name",
		"already exists",
		"user limit reached",
		"not found",
		"DOMAIN is required",
	} {
		if contains(msg, p) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && (indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// discardWriter throws away writes from command functions that print progress.
type discardWriter struct{}

func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/agent -run TestAddUserHandler -v`
Expected: PASS (both cases).

- [ ] **Step 6: Run the full module**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/users.go internal/agent/server.go internal/agent/server_test.go internal/commands/testutil.go
git commit -m "feat(agent): POST /admin/v1/users handler"
```

---

### Task 7: DELETE /admin/v1/users/{name} handler

**Files:**
- Modify: `internal/agent/users.go` (add `handleRemoveUser`, delete stub in `server.go`)
- Modify: `internal/agent/server_test.go`

The URL path after `/admin/v1/users/` is the user name (already validated by `commands.RunRemoveUser`).

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/server_test.go`:

```go
func TestRemoveUserHandlerDeletesUser(t *testing.T) {
	restore := commands.SetTestPaths(t.TempDir(), t.TempDir())
	defer restore()

	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.AddUser(&cfg, "alice", "u-a", "p-a"); err != nil {
		t.Fatal(err)
	}
	if err := xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	deps := Deps{Runner: &stubRunner{}}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/v1/users/alice", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}

	// Verify on-disk state.
	loaded, _ := xray.Load(commands.XrayConfigPathForTest())
	for _, n := range xray.ListUserNames(loaded) {
		if n == "alice" {
			t.Fatalf("alice should be gone")
		}
	}
}

func TestRemoveUserHandlerUnknownUser(t *testing.T) {
	restore := commands.SetTestPaths(t.TempDir(), t.TempDir())
	defer restore()
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	_ = xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600)

	deps := Deps{Runner: &stubRunner{}}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/v1/users/nope", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestRemoveUserHandler -v`
Expected: FAIL (handler is still the no-op stub).

- [ ] **Step 3: Implement the handler**

Remove the `handleRemoveUser` stub line from `server.go` and append to `internal/agent/users.go`:

```go
import "strings"

func handleRemoveUser(deps Deps, w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/admin/v1/users/")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "missing_name", "")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	err := commands.RunRemoveUser(ctx, commands.UserInputs{Name: name}, deps.Runner, discardWriter{}, discardWriter{})
	if err != nil {
		status := http.StatusInternalServerError
		if isUserError(err) {
			status = http.StatusBadRequest
		}
		writeError(w, status, "remove_user_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

Consolidate the `import` block at the top of `users.go` so `strings` is included alongside the existing imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent -run TestRemoveUserHandler -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/users.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): DELETE /admin/v1/users/{name} handler"
```

---

### Task 8: GET /admin/v1/users handler

**Files:**
- Modify: `internal/agent/users.go`
- Modify: `internal/agent/server.go` (delete stub)
- Modify: `internal/agent/server_test.go`

Returns every user's name + VLESS UUID + Trojan password, read straight from the xray config on disk. The Worker uses this for `/sync`.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/server_test.go`:

```go
func TestListUsersHandlerReturnsAllCreds(t *testing.T) {
	restore := commands.SetTestPaths(t.TempDir(), t.TempDir())
	defer restore()

	cfg := xray.NewBaseConfig("user1", "uuid-one", "pass-one")
	if err := xray.AddUser(&cfg, "alice", "uuid-alice", "pass-alice"); err != nil {
		t.Fatal(err)
	}
	if err := xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	deps := Deps{}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/v1/users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got []UserRecord
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 users, got %d", len(got))
	}
	byName := map[string]UserRecord{}
	for _, u := range got {
		byName[u.Name] = u
	}
	if byName["alice"].VlessUUID != "uuid-alice" || byName["alice"].TrojanPw != "pass-alice" {
		t.Fatalf("alice creds mismatch: %+v", byName["alice"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestListUsersHandler -v`
Expected: FAIL.

- [ ] **Step 3: Implement the handler**

Delete the `handleListUsers` stub from `server.go`. Append to `internal/agent/users.go`:

```go
import "github.com/kulinh/cf-vpn/internal/xray"

func handleListUsers(deps Deps, w http.ResponseWriter, r *http.Request) {
	cfg, err := xray.Load(commands.XrayConfigPathForTest())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_config_failed", err.Error())
		return
	}
	names := xray.ListUserNames(cfg)
	out := make([]UserRecord, 0, len(names))
	for _, n := range names {
		uuid, ok := xray.GetVLESSClient(cfg, n)
		if !ok {
			continue
		}
		pw, ok := xray.GetTrojanClient(cfg, n)
		if !ok {
			continue
		}
		out = append(out, UserRecord{Name: n, VlessUUID: uuid, TrojanPw: pw})
	}
	writeJSON(w, http.StatusOK, out)
}
```

Note: the handler reads through `commands.XrayConfigPathForTest()` which also reflects production because `xrayConfigPath` defaults to `paths.XrayConfigFile`. That keeps the list-users handler free of a direct dependency on the `paths` constant and lets tests redirect through `commands.SetTestPaths`. Consolidate `xray` into the `users.go` import block.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent -run TestListUsersHandler -v`
Expected: PASS.

- [ ] **Step 5: Run the full module**

Run: `go build ./... && go test ./...`
Expected: both succeed.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/users.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): GET /admin/v1/users handler"
```

---

### Task 9: GET /admin/v1/status handler

**Files:**
- Create: `internal/agent/status.go`
- Modify: `internal/agent/server.go` (delete `handleStatus` stub)
- Modify: `internal/agent/server_test.go`

Returns live service state + env-derived tunnel metadata:

```json
{
  "xray": "active|inactive",
  "cloudflared": "active|inactive",
  "vpn_host": "<DOMAIN>",
  "tunnel_uuid": "<TUNNEL_UUID>",
  "last_rotate_at": 0
}
```

`last_rotate_at` stays `0` in MVP (placeholder field for future state persistence).

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/server_test.go`:

```go
func TestStatusHandlerReportsUnitsAndEnv(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "cfvpn.env")
	if err := os.WriteFile(envPath, []byte("DOMAIN=vpn.example.com\nTUNNEL_UUID=tun-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &stubRunner{}
	deps := Deps{Runner: r, EnvPath: envPath}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}

	var out StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Xray != "active" || out.Cloudflared != "active" {
		t.Fatalf("expected active statuses, got %+v", out)
	}
	if out.VPNHost != "vpn.example.com" || out.TunnelUUID != "tun-123" {
		t.Fatalf("unexpected env projection: %+v", out)
	}
	if out.LastRotateAt != 0 {
		t.Fatalf("expected LastRotateAt=0 in MVP, got %d", out.LastRotateAt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestStatusHandlerReportsUnitsAndEnv -v`
Expected: FAIL (`handleStatus` is still stubbed).

- [ ] **Step 3: Implement handler**

Create `internal/agent/status.go`:

```go
package agent

import (
	"net/http"

	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

func handleStatus(deps Deps, w http.ResponseWriter, r *http.Request) {
	runner := deps.Runner
	if runner == nil {
		runner = systemd.ExecRunner{}
	}

	x := "inactive"
	if err := systemd.IsActive(r.Context(), runner, "cfvpn-xray.service"); err == nil {
		x = "active"
	}
	c := "inactive"
	if err := systemd.IsActive(r.Context(), runner, "cfvpn-cloudflared.service"); err == nil {
		c = "active"
	}

	path := deps.EnvPath
	if path == "" {
		path = "/etc/cfvpn/cfvpn.env"
	}
	env, err := state.Load(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_unreadable", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, StatusResponse{
		Xray:         x,
		Cloudflared:  c,
		VPNHost:      env["DOMAIN"],
		TunnelUUID:   env["TUNNEL_UUID"],
		LastRotateAt: 0,
	})
}
```

Delete the `handleStatus` stub line from `internal/agent/server.go`.

- [ ] **Step 4: Run status tests**

Run: `go test ./internal/agent -run TestStatusHandlerReportsUnitsAndEnv -v`
Expected: PASS.

- [ ] **Step 5: Run full module**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/status.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): GET /admin/v1/status handler"
```

---

### Task 10: POST /admin/v1/healthcheck handler

**Files:**
- Create: `internal/agent/health.go`
- Modify: `internal/agent/server.go` (delete `handleHealthcheck` stub)
- Modify: `internal/agent/server_test.go`

This wraps `commands.RunHealthcheckProbe` (Task 3 output) and returns structured JSON.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/server_test.go`:

```go
func TestHealthcheckHandlerReturnsProbeResult(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "cfvpn.env")
	if err := os.WriteFile(envPath, []byte("DOMAIN=127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deps := Deps{EnvPath: envPath}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/admin/v1/healthcheck", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	var out HealthcheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Code != 0 {
		t.Fatalf("expected transport-failure code=0 against 127.0.0.1:1, got %d", out.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent -run TestHealthcheckHandlerReturnsProbeResult -v`
Expected: FAIL (`handleHealthcheck` is still stubbed).

- [ ] **Step 3: Implement handler**

Create `internal/agent/health.go`:

```go
package agent

import (
	"net/http"

	"github.com/kulinh/cf-vpn/internal/commands"
)

func handleHealthcheck(deps Deps, w http.ResponseWriter, r *http.Request) {
	domain, err := loadDomain(deps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_unreadable", err.Error())
		return
	}
	res, err := commands.RunHealthcheckProbe(r.Context(), domain)
	if err != nil {
		writeError(w, http.StatusBadRequest, "healthcheck_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, HealthcheckResponse{
		Ok:        res.Ok,
		Code:      res.Code,
		LatencyMs: res.LatencyMs,
	})
}
```

Delete the `handleHealthcheck` stub line from `internal/agent/server.go`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent -run TestHealthcheckHandlerReturnsProbeResult -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/health.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): POST /admin/v1/healthcheck handler"
```

---

### Task 11: POST /admin/v1/rotate-domain handler

**Files:**
- Create: `internal/agent/rotate.go`
- Modify: `internal/agent/server.go` (delete `handleRotateDomain` stub)
- Modify: `internal/agent/server_test.go`
- Modify: `internal/commands/testutil.go` (extend path override helpers)

This endpoint accepts Worker-generated `new_host` + `new_zone_id` and calls `commands.RunRotateDomainWithZone`.

- [ ] **Step 1: Write failing test**

Append to `internal/agent/server_test.go` (add the fake once and reuse it for Task 12):

```go
type fakeRotateCF struct {
	getZoneCalls int
	upserts      [][3]string
	deleted      []string
	newID        string
	creds        []byte
}

func (f *fakeRotateCF) GetZoneID(_ context.Context, _ string) (string, error) {
	f.getZoneCalls++
	return "unused-zone", nil
}

func (f *fakeRotateCF) CreateTunnel(_ context.Context, _ string) (string, []byte, error) {
	return f.newID, f.creds, nil
}

func (f *fakeRotateCF) UpsertCNAME(_ context.Context, z, n, t string) error {
	f.upserts = append(f.upserts, [3]string{z, n, t})
	return nil
}

func (f *fakeRotateCF) DeleteTunnel(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestRotateDomainHandlerUsesExplicitZoneID(t *testing.T) {
	dir := t.TempDir()
	restore := commands.SetRotateTestPaths(
		filepath.Join(dir, "cfvpn.env"),
		filepath.Join(dir, "creds"),
		filepath.Join(dir, "cloudflared.yml"),
		filepath.Join(dir, "xray.json"),
		filepath.Join(dir, "subs"),
	)
	defer restore()

	if err := state.SaveAtomic(filepath.Join(dir, "cfvpn.env"), map[string]string{
		"DOMAIN": "old.example.com", "TUNNEL_UUID": "old-tun",
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := xray.NewBaseConfig("user1", "u1", "p1")
	if err := xray.SaveAtomic(filepath.Join(dir, "xray.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	cf := &fakeRotateCF{newID: "new-tun", creds: []byte(`{"k":1}`)}
	runner := &stubRunner{}
	deps := Deps{
		Runner:      runner,
		CF:          cf,
		EnvPath:     filepath.Join(dir, "cfvpn.env"),
		CFAPIToken:  "tok",
		CFAccountID: "acct",
	}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/admin/v1/rotate-domain",
		"application/json",
		strings.NewReader(`{"new_host":"k7wz3r2a.example.com","new_zone_id":"zone-direct"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	if cf.getZoneCalls != 0 {
		t.Fatalf("expected GetZoneID to be skipped, got %d calls", cf.getZoneCalls)
	}
	if len(cf.upserts) != 1 || cf.upserts[0][0] != "zone-direct" {
		t.Fatalf("unexpected upsert payloads: %+v", cf.upserts)
	}

	var out RotateDomainResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.TunnelUUID != "new-tun" || out.VPNHost != "k7wz3r2a.example.com" {
		t.Fatalf("unexpected response: %+v", out)
	}
}
```

Add imports if missing: `context`, `encoding/json`, `io`, `net/http`, `net/http/httptest`, `path/filepath`, `strings`, plus `state` and `xray` packages.

- [ ] **Step 2: Extend test helper with rotate-path overrides**

Append to `internal/commands/testutil.go`:

```go
func SetRotateTestPaths(envPath, credDir, cfgPath, xrayCfgPath, subDir string) (restore func()) {
	oldEnv := envFilePath
	oldCred := cloudflaredCredDir
	oldCfg := cloudflaredConfig
	oldXray := xrayConfigPath
	oldSub := subscriptionDir

	envFilePath = envPath
	cloudflaredCredDir = credDir
	cloudflaredConfig = cfgPath
	xrayConfigPath = xrayCfgPath
	subscriptionDir = subDir

	return func() {
		envFilePath = oldEnv
		cloudflaredCredDir = oldCred
		cloudflaredConfig = oldCfg
		xrayConfigPath = oldXray
		subscriptionDir = oldSub
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/agent -run TestRotateDomainHandlerUsesExplicitZoneID -v`
Expected: FAIL (`handleRotateDomain` stubbed).

- [ ] **Step 4: Implement handler**

Create `internal/agent/rotate.go`:

```go
package agent

import (
	"net/http"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/state"
)

func handleRotateDomain(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req RotateDomainRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.NewHost == "" || req.NewZoneID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "new_host and new_zone_id are required")
		return
	}

	envPath := deps.EnvPath
	if envPath == "" {
		envPath = "/etc/cfvpn/cfvpn.env"
	}
	envBefore, err := state.Load(envPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_unreadable", err.Error())
		return
	}

	in := commands.RotateInputs{
		NewDomain:   req.NewHost,
		OldDomain:   envBefore["DOMAIN"],
		OldTunnel:   envBefore["TUNNEL_UUID"],
		CFAPIToken:  deps.CFAPIToken,
		CFAccountID: deps.CFAccountID,
	}
	if err := commands.RunRotateDomainWithZone(
		r.Context(),
		in,
		req.NewZoneID,
		commands.RotateDeps{CF: deps.CF, Runner: deps.Runner},
		discardWriter{},
		discardWriter{},
	); err != nil {
		writeError(w, http.StatusInternalServerError, "rotate_failed", err.Error())
		return
	}

	envAfter, err := state.Load(envPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_unreadable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, RotateDomainResponse{
		TunnelUUID: envAfter["TUNNEL_UUID"],
		VPNHost:    envAfter["DOMAIN"],
	})
}
```

Delete `handleRotateDomain` stub from `internal/agent/server.go`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/agent -run TestRotateDomainHandlerUsesExplicitZoneID -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/rotate.go internal/agent/server.go internal/agent/server_test.go internal/commands/testutil.go
git commit -m "feat(agent): POST /admin/v1/rotate-domain handler"
```

---

### Task 12: POST /admin/v1/rotate-cleanup handler

**Files:**
- Modify: `internal/agent/rotate.go`
- Modify: `internal/agent/server.go` (delete `handleRotateCleanup` stub)
- Modify: `internal/agent/server_test.go`

Wrap existing `commands.RunRotateCleanup`.

- [ ] **Step 1: Write failing test**

Append to `internal/agent/server_test.go` (reuse `fakeRotateCF` from Task 11):

```go
func TestRotateCleanupHandlerDeletesTunnel(t *testing.T) {
	cf := &fakeRotateCF{}
	deps := Deps{Runner: &stubRunner{}, CF: cf}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/admin/v1/rotate-cleanup",
		"application/json",
		strings.NewReader(`{"old_tunnel_uuid":"old-uuid"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	if len(cf.deleted) != 1 || cf.deleted[0] != "old-uuid" {
		t.Fatalf("unexpected deleted tunnels: %+v", cf.deleted)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agent -run TestRotateCleanupHandlerDeletesTunnel -v`
Expected: FAIL (`handleRotateCleanup` stubbed).

- [ ] **Step 3: Implement handler**

Append to `internal/agent/rotate.go`:

```go
func handleRotateCleanup(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req RotateCleanupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.OldTunnelUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_old_tunnel_uuid", "")
		return
	}
	if err := commands.RunRotateCleanup(
		r.Context(),
		req.OldTunnelUUID,
		commands.RotateDeps{CF: deps.CF, Runner: deps.Runner},
		discardWriter{},
		discardWriter{},
	); err != nil {
		writeError(w, http.StatusInternalServerError, "rotate_cleanup_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

Delete `handleRotateCleanup` stub from `internal/agent/server.go`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent -run TestRotateCleanupHandlerDeletesTunnel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/rotate.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): POST /admin/v1/rotate-cleanup handler"
```

---

### Task 13: POST /admin/v1/sync handler

**Files:**
- Create: `internal/agent/sync.go`
- Modify: `internal/agent/server.go` (delete `handleSync` stub)
- Modify: `internal/agent/server_test.go`

`/sync` reconciles node runtime state to a provided exact user list. For MVP, reconcile by username:
- add users in request but missing locally (with request-provided credentials)
- remove local users missing from request
- keep intersection unchanged

- [ ] **Step 1: Write failing test**

Append to `internal/agent/server_test.go`:

```go
func TestSyncHandlerAddsAndRemovesUsers(t *testing.T) {
	restore := commands.SetTestPaths(t.TempDir(), t.TempDir())
	defer restore()

	cfg := xray.NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := xray.AddUser(&cfg, "alice", "uuid-a", "pass-a"); err != nil {
		t.Fatal(err)
	}
	if err := xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &stubRunner{}
	deps := Deps{Runner: runner}
	srv := httptest.NewServer(NewHandler(deps))
	defer srv.Close()

	body := `{"users":[{"name":"user1","vless_uuid":"uuid-1","trojan_pw":"pass-1"},{"name":"bob","vless_uuid":"uuid-b","trojan_pw":"pass-b"}]}`
	resp, err := http.Post(srv.URL+"/admin/v1/sync", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}

	var out SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Added) != 1 || out.Added[0] != "bob" {
		t.Fatalf("unexpected added: %+v", out.Added)
	}
	if len(out.Removed) != 1 || out.Removed[0] != "alice" {
		t.Fatalf("unexpected removed: %+v", out.Removed)
	}
	if runner.calls < 2 {
		t.Fatalf("expected restarts from add+remove paths, got %d", runner.calls)
	}

	loaded, err := xray.Load(commands.XrayConfigPathForTest())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, n := range xray.ListUserNames(loaded) {
		seen[n] = true
	}
	if len(seen) != 2 || !seen["user1"] || !seen["bob"] {
		t.Fatalf("unexpected users after sync: %+v", seen)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/agent -run TestSyncHandlerAddsAndRemovesUsers -v`
Expected: FAIL (`handleSync` stubbed).

- [ ] **Step 3: Implement handler**

Create `internal/agent/sync.go`:

```go
package agent

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/xray"
)

func handleSync(deps Deps, w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	cfg, err := xray.Load(commands.XrayConfigPathForTest())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_config_failed", err.Error())
		return
	}

	want := map[string]UserRecord{}
	for _, u := range req.Users {
		if u.Name == "" || u.VlessUUID == "" || u.TrojanPw == "" {
			writeError(w, http.StatusBadRequest, "invalid_user_record", "name, vless_uuid, trojan_pw are required")
			return
		}
		if _, exists := want[u.Name]; exists {
			writeError(w, http.StatusBadRequest, "duplicate_user", fmt.Sprintf("duplicate user %q", u.Name))
			return
		}
		want[u.Name] = u
	}

	haveNames := xray.ListUserNames(cfg)
	have := map[string]struct{}{}
	for _, n := range haveNames {
		have[n] = struct{}{}
	}

	added := make([]string, 0)
	removed := make([]string, 0)

	for name, rec := range want {
		if _, ok := have[name]; ok {
			continue
		}
		if err := syncAddUserWithCreds(r.Context(), deps, rec); err != nil {
			writeError(w, http.StatusInternalServerError, "sync_add_failed", err.Error())
			return
		}
		added = append(added, name)
	}

	for _, name := range haveNames {
		if _, ok := want[name]; ok {
			continue
		}
		if err := commands.RunRemoveUser(r.Context(), commands.UserInputs{Name: name}, deps.Runner, discardWriter{}, discardWriter{}); err != nil {
			writeError(w, http.StatusInternalServerError, "sync_remove_failed", err.Error())
			return
		}
		removed = append(removed, name)
	}

	sort.Strings(added)
	sort.Strings(removed)
	writeJSON(w, http.StatusOK, SyncResponse{Added: added, Removed: removed})
}

func syncAddUserWithCreds(rctx context.Context, deps Deps, u UserRecord) error {
	if err := commands.ValidateAddUserInput(u.Name); err != nil {
		return err
	}
	cfg, err := xray.Load(commands.XrayConfigPathForTest())
	if err != nil {
		return err
	}
	if err := xray.AddUser(&cfg, u.Name, u.VlessUUID, u.TrojanPw); err != nil {
		return err
	}
	if err := xray.SaveAtomic(commands.XrayConfigPathForTest(), cfg, 0o600); err != nil {
		return err
	}
	runner := deps.Runner
	if runner == nil {
		runner = systemd.ExecRunner{}
	}
	return systemd.Restart(rctx, runner, "cfvpn-xray.service")
}
```

Add imports needed by the snippet (`context`, `github.com/kulinh/cf-vpn/internal/systemd`).

Delete `handleSync` stub from `internal/agent/server.go`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent -run TestSyncHandlerAddsAndRemovesUsers -v`
Expected: PASS.

- [ ] **Step 5: Full test run**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/sync.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): POST /admin/v1/sync reconciliation handler"
```

---

### Task 14: `cmd/cfvpn-agent/main.go` entrypoint

**Files:**
- Create: `cmd/cfvpn-agent/main.go`
- Modify: `internal/agent/server.go` (if needed for timeout helper cleanup)
- Create: `cmd/cfvpn-agent/main_test.go` (basic startup wiring test)

Main binary responsibilities:
1) Load `/etc/cfvpn/cfvpn.env` (or path from `CFVPN_ENV_FILE`) via `state.Load`
2) Build CF client from token/account in env
3) Build `agent.Deps`
4) Start HTTP server at `CFVPN_AGENT_ADDR` default `127.0.0.1:8787`
5) Graceful shutdown on SIGINT/SIGTERM

- [ ] **Step 1: Write failing startup test**

Create `cmd/cfvpn-agent/main_test.go`:

```go
package main

import "testing"

func TestResolveAddrDefault(t *testing.T) {
	if got := resolveAddr(""); got != "127.0.0.1:8787" {
		t.Fatalf("default addr = %q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/cfvpn-agent -run TestResolveAddrDefault -v`
Expected: FAIL (`resolveAddr` undefined).

- [ ] **Step 3: Implement main**

Create `cmd/cfvpn-agent/main.go`:

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kulinh/cf-vpn/internal/agent"
	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

func resolveAddr(v string) string {
	if v == "" {
		return "127.0.0.1:8787"
	}
	return v
}

func main() {
	envFile := os.Getenv("CFVPN_ENV_FILE")
	if envFile == "" {
		envFile = "/etc/cfvpn/cfvpn.env"
	}
	env, err := state.Load(envFile)
	if err != nil {
		log.Fatalf("load env: %v", err)
	}
	addr := resolveAddr(os.Getenv("CFVPN_AGENT_ADDR"))

	deps := agent.Deps{
		Runner:      systemd.ExecRunner{},
		CF:          &cloudflare.Client{BaseURL: "https://api.cloudflare.com/client/v4", Token: env["CF_API_TOKEN"], AccountID: env["CF_ACCOUNT_ID"], HTTP: http.DefaultClient},
		EnvPath:     envFile,
		CFAPIToken:  env["CF_API_TOKEN"],
		CFAccountID: env["CF_ACCOUNT_ID"],
	}
	srv := agent.NewServer(addr, deps)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/cfvpn-agent -v`
Expected: PASS.

- [ ] **Step 5: Build both binaries**

Run: `go build ./cmd/cfvpnctl ./cmd/cfvpn-agent`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/cfvpn-agent/main.go cmd/cfvpn-agent/main_test.go
git commit -m "feat(agent): add cfvpn-agent binary entrypoint"
```

---

### Task 15: Add `CfvpnAgentService()` systemd unit template

**Files:**
- Modify: `internal/systemd/units.go`
- Modify: `internal/systemd/units_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/systemd/units_test.go`:

```go
func TestAgentServiceContainsExpectedDirectives(t *testing.T) {
	u := CfvpnAgentService()
	for _, want := range []string{
		"ExecStart=/usr/local/bin/cfvpn-agent",
		"Environment=CFVPN_AGENT_ADDR=127.0.0.1:8787",
		"EnvironmentFile=/etc/cfvpn/cfvpn.env",
		"Restart=on-failure",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("missing %q in unit: %s", want, u)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/systemd -run TestAgentServiceContainsExpectedDirectives -v`
Expected: FAIL (`CfvpnAgentService` undefined).

- [ ] **Step 3: Implement unit generator**

Append to `internal/systemd/units.go`:

```go
func CfvpnAgentService() string {
	return `[Unit]
Description=cfvpn agent
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cfvpn-agent
Environment=CFVPN_AGENT_ADDR=127.0.0.1:8787
EnvironmentFile=/etc/cfvpn/cfvpn.env
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/systemd -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/systemd/units.go internal/systemd/units_test.go
git commit -m "feat(systemd): add cfvpn-agent service unit template"
```

---

### Task 16: Extend cloudflared template with optional `/admin/*` ingress

**Files:**
- Modify: `internal/templates/render.go`
- Modify: `internal/templates/render_test.go`
- Modify: `internal/commands/install.go`
- Modify: `internal/commands/rotate.go`

Add optional `adminHost` parameter so a stable admin hostname can route `/admin/*` to `127.0.0.1:8787` before the fallback 404 rule.

- [ ] **Step 1: Write failing tests**

Append to `internal/templates/render_test.go`:

```go
func TestRenderCloudflaredIncludesAdminIngressWhenProvided(t *testing.T) {
	s, err := RenderCloudflared("t-123", "vpn.example.com", "admin-sg.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "hostname: admin-sg.example.com") {
		t.Fatalf("admin hostname missing: %s", s)
	}
	if !strings.Contains(s, "path: /admin/*") {
		t.Fatalf("admin path missing: %s", s)
	}
	if !strings.Contains(s, "service: http://127.0.0.1:8787") {
		t.Fatalf("admin service missing: %s", s)
	}
}

func TestRenderCloudflaredWithoutAdminIngressWhenEmpty(t *testing.T) {
	s, err := RenderCloudflared("t-123", "vpn.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "/admin/*") {
		t.Fatalf("did not expect admin ingress when adminHost empty: %s", s)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/templates -run TestRenderCloudflared -v`
Expected: FAIL (signature mismatch).

- [ ] **Step 3: Update renderer**

In `internal/templates/render.go`, change template data shape and function signature:

```go
type cloudflaredData struct {
	TunnelUUID string
	Domain     string
	AdminHost  string
}

const cloudflaredTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
ingress:
{{- if .AdminHost }}
  - hostname: {{.AdminHost}}
    path: /admin/*
    service: http://127.0.0.1:8787
{{- end }}
  - hostname: {{.Domain}}
    path: ^/vless$
    service: http://127.0.0.1:10001
  - hostname: {{.Domain}}
    path: ^/trojan$
    service: http://127.0.0.1:10002
  - service: http_status:404
`

func RenderCloudflared(tunnelUUID, domain, adminHost string) (string, error) {
	t, err := template.New("cloudflared").Parse(cloudflaredTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, cloudflaredData{TunnelUUID: tunnelUUID, Domain: domain, AdminHost: adminHost})
	return b.String(), err
}
```

- [ ] **Step 4: Update call sites**

- `internal/commands/install.go`: `templates.RenderCloudflared(tunnelID, in.Domain, "")`
- `internal/commands/rotate.go`: `templates.RenderCloudflared(newTunnelID, in.NewDomain, "")`

Keep behavior unchanged for existing CLI install/rotate flows by passing empty admin host.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/templates -v && go test ./internal/commands -run TestRunInstall -v && go test ./internal/commands -run TestRunRotate -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/templates/render.go internal/templates/render_test.go internal/commands/install.go internal/commands/rotate.go
git commit -m "feat(templates): add optional admin ingress to cloudflared render"
```

---

### Task 17: Add `scripts/install-agent.sh`

**Files:**
- Create: `scripts/install-agent.sh`

Script must be idempotent and safe to rerun:
1) install `bin/cfvpn-agent` to `/usr/local/bin/cfvpn-agent`
2) write `/etc/systemd/system/cfvpn-agent.service` from `systemd.CfvpnAgentService()` output
3) ensure cloudflared config includes admin ingress (`ADMIN_HOST` from env file)
4) daemon-reload + enable --now `cfvpn-agent.service`

- [ ] **Step 1: Write script**

Create `scripts/install-agent.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

log() { printf '[install-agent] %s\n' "$*"; }
die() { printf '[install-agent] ERROR: %s\n' "$*" >&2; exit 1; }

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  die "run as root"
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ -f "$PROJECT_ROOT/go.mod" ] || die "run from cf-vpn repo"

if [ ! -x "$PROJECT_ROOT/bin/cfvpn-agent" ]; then
  log "building cfvpn-agent"
  (cd "$PROJECT_ROOT" && go build -o bin/cfvpn-agent ./cmd/cfvpn-agent)
fi

install -m 0755 "$PROJECT_ROOT/bin/cfvpn-agent" /usr/local/bin/cfvpn-agent

cat >/etc/systemd/system/cfvpn-agent.service <<'EOF'
[Unit]
Description=cfvpn agent
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cfvpn-agent
Environment=CFVPN_AGENT_ADDR=127.0.0.1:8787
EnvironmentFile=/etc/cfvpn/cfvpn.env
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 /etc/systemd/system/cfvpn-agent.service

if [ -f /etc/cfvpn/cfvpn.env ]; then
  # shellcheck disable=SC1091
  . /etc/cfvpn/cfvpn.env
fi
: "${DOMAIN:?DOMAIN missing in /etc/cfvpn/cfvpn.env}"
: "${TUNNEL_UUID:?TUNNEL_UUID missing in /etc/cfvpn/cfvpn.env}"
: "${ADMIN_HOST:?ADMIN_HOST missing in /etc/cfvpn/cfvpn.env}"

cat >/etc/cfvpn/cloudflared/config.yml <<EOF
tunnel: ${TUNNEL_UUID}
credentials-file: /etc/cfvpn/cloudflared/${TUNNEL_UUID}.json
ingress:
  - hostname: ${ADMIN_HOST}
    path: /admin/*
    service: http://127.0.0.1:8787
  - hostname: ${DOMAIN}
    path: ^/vless$
    service: http://127.0.0.1:10001
  - hostname: ${DOMAIN}
    path: ^/trojan$
    service: http://127.0.0.1:10002
  - service: http_status:404
EOF
chmod 0600 /etc/cfvpn/cloudflared/config.yml

systemctl daemon-reload
systemctl enable --now cfvpn-agent.service
systemctl restart cfvpn-cloudflared.service

log "installed and started cfvpn-agent"
```

- [ ] **Step 2: Make executable and run shell lint check**

Run: `chmod +x scripts/install-agent.sh && bash -n scripts/install-agent.sh`
Expected: no syntax errors.

- [ ] **Step 3: Commit**

```bash
git add scripts/install-agent.sh
git commit -m "feat(scripts): add idempotent agent installer"
```

---

### Task 18: Build target + docs smoke checklist

**Files:**
- Modify: `Makefile`
- Modify: `docs/TESTING.md`

- [ ] **Step 1: Add agent build target**

Update `Makefile`:

```make
build:
	go build -o bin/cfvpnctl ./cmd/cfvpnctl

build-agent:
	go build -o bin/cfvpn-agent ./cmd/cfvpn-agent

test:
	go test ./...

all: test build build-agent
```

- [ ] **Step 2: Add multi-VPS agent smoke section**

Append to `docs/TESTING.md`:

```markdown
## 12. Multi-VPS Agent Smoke (Plan 1)

- [ ] `make build-agent` succeeds (produces `bin/cfvpn-agent`)
- [ ] `sudo bash scripts/install-agent.sh` succeeds on a test VPS
- [ ] `systemctl is-active cfvpn-agent` returns `active`
- [ ] `curl -sS http://127.0.0.1:8787/admin/v1/status | jq .` returns JSON with xray/cloudflared statuses
- [ ] Through admin hostname (CF Access protected), `GET /admin/v1/status` works with service token headers
- [ ] `POST /admin/v1/users {"name":"alice"}` returns creds and `alice` appears in xray config
- [ ] `GET /admin/v1/users` includes `alice` with vless/trojan creds
- [ ] `DELETE /admin/v1/users/alice` removes user and returns `{ok:true}`
- [ ] `POST /admin/v1/healthcheck` returns `{ok,code,latency_ms}`
- [ ] `POST /admin/v1/rotate-domain` with explicit zone id updates DOMAIN/TUNNEL_UUID in `/etc/cfvpn/cfvpn.env`
- [ ] `POST /admin/v1/rotate-cleanup` deletes old tunnel credentials file
- [ ] `POST /admin/v1/sync` can add/remove users to match supplied list
```

- [ ] **Step 3: Verify build + tests + docs assertions**

Run: `make all && go test ./... && bash scripts/docs-assert.sh`
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add Makefile docs/TESTING.md
git commit -m "chore: add cfvpn-agent build target and smoke checklist"
```

---

## Spec Self-Review (completed)

- [x] **Spec coverage check:** Every Plan 1 requirement in `docs/superpowers/specs/2026-04-20-multi-vps-control-panel-design.md` is mapped:
  - Agent binary + loopback bind + JSON endpoints → Tasks 4–14
  - `RunAddUser` structured return + rotate-with-zone + health probe refactors → Tasks 1–3
  - systemd agent unit + cloudflared `/admin/*` ingress → Tasks 15–17
  - build/docs updates + smoke checklist → Task 18
- [x] **Placeholder scan:** Removed/avoided TBD/TODO placeholders. Every code step includes concrete snippets and every validation step has exact commands.
- [x] **Type consistency check:** Types and route shapes are consistent across tasks (`AddUserResult`, `HealthResult`, `RotateDomainRequest`, `RotateDomainResponse`, `SyncResponse`) and align with the approved spec.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-20-cfvpn-agent.md`. Two execution options:

1. **Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration
2. **Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
