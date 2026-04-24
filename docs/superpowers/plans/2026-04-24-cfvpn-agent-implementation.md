# cfvpn-agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add HTTP agent server to cfvpnctl, running under systemd, callable from Cloudflare panel on port 6788.

**Architecture:** Agent is a subcommand of cfvpnctl (`cfvpnctl agent`). HTTP server on localhost:6788 with Cloudflare Access auth. Shares config via `/etc/cfvpn/cfvpn.env`. 4 endpoints: status, healthcheck, rotate-domain, sync.

**Tech Stack:** Go stdlib `net/http`, no external dependencies.

---

## File Structure

```
cmd/cfvpnctl/main.go          Modify: add "agent" to Run() args
internal/cli/dispatch.go      Modify: add case "agent"
internal/agent/server.go      Create: HTTP server, graceful shutdown
internal/agent/handlers.go    Create: 4 route handlers
internal/agent/auth.go         Create: CF Access header middleware
internal/systemd/units.go      Modify: add AgentService() template
scripts/bootstrap-vps.sh       Modify: install cfvpn-agent unit
docs/superpowers/plans/...     This plan
```

---

## Task 1: Add AgentService unit template

**Files:**
- Modify: `internal/systemd/units.go`

- [ ] **Step 1: Read current units.go**

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

- [ ] **Step 2: Add AgentService function** — add after HealthcheckTimer():

```go
func AgentService() string {
	return `[Unit]
Description=cfvpn agent
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cfvpnctl agent
Restart=on-failure
RestartSec=3
RestartPreventExitStatus=1

[Install]
WantedBy=multi-user.target
`
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/systemd/units.go
git commit -m "systemd: add AgentService unit template"
```

---

## Task 2: Create auth middleware

**Files:**
- Create: `internal/agent/auth.go`

- [ ] **Step 1: Write auth.go**

```go
package agent

import (
	"context"
	"net/http"
	"os"
)

type contextKey string

const (
	ctxKeyActor contextKey = "actor"
)

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := r.Header.Get("CF-Access-Client-Id")
		clientSecret := r.Header.Get("CF-Access-Client-Secret")

		expectedID := os.Getenv("CF_ACCESS_CLIENT_ID")
		expectedSecret := os.Getenv("CF_ACCESS_CLIENT_SECRET")

		if expectedID == "" || expectedSecret == "" {
			http.Error(w, "CF Access not configured", http.StatusInternalServerError)
			return
		}

		if clientID != expectedID || clientSecret != expectedSecret {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		actor := r.Header.Get("CF-Access-Audience-Account-ID")
		if actor == "" {
			actor = clientID
		}
		ctx := context.WithValue(r.Context(), ctxKeyActor, actor)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ActorFromRequest(r *http.Request) string {
	if actor, ok := r.Context().Value(ctxKeyActor).(string); ok {
		return actor
	}
	return "unknown"
}
```

- [ ] **Step 2: Run go build to verify**

```bash
go build ./internal/agent/
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/auth.go
git commit -m "agent: add CF Access auth middleware"
```

---

## Task 3: Create route handlers

**Files:**
- Create: `internal/agent/handlers.go`

- [ ] **Step 1: Write handlers.go**

