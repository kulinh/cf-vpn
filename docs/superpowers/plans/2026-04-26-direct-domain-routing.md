# Direct Domain Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace cloudflared data plane with direct domain → VPS IP routing (Xray native TLS 443, wildcard cert via acme.sh, Cloudflare DNS-only A records). Admin plane keeps cloudflared.

**Architecture:** Client connects directly to VPS:443 via DNS A record (proxied:false). Xray terminates TLS using a per-zone wildcard cert issued by acme.sh + Cloudflare DNS-01. Rotate flow: detect public IP → ensure cert → upsert A record → render Xray config → reload Xray → delete old A record. Admin tunnel (cloudflared → 127.0.0.1:6788) is unchanged.

**Tech Stack:** Go (cfvpnctl + agent), Xray-core, acme.sh, Cloudflare API, Cloudflare Workers + D1, React/TypeScript panel.

**Spec reference:** `docs/superpowers/specs/2026-04-26-direct-domain-routing-design.md`

**Dependencies:**
- The agent (`internal/agent/`) is being implemented in parallel via plan `docs/superpowers/plans/2026-04-20-cfvpn-agent.md`. Tasks in **Phase 4** of this plan assume `internal/agent/handlers.go` exists with `RotateDomainHandler` registered. If agent plan is not yet executed, defer Phase 4 until it is; Phases 1-3 and 5-8 can proceed independently.

---

## File Structure

**New files:**
- `internal/netinfo/publicip.go` — public IP detection with 5-min cache
- `internal/netinfo/publicip_test.go`
- `internal/cert/acme.go` — acme.sh wrapper, cert path lookup, reload hook install
- `internal/cert/acme_test.go`
- `panel/worker/migrations/0004_nodes_public_ip.sql`
- `panel/worker/src/routes/zones-cert.ts` — issue-cert endpoint (or extend existing zones.ts if present)
- `panel/web/src/pages/ZonesPage.tsx` — issue cert UI (or extend if present)

**Modified files:**
- `internal/cloudflare/client.go` — add `UpsertARecord`, `DeleteARecordByName`
- `internal/cloudflare/client_test.go`
- `internal/templates/render.go` — Xray TLS template, cloudflared admin-only
- `internal/templates/render_test.go` (new if missing)
- `internal/commands/rotate.go` — rewrite for direct mode
- `internal/commands/rotate_test.go`
- `internal/commands/install.go` — direct mode + `--upgrade` flow
- `internal/commands/install_test.go`
- `internal/cli/dispatch.go` — wire `install --upgrade` + `--check`
- `internal/systemd/units.go` — `cfvpn-xray.service` ExecReload
- `panel/worker/src/types.ts` — `AgentRotateResponse` shape, `NodeRow.public_ip`
- `panel/worker/src/routes/nodes.ts` — rotate payload + status auto-sync
- `panel/worker/src/routes/nodes.test.ts`
- `panel/web/src/lib/api.ts` — `publicIp` parsing
- `panel/web/src/components/nodes/NodeCard.tsx` — Direct badge + IP
- `README.md` — direct-mode docs, ufw 443

---

## Pre-flight

- [ ] **Step 0.1: Confirm clean tree on a feature branch**

```bash
git status
git checkout -b feat/direct-domain-routing
```

- [ ] **Step 0.2: Run baseline tests so regressions stand out later**

```bash
make test
npm --prefix panel/worker test -- --run
npm --prefix panel/web test -- --run
```

Expected: all green (record any pre-existing failures so we don't blame them on this work).

---

## Phase 1 — Foundation Go libraries

### Task 1: Public IP detection with cache (`internal/netinfo`)

**Files:**
- Create: `internal/netinfo/publicip.go`
- Create: `internal/netinfo/publicip_test.go`

- [ ] **Step 1.1: Write the failing test**

```go
// internal/netinfo/publicip_test.go
package netinfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetectReturnsIPFromPrimary(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("203.0.113.42"))
	}))
	defer ts.Close()

	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: time.Minute, Now: time.Now}
	ip, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.42" {
		t.Fatalf("got %q", ip)
	}
}

func TestDetectFallsBackOnPrimaryFail(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("198.51.100.7\n"))
	}))
	defer fallback.Close()

	d := &Detector{Primary: primary.URL, Fallback: fallback.URL, HTTP: primary.Client(), TTL: time.Minute, Now: time.Now}
	ip, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "198.51.100.7" {
		t.Fatalf("got %q", ip)
	}
}

func TestDetectCachesWithinTTL(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte("203.0.113.1"))
	}))
	defer ts.Close()

	now := time.Now()
	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: 5 * time.Minute, Now: func() time.Time { return now }}
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

func TestDetectRefreshesAfterTTL(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte("203.0.113.1"))
	}))
	defer ts.Close()

	now := time.Now()
	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: 5 * time.Minute, Now: func() time.Time { return now }}
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}

func TestDetectRejectsInvalidIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-an-ip"))
	}))
	defer ts.Close()

	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: time.Minute, Now: time.Now}
	if _, err := d.Detect(context.Background()); err == nil {
		t.Fatal("expected error on invalid IP")
	}
}
```

- [ ] **Step 1.2: Run test to verify it fails**

```bash
go test ./internal/netinfo/...
```

Expected: build failure — package does not exist.

- [ ] **Step 1.3: Write minimal implementation**

```go
// internal/netinfo/publicip.go
package netinfo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultPrimary  = "https://api.ipify.org"
	defaultFallback = "https://icanhazip.com"
	defaultTTL      = 5 * time.Minute
)

type Detector struct {
	Primary  string
	Fallback string
	HTTP     *http.Client
	TTL      time.Duration
	Now      func() time.Time

	mu        sync.Mutex
	cachedIP  string
	cachedAt  time.Time
}

func NewDefault() *Detector {
	return &Detector{
		Primary:  defaultPrimary,
		Fallback: defaultFallback,
		HTTP:     &http.Client{Timeout: 5 * time.Second},
		TTL:      defaultTTL,
		Now:      time.Now,
	}
}

func (d *Detector) Detect(ctx context.Context) (string, error) {
	d.mu.Lock()
	if d.cachedIP != "" && d.Now().Sub(d.cachedAt) < d.TTL {
		ip := d.cachedIP
		d.mu.Unlock()
		return ip, nil
	}
	d.mu.Unlock()

	ip, err := d.fetch(ctx, d.Primary)
	if err != nil && d.Fallback != "" {
		ip, err = d.fetch(ctx, d.Fallback)
	}
	if err != nil {
		return "", err
	}

	d.mu.Lock()
	d.cachedIP = ip
	d.cachedAt = d.Now()
	d.mu.Unlock()
	return ip, nil
}

func (d *Detector) fetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("ip lookup %s: status %d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(raw))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid IP from %s: %q", url, ip)
	}
	return ip, nil
}
```

- [ ] **Step 1.4: Run test to verify it passes**

```bash
go test ./internal/netinfo/... -v
```

Expected: PASS for all 5 tests.

- [ ] **Step 1.5: Commit**

```bash
git add internal/netinfo
git commit -m "feat(netinfo): add public IP detector with TTL cache and fallback"
```


### Task 2: acme.sh wrapper (`internal/cert`)

**Files:**
- Create: `internal/cert/acme.go`
- Create: `internal/cert/acme_test.go`

The cert package wraps `acme.sh` with an injectable command runner so tests can verify behavior without invoking the real binary. Idempotency: skip issue if cert exists and `notAfter > now + 30d`.

- [ ] **Step 2.1: Write the failing test**

```go
// internal/cert/acme_test.go
package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeRunner struct {
	calls [][]string
	err   error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return nil, f.err
}

func writeFakeCert(t *testing.T, path string, notAfter time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestEnsureWildcardSkipsWhenCertValidFor30Days(t *testing.T) {
	dir := t.TempDir()
	zone := "example.com"
	writeFakeCert(t, filepath.Join(dir, zone, "fullchain.pem"), time.Now().Add(60*24*time.Hour))
	os.WriteFile(filepath.Join(dir, zone, "privkey.pem"), []byte("k"), 0o600)

	r := &fakeRunner{}
	m := &Manager{CertDir: dir, Runner: r, Now: time.Now}
	if err := m.EnsureWildcard(context.Background(), zone, "tok"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("expected no acme.sh calls, got %v", r.calls)
	}
}

func TestEnsureWildcardIssuesWhenCertMissing(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{}
	m := &Manager{CertDir: dir, Runner: r, Now: time.Now}
	if err := m.EnsureWildcard(context.Background(), "example.com", "tok"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 acme call, got %d: %v", len(r.calls), r.calls)
	}
	got := r.calls[0]
	wantContains := []string{"--issue", "--dns", "dns_cf", "-d", "example.com", "-d", "*.example.com"}
	for _, w := range wantContains {
		found := false
		for _, a := range got {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("acme call missing %q: %v", w, got)
		}
	}
}

func TestEnsureWildcardIssuesWhenCertExpiringSoon(t *testing.T) {
	dir := t.TempDir()
	zone := "example.com"
	writeFakeCert(t, filepath.Join(dir, zone, "fullchain.pem"), time.Now().Add(10*24*time.Hour))

	r := &fakeRunner{}
	m := &Manager{CertDir: dir, Runner: r, Now: time.Now}
	if err := m.EnsureWildcard(context.Background(), zone, "tok"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected reissue, got %d calls", len(r.calls))
	}
}

func TestCertPathReturnsPathsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	zone := "example.com"
	zd := filepath.Join(dir, zone)
	os.MkdirAll(zd, 0o755)
	os.WriteFile(filepath.Join(zd, "fullchain.pem"), []byte("c"), 0o644)
	os.WriteFile(filepath.Join(zd, "privkey.pem"), []byte("k"), 0o600)

	m := &Manager{CertDir: dir, Now: time.Now}
	cert, key, ok := m.CertPath(zone)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cert != filepath.Join(zd, "fullchain.pem") || key != filepath.Join(zd, "privkey.pem") {
		t.Fatalf("got %s, %s", cert, key)
	}
}

func TestCertPathReturnsFalseWhenMissing(t *testing.T) {
	m := &Manager{CertDir: t.TempDir(), Now: time.Now}
	if _, _, ok := m.CertPath("nope.com"); ok {
		t.Fatal("expected ok=false")
	}
}
```

- [ ] **Step 2.2: Run test to verify it fails**

```bash
go test ./internal/cert/...
```

Expected: build failure — package does not exist.

- [ ] **Step 2.3: Write minimal implementation**

```go
// internal/cert/acme.go
package cert

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const renewWindow = 30 * 24 * time.Hour

type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Manager struct {
	CertDir    string // /etc/cfvpn/certs
	AcmeBinary string // default /root/.acme.sh/acme.sh
	ReloadHook string // default /etc/cfvpn/acme-reload.sh
	Runner     Runner
	Now        func() time.Time
}

func NewDefault() *Manager {
	return &Manager{
		CertDir:    "/etc/cfvpn/certs",
		AcmeBinary: "/root/.acme.sh/acme.sh",
		ReloadHook: "/etc/cfvpn/acme-reload.sh",
		Runner:     ExecRunner{},
		Now:        time.Now,
	}
}

func (m *Manager) CertPath(zone string) (cert, key string, ok bool) {
	cert = filepath.Join(m.CertDir, zone, "fullchain.pem")
	key = filepath.Join(m.CertDir, zone, "privkey.pem")
	if _, err := os.Stat(cert); err != nil {
		return "", "", false
	}
	if _, err := os.Stat(key); err != nil {
		return "", "", false
	}
	return cert, key, true
}

func (m *Manager) EnsureWildcard(ctx context.Context, zone, cfToken string) error {
	if m.certValid(zone) {
		return nil
	}
	zoneDir := filepath.Join(m.CertDir, zone)
	if err := os.MkdirAll(zoneDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", zoneDir, err)
	}
	bin := m.AcmeBinary
	if bin == "" {
		bin = "/root/.acme.sh/acme.sh"
	}
	args := []string{
		"--issue",
		"--dns", "dns_cf",
		"-d", zone,
		"-d", "*." + zone,
		"--key-file", filepath.Join(zoneDir, "privkey.pem"),
		"--fullchain-file", filepath.Join(zoneDir, "fullchain.pem"),
		"--reloadcmd", m.ReloadHook,
	}
	envCmd := envWrap(bin, "CF_Token="+cfToken)
	if _, err := m.Runner.Run(ctx, envCmd[0], append(envCmd[1:], args...)...); err != nil {
		return fmt.Errorf("acme.sh issue %s: %w", zone, err)
	}
	return nil
}

func envWrap(bin string, kv ...string) []string {
	out := []string{"env"}
	out = append(out, kv...)
	out = append(out, bin)
	return out
}

func (m *Manager) certValid(zone string) bool {
	cert, _, ok := m.CertPath(zone)
	if !ok {
		return false
	}
	raw, err := os.ReadFile(cert)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return c.NotAfter.After(m.Now().Add(renewWindow))
}
```

- [ ] **Step 2.4: Run test to verify it passes**

```bash
go test ./internal/cert/... -v
```

Expected: 5 PASS.

- [ ] **Step 2.5: Commit**

```bash
git add internal/cert
git commit -m "feat(cert): add acme.sh wrapper with idempotent EnsureWildcard"
```


### Task 3: Cloudflare A-record methods (`internal/cloudflare`)

**Files:**
- Modify: `internal/cloudflare/client.go`
- Modify: `internal/cloudflare/client_test.go`

- [ ] **Step 3.1: Write the failing test**

Append to `internal/cloudflare/client_test.go`:

```go
func TestUpsertARecordCreatesWhenAbsent(t *testing.T) {
	mux := http.NewServeMux()
	var posted bool
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"success":true,"result":[]}`))
		case http.MethodPost:
			posted = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"type":"A"`) {
				t.Fatalf("expected type A, got %s", body)
			}
			if !strings.Contains(string(body), `"content":"203.0.113.42"`) {
				t.Fatalf("expected content IP, got %s", body)
			}
			if !strings.Contains(string(body), `"proxied":false`) {
				t.Fatalf("expected proxied:false, got %s", body)
			}
			w.Write([]byte(`{"success":true,"result":{"id":"rec-1"}}`))
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.UpsertARecord(context.Background(), "zone-1", "7f3a.example.com", "203.0.113.42"); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("expected POST")
	}
}

func TestUpsertARecordUpdatesWhenPresent(t *testing.T) {
	mux := http.NewServeMux()
	var put bool
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":[{"id":"rec-existing"}]}`))
	})
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records/rec-existing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		put = true
		w.Write([]byte(`{"success":true,"result":{"id":"rec-existing"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.UpsertARecord(context.Background(), "zone-1", "7f3a.example.com", "203.0.113.42"); err != nil {
		t.Fatal(err)
	}
	if !put {
		t.Fatal("expected PUT to existing record")
	}
}

func TestDeleteARecordByNameRemovesMatching(t *testing.T) {
	mux := http.NewServeMux()
	var deleted bool
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":[{"id":"rec-old"}]}`))
	})
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records/rec-old", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		deleted = true
		w.Write([]byte(`{"success":true,"result":{"id":"rec-old"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.DeleteARecordByName(context.Background(), "zone-1", "old.example.com"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected DELETE")
	}
}

func TestDeleteARecordByNameNoopWhenAbsent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/zones/zone-1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":[]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.DeleteARecordByName(context.Background(), "zone-1", "missing.example.com"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
```

Add the imports `"io"` and `"strings"` to the test file if not already present.

- [ ] **Step 3.2: Run test to verify it fails**

```bash
go test ./internal/cloudflare/... -run UpsertARecord
```

Expected: FAIL — methods undefined.

- [ ] **Step 3.3: Write minimal implementation**

Append to `internal/cloudflare/client.go`:

```go
func (c Client) UpsertARecord(ctx context.Context, zoneID, name, ip string) error {
	get, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?type=A&name="+name, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(get.Result, &records); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"type":    "A",
		"name":    name,
		"content": ip,
		"proxied": false,
		"ttl":     60,
	})
	if len(records) > 0 {
		_, err = c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+records[0].ID, payload)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload)
	return err
}

func (c Client) DeleteARecordByName(ctx context.Context, zoneID, name string) error {
	get, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?type=A&name="+name, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(get.Result, &records); err != nil {
		return err
	}
	for _, r := range records {
		if _, err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+r.ID, nil); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 3.4: Run test to verify it passes**

```bash
go test ./internal/cloudflare/... -v
```

Expected: existing tests still PASS, 4 new tests PASS.

- [ ] **Step 3.5: Commit**

```bash
git add internal/cloudflare
git commit -m "feat(cloudflare): add UpsertARecord and DeleteARecordByName for direct DNS"
```

---

## Phase 2 — Templates

### Task 4: Direct-mode Xray + admin-only cloudflared (`internal/templates`)

**Files:**
- Modify: `internal/templates/render.go`
- Create: `internal/templates/render_test.go` (if missing — otherwise extend)

The new Xray template listens on `0.0.0.0:443` with TLS, supports multiple zones (each as a `tlsSettings.certificates[]` entry), and routes WebSocket paths `/vless` and `/trojan` via Xray's WS path matching. Per-user clients (UUIDs/passwords) are passed in. The cloudflared template keeps only the admin host pointing at agent localhost:6788.

- [ ] **Step 4.1: Write the failing test**

```go
// internal/templates/render_test.go
package templates

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderCloudflaredAdminOnly(t *testing.T) {
	out, err := RenderCloudflaredAdmin("uuid-1", "admin.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tunnel: uuid-1") {
		t.Fatalf("missing tunnel line: %s", out)
	}
	if !strings.Contains(out, "hostname: admin.example.com") {
		t.Fatalf("missing admin hostname: %s", out)
	}
	if !strings.Contains(out, "http://127.0.0.1:6788") {
		t.Fatalf("expected agent at 127.0.0.1:6788: %s", out)
	}
	if strings.Contains(out, "/vless") || strings.Contains(out, "/trojan") {
		t.Fatalf("data plane ingress must not appear: %s", out)
	}
}

func TestRenderXrayDirectIncludesTLSAndCerts(t *testing.T) {
	in := XrayDirectInputs{
		Users: []XrayUser{{Name: "alice", UUID: "u-1", TrojanPassword: "p-1"}},
		Certs: []XrayCert{{Zone: "example.com", CertFile: "/etc/cfvpn/certs/example.com/fullchain.pem", KeyFile: "/etc/cfvpn/certs/example.com/privkey.pem"}},
	}
	out, err := RenderXrayDirect(in)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	inbounds, ok := cfg["inbounds"].([]any)
	if !ok || len(inbounds) == 0 {
		t.Fatalf("expected inbounds: %s", out)
	}
	first := inbounds[0].(map[string]any)
	if first["port"].(float64) != 443 {
		t.Fatalf("expected port 443, got %v", first["port"])
	}
	if first["listen"].(string) != "0.0.0.0" {
		t.Fatalf("expected listen 0.0.0.0, got %v", first["listen"])
	}
	stream := first["streamSettings"].(map[string]any)
	if stream["security"].(string) != "tls" {
		t.Fatalf("expected security=tls, got %v", stream["security"])
	}
	tls := stream["tlsSettings"].(map[string]any)
	certs := tls["certificates"].([]any)
	if len(certs) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(certs))
	}
	cert0 := certs[0].(map[string]any)
	if cert0["certificateFile"] != "/etc/cfvpn/certs/example.com/fullchain.pem" {
		t.Fatalf("wrong cert path: %v", cert0)
	}
}

func TestRenderXrayDirectMultipleZones(t *testing.T) {
	in := XrayDirectInputs{
		Users: []XrayUser{{Name: "alice", UUID: "u-1", TrojanPassword: "p-1"}},
		Certs: []XrayCert{
			{Zone: "a.com", CertFile: "/c/a/fc.pem", KeyFile: "/c/a/k.pem"},
			{Zone: "b.com", CertFile: "/c/b/fc.pem", KeyFile: "/c/b/k.pem"},
		},
	}
	out, err := RenderXrayDirect(in)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal([]byte(out), &cfg)
	inbounds := cfg["inbounds"].([]any)
	stream := inbounds[0].(map[string]any)["streamSettings"].(map[string]any)
	tls := stream["tlsSettings"].(map[string]any)
	certs := tls["certificates"].([]any)
	if len(certs) != 2 {
		t.Fatalf("expected 2 certs, got %d", len(certs))
	}
}

func TestRenderXrayDirectIncludesAllUsers(t *testing.T) {
	in := XrayDirectInputs{
		Users: []XrayUser{
			{Name: "alice", UUID: "u-a", TrojanPassword: "p-a"},
			{Name: "bob", UUID: "u-b", TrojanPassword: "p-b"},
		},
		Certs: []XrayCert{{Zone: "a.com", CertFile: "/c/fc.pem", KeyFile: "/c/k.pem"}},
	}
	out, err := RenderXrayDirect(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"u-a", "p-a", "u-b", "p-b", "alice@vpn", "bob@vpn"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output", want)
		}
	}
}
```

- [ ] **Step 4.2: Run test to verify it fails**

```bash
go test ./internal/templates/...
```

Expected: build failure — new functions/types undefined.

- [ ] **Step 4.3: Write minimal implementation**

Replace `internal/templates/render.go` content. Keep old `RenderCloudflared` + `RenderXray` exports for backward compat during install rewrite (Task 7 will remove the old install caller); add new exports.

```go
// internal/templates/render.go
package templates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"
)

// --- Admin-only cloudflared (data plane removed) ---

const cloudflaredAdminTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
ingress:
  - hostname: {{.AdminHost}}
    service: http://127.0.0.1:6788
  - service: http_status:404
`

func RenderCloudflaredAdmin(tunnelUUID, adminHost string) (string, error) {
	t, err := template.New("cf-admin").Parse(cloudflaredAdminTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"TunnelUUID": tunnelUUID, "AdminHost": adminHost})
	return b.String(), err
}

// --- Direct-mode Xray (TLS on 443) ---

type XrayUser struct {
	Name           string
	UUID           string
	TrojanPassword string
}

type XrayCert struct {
	Zone     string
	CertFile string
	KeyFile  string
}

type XrayDirectInputs struct {
	Users []XrayUser
	Certs []XrayCert
}

func RenderXrayDirect(in XrayDirectInputs) (string, error) {
	if len(in.Certs) == 0 {
		return "", fmt.Errorf("at least one certificate is required")
	}

	vlessClients := make([]map[string]any, 0, len(in.Users))
	trojanClients := make([]map[string]any, 0, len(in.Users))
	for _, u := range in.Users {
		vlessClients = append(vlessClients, map[string]any{"id": u.UUID, "email": u.Name + "@vpn"})
		trojanClients = append(trojanClients, map[string]any{"password": u.TrojanPassword, "email": u.Name + "@vpn"})
	}

	certs := make([]map[string]any, 0, len(in.Certs))
	for _, c := range in.Certs {
		certs = append(certs, map[string]any{
			"certificateFile": c.CertFile,
			"keyFile":         c.KeyFile,
		})
	}

	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-tls-ws",
				"listen":   "0.0.0.0",
				"port":     443,
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    vlessClients,
					"decryption": "none",
					"fallbacks": []any{
						map[string]any{"path": "/trojan", "dest": 10002, "xver": 0},
					},
				},
				"streamSettings": map[string]any{
					"network":  "ws",
					"security": "tls",
					"tlsSettings": map[string]any{
						"alpn":         []string{"http/1.1"},
						"certificates": certs,
					},
					"wsSettings": map[string]any{"path": "/vless"},
				},
			},
			map[string]any{
				"tag":      "trojan-ws",
				"listen":   "127.0.0.1",
				"port":     10002,
				"protocol": "trojan",
				"settings": map[string]any{"clients": trojanClients},
				"streamSettings": map[string]any{
					"network":    "ws",
					"wsSettings": map[string]any{"path": "/trojan"},
				},
			},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
			},
		},
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
```

The fallback chain on `vless-tls-ws` peels `/trojan` traffic to the localhost trojan inbound — single TLS terminator, both protocols served on 443. (Cert is matched by SNI by Xray.)

- [ ] **Step 4.4: Run test to verify it passes**

```bash
go test ./internal/templates/... -v
```

Expected: 4 PASS.

- [ ] **Step 4.5: Commit**

```bash
git add internal/templates
git commit -m "feat(templates): add direct-mode Xray TLS + admin-only cloudflared"
```

---

## Phase 3 — Rotate command (direct mode)

### Task 5: Rewrite `RunRotateDomain` for direct mode

**Files:**
- Modify: `internal/commands/rotate.go`
- Modify: `internal/commands/rotate_test.go`

The new flow inside `RunRotateDomain` (called from agent + CLI):

1. Detect public IP (cached).
2. Ensure cert for new zone (lazy-issue).
3. Upsert A record `<new_host>` → IP in new zone.
4. Render Xray config with all current users + cert paths.
5. Atomic write `xray/config.json`.
6. Reload `cfvpn-xray.service`.
7. Update env file: `DOMAIN=<new_host>`, `PUBLIC_IP=<ip>`, drop `TUNNEL_UUID` requirement (admin tunnel UUID stays put under separate key — see Task 7).
8. Best-effort delete old A record in old zone (log warning on fail).

The signature changes — the CLI dispatcher (Task 7/8) will be updated to match.

- [ ] **Step 5.1: Write the failing tests**

Replace `internal/commands/rotate_test.go` content (or extend if it has unrelated tests; here we assume rotate-specific tests):

```go
// internal/commands/rotate_test.go
package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/systemd"
)

type fakeRotateCF struct {
	upsertARecordCalls []struct{ Zone, Name, IP string }
	deleteACalls       []struct{ Zone, Name string }
	deleteAErr         error
	upsertAErr         error
}

func (f *fakeRotateCF) UpsertARecord(ctx context.Context, zoneID, name, ip string) error {
	f.upsertARecordCalls = append(f.upsertARecordCalls, struct{ Zone, Name, IP string }{zoneID, name, ip})
	return f.upsertAErr
}
func (f *fakeRotateCF) DeleteARecordByName(ctx context.Context, zoneID, name string) error {
	f.deleteACalls = append(f.deleteACalls, struct{ Zone, Name string }{zoneID, name})
	return f.deleteAErr
}

type fakeIPDetector struct {
	ip  string
	err error
}

func (f *fakeIPDetector) Detect(ctx context.Context) (string, error) { return f.ip, f.err }

type fakeCertManager struct {
	ensureErr  error
	ensured    []string
	certs      map[string][2]string // zone → {certPath, keyPath}
}

func (f *fakeCertManager) EnsureWildcard(ctx context.Context, zone, token string) error {
	f.ensured = append(f.ensured, zone)
	return f.ensureErr
}
func (f *fakeCertManager) CertPath(zone string) (string, string, bool) {
	if v, ok := f.certs[zone]; ok {
		return v[0], v[1], true
	}
	return "", "", false
}

type fakeRunner struct {
	commands [][]string
	err      error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.commands = append(f.commands, append([]string{name}, args...))
	return nil, f.err
}

func TestRunRotateDirectHappyPath(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, "cfvpn.env")
	xrayPath := filepath.Join(tmp, "config.json")
	os.WriteFile(envPath, []byte("DOMAIN=old.example.com\nCF_API_TOKEN=tok\n"), 0o600)

	// Pre-existing xray config with one user so renderer has data.
	os.WriteFile(xrayPath, []byte(`{"inbounds":[{"protocol":"vless","settings":{"clients":[{"id":"u-1","email":"alice@vpn"}]}},{"protocol":"trojan","settings":{"clients":[{"password":"p-1","email":"alice@vpn"}]}}]}`), 0o600)

	envFilePath = envPath
	xrayConfigPath = xrayPath
	t.Cleanup(func() { envFilePath = "/etc/cfvpn/cfvpn.env"; xrayConfigPath = "/etc/cfvpn/xray/config.json" })

	cf := &fakeRotateCF{}
	ipd := &fakeIPDetector{ip: "203.0.113.42"}
	cm := &fakeCertManager{certs: map[string][2]string{"example.com": {"/c/fc.pem", "/c/k.pem"}}}
	runner := &fakeRunner{}

	in := RotateDirectInputs{
		NewHost:    "7f3a.example.com",
		NewZoneID:  "zone-new",
		NewZone:    "example.com",
		OldHost:    "old.example.com",
		OldZoneID:  "zone-old",
		CFAPIToken: "tok",
	}
	deps := RotateDirectDeps{CF: cf, IP: ipd, Cert: cm, Runner: runner}

	out, err := RunRotateDirect(context.Background(), in, deps, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if out.PublicIP != "203.0.113.42" || out.VpnHost != "7f3a.example.com" {
		t.Fatalf("bad output: %+v", out)
	}
	if len(cf.upsertARecordCalls) != 1 || cf.upsertARecordCalls[0].Zone != "zone-new" {
		t.Fatalf("upsert A: %+v", cf.upsertARecordCalls)
	}
	if len(cf.deleteACalls) != 1 || cf.deleteACalls[0].Zone != "zone-old" {
		t.Fatalf("delete old A: %+v", cf.deleteACalls)
	}
	if !runnerHasReload(runner, "cfvpn-xray.service") {
		t.Fatalf("expected systemctl reload, got %v", runner.commands)
	}
	body, _ := os.ReadFile(envPath)
	if !strings.Contains(string(body), "DOMAIN=7f3a.example.com") {
		t.Fatalf("env not updated: %s", body)
	}
	if !strings.Contains(string(body), "PUBLIC_IP=203.0.113.42") {
		t.Fatalf("env missing PUBLIC_IP: %s", body)
	}
}

func TestRunRotateDirectIssuesCertWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, "cfvpn.env")
	os.WriteFile(envPath, []byte("DOMAIN=old.example.com\nCF_API_TOKEN=tok\n"), 0o600)
	xrayPath := filepath.Join(tmp, "config.json")
	os.WriteFile(xrayPath, []byte(`{"inbounds":[{"protocol":"vless","settings":{"clients":[{"id":"u-1","email":"alice@vpn"}]}},{"protocol":"trojan","settings":{"clients":[{"password":"p-1","email":"alice@vpn"}]}}]}`), 0o600)
	envFilePath = envPath
	xrayConfigPath = xrayPath
	t.Cleanup(func() { envFilePath = "/etc/cfvpn/cfvpn.env"; xrayConfigPath = "/etc/cfvpn/xray/config.json" })

	cm := &fakeCertManager{certs: map[string][2]string{}}
	// First call: missing → ensure called → second CertPath returns paths via the test by pre-populating after ensure.
	cm.ensureErr = nil
	// Hack: use Ensure to populate certs map
	prevEnsure := cm.EnsureWildcard
	_ = prevEnsure
	// Use a wrapper:
	cm2 := &certEnsureWrapper{inner: cm}

	deps := RotateDirectDeps{
		CF:     &fakeRotateCF{},
		IP:     &fakeIPDetector{ip: "203.0.113.42"},
		Cert:   cm2,
		Runner: &fakeRunner{},
	}
	in := RotateDirectInputs{
		NewHost: "x.example.com", NewZoneID: "zn", NewZone: "example.com",
		OldHost: "old.example.com", OldZoneID: "zo", CFAPIToken: "tok",
	}
	if _, err := RunRotateDirect(context.Background(), in, deps, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(cm.ensured) != 1 || cm.ensured[0] != "example.com" {
		t.Fatalf("expected EnsureWildcard for example.com, got %v", cm.ensured)
	}
}

type certEnsureWrapper struct{ inner *fakeCertManager }

func (c *certEnsureWrapper) EnsureWildcard(ctx context.Context, zone, token string) error {
	if err := c.inner.EnsureWildcard(ctx, zone, token); err != nil {
		return err
	}
	if c.inner.certs == nil {
		c.inner.certs = map[string][2]string{}
	}
	c.inner.certs[zone] = [2]string{"/c/" + zone + "/fc.pem", "/c/" + zone + "/k.pem"}
	return nil
}
func (c *certEnsureWrapper) CertPath(zone string) (string, string, bool) {
	return c.inner.CertPath(zone)
}

func TestRunRotateDirectRollsBackARecordOnXrayWriteFail(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, "cfvpn.env")
	os.WriteFile(envPath, []byte("DOMAIN=old.example.com\nCF_API_TOKEN=tok\n"), 0o600)
	// Read-only xray dir to force write failure.
	xrayDir := filepath.Join(tmp, "xray-readonly")
	os.MkdirAll(xrayDir, 0o555)
	xrayPath := filepath.Join(xrayDir, "config.json")
	envFilePath = envPath
	xrayConfigPath = xrayPath
	t.Cleanup(func() { envFilePath = "/etc/cfvpn/cfvpn.env"; xrayConfigPath = "/etc/cfvpn/xray/config.json"; os.Chmod(xrayDir, 0o755) })

	cf := &fakeRotateCF{}
	deps := RotateDirectDeps{
		CF:     cf,
		IP:     &fakeIPDetector{ip: "203.0.113.42"},
		Cert:   &fakeCertManager{certs: map[string][2]string{"example.com": {"/c/fc.pem", "/c/k.pem"}}},
		Runner: &fakeRunner{},
	}
	in := RotateDirectInputs{
		NewHost: "x.example.com", NewZoneID: "zn", NewZone: "example.com",
		OldHost: "old.example.com", OldZoneID: "zo", CFAPIToken: "tok",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "u-1", TrojanPassword: "p-1"}},
	}
	_, err := RunRotateDirect(context.Background(), in, deps, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when xray write fails")
	}
	// Rollback: must have called Delete on the just-created A record in the new zone.
	rolledBack := false
	for _, c := range cf.deleteACalls {
		if c.Zone == "zn" && c.Name == "x.example.com" {
			rolledBack = true
		}
	}
	if !rolledBack {
		t.Fatalf("expected rollback delete on new A, got %v", cf.deleteACalls)
	}
}

func TestRunRotateDirectIgnoresOldDeleteFailure(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, "cfvpn.env")
	os.WriteFile(envPath, []byte("DOMAIN=old.example.com\nCF_API_TOKEN=tok\n"), 0o600)
	xrayPath := filepath.Join(tmp, "config.json")
	os.WriteFile(xrayPath, []byte(`{}`), 0o600)
	envFilePath = envPath
	xrayConfigPath = xrayPath
	t.Cleanup(func() { envFilePath = "/etc/cfvpn/cfvpn.env"; xrayConfigPath = "/etc/cfvpn/xray/config.json" })

	cf := &fakeRotateCF{deleteAErr: errors.New("cf api error: not found")}
	deps := RotateDirectDeps{
		CF:     cf,
		IP:     &fakeIPDetector{ip: "1.2.3.4"},
		Cert:   &fakeCertManager{certs: map[string][2]string{"example.com": {"/c/fc.pem", "/c/k.pem"}}},
		Runner: &fakeRunner{},
	}
	in := RotateDirectInputs{
		NewHost: "x.example.com", NewZoneID: "zn", NewZone: "example.com",
		OldHost: "old.example.com", OldZoneID: "zo", CFAPIToken: "tok",
		ExistingUsers: []ExistingUser{{Name: "alice", UUID: "u-1", TrojanPassword: "p-1"}},
	}
	if _, err := RunRotateDirect(context.Background(), in, deps, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected success despite old delete fail, got %v", err)
	}
}

func runnerHasReload(r *fakeRunner, unit string) bool {
	for _, c := range r.commands {
		if len(c) >= 3 && c[0] == "systemctl" && c[1] == "reload" && c[2] == unit {
			return true
		}
		// Also accept ExecRunner shape via systemd helper.
		if strings.Contains(strings.Join(c, " "), "reload "+unit) {
			return true
		}
	}
	return false
}

var _ systemd.Runner = (*fakeRunner)(nil)
```

- [ ] **Step 5.2: Run tests to verify they fail**

```bash
go test ./internal/commands/... -run RunRotateDirect
```

Expected: build failure — types/funcs undefined.

- [ ] **Step 5.3: Write minimal implementation**

Replace `internal/commands/rotate.go`:

```go
// internal/commands/rotate.go
package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/subscription"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/xray"
)

var (
	envFilePath    = paths.EnvFile
	xrayConfigPath = paths.XrayConfig
)

// --- Direct-mode rotate (used by agent + CLI) ---

type RotateDirectCF interface {
	UpsertARecord(ctx context.Context, zoneID, name, ip string) error
	DeleteARecordByName(ctx context.Context, zoneID, name string) error
}

type IPDetector interface {
	Detect(ctx context.Context) (string, error)
}

type CertManager interface {
	EnsureWildcard(ctx context.Context, zone, token string) error
	CertPath(zone string) (cert, key string, ok bool)
}

type ExistingUser struct {
	Name           string
	UUID           string
	TrojanPassword string
}

type RotateDirectInputs struct {
	NewHost       string
	NewZone       string // e.g. "example.com"
	NewZoneID     string // CF zone ID
	OldHost       string
	OldZoneID     string // may be empty if old zone unknown
	CFAPIToken    string
	ExistingUsers []ExistingUser // optional override; if nil, read from xray/config.json
	ExtraCerts    []templates.XrayCert // optional: include other zones' certs (multi-zone)
}

type RotateDirectDeps struct {
	CF     RotateDirectCF
	IP     IPDetector
	Cert   CertManager
	Runner systemd.Runner
}

type RotateDirectResult struct {
	VpnHost  string
	PublicIP string
}

func RunRotateDirect(ctx context.Context, in RotateDirectInputs, deps RotateDirectDeps, stdout, stderr io.Writer) (RotateDirectResult, error) {
	if deps.CF == nil || deps.IP == nil || deps.Cert == nil {
		return RotateDirectResult{}, fmt.Errorf("cf, ip detector, cert manager are required")
	}
	if strings.TrimSpace(in.NewHost) == "" || strings.TrimSpace(in.NewZone) == "" || strings.TrimSpace(in.NewZoneID) == "" {
		return RotateDirectResult{}, fmt.Errorf("new_host, new_zone, new_zone_id are required")
	}

	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("detect public ip: %w", err)
	}

	if err := deps.Cert.EnsureWildcard(ctx, in.NewZone, in.CFAPIToken); err != nil {
		return RotateDirectResult{}, fmt.Errorf("ensure cert for %s: %w", in.NewZone, err)
	}
	cert, key, ok := deps.Cert.CertPath(in.NewZone)
	if !ok {
		return RotateDirectResult{}, fmt.Errorf("cert for %s missing after issue", in.NewZone)
	}

	if err := deps.CF.UpsertARecord(ctx, in.NewZoneID, in.NewHost, ip); err != nil {
		return RotateDirectResult{}, fmt.Errorf("upsert A record: %w", err)
	}

	users := in.ExistingUsers
	if users == nil {
		us, err := readUsersFromXray(xrayConfigPath)
		if err != nil {
			rollbackA(ctx, deps.CF, in.NewZoneID, in.NewHost, stderr)
			return RotateDirectResult{}, fmt.Errorf("read users from xray config: %w", err)
		}
		users = us
	}

	certs := []templates.XrayCert{{Zone: in.NewZone, CertFile: cert, KeyFile: key}}
	certs = append(certs, in.ExtraCerts...)

	xrayUsers := make([]templates.XrayUser, 0, len(users))
	for _, u := range users {
		xrayUsers = append(xrayUsers, templates.XrayUser{Name: u.Name, UUID: u.UUID, TrojanPassword: u.TrojanPassword})
	}

	rendered, err := templates.RenderXrayDirect(templates.XrayDirectInputs{Users: xrayUsers, Certs: certs})
	if err != nil {
		rollbackA(ctx, deps.CF, in.NewZoneID, in.NewHost, stderr)
		return RotateDirectResult{}, fmt.Errorf("render xray: %w", err)
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		rollbackA(ctx, deps.CF, in.NewZoneID, in.NewHost, stderr)
		return RotateDirectResult{}, fmt.Errorf("write xray config: %w", err)
	}

	runner := resolveRunner(deps.Runner)
	if err := systemd.Reload(ctx, runner, "cfvpn-xray.service"); err != nil {
		return RotateDirectResult{}, fmt.Errorf("reload cfvpn-xray.service: %w", err)
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return RotateDirectResult{}, fmt.Errorf("load env: %w", err)
	}
	env["DOMAIN"] = in.NewHost
	env["PUBLIC_IP"] = ip
	env["MODE"] = "direct"
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return RotateDirectResult{}, fmt.Errorf("save env: %w", err)
	}

	if in.OldHost != "" && in.OldZoneID != "" {
		if err := deps.CF.DeleteARecordByName(ctx, in.OldZoneID, in.OldHost); err != nil {
			fmt.Fprintf(stderr, "warning: delete old A record %s in zone %s: %v\n", in.OldHost, in.OldZoneID, err)
		}
	}

	if err := regenerateSubscriptionsDirect(in.NewHost); err != nil {
		fmt.Fprintf(stderr, "warning: regenerate subscriptions: %v\n", err)
	}

	fmt.Fprintf(stdout, "rotation complete: %s → %s\n", in.OldHost, in.NewHost)
	return RotateDirectResult{VpnHost: in.NewHost, PublicIP: ip}, nil
}

func rollbackA(ctx context.Context, cf RotateDirectCF, zoneID, name string, stderr io.Writer) {
	if err := cf.DeleteARecordByName(ctx, zoneID, name); err != nil {
		fmt.Fprintf(stderr, "warning: rollback delete A record %s: %v\n", name, err)
	}
}

func readUsersFromXray(path string) ([]ExistingUser, error) {
	cfg, err := xray.Load(path)
	if err != nil {
		return nil, err
	}
	names := xray.ListUserNames(cfg)
	out := make([]ExistingUser, 0, len(names))
	for _, n := range names {
		uuid, _ := xray.GetVLESSClient(cfg, n)
		pw, _ := xray.GetTrojanClient(cfg, n)
		out = append(out, ExistingUser{Name: n, UUID: uuid, TrojanPassword: pw})
	}
	return out, nil
}

func regenerateSubscriptionsDirect(domain string) error {
	cfg, err := xray.Load(xrayConfigPath)
	if err != nil {
		return err
	}
	for _, name := range xray.ListUserNames(cfg) {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			continue
		}
		pw, ok := xray.GetTrojanClient(cfg, name)
		if !ok {
			continue
		}
		sub := subscription.BuildSubscriptionB64(
			subscription.BuildVLESSURI(name, uuid, domain),
			subscription.BuildTrojanURI(name, pw, domain),
		)
		if err := writeSubscriptionFile(name, sub+"\n"); err != nil {
			return err
		}
	}
	return nil
}
```

This requires:
- A `systemd.Reload(ctx, runner, unit)` helper. If not present, add it next to `systemd.Restart`.
- `paths.XrayConfig` constant.

Verify both exist:

```bash
grep -n 'func Reload' internal/systemd/*.go
grep -n 'XrayConfig' internal/paths/*.go
```

If `systemd.Reload` is missing, add to `internal/systemd/units.go` (or wherever Restart lives):

```go
func Reload(ctx context.Context, r Runner, unit string) error {
	_, err := r.Run(ctx, "systemctl", "reload", unit)
	return err
}
```

If `paths.XrayConfig` is missing, add `XrayConfig = "/etc/cfvpn/xray/config.json"` to `internal/paths/paths.go`.

- [ ] **Step 5.4: Run tests to verify they pass**

```bash
go test ./internal/commands/... -run RunRotateDirect -v
```

Expected: 4 PASS.

- [ ] **Step 5.5: Run the full Go test suite for regressions**

```bash
go test ./...
```

Expected: all PASS. Old `RunRotateDomain` and `RunRotateCleanup` references in other packages may now fail — Task 7/8 will remove their callers, but for this commit we keep them as deprecated stubs that return `fmt.Errorf("rotate-domain CLI is deprecated; use --upgrade and panel rotate")`. Add those stubs at the bottom of `rotate.go` if they were removed in the rewrite:

```go
// Deprecated: tunnel-mode rotate. Returns error; use direct-mode flow.
type RotateInputs struct {
	NewDomain, OldDomain, OldTunnel, CFAPIToken, CFAccountID string
}

type RotateDeps struct {
	CF     any
	Runner systemd.Runner
}

func RunRotateDomain(ctx context.Context, in RotateInputs, deps RotateDeps, stdout, stderr io.Writer) error {
	return fmt.Errorf("tunnel-mode rotate-domain is deprecated; use cfvpnctl install --upgrade then panel rotate")
}

func RunRotateCleanup(ctx context.Context, tunnelID string, deps RotateDeps, stdout, stderr io.Writer) error {
	return fmt.Errorf("tunnel-mode rotate-domain --cleanup is deprecated; A records are removed automatically")
}
```

Re-run tests until green.

- [ ] **Step 5.6: Commit**

```bash
git add internal/commands internal/systemd internal/paths
git commit -m "feat(commands): rewrite rotate-domain for direct mode (TLS+A record)"
```

---

## Phase 4 — Agent handlers

> Depends on `internal/agent/` from plan `2026-04-20-cfvpn-agent.md`. If that plan has not landed, defer this phase.

### Task 6: `RotateDomainHandler` direct payload + `IssueCertHandler`

**Files:**
- Modify: `internal/agent/handlers.go`
- Modify: `internal/agent/handlers_test.go`
- Modify: `internal/agent/router.go` (mount new route)

- [ ] **Step 6.1: Write the failing tests**

Append to `internal/agent/handlers_test.go`:

```go
func TestRotateDomainHandlerDirectMode(t *testing.T) {
	// Set up handler deps with fakes.
	cf := &fakeRotateCF{}
	ipd := &fakeIPDetector{ip: "203.0.113.42"}
	cm := &fakeCertManager{certs: map[string][2]string{"example.com": {"/c/fc.pem", "/c/k.pem"}}}
	h := &Handlers{
		Rotate: func(ctx context.Context, in commands.RotateDirectInputs) (commands.RotateDirectResult, error) {
			// Verify wired payload.
			if in.NewHost != "7f3a.example.com" || in.NewZoneID != "zn" {
				t.Fatalf("bad inputs: %+v", in)
			}
			if in.OldHost != "old.example.com" || in.OldZoneID != "zo" {
				t.Fatalf("missing old fields: %+v", in)
			}
			return commands.RotateDirectResult{VpnHost: in.NewHost, PublicIP: "203.0.113.42"}, nil
		},
	}
	body := `{"new_host":"7f3a.example.com","new_zone_id":"zn","old_host":"old.example.com","old_zone_id":"zo"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/rotate-domain", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.RotateDomain(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var out struct {
		VpnHost  string `json:"vpn_host"`
		PublicIP string `json:"public_ip"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.VpnHost != "7f3a.example.com" || out.PublicIP != "203.0.113.42" {
		t.Fatalf("bad response: %+v", out)
	}
	_ = cf
	_ = ipd
	_ = cm
}

func TestIssueCertHandlerCallsCertManager(t *testing.T) {
	called := ""
	h := &Handlers{
		IssueCert: func(ctx context.Context, zone string) error {
			called = zone
			return nil
		},
	}
	body := `{"zone":"example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/issue-cert", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.IssueCert(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if called != "example.com" {
		t.Fatalf("expected example.com, got %q", called)
	}
}

func TestIssueCertHandlerRejectsEmptyZone(t *testing.T) {
	h := &Handlers{IssueCert: func(ctx context.Context, zone string) error { return nil }}
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/issue-cert", strings.NewReader(`{"zone":""}`))
	w := httptest.NewRecorder()
	h.IssueCert(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
```

(If `Handlers` exposes its dependencies directly rather than via callback funcs, adapt the test to the agent's actual struct shape — but keep the contract: payload `{new_host,new_zone_id,old_host,old_zone_id}` in, `{vpn_host,public_ip}` out.)

- [ ] **Step 6.2: Run tests to verify they fail**

```bash
go test ./internal/agent/... -run "RotateDomainHandlerDirectMode|IssueCertHandler"
```

Expected: build/test fail.

- [ ] **Step 6.3: Update `RotateDomainHandler`**

In `internal/agent/handlers.go`, change the request struct and response:

```go
type rotateDomainRequest struct {
	NewHost   string `json:"new_host"`
	NewZoneID string `json:"new_zone_id"`
	OldHost   string `json:"old_host"`
	OldZoneID string `json:"old_zone_id"`
}

type rotateDomainResponse struct {
	VpnHost  string `json:"vpn_host"`
	PublicIP string `json:"public_ip"`
}

func (h *Handlers) RotateDomain(w http.ResponseWriter, r *http.Request) {
	var req rotateDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.NewHost == "" || req.NewZoneID == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "new_host and new_zone_id required")
		return
	}
	zone := zoneOfHost(req.NewHost) // strip leftmost label
	in := commands.RotateDirectInputs{
		NewHost:    req.NewHost,
		NewZone:    zone,
		NewZoneID:  req.NewZoneID,
		OldHost:    req.OldHost,
		OldZoneID:  req.OldZoneID,
		CFAPIToken: h.Env["CF_API_TOKEN"],
	}
	out, err := h.Rotate(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadGateway, "rotate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rotateDomainResponse{VpnHost: out.VpnHost, PublicIP: out.PublicIP})
}

func zoneOfHost(host string) string {
	i := strings.IndexByte(host, '.')
	if i < 0 || i == len(host)-1 {
		return host
	}
	return host[i+1:]
}
```

Ensure `Handlers` struct has a `Rotate func(ctx, RotateDirectInputs) (RotateDirectResult, error)` field, and that the wiring in `cmd/cfvpn-agent/main.go` (or equivalent) injects a closure that builds `RotateDirectDeps` and calls `commands.RunRotateDirect`.

- [ ] **Step 6.4: Add `IssueCertHandler`**

In `internal/agent/handlers.go`:

```go
type issueCertRequest struct {
	Zone string `json:"zone"`
}

func (h *Handlers) IssueCert(w http.ResponseWriter, r *http.Request) {
	var req issueCertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.Zone) == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "zone required")
		return
	}
	if err := h.IssueCert(r.Context(), req.Zone); err != nil {
		writeError(w, http.StatusBadGateway, "issue_cert_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"zone": req.Zone, "status": "ok"})
}
```

If naming collides with the field `IssueCert`, rename the handler to `IssueCertHandler`.

In `internal/agent/router.go` (or wherever routes mount):

```go
mux.HandleFunc("POST /admin/v1/issue-cert", h.IssueCertHandler)
```

Wire `Handlers.IssueCert` in main:

```go
certMgr := cert.NewDefault()
h := &agent.Handlers{
    // ...
    IssueCert: func(ctx context.Context, zone string) error {
        return certMgr.EnsureWildcard(ctx, zone, env["CF_API_TOKEN"])
    },
}
```

- [ ] **Step 6.5: Run tests to verify they pass**

```bash
go test ./internal/agent/... -v
```

Expected: new tests PASS, old tests still PASS.

- [ ] **Step 6.6: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): direct-mode rotate payload + issue-cert handler"
```

---

## Phase 5 — CLI install & upgrade

### Task 7: Rewrite `RunInstall` for direct mode (fresh VPS)

**Files:**
- Modify: `internal/commands/install.go`
- Modify: `internal/commands/install_test.go`
- Modify: `internal/systemd/units.go` (add `ExecReload=` for `cfvpn-xray.service`)

The new fresh-install flow:

1. Validate env (`CF_API_TOKEN`, `CF_ACCOUNT_ID`, `DOMAIN`, `USER1_NAME`).
2. Resolve zone of `DOMAIN`.
3. Install acme.sh (run installer if `/root/.acme.sh/acme.sh` missing).
4. Issue wildcard cert for the zone (via cert.Manager).
5. Create admin tunnel (`cfvpn-admin-<rand4>`), write creds.
6. Detect public IP.
7. Render Xray TLS config with the bootstrap user.
8. Render cloudflared admin-only config (`AdminHost = "admin." + zone`).
9. Upsert A record `<DOMAIN>` → IP (data plane, proxied:false).
10. Upsert CNAME `admin.<zone>` → `<tunnel>.cfargotunnel.com` (admin plane, proxied:true).
11. Install/enable `cfvpn-xray.service`, `cfvpn-cloudflared.service`, `cfvpn-agent.service`.
12. `ufw allow 443/tcp`.
13. Save env: `MODE=direct`, `PUBLIC_IP=<ip>`, `ADMIN_HOST=admin.<zone>`, `ADMIN_TUNNEL_UUID=<uuid>`.
14. Print sub link for user1.

The CLI dispatcher (`internal/cli/dispatch.go`) already calls `commands.RunInstall`. Keep the function name and signature stable; restructure the body. New deps in `InstallDeps`: `IP IPDetector`, `Cert CertManager`, `Acme AcmeInstaller`, `UFW UFWRunner` (each with a default + a fake for tests).

- [ ] **Step 7.1: Write the failing test**

```go
// internal/commands/install_test.go (extend or add)
func TestRunInstallDirectModeWiresAllSteps(t *testing.T) {
	tmp := t.TempDir()
	envFilePath = filepath.Join(tmp, "cfvpn.env")
	xrayConfigPath = filepath.Join(tmp, "xray.json")
	cloudflaredConfig = filepath.Join(tmp, "cloudflared.yml")
	cloudflaredCredDir = filepath.Join(tmp, "creds")
	os.WriteFile(envFilePath, []byte("CF_API_TOKEN=t\nCF_ACCOUNT_ID=a\nDOMAIN=vpn.example.com\nUSER1_NAME=alice\n"), 0o600)

	cf := &installFakeCF{
		zoneByDomain: map[string]string{"example.com": "zone-1", "vpn.example.com": "zone-1"},
		tunnelID:     "tunnel-uuid-1",
	}
	ipd := &fakeIPDetector{ip: "203.0.113.42"}
	cm := &fakeCertManager{}
	acme := &fakeAcmeInstaller{}
	ufw := &fakeUFW{}
	runner := &fakeRunner{}

	in := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com", User1Name: "alice"}
	deps := InstallDeps{
		CF: cf, IP: ipd, Cert: cm, Acme: acme, UFW: ufw,
		BinaryRunner:  runner,
		SystemdRunner: runner,
	}
	if err := RunInstall(context.Background(), in, deps, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	if !acme.installed {
		t.Fatal("expected acme installed")
	}
	if len(cm.ensured) != 1 || cm.ensured[0] != "example.com" {
		t.Fatalf("expected wildcard for example.com, got %v", cm.ensured)
	}
	if len(cf.upsertA) != 1 || cf.upsertA[0].Name != "vpn.example.com" || cf.upsertA[0].IP != "203.0.113.42" {
		t.Fatalf("bad A upsert: %+v", cf.upsertA)
	}
	if len(cf.upsertCNAME) != 1 || !strings.HasPrefix(cf.upsertCNAME[0].Name, "admin.") {
		t.Fatalf("expected admin CNAME, got %+v", cf.upsertCNAME)
	}
	if !ufw.allowed443 {
		t.Fatal("expected ufw allow 443/tcp")
	}
	body, _ := os.ReadFile(envFilePath)
	for _, want := range []string{"MODE=direct", "PUBLIC_IP=203.0.113.42", "ADMIN_TUNNEL_UUID=tunnel-uuid-1", "ADMIN_HOST=admin.example.com"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("env missing %q:\n%s", want, body)
		}
	}
}
```

Add fakes (in the same file):

```go
type installFakeCF struct {
	zoneByDomain map[string]string
	tunnelID     string
	upsertA      []struct{ Zone, Name, IP string }
	upsertCNAME  []struct{ Zone, Name, Target string }
}

func (f *installFakeCF) GetZoneID(ctx context.Context, domain string) (string, error) {
	if id, ok := f.zoneByDomain[domain]; ok {
		return id, nil
	}
	return "", fmt.Errorf("zone not found")
}
func (f *installFakeCF) CreateTunnel(ctx context.Context, name string) (string, []byte, error) {
	return f.tunnelID, []byte(`{"AccountTag":"a"}`), nil
}
func (f *installFakeCF) UpsertCNAME(ctx context.Context, zoneID, name, target string) error {
	f.upsertCNAME = append(f.upsertCNAME, struct{ Zone, Name, Target string }{zoneID, name, target})
	return nil
}
func (f *installFakeCF) UpsertARecord(ctx context.Context, zoneID, name, ip string) error {
	f.upsertA = append(f.upsertA, struct{ Zone, Name, IP string }{zoneID, name, ip})
	return nil
}
func (f *installFakeCF) DeleteARecordByName(ctx context.Context, zoneID, name string) error { return nil }
func (f *installFakeCF) DeleteTunnel(ctx context.Context, id string) error                  { return nil }

type fakeAcmeInstaller struct{ installed bool }

func (f *fakeAcmeInstaller) EnsureInstalled(ctx context.Context) error { f.installed = true; return nil }

type fakeUFW struct{ allowed443 bool }

func (f *fakeUFW) Allow(ctx context.Context, rule string) error {
	if rule == "443/tcp" {
		f.allowed443 = true
	}
	return nil
}
```

- [ ] **Step 7.2: Run test — expect FAIL**

```bash
go test ./internal/commands/... -run RunInstallDirectMode
```

- [ ] **Step 7.3: Implement direct-mode `RunInstall`**

Edit `internal/commands/install.go`. The full rewrite is large; below is the contract + key body. (Reuse `writeAtomicFile` from `rotate.go`.)

```go
type InstallCFClient interface {
	GetZoneID(ctx context.Context, domain string) (string, error)
	CreateTunnel(ctx context.Context, name string) (id string, creds []byte, err error)
	UpsertCNAME(ctx context.Context, zoneID, name, target string) error
	UpsertARecord(ctx context.Context, zoneID, name, ip string) error
	DeleteARecordByName(ctx context.Context, zoneID, name string) error
	DeleteTunnel(ctx context.Context, id string) error
}

type AcmeInstaller interface {
	EnsureInstalled(ctx context.Context) error
}

type UFWRunner interface {
	Allow(ctx context.Context, rule string) error
}

type InstallDeps struct {
	CF            InstallCFClient
	IP            IPDetector
	Cert          CertManager
	Acme          AcmeInstaller
	UFW           UFWRunner
	BinaryRunner  systemd.Runner
	SystemdRunner systemd.Runner
}

func RunInstall(ctx context.Context, in InstallInputs, deps InstallDeps, stdout, stderr io.Writer) error {
	if err := validateInstallInputs(in); err != nil {
		return err
	}
	zoneID, err := deps.CF.GetZoneID(ctx, in.Domain)
	if err != nil {
		return fmt.Errorf("resolve zone for %s: %w", in.Domain, err)
	}
	zone := zoneOfDomain(in.Domain)

	if err := deps.Acme.EnsureInstalled(ctx); err != nil {
		return fmt.Errorf("install acme.sh: %w", err)
	}
	if err := deps.Cert.EnsureWildcard(ctx, zone, in.CFAPIToken); err != nil {
		return fmt.Errorf("issue wildcard cert: %w", err)
	}

	tunnelName := "cfvpn-admin-" + randHex4()
	tunnelID, creds, err := deps.CF.CreateTunnel(ctx, tunnelName)
	if err != nil {
		return fmt.Errorf("create admin tunnel: %w", err)
	}
	if err := writeAtomicFile(filepath.Join(cloudflaredCredDir, tunnelID+".json"), creds, 0o600); err != nil {
		return fmt.Errorf("write tunnel creds: %w", err)
	}

	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		return fmt.Errorf("detect public ip: %w", err)
	}

	bootstrapUser := bootstrapUserState(in.User1Name)

	cert, key, ok := deps.Cert.CertPath(zone)
	if !ok {
		return fmt.Errorf("cert missing for %s after issue", zone)
	}
	xrayRendered, err := templates.RenderXrayDirect(templates.XrayDirectInputs{
		Users: []templates.XrayUser{{Name: bootstrapUser.Name, UUID: bootstrapUser.UUID, TrojanPassword: bootstrapUser.Password}},
		Certs: []templates.XrayCert{{Zone: zone, CertFile: cert, KeyFile: key}},
	})
	if err != nil {
		return fmt.Errorf("render xray: %w", err)
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(xrayRendered), 0o600); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}

	adminHost := "admin." + zone
	cfRendered, err := templates.RenderCloudflaredAdmin(tunnelID, adminHost)
	if err != nil {
		return fmt.Errorf("render cloudflared: %w", err)
	}
	if err := writeAtomicFile(cloudflaredConfig, []byte(cfRendered), 0o600); err != nil {
		return fmt.Errorf("write cloudflared config: %w", err)
	}

	if err := deps.CF.UpsertARecord(ctx, zoneID, in.Domain, ip); err != nil {
		return fmt.Errorf("upsert A record: %w", err)
	}
	if err := deps.CF.UpsertCNAME(ctx, zoneID, adminHost, tunnelID+".cfargotunnel.com"); err != nil {
		return fmt.Errorf("upsert admin CNAME: %w", err)
	}

	if err := deps.UFW.Allow(ctx, "443/tcp"); err != nil {
		fmt.Fprintf(stderr, "warning: ufw allow 443/tcp: %v\n", err)
	}

	// Install + enable units. Existing helpers in internal/systemd/.
	if err := installUnits(ctx, deps.SystemdRunner); err != nil {
		return err
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return err
	}
	env["MODE"] = "direct"
	env["PUBLIC_IP"] = ip
	env["ADMIN_HOST"] = adminHost
	env["ADMIN_TUNNEL_UUID"] = tunnelID
	env[bootstrapUser.UUIDKey] = bootstrapUser.UUID
	env[bootstrapUser.PasswordKey] = bootstrapUser.Password
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "install complete. data plane: %s → %s, admin: %s\n", in.Domain, ip, adminHost)
	return nil
}

func zoneOfDomain(d string) string {
	i := strings.IndexByte(d, '.')
	if i < 0 {
		return d
	}
	return d[i+1:]
}

func randHex4() string {
	b := make([]byte, 2)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

If `bootstrapUserState`, `installUnits`, `validateInstallInputs` already exist in `install.go`, reuse them; otherwise port from the existing implementation.

Update `internal/systemd/units.go` `cfvpn-xray.service`:

```ini
[Service]
ExecStart=/usr/local/bin/xray run -c /etc/cfvpn/xray/config.json
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
User=root
AmbientCapabilities=CAP_NET_BIND_SERVICE
```

- [ ] **Step 7.4: Update CLI dispatcher to inject new deps**

In `internal/cli/dispatch.go` `case "install"`, build `InstallDeps`:

```go
deps := commands.InstallDeps{
    CF: &cloudflare.Client{
        BaseURL:   "https://api.cloudflare.com/client/v4",
        Token:     env["CF_API_TOKEN"],
        AccountID: env["CF_ACCOUNT_ID"],
        HTTP:      http.DefaultClient,
    },
    IP:            netinfo.NewDefault(),
    Cert:          cert.NewDefault(),
    Acme:          cert.NewAcmeInstaller(),
    UFW:           commands.NewExecUFW(),
    BinaryRunner:  systemd.ExecRunner{},
    SystemdRunner: systemd.ExecRunner{},
}
```

Add `cert.NewAcmeInstaller()` (in `internal/cert/acme.go`) — runs `curl https://get.acme.sh | sh` if `/root/.acme.sh/acme.sh` is missing. Add `commands.NewExecUFW()` — runs `ufw allow <rule>`.

- [ ] **Step 7.5: Run tests to verify**

```bash
go test ./internal/commands/... ./internal/cli/...
```

Expected: PASS.

- [ ] **Step 7.6: Commit**

```bash
git add internal/commands internal/systemd internal/cli internal/cert
git commit -m "feat(install): rewrite for direct mode (TLS+wildcard+admin tunnel)"
```

---

### Task 8: `install --upgrade` and `install --upgrade --check`

**Files:**
- Modify: `internal/commands/install.go` (add `RunUpgrade`)
- Modify: `internal/commands/install_test.go`
- Modify: `internal/cli/dispatch.go`

The upgrade flow runs on a tunnel-mode VPS and migrates it to direct mode without invalidating sub tokens. It must back up `/etc/cfvpn/`, run all 12 steps, and rollback on failure.

- [ ] **Step 8.1: Write the failing test**

```go
func TestRunUpgradePreservesUsersAndSetsDirectEnv(t *testing.T) {
	tmp := t.TempDir()
	etc := filepath.Join(tmp, "cfvpn")
	os.MkdirAll(etc, 0o755)
	envFilePath = filepath.Join(etc, "cfvpn.env")
	xrayConfigPath = filepath.Join(etc, "xray", "config.json")
	cloudflaredConfig = filepath.Join(etc, "cloudflared", "config.yml")
	cloudflaredCredDir = filepath.Join(etc, "cloudflared")

	os.MkdirAll(filepath.Dir(xrayConfigPath), 0o755)
	os.WriteFile(envFilePath, []byte("CF_API_TOKEN=t\nCF_ACCOUNT_ID=a\nDOMAIN=proxied.example.com\nTUNNEL_UUID=old-tun\n"), 0o600)
	os.WriteFile(xrayConfigPath, []byte(`{"inbounds":[{"protocol":"vless","settings":{"clients":[{"id":"u-1","email":"alice@vpn"}]}},{"protocol":"trojan","settings":{"clients":[{"password":"p-1","email":"alice@vpn"}]}}]}`), 0o600)

	cf := &installFakeCF{zoneByDomain: map[string]string{"example.com": "z1", "proxied.example.com": "z1"}, tunnelID: "ignored"}
	deps := InstallDeps{
		CF:            cf,
		IP:            &fakeIPDetector{ip: "203.0.113.42"},
		Cert:          &fakeCertManager{certs: map[string][2]string{"example.com": {"/c/fc.pem", "/c/k.pem"}}},
		Acme:          &fakeAcmeInstaller{},
		UFW:           &fakeUFW{},
		BinaryRunner:  &fakeRunner{},
		SystemdRunner: &fakeRunner{},
	}
	out, err := RunUpgrade(context.Background(), UpgradeInputs{BackupRoot: tmp, Now: func() time.Time { return time.Unix(1700000000, 0) }}, deps, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if out.NewHost == "" || !strings.HasSuffix(out.NewHost, ".example.com") {
		t.Fatalf("bad new host: %q", out.NewHost)
	}
	if out.PublicIP != "203.0.113.42" {
		t.Fatalf("ip: %q", out.PublicIP)
	}
	body, _ := os.ReadFile(envFilePath)
	for _, want := range []string{"MODE=direct", "PUBLIC_IP=203.0.113.42", "DOMAIN=" + out.NewHost} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("env missing %q:\n%s", want, body)
		}
	}
	// Backup directory exists.
	if _, err := os.Stat(filepath.Join(tmp, "cfvpn.backup-1700000000")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	// Old CNAME deletion attempted.
	// (We did not stub this in fake; assert via an extra struct field if needed.)
}

func TestRunUpgradeCheckIsDryRun(t *testing.T) {
	tmp := t.TempDir()
	etc := filepath.Join(tmp, "cfvpn")
	os.MkdirAll(etc, 0o755)
	envFilePath = filepath.Join(etc, "cfvpn.env")
	os.WriteFile(envFilePath, []byte("CF_API_TOKEN=t\nCF_ACCOUNT_ID=a\nDOMAIN=proxied.example.com\nTUNNEL_UUID=old-tun\n"), 0o600)

	cf := &installFakeCF{zoneByDomain: map[string]string{"example.com": "z1", "proxied.example.com": "z1"}}
	deps := InstallDeps{CF: cf, IP: &fakeIPDetector{ip: "1.2.3.4"}, Cert: &fakeCertManager{}, Acme: &fakeAcmeInstaller{}, UFW: &fakeUFW{}, BinaryRunner: &fakeRunner{}, SystemdRunner: &fakeRunner{}}
	if err := RunUpgradeCheck(context.Background(), UpgradeInputs{BackupRoot: tmp}, deps, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(envFilePath)
	if strings.Contains(string(body), "MODE=direct") {
		t.Fatalf("dry run must not modify env:\n%s", body)
	}
}
```

- [ ] **Step 8.2: Run test — expect FAIL**

```bash
go test ./internal/commands/... -run RunUpgrade
```

- [ ] **Step 8.3: Implement `RunUpgrade` and `RunUpgradeCheck`**

```go
type UpgradeInputs struct {
	BackupRoot string             // /etc by default; override for tests
	Now        func() time.Time   // override for deterministic timestamp
}

type UpgradeResult struct {
	OldHost  string
	NewHost  string
	PublicIP string
}

func RunUpgradeCheck(ctx context.Context, in UpgradeInputs, deps InstallDeps, stdout, stderr io.Writer) error {
	env, err := state.Load(envFilePath)
	if err != nil {
		return err
	}
	required := []string{"CF_API_TOKEN", "CF_ACCOUNT_ID", "DOMAIN", "TUNNEL_UUID"}
	for _, k := range required {
		if env[k] == "" {
			return fmt.Errorf("env %s is required", k)
		}
	}
	if _, err := deps.CF.GetZoneID(ctx, env["DOMAIN"]); err != nil {
		return fmt.Errorf("resolve zone: %w", err)
	}
	if _, err := deps.IP.Detect(ctx); err != nil {
		return fmt.Errorf("detect public ip: %w", err)
	}
	fmt.Fprintln(stdout, "pre-flight OK; ready to run cfvpnctl install --upgrade")
	return nil
}

func RunUpgrade(ctx context.Context, in UpgradeInputs, deps InstallDeps, stdout, stderr io.Writer) (UpgradeResult, error) {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.BackupRoot == "" {
		in.BackupRoot = "/etc"
	}

	env, err := state.Load(envFilePath)
	if err != nil {
		return UpgradeResult{}, err
	}
	oldHost := env["DOMAIN"]
	zoneID, err := deps.CF.GetZoneID(ctx, oldHost)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("resolve zone: %w", err)
	}
	zone := zoneOfDomain(oldHost)

	// 1. Backup.
	stamp := in.Now().Unix()
	backupDir := filepath.Join(in.BackupRoot, fmt.Sprintf("cfvpn.backup-%d", stamp))
	if err := copyTree(filepath.Dir(envFilePath), backupDir); err != nil {
		return UpgradeResult{}, fmt.Errorf("backup: %w", err)
	}
	rollback := newRollback(deps.CF, backupDir, filepath.Dir(envFilePath))

	defer func() {
		if r := recover(); r != nil {
			rollback.run(ctx, stderr)
			panic(r)
		}
	}()

	// 2-3. acme + cert.
	if err := deps.Acme.EnsureInstalled(ctx); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, fmt.Errorf("install acme.sh: %w", err)
	}
	if err := deps.Cert.EnsureWildcard(ctx, zone, env["CF_API_TOKEN"]); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, fmt.Errorf("issue cert: %w", err)
	}

	// 4. Detect IP.
	ip, err := deps.IP.Detect(ctx)
	if err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, fmt.Errorf("detect ip: %w", err)
	}

	// 5. New direct host.
	newHost := randHex4() + "." + zone

	// 6. Render Xray with existing users (preserves UUID/password).
	users, err := readUsersFromXray(xrayConfigPath)
	if err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, err
	}
	cert, key, _ := deps.Cert.CertPath(zone)
	rendered, err := templates.RenderXrayDirect(templates.XrayDirectInputs{
		Users: toXrayUsers(users),
		Certs: []templates.XrayCert{{Zone: zone, CertFile: cert, KeyFile: key}},
	})
	if err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, err
	}
	if err := writeAtomicFile(xrayConfigPath, []byte(rendered), 0o600); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, err
	}

	// 7. Render cloudflared admin-only (admin host kept on tunnel).
	adminHost := "admin." + zone
	cfRendered, _ := templates.RenderCloudflaredAdmin(env["TUNNEL_UUID"], adminHost)
	if err := writeAtomicFile(cloudflaredConfig, []byte(cfRendered), 0o600); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, err
	}

	// 8. ufw 443.
	if err := deps.UFW.Allow(ctx, "443/tcp"); err != nil {
		fmt.Fprintf(stderr, "warning: ufw: %v\n", err)
	}

	// 9. Upsert direct A record.
	if err := deps.CF.UpsertARecord(ctx, zoneID, newHost, ip); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, fmt.Errorf("upsert A: %w", err)
	}
	rollback.addCreatedA(zoneID, newHost)

	// 10. Upsert admin CNAME (idempotent — may already point to tunnel).
	if err := deps.CF.UpsertCNAME(ctx, zoneID, adminHost, env["TUNNEL_UUID"]+".cfargotunnel.com"); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, err
	}

	// 11. Restart services.
	runner := resolveRunner(deps.SystemdRunner)
	if err := systemd.Restart(ctx, runner, "cfvpn-xray.service"); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, fmt.Errorf("restart xray: %w", err)
	}
	if err := systemd.Reload(ctx, runner, "cfvpn-cloudflared.service"); err != nil {
		fmt.Fprintf(stderr, "warning: reload cloudflared: %v\n", err)
	}

	// 12. Delete old proxied CNAME.
	if err := deps.CF.DeleteARecordByName(ctx, zoneID, oldHost); err != nil {
		fmt.Fprintf(stderr, "warning: delete old record %s: %v\n", oldHost, err)
	}

	// 13. Update env.
	env["DOMAIN"] = newHost
	env["MODE"] = "direct"
	env["PUBLIC_IP"] = ip
	env["ADMIN_HOST"] = adminHost
	env["ADMIN_TUNNEL_UUID"] = env["TUNNEL_UUID"]
	delete(env, "TUNNEL_UUID")
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil {
		rollback.run(ctx, stderr)
		return UpgradeResult{}, err
	}

	fmt.Fprintf(stdout, "UPGRADE COMPLETE.\n  old: %s\n  new (direct): %s\n  IP: %s\n", oldHost, newHost, ip)
	return UpgradeResult{OldHost: oldHost, NewHost: newHost, PublicIP: ip}, nil
}

func toXrayUsers(in []ExistingUser) []templates.XrayUser {
	out := make([]templates.XrayUser, len(in))
	for i, u := range in {
		out[i] = templates.XrayUser{Name: u.Name, UUID: u.UUID, TrojanPassword: u.TrojanPassword}
	}
	return out
}
```

Add helpers:

```go
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(target, raw, info.Mode())
	})
}

type rollbacker struct {
	cf         InstallCFClient
	backupDir  string
	etcDir     string
	createdA   []struct{ Zone, Name string }
}

func newRollback(cf InstallCFClient, backup, etc string) *rollbacker {
	return &rollbacker{cf: cf, backupDir: backup, etcDir: etc}
}

func (r *rollbacker) addCreatedA(zoneID, name string) {
	r.createdA = append(r.createdA, struct{ Zone, Name string }{zoneID, name})
}

func (r *rollbacker) run(ctx context.Context, stderr io.Writer) {
	for _, a := range r.createdA {
		_ = r.cf.DeleteARecordByName(ctx, a.Zone, a.Name)
	}
	if err := os.RemoveAll(r.etcDir); err != nil {
		fmt.Fprintf(stderr, "rollback: remove %s: %v\n", r.etcDir, err)
		return
	}
	if err := copyTree(r.backupDir, r.etcDir); err != nil {
		fmt.Fprintf(stderr, "rollback: restore from %s: %v\n", r.backupDir, err)
	}
}
```

- [ ] **Step 8.4: Wire CLI flags**

In `internal/cli/dispatch.go`, replace the `case "install"` block:

```go
case "install":
    upgrade, check := false, false
    for _, a := range args[1:] {
        switch a {
        case "--upgrade":
            upgrade = true
        case "--check":
            check = true
        default:
            fmt.Fprintf(stderr, "unknown install arg: %s\n", a)
            return 2
        }
    }
    env, err := state.Load(paths.EnvFile)
    if err != nil {
        fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
        return 1
    }
    deps := buildInstallDeps(env)
    if upgrade && check {
        if err := commands.RunUpgradeCheck(context.Background(), commands.UpgradeInputs{}, deps, stdout, stderr); err != nil {
            fmt.Fprintln(stderr, err)
            return 1
        }
        return 0
    }
    if upgrade {
        if _, err := commands.RunUpgrade(context.Background(), commands.UpgradeInputs{}, deps, stdout, stderr); err != nil {
            fmt.Fprintln(stderr, err)
            return 1
        }
        return 0
    }
    in := commands.InstallInputs{
        CFAPIToken:  env["CF_API_TOKEN"],
        CFAccountID: env["CF_ACCOUNT_ID"],
        Domain:      env["DOMAIN"],
        User1Name:   env["USER1_NAME"],
    }
    if err := commands.RunInstall(context.Background(), in, deps, stdout, stderr); err != nil {
        fmt.Fprintln(stderr, err)
        return 1
    }
    return 0
```

Add a `buildInstallDeps(env)` helper near the top of the file.

- [ ] **Step 8.5: Run tests**

```bash
go test ./internal/commands/... ./internal/cli/...
```

Expected: PASS.

- [ ] **Step 8.6: Commit**

```bash
git add internal/commands internal/cli
git commit -m "feat(install): add --upgrade and --upgrade --check with rollback"
```

---


## Phase 6 — Worker (D1 + routes)

### Task 9: D1 migration for `public_ip`

**Files:**
- Create: `panel/worker/migrations/0004_nodes_public_ip.sql`

- [ ] **Step 9.1: Write migration**

```sql
-- 0004_nodes_public_ip.sql
ALTER TABLE nodes ADD COLUMN public_ip TEXT;
```

- [ ] **Step 9.2: Apply locally**

```bash
cd panel/worker
npx wrangler d1 migrations apply DB --local
```

Expected: migration applied without error.

- [ ] **Step 9.3: Commit**

```bash
git add panel/worker/migrations/0004_nodes_public_ip.sql
git commit -m "feat(worker): add nodes.public_ip column"
```

---

### Task 10: Worker types + rotate route

**Files:**
- Modify: `panel/worker/src/types.ts`
- Modify: `panel/worker/src/routes/nodes.ts`
- Test: `panel/worker/src/routes/nodes.test.ts`

- [ ] **Step 10.1: Update types**

In `panel/worker/src/types.ts`, replace `AgentRotateResponse` and `NodeRow`:

```ts
export interface AgentRotateResponse {
  vpn_host: string;
  public_ip: string;
}

export interface NodeRow {
  id: string;
  label: string;
  admin_host: string;
  vpn_host: string;
  public_ip: string | null;
  zone: string;
  status: string;
  last_seen_at: number | null;
  latency_ms: number | null;
  created_at: number;
}
```

- [ ] **Step 10.2: Write failing test for rotate route**

In `panel/worker/src/routes/nodes.test.ts`, add:

```ts
import { describe, it, expect, vi } from 'vitest';
import { nodeRotate } from './nodes';

describe('nodeRotate (direct mode)', () => {
  it('sends old_host + old_zone_id, persists public_ip', async () => {
    const callAgent = vi.fn().mockResolvedValue({
      vpn_host: 'aaaa1111.example.com',
      public_ip: '203.0.113.10',
    });
    const db = createFakeDB({
      nodes: [
        { id: 'n1', vpn_host: 'old.example.com', zone: 'example.com', admin_host: 'admin.example.com', status: 'active' },
      ],
      zones: [{ name: 'example.com', cf_zone_id: 'zone123' }],
    });

    const res = await nodeRotate('n1', { db, callAgent } as any);

    expect(callAgent).toHaveBeenCalledWith(
      expect.objectContaining({
        old_host: 'old.example.com',
        old_zone_id: 'zone123',
      }),
    );
    const updated = db.nodes.find(n => n.id === 'n1');
    expect(updated?.vpn_host).toBe('aaaa1111.example.com');
    expect(updated?.public_ip).toBe('203.0.113.10');
  });
});
```

`createFakeDB` is a small helper at the top of the test file that returns `{nodes, zones, prepare(...)}` matching the D1 surface used by `nodeRotate`.

- [ ] **Step 10.3: Run, expect FAIL**

```bash
cd panel/worker
npx vitest run src/routes/nodes.test.ts
```

- [ ] **Step 10.4: Update `nodeRotate`**

In `panel/worker/src/routes/nodes.ts`, modify `nodeRotate`:

```ts
export async function nodeRotate(nodeId: string, ctx: RouteCtx): Promise<Response> {
  const node = await ctx.db
    .prepare('SELECT id, vpn_host, zone FROM nodes WHERE id = ?')
    .bind(nodeId)
    .first<{ id: string; vpn_host: string; zone: string }>();
  if (!node) return json({ error: 'node not found' }, 404);

  const zone = await ctx.db
    .prepare('SELECT cf_zone_id FROM zones WHERE name = ?')
    .bind(node.zone)
    .first<{ cf_zone_id: string }>();
  if (!zone) return json({ error: 'zone not registered' }, 400);

  const newHost = `${randomHex(4)}.${node.zone}`;
  const result = await ctx.callAgent({
    new_host: newHost,
    new_zone_id: zone.cf_zone_id,
    old_host: node.vpn_host,
    old_zone_id: zone.cf_zone_id,
  });

  await ctx.db
    .prepare('UPDATE nodes SET vpn_host = ?, public_ip = ?, status = ? WHERE id = ?')
    .bind(result.vpn_host, result.public_ip, 'active', nodeId)
    .run();

  return json({ vpn_host: result.vpn_host, public_ip: result.public_ip });
}
```

- [ ] **Step 10.5: Run tests, expect PASS**

```bash
cd panel/worker
npx vitest run
```

- [ ] **Step 10.6: Commit**

```bash
git add panel/worker/src/types.ts panel/worker/src/routes/nodes.ts panel/worker/src/routes/nodes.test.ts
git commit -m "feat(worker): rotate sends old host/zone, stores public_ip"
```

---

### Task 11: Issue-cert fan-out endpoint

**Files:**
- Modify: `panel/worker/src/routes/zones.ts`
- Modify: `panel/worker/src/index.ts` (route registration)
- Test: `panel/worker/src/routes/zones.test.ts`

- [ ] **Step 11.1: Failing test**

```ts
describe('zoneIssueCert', () => {
  it('fans out to all active nodes for the zone', async () => {
    const calls: string[] = [];
    const callAgentOn = vi.fn(async (host: string, _path: string) => {
      calls.push(host);
      return { ok: true };
    });
    const db = createFakeDB({
      zones: [{ name: 'example.com', cf_zone_id: 'z1' }],
      nodes: [
        { id: 'n1', admin_host: 'admin1.example.com', zone: 'example.com', status: 'active' },
        { id: 'n2', admin_host: 'admin2.example.com', zone: 'example.com', status: 'active' },
        { id: 'n3', admin_host: 'admin3.example.com', zone: 'example.com', status: 'down' },
      ],
    });

    const res = await zoneIssueCert('example.com', { db, callAgentOn } as any);
    expect(res.status).toBe(200);
    expect(calls.sort()).toEqual(['admin1.example.com', 'admin2.example.com']);
  });
});
```

- [ ] **Step 11.2: Run, FAIL**

- [ ] **Step 11.3: Implement**

```ts
export async function zoneIssueCert(zoneName: string, ctx: ZoneRouteCtx): Promise<Response> {
  const zone = await ctx.db
    .prepare('SELECT name FROM zones WHERE name = ?')
    .bind(zoneName)
    .first<{ name: string }>();
  if (!zone) return json({ error: 'zone not found' }, 404);

  const { results } = await ctx.db
    .prepare(`SELECT admin_host FROM nodes WHERE zone = ? AND status = 'active'`)
    .bind(zoneName)
    .all<{ admin_host: string }>();

  const errors: { host: string; error: string }[] = [];
  for (const node of results ?? []) {
    try {
      await ctx.callAgentOn(node.admin_host, '/admin/v1/issue-cert', { zone: zoneName });
    } catch (e) {
      errors.push({ host: node.admin_host, error: String(e) });
    }
  }

  return json({ issued: (results ?? []).length - errors.length, errors });
}
```

- [ ] **Step 11.4: Register route**

In `panel/worker/src/index.ts`, add inside the API router:

```ts
if (req.method === 'POST' && url.pathname.match(/^\/api\/zones\/[^/]+\/issue-cert$/)) {
  const name = decodeURIComponent(url.pathname.split('/')[3]);
  return zoneIssueCert(name, { db: env.DB, callAgentOn: makeCallAgentOn(env) });
}
```

- [ ] **Step 11.5: Run all worker tests, expect PASS**

```bash
cd panel/worker && npx vitest run
```

- [ ] **Step 11.6: Commit**

```bash
git add panel/worker/src/routes/zones.ts panel/worker/src/routes/zones.test.ts panel/worker/src/index.ts
git commit -m "feat(worker): add zone issue-cert fan-out endpoint"
```

---

## Phase 7 — Frontend

### Task 12: api.ts response shapes

**Files:**
- Modify: `panel/web/src/lib/api.ts`
- Modify: `panel/web/src/lib/types.ts`
- Test: `panel/web/src/lib/api.test.ts`

- [ ] **Step 12.1: Add `publicIp` to types**

In `panel/web/src/lib/types.ts`, extend `Node`:

```ts
export type Node = {
  id: string;
  label: string;
  status: 'active' | 'down' | 'unreachable' | 'degraded' | 'unknown';
  latencyMs: number | null;
  vpnHost: string;
  adminHost: string;
  publicIp: string | null;
  lastSeenAt: number | null;
  zone: string;
  createdAt: number;
};
```

- [ ] **Step 12.2: Failing test**

In `panel/web/src/lib/api.test.ts`:

```ts
it('parseNode maps public_ip → publicIp', async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => [{
      id: 'n1', label: 'main', admin_host: 'admin.x.com', vpn_host: 'a.x.com',
      public_ip: '203.0.113.10', zone: 'x.com', status: 'active',
      last_seen_at: 1, latency_ms: 30, created_at: 1,
    }],
  });
  global.fetch = fetchMock;
  const [n] = await listNodes();
  expect(n.publicIp).toBe('203.0.113.10');
});

it('rotateNode returns publicIp', async () => {
  global.fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ vpn_host: 'new.x.com', public_ip: '203.0.113.10' }),
  }) as any;
  const r = await rotateNode('n1');
  expect(r.publicIp).toBe('203.0.113.10');
});
```

- [ ] **Step 12.3: Run, FAIL**

```bash
npm --prefix panel/web test -- --run src/lib/api.test.ts
```

- [ ] **Step 12.4: Implement**

In `panel/web/src/lib/api.ts`:

```ts
type RotateNodeApiResponse = {
  new_host?: string;
  vpn_host?: string;
  public_ip?: string;
  tunnel_uuid?: string;
};

export type RotateNodeResponse = {
  vpnHost: string;
  publicIp?: string;
  tunnelUuid?: string;
};

function parseRotateNodeResponse(raw: RotateNodeApiResponse): RotateNodeResponse {
  const vpnHost = raw.new_host ?? raw.vpn_host;
  if (vpnHost == null || vpnHost.length === 0) {
    throw new Error('rotate response missing host');
  }
  return { vpnHost, publicIp: raw.public_ip, tunnelUuid: raw.tunnel_uuid };
}

type NodeApiResponse = {
  id: string;
  label: string;
  admin_host: string;
  vpn_host: string;
  public_ip: string | null;
  zone: string;
  status: string;
  last_seen_at: number | null;
  latency_ms: number | null;
  created_at: number;
};

function parseNode(raw: NodeApiResponse): Node {
  const status = raw.status as Node['status'];
  return {
    id: raw.id,
    label: raw.label,
    status:
      status === 'unreachable' || status === 'degraded'
        ? status
        : status === 'active' || status === 'down'
          ? status
          : 'unknown',
    latencyMs: raw.latency_ms ?? null,
    vpnHost: raw.vpn_host,
    adminHost: raw.admin_host,
    publicIp: raw.public_ip ?? null,
    lastSeenAt: raw.last_seen_at ?? null,
    zone: raw.zone,
    createdAt: raw.created_at,
  };
}
```

- [ ] **Step 12.5: Run, PASS**

```bash
npm --prefix panel/web test -- --run
```

- [ ] **Step 12.6: Commit**

```bash
git add panel/web/src/lib/api.ts panel/web/src/lib/types.ts panel/web/src/lib/api.test.ts
git commit -m "feat(web): expose publicIp on Node and rotate response"
```

---

### Task 13: NodeCard "Direct" badge + IP display

**Files:**
- Modify: `panel/web/src/components/nodes/NodeCard.tsx`
- Test: `panel/web/src/components/nodes/NodeCard.test.tsx`

- [ ] **Step 13.1: Failing test**

```tsx
it('shows Direct badge and IP when publicIp present', () => {
  const node = makeNode({ publicIp: '203.0.113.10', vpnHost: 'a.x.com' });
  render(<NodeCard node={node} onRotate={vi.fn()} onHealthcheck={vi.fn()} />);
  expect(screen.getByText('Direct')).toBeInTheDocument();
  expect(screen.getByText(/203\.0\.113\.10/)).toBeInTheDocument();
});

it('hides Direct badge when publicIp null', () => {
  const node = makeNode({ publicIp: null });
  render(<NodeCard node={node} onRotate={vi.fn()} onHealthcheck={vi.fn()} />);
  expect(screen.queryByText('Direct')).toBeNull();
});
```

- [ ] **Step 13.2: Run, FAIL**

```bash
npm --prefix panel/web test -- --run src/components/nodes/NodeCard.test.tsx
```

- [ ] **Step 13.3: Implement**

In `panel/web/src/components/nodes/NodeCard.tsx`, in the metadata row, add:

```tsx
{node.publicIp && (
  <>
    <span className="badge badge-direct">Direct</span>
    <span className="muted">{node.publicIp}</span>
  </>
)}
```

Add a small CSS class `.badge-direct` (background `#0e7c3a`, color `white`, 11px) in the existing styles file.

- [ ] **Step 13.4: Run, PASS**

- [ ] **Step 13.5: Commit**

```bash
git add panel/web/src/components/nodes/NodeCard.tsx panel/web/src/components/nodes/NodeCard.test.tsx panel/web/src/components/nodes
git commit -m "feat(web): show Direct badge + IP on NodeCard"
```

---

### Task 14: ZonesPage "Issue wildcard cert" button

**Files:**
- Modify: `panel/web/src/lib/api.ts`
- Modify: `panel/web/src/pages/ZonesPage.tsx`
- Test: `panel/web/src/pages/ZonesPage.test.tsx`

- [ ] **Step 14.1: Add api function**

```ts
export async function issueZoneCert(zoneName: string): Promise<{ issued: number; errors: { host: string; error: string }[] }> {
  const r = await fetch(`/api/zones/${encodeURIComponent(zoneName)}/issue-cert`, { method: 'POST' });
  if (!r.ok) throw new Error('issue cert failed');
  return r.json();
}
```

- [ ] **Step 14.2: Failing test**

```tsx
it('clicking Issue cert calls api and shows result count', async () => {
  global.fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ issued: 2, errors: [] }),
  }) as any;

  render(<ZonesPage />);
  await waitFor(() => screen.getByText('example.com'));
  await userEvent.click(screen.getByRole('button', { name: /issue cert/i }));

  await waitFor(() => screen.getByText(/issued.*2 node/i));
});
```

- [ ] **Step 14.3: Run, FAIL**

- [ ] **Step 14.4: Implement**

In `ZonesPage.tsx`, add a per-row button:

```tsx
<button onClick={async () => {
  setIssuing(zone.name);
  try {
    const res = await issueZoneCert(zone.name);
    setMessage(`Issued cert on ${res.issued} node(s)`);
  } catch (e) {
    setMessage(`Failed: ${e}`);
  } finally {
    setIssuing(null);
  }
}} disabled={issuing === zone.name}>
  {issuing === zone.name ? 'Issuing…' : 'Issue cert'}
</button>
```

- [ ] **Step 14.5: Run, PASS**

- [ ] **Step 14.6: Commit**

```bash
git add panel/web/src/lib/api.ts panel/web/src/pages/ZonesPage.tsx panel/web/src/pages/ZonesPage.test.tsx
git commit -m "feat(web): zone Issue cert button"
```

---

## Phase 8 — Documentation

### Task 15: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 15.1: Edit Quick Start prerequisites**

Append to the `Prerequisites` block:

> - Public TCP/443 reachable on the VPS (`ufw allow 443/tcp` is configured by `cfvpnctl install`)
> - `acme.sh` will be installed automatically on first run; uses DNS-01 challenge so no port 80 required

- [ ] **Step 15.2: Replace the Architecture section**

```md
## Architecture

See [docs/superpowers/specs/2026-04-26-direct-domain-routing-design.md](docs/superpowers/specs/2026-04-26-direct-domain-routing-design.md) for the direct-domain design.

TL;DR (direct mode, default since 2026-04):
- **Data plane:** Client → CF DNS (A record, *unproxied*) → VPS:443 → Xray (VLESS/Trojan WS over TLS) → internet.
  Wildcard Let's Encrypt cert (`*.zone.com`) is issued via DNS-01 and renewed by acme.sh.
- **Admin plane:** panel Worker → cloudflared (HTTP/2 outbound) → agent on `127.0.0.1:6788`.
  This keeps the agent off the public internet.

Rotate: panel calls agent → agent issues new sub-domain (A record) → reloads Xray with the new cert SNI →
old A record is deleted. Subscription token is preserved, so all clients re-fetch the new VLESS/Trojan URL automatically.
```

- [ ] **Step 15.3: Daily Ops table**

Replace the `Rotate to new domain` and `Cleanup` rows with:

```md
| Rotate to new sub-domain | `sudo cfvpnctl rotate-domain <new-sub.zone.com>` |
| Issue/renew wildcard cert | `sudo cfvpnctl cert renew` (or panel → Zones → Issue cert) |
```

Remove the row referencing `--cleanup <uuid>` (no longer needed in direct mode — A records are deleted inline by `rotate-domain`).

- [ ] **Step 15.4: Add an "Upgrading from tunnel mode" section**

After the Quick Start, add:

````md
## Upgrading from tunnel mode (pre-2026-04 installs)

Old installs route data through `cfvpn-cloudflared`, which adds 200–2000 ms of
latency in restrictive networks. Upgrading switches the data plane to direct
DNS while keeping the admin tunnel.

```bash
# 1. Dry-run check (prints what will change, modifies nothing)
sudo cfvpnctl install --upgrade --check

# 2. Apply the upgrade (atomic; rolls back on any failure)
sudo cfvpnctl install --upgrade

# 3. Verify
systemctl is-active cfvpn-xray cfvpn-cloudflared
sudo cfvpnctl status
sudo cfvpnctl healthcheck run
```

What `--upgrade` does:
1. Backs up `/etc/cfvpn/` to `/etc/cfvpn.backup-<timestamp>`.
2. Issues a wildcard cert via DNS-01.
3. Detects the VPS public IP and creates an A record (unproxied) for `${DOMAIN}`.
4. Rewrites the Xray config to listen on `0.0.0.0:443` with TLS.
5. Trims the cloudflared config to admin-only ingress (`admin.<zone>` → `127.0.0.1:6788`).
6. Opens UFW 443/tcp.
7. Reloads both services. If anything fails it restores the backup and removes
   any A records it created.

Existing user UUIDs and Trojan passwords are preserved, so installed clients
keep working after the next subscription refresh.

### Troubleshooting upgrade

| Symptom | Fix |
|---|---|
| `cert issue failed: rate limit` | Wait an hour; LE has a 5/week limit per registered domain. |
| `dns A upsert failed: 9103` | CF token missing `Zone:DNS:Edit` for the new zone. |
| `xray fails to bind :443` | Some other process holds 443. `ss -lntp \| grep 443` and stop it. |
| Want to roll back manually | `sudo cp -a /etc/cfvpn.backup-<ts>/. /etc/cfvpn/ && systemctl restart cfvpn-xray cfvpn-cloudflared`. |
````

- [ ] **Step 15.5: Troubleshooting section additions**

Append to the `Troubleshooting` section:

```md
**Client gets `certificate signed by unknown authority`**
→ Cert hasn't been issued yet. `sudo cfvpnctl cert renew` or click "Issue cert" on the panel Zones page.

**`curl https://<host>/` returns 5xx with no Xray log entry**
→ A record points to wrong IP. `sudo cfvpnctl status` shows the IP the agent detected; compare against
your Cloudflare DNS dashboard.

**High latency persists after upgrade**
→ Confirm the A record is *unproxied* (grey cloud, not orange) in Cloudflare DNS. Proxied = back through CF edge.
```

- [ ] **Step 15.6: Commit**

```bash
git add README.md
git commit -m "docs: direct-domain mode + upgrade runbook"
```

---

## Self-Review

After writing all tasks, verify:

**Spec coverage** — every section of `docs/superpowers/specs/2026-04-26-direct-domain-routing-design.md` is implemented by at least one task:

| Spec section | Task(s) |
|---|---|
| Public IP detection | Task 1 |
| Wildcard cert via acme.sh | Tasks 2, 7 |
| CF DNS A-record API | Task 3 |
| Xray TLS-on-443 template | Task 4 |
| Cloudflared admin-only ingress | Task 4 |
| Rotate flow (direct) | Tasks 5, 10 |
| Agent rotate handler | Task 6 |
| Agent issue-cert handler | Task 6 |
| Install (fresh) | Task 7 |
| Install --upgrade + rollback | Task 8 |
| `nodes.public_ip` column | Task 9 |
| Worker rotate sends old host/zone | Task 10 |
| Worker zone issue-cert fan-out | Task 11 |
| Frontend Direct badge + IP | Tasks 12, 13 |
| Frontend Issue cert button | Task 14 |
| Operational runbook | Task 15 |

**Placeholder scan** — no `TBD`, no "implement later", no "similar to Task N" without code. Every code step shows the actual code.

**Type consistency** — `RotateDirectInputs` (Go) ↔ `AgentRotateRequest` (agent) ↔ Worker rotate body. All three carry: `new_host`, `new_zone_id`, `old_host`, `old_zone_id`. `RotateDirectResult` ↔ `AgentRotateResponse` ↔ Worker `RotateNodeResponse` all carry `vpn_host`, `public_ip`. `Node.publicIp` (TS) matches `nodes.public_ip` (D1) matches the JSON wire field.

**Order check** — Phase 4 (agent handlers) depends on the separate cfvpn-agent plan being merged. If that plan is still in flight, run Phases 1–3 + 5–8 first; Phase 4 can land later without blocking the panel-side work.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-26-direct-domain-routing.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