```go
package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/xray"
)

type StatusHandler struct{}

type AgentStatusResponse struct {
	Xray         string `json:"xray"`
	Cloudflared  string `json:"cloudflared"`
	VPNHost      string `json:"vpn_host"`
	TunnelUUID   string `json:"tunnel_uuid"`
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runner := systemd.ExecRunner{}

	xrayStatus := "inactive"
	if systemd.IsActive(ctx, runner, "cfvpn-xray.service") == nil {
		xrayStatus = "active"
	}

	cfStatus := "inactive"
	if systemd.IsActive(ctx, runner, "cfvpn-cloudflared.service") == nil {
		cfStatus = "active"
	}

	env, _ := state.Load(paths.EnvFile)
	vpnHost := ""
	tunnelUUID := ""
	if env != nil {
		vpnHost = env["DOMAIN"]
		tunnelUUID = env["TUNNEL_UUID"]
	}

	resp := AgentStatusResponse{
		Xray:        xrayStatus,
		Cloudflared: cfStatus,
		VPNHost:     vpnHost,
		TunnelUUID:  tunnelUUID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type HealthcheckHandler struct{}

type AgentHealthcheckResponse struct {
	LatencyMs int64  `json:"latency_ms"`
	XrayOK     bool   `json:"xray_ok"`
	TunnelOK   bool   `json:"tunnel_ok"`
}

func (h *HealthcheckHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	runner := systemd.ExecRunner{}

	xrayOK := systemd.IsActive(ctx, runner, "cfvpn-xray.service") == nil
	tunnelOK := systemd.IsActive(ctx, runner, "cfvpn-cloudflared.service") == nil
	latencyMs := time.Since(start).Milliseconds()

	resp := AgentHealthcheckResponse{
		LatencyMs: latencyMs,
		XrayOK:    xrayOK,
		TunnelOK:  tunnelOK,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type RotateDomainHandler struct{}

type AgentRotateResponse struct {
	VPNHost    string `json:"vpn_host"`
	TunnelUUID string `json:"tunnel_uuid"`
}

type RotateDomainRequest struct {
	NewHost    string `json:"new_host"`
	NewZoneID  string `json:"new_zone_id"`
}

func (h *RotateDomainHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req RotateDomainRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	cmd := exec.CommandContext(ctx, "cfvpnctl", "rotate-domain", req.NewHost)
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, string(out), http.StatusInternalServerError)
		return
	}

	env, _ := state.Load(paths.EnvFile)
	resp := AgentRotateResponse{
		VPNHost:    req.NewHost,
		TunnelUUID: env["TUNNEL_UUID"],
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type SyncHandler struct{}

type AgentSyncResponse struct {
	OK          bool `json:"ok"`
	UsersSynced int  `json:"users_synced"`
}

type SyncRequest struct {
	Users []struct {
		Name      string `json:"name"`
		VLESSUUID string `json:"vless_uuid"`
		TrojanPW  string `json:"trojan_pw"`
	} `json:"users"`
}

func (h *SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var req SyncRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	cfg, err := xray.Load(paths.XrayConfigFile)
	if err != nil {
		http.Error(w, "load xray config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	synced := 0
	for _, u := range req.Users {
		if xray.AddClient(cfg, u.Name, u.VLESSUUID, u.TrojanPW) {
			synced++
		}
	}

	if err := xray.Save(paths.XrayConfigFile, cfg); err != nil {
		http.Error(w, "save xray config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	runner := systemd.ExecRunner{}
	systemd.Restart(ctx, runner, "cfvpn-xray.service")

	resp := AgentSyncResponse{
		OK:          true,
		UsersSynced: synced,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 2: Run go build to verify**

```bash
go build ./internal/agent/
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/handlers.go
git commit -m "agent: add route handlers"
```

---

## Task 4: Create HTTP server

**Files:**
- Create: `internal/agent/server.go`

- [ ] **Step 1: Write server.go**

```go
package agent

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	Port        = ":6788"
	ReadTimeout  = 10 * time.Second
	WriteTimeout = 30 * time.Second
	IdleTimeout  = 120 * time.Second
)

func RunServer() error {
	mux := http.NewServeMux()

	mux.Handle("/admin/v1/status", AuthMiddleware(&StatusHandler{}))
	mux.Handle("/admin/v1/healthcheck", AuthMiddleware(&HealthcheckHandler{}))
	mux.Handle("/admin/v1/rotate-domain", AuthMiddleware(&RotateDomainHandler{}))
	mux.Handle("/admin/v1/sync", AuthMiddleware(&SyncHandler{}))

	srv := &http.Server{
		Addr:         Port,
		Handler:      mux,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
	}

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("shutting down agent...")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("agent listening on localhost%s", Port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Run go build to verify**

```bash
go build ./internal/agent/
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/server.go
git commit -m "agent: add HTTP server with graceful shutdown"
```

---

## Task 5: Add agent dispatch in CLI

**Files:**
- Modify: `internal/cli/dispatch.go`
- Modify: `cmd/cfvpnctl/main.go` (no changes needed, just verify)

- [ ] **Step 1: Read current dispatch.go**

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	// ... existing code
```

- [ ] **Step 2: Add "agent" case in switch** — find the `default` case and add before it:

```go
	case "agent":
		if err := agent.RunServer(); err != nil {
			fmt.Fprintf(stderr, "agent error: %v\n", err)
			return 1
		}
		return 0
```

Add import at top of file:

```go
	"github.com/kulinh/cf-vpn/internal/agent"
```

- [ ] **Step 3: Run go build to verify**

```bash
go build ./cmd/cfvpnctl/
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/cli/dispatch.go
git commit -m "cli: add agent subcommand"
```

---

## Task 6: Update bootstrap script

**Files:**
- Modify: `scripts/bootstrap-vps.sh`

- [ ] **Step 1: Read current bootstrap script** (find systemd unit installation section)

Look for where `cfvpn-healthcheck.service` or `cfvpn-xray.service` is installed.

- [ ] **Step 2: Add agent unit installation** — after healthcheck timer install, add:

```bash
# Install cfvpn-agent systemd unit
cfvpnctl healthcheck install

# Install cfvpn-agent service
cat > /etc/systemd/system/cfvpn-agent.service << 'EOF'
[Unit]
Description=cfvpn agent
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cfvpnctl agent
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now cfvpn-agent
```

- [ ] **Step 3: Commit**

```bash
git add scripts/bootstrap-vps.sh
git commit -m "bootstrap: install cfvpn-agent systemd unit"
```

---

## Task 7: Add unit test for handlers

**Files:**
- Create: `internal/agent/handlers_test.go`

- [ ] **Step 1: Write handlers_test.go**

```go
package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusHandler(t *testing.T) {
	handler := &StatusHandler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp AgentStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Errorf("decode response: %v", err)
	}

	if resp.Xray == "" {
		t.Error("xray status should not be empty")
	}
	if resp.Cloudflared == "" {
		t.Error("cloudflared status should not be empty")
	}
}

func TestHealthcheckHandler(t *testing.T) {
	handler := &HealthcheckHandler{}
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/healthcheck", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp AgentHealthcheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Errorf("decode response: %v", err)
	}

	if resp.LatencyMs < 0 {
		t.Errorf("latency should be non-negative, got %d", resp.LatencyMs)
	}
}

func TestRotateDomainHandler_MethodNotAllowed(t *testing.T) {
	handler := &RotateDomainHandler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/rotate-domain", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestSyncHandler_MethodNotAllowed(t *testing.T) {
	handler := &SyncHandler{}
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/sync", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/agent/... -v
```

Expected: PASS (may skip tests that need real xray config)

- [ ] **Step 3: Commit**

```bash
git add internal/agent/handlers_test.go
git commit -m "agent: add unit tests for handlers"
```

---

## Task 8: Final build verification

- [ ] **Step 1: Build binary**

```bash
make build
```

Expected: `bin/cfvpnctl` created

- [ ] **Step 2: Verify agent subcommand exists**

```bash
./bin/cfvpnctl help
```

Expected: output includes "agent" in command list

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all tests pass

---

## Summary

After all tasks:
1. `cfvpnctl agent` starts HTTP server on localhost:6788
2. 4 endpoints: `/admin/v1/status`, `/healthcheck`, `/rotate-domain`, `/sync`
3. Auth via CF Access headers
4. systemd unit `cfvpn-agent.service` installed on bootstrap
5. All tests pass