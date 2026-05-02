# Reality + XHTTP Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Resumability:** Future sessions resume by scanning unchecked `- [ ]` boxes top-to-bottom. After finishing each task, mark its checkboxes `- [x]` and append a `<!-- DONE @ <commit-sha> on <date>: <one-line note> -->` comment immediately under the task header. Do NOT delete completed tasks; the trail is the audit log.

**Goal:** Migrate VPN protocol so `direct mode` uses VLESS+XTLS-Reality (Vision flow), `cloudflare mode` uses VLESS+HTTPUpgrade (no WS), apply to install + upgrade + rotate, change default WS path `/vless` to `/api/v1/sync`, and ship a panel + D1 schema bump so subscription URIs are produced correctly per node.

**Architecture:**
- **Direct nodes** (port 443 reachable, no Cloudflare proxy in front): Reality with `dest=www.microsoft.com:443`, `serverNames=[www.microsoft.com]`, X25519 keypair + 8-byte shortId per node. Vision flow (`xtls-rprx-vision`). No Let's Encrypt cert needed.
- **Cloudflare-tunnel nodes**: VLESS HTTPUpgrade listening 127.0.0.1:10001, behind cloudflared. Path moved from `/vless` → `/api/v1/sync` (neutral). XHTTP was tested and FAILED through cloudflared (stream-up: 404, stream-one: 403 via CF edge); HTTPUpgrade works because it uses HTTP/1.1 upgrade handshake compatible with cloudflared's HTTP transport.
- **Hysteria2** unchanged in both modes.
- Mode auto-detected at install: if outbound 443 reachable & no existing service binds it, default to `direct` + Reality; else `cloudflare` + XHTTP.
- All node-specific Reality params + xhttp_path persisted to `/etc/cfvpn/cfvpn.env`, exposed via agent `/admin/v1/sync` + `/status`, mirrored to D1 `nodes` table, consumed by panel subscription URI builder.
- Manual JPY-02 baseline (already migrated to Reality by hand on 2026-05-02) is the reference config; agent code must produce byte-equivalent output for that node so re-running install/upgrade is a no-op.

**Tech Stack:** Go 1.22+ (agent), Xray-core 26.x (`xray x25519` for keygen, `openssl rand` fallback for shortId), Cloudflare Workers + D1 + TS (panel), systemd, Hysteria2 (unchanged), Lego (cert; skipped for Reality direct nodes).

**Reference state (JPY-02, source of truth for byte-equivalence):**
- Domain `media-62b2dea9.888vn.net` → `185.200.65.215`, mode=direct
- PrivateKey `QPTgOMQeFazVzKeLaHafW89CpJN6mQoPZl9Lsi66I34`
- PublicKey `RiBOkNi6x0-rADpYQ8SE_7Jmpzb6d2W1l3ULDRcHKF0`
- shortId `d3cbbc0b4c5bc5f9`
- dest `www.microsoft.com:443`, serverName `www.microsoft.com`
- User `kulinh` UUID `b3252cd7-d1c5-4f7f-b257-ef750ac838c9` (must be preserved across migration)
- Backup: `/etc/cfvpn/xray/config.json.bak-pre-reality-1777689067`

---

## Phase 0: XHTTP-through-cloudflared Spike

Validate that XHTTP `stream-up` actually streams cleanly through a Cloudflare tunnel before committing the cloudflare-mode rewrite. If this fails we fall back to HTTPUpgrade for cloudflare mode.

### Task 0.1: Manual XHTTP test on a CF-mode node

**Files:** none (manual ssh session). Record findings in this plan as a comment under the task.

- [ ] **Step 1: Pick a CF-mode test node**

Run: `cd /opt/cf-vpn/panel/worker && wrangler d1 execute cfvpn --remote --command "SELECT id, label, vpn_host, mode FROM nodes WHERE mode='cloudflare' ORDER BY created_at DESC LIMIT 3;"`
Expected: list of CF nodes. Pick the one whose label contains "test" or the most recent.

- [ ] **Step 2: SSH and back up config**

```bash
ssh -i /tmp/rwl01 root@<NODE_IP>
cp /etc/cfvpn/xray/config.json /etc/cfvpn/xray/config.json.bak-pre-xhttp-$(date +%s)
```

- [ ] **Step 3: Patch xray inbound to XHTTP stream-up**

Replace the `streamSettings` block of the vless inbound with:

```json
"streamSettings": {
  "network": "xhttp",
  "xhttpSettings": {
    "path": "/api/v1/sync",
    "host": "<vpn_host>",
    "mode": "stream-up"
  }
}
```

Patch cloudflared ingress `path` regex from `^/vless$` → `^/api/v1/sync$`.

- [ ] **Step 4: Restart and validate**

```bash
xray -test -c /etc/cfvpn/xray/config.json
systemctl restart cfvpn-xray.service cfvpn-cloudflared.service
journalctl -u cfvpn-xray.service -u cfvpn-cloudflared.service --since="2 min ago"
```
Expected: both services active, no protocol errors.

- [ ] **Step 5: Client-side smoke (Shadowrocket / v2rayN)**

Build a manual XHTTP URI: `vless://<UUID>@<vpn_host>:443?encryption=none&security=tls&type=xhttp&host=<vpn_host>&path=%2Fapi%2Fv1%2Fsync&mode=stream-up&sni=<vpn_host>#XHTTP-test`
Pull subscription, connect, browse. Record latency vs WS baseline.

- [ ] **Step 6: Roll back to WS if any failure**

```bash
mv /etc/cfvpn/xray/config.json.bak-pre-xhttp-* /etc/cfvpn/xray/config.json
systemctl restart cfvpn-xray.service cfvpn-cloudflared.service
```

- [ ] **Step 7: Record verdict in this file**

Append a comment: `<!-- PHASE0 VERDICT: <pass|fail> — <details on latency, throughput, any blockers>. Decision: <proceed with XHTTP | fall back to HTTPUpgrade> -->`

---

## Phase 1: Core Templates + URI Builders (Go)

Add new render functions and URI builders without touching install/upgrade flows yet. Pure-function additions, fully unit-tested.

### Task 1.1: Neutral path constant

**Files:**
- Create: `internal/templates/paths.go`

- [ ] **Step 1: Write the constant + a helper test**

Create `internal/templates/paths.go`:
```go
package templates

// VLESSPath is the neutral request path used by both XHTTP (cloudflare mode)
// and any legacy WS endpoints during transition. Was "/vless"; renamed to
// avoid being a GFW signature.
const VLESSPath = "/api/v1/sync"
```

Create `internal/templates/paths_test.go`:
```go
package templates

import "testing"

func TestVLESSPathIsNeutral(t *testing.T) {
	if VLESSPath != "/api/v1/sync" {
		t.Fatalf("VLESSPath got %q want /api/v1/sync", VLESSPath)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/templates/ -run TestVLESSPathIsNeutral -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/templates/paths.go internal/templates/paths_test.go
git commit -m "feat(templates): add neutral VLESSPath constant"
```

### Task 1.2: RenderXrayDirectReality

**Files:**
- Modify: `internal/templates/render.go`
- Modify: `internal/templates/render_test.go`

- [ ] **Step 1: Write failing test in `internal/templates/render_test.go`**

```go
func TestRenderXrayDirectReality(t *testing.T) {
	out, err := RenderXrayDirectReality(XrayDirectRealityInputs{
		Users:        []XrayUser{{Name: "alice", UUID: "uuid-a"}},
		PrivateKey:   "priv-x25519",
		ShortIDs:     []string{"d3cbbc0b4c5bc5f9"},
		Dest:         "www.microsoft.com:443",
		ServerNames:  []string{"www.microsoft.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"flow": "xtls-rprx-vision"`,
		`"security": "reality"`,
		`"dest": "www.microsoft.com:443"`,
		`"privateKey": "priv-x25519"`,
		`"shortIds"`,
		`"d3cbbc0b4c5bc5f9"`,
		`"alice@vpn"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/templates/ -run TestRenderXrayDirectReality -v`
Expected: FAIL (`undefined: RenderXrayDirectReality`).

- [ ] **Step 3: Implement in `internal/templates/render.go`**

Add types + function:
```go
type XrayDirectRealityInputs struct {
	Users       []XrayUser
	PrivateKey  string
	ShortIDs    []string
	Dest        string   // e.g. "www.microsoft.com:443"
	ServerNames []string // e.g. []string{"www.microsoft.com"}
}

func RenderXrayDirectReality(in XrayDirectRealityInputs) (string, error) {
	if in.PrivateKey == "" || in.Dest == "" || len(in.ServerNames) == 0 || len(in.ShortIDs) == 0 {
		return "", errors.New("reality requires privateKey, dest, serverNames, shortIds")
	}
	clients := make([]map[string]string, 0, len(in.Users))
	for _, u := range in.Users {
		clients = append(clients, map[string]string{
			"id":    u.UUID,
			"email": u.Name + "@vpn",
			"flow":  "xtls-rprx-vision",
		})
	}
	cfg := map[string]any{
		"log": map[string]string{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-reality",
				"listen":   "0.0.0.0",
				"port":     443,
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    clients,
					"decryption": "none",
				},
				"streamSettings": map[string]any{
					"network":  "tcp",
					"security": "reality",
					"realitySettings": map[string]any{
						"show":        false,
						"dest":        in.Dest,
						"xver":        0,
						"serverNames": in.ServerNames,
						"privateKey":  in.PrivateKey,
						"shortIds":    in.ShortIDs,
					},
				},
			},
		},
		"outbounds": []map[string]any{
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
```

- [ ] **Step 4: Run test to confirm pass**

Run: `go test ./internal/templates/ -run TestRenderXrayDirectReality -v`
Expected: PASS.

- [ ] **Step 5: Byte-equivalence test against JPY-02 backup**

Manually scp `/etc/cfvpn/xray/config.json` from JPY-02 to `internal/templates/testdata/jpy02_reality_golden.json` and add a golden test:
```go
func TestRenderXrayDirectRealityMatchesJPY02(t *testing.T) {
	out, err := RenderXrayDirectReality(XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "kulinh", UUID: "b3252cd7-d1c5-4f7f-b257-ef750ac838c9"}},
		PrivateKey:  "QPTgOMQeFazVzKeLaHafW89CpJN6mQoPZl9Lsi66I34",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/jpy02_reality_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	// Allow whitespace-only diffs by re-marshaling both via the same path.
	if normJSON(t, out) != normJSON(t, string(want)) {
		t.Fatalf("config drift vs JPY-02:\n--- got ---\n%s\n--- want ---\n%s", out, string(want))
	}
}
```
Add helper `normJSON` that round-trips through `json.Unmarshal`/`json.MarshalIndent`.

- [ ] **Step 6: Run + commit**

Run: `go test ./internal/templates/ -v`
Expected: PASS.
```bash
git add internal/templates/render.go internal/templates/render_test.go internal/templates/testdata/jpy02_reality_golden.json
git commit -m "feat(templates): add RenderXrayDirectReality with JPY-02 golden test"
```

### Task 1.3: RenderXrayCloudflareXHTTP

**Files:**
- Modify: `internal/templates/render.go`
- Modify: `internal/templates/render_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestRenderXrayCloudflareXHTTP(t *testing.T) {
	out, err := RenderXrayCloudflareXHTTP([]XrayUser{{Name: "alice", UUID: "uuid-a"}}, "vpn.example.com")
	if err != nil { t.Fatal(err) }
	for _, want := range []string{
		`"network": "xhttp"`,
		`"mode": "stream-up"`,
		`"path": "/api/v1/sync"`,
		`"host": "vpn.example.com"`,
		`"listen": "127.0.0.1"`,
		`"port": 10001`,
		`"alice@vpn"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/templates/ -run TestRenderXrayCloudflareXHTTP -v`

- [ ] **Step 3: Implement**

```go
func RenderXrayCloudflareXHTTP(users []XrayUser, vpnHost string) (string, error) {
	clients := make([]map[string]string, 0, len(users))
	for _, u := range users {
		clients = append(clients, map[string]string{"id": u.UUID, "email": u.Name + "@vpn"})
	}
	cfg := map[string]any{
		"log": map[string]string{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-xhttp",
				"listen":   "127.0.0.1",
				"port":     10001,
				"protocol": "vless",
				"settings": map[string]any{"clients": clients, "decryption": "none"},
				"streamSettings": map[string]any{
					"network": "xhttp",
					"xhttpSettings": map[string]any{
						"path": VLESSPath,
						"host": vpnHost,
						"mode": "stream-up",
					},
				},
			},
		},
		"outbounds": []map[string]any{
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"}},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil { return "", err }
	return string(data), nil
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/templates/ -run TestRenderXrayCloudflareXHTTP -v
git add internal/templates/render.go internal/templates/render_test.go
git commit -m "feat(templates): add RenderXrayCloudflareXHTTP (stream-up)"
```

### Task 1.4: Cloudflared ingress path update

**Files:**
- Modify: `internal/templates/render.go` (constants `cloudflaredWithAdminTemplate`)

- [ ] **Step 1: Update template path regex**

In `cloudflaredWithAdminTemplate`, replace `path: ^/vless$` with `path: ^/api/v1/sync$`.

- [ ] **Step 2: Update existing tests if any reference `/vless` path**

Run: `grep -rn '\^/vless\$' internal/ panel/`
Update each match to `^/api/v1/sync$`.

- [ ] **Step 3: Run full template tests**

Run: `go test ./internal/templates/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/templates/render.go
git commit -m "feat(templates): rename cloudflared ingress path to /api/v1/sync"
```

### Task 1.5: Subscription URI builders (Go)

**Files:**
- Modify: `internal/subscription/subscription.go`
- Modify: `internal/subscription/subscription_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestBuildVLESSRealityURI(t *testing.T) {
	got := BuildVLESSRealityURI("alice", "uuid-a", "node1.example.com",
		"www.microsoft.com", "pubkey-x25519", "d3cbbc0b4c5bc5f9")
	for _, want := range []string{
		"vless://uuid-a@node1.example.com:443",
		"security=reality",
		"flow=xtls-rprx-vision",
		"sni=www.microsoft.com",
		"pbk=pubkey-x25519",
		"sid=d3cbbc0b4c5bc5f9",
		"#alice-Reality",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestBuildVLESSXHTTPURI(t *testing.T) {
	got := BuildVLESSXHTTPURI("alice", "uuid-a", "vpn.example.com", "/api/v1/sync")
	for _, want := range []string{
		"vless://uuid-a@vpn.example.com:443",
		"security=tls",
		"type=xhttp",
		"path=%2Fapi%2Fv1%2Fsync",
		"mode=stream-up",
		"sni=vpn.example.com",
		"#alice-XHTTP",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/subscription/ -v`

- [ ] **Step 3: Implement**

```go
func BuildVLESSRealityURI(name, uuid, host, sni, pbk, sid string) string {
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=%s&pbk=%s&sid=%s&fp=chrome#%s-Reality",
		uuid, host, sni, pbk, sid, name,
	)
}

func BuildVLESSXHTTPURI(name, uuid, domain, path string) string {
	encPath := strings.ReplaceAll(path, "/", "%2F")
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=xhttp&host=%s&path=%s&mode=stream-up&sni=%s#%s-XHTTP",
		uuid, domain, domain, encPath, domain, name,
	)
}
```

(Keep existing `BuildVLESSURI` for transitional callers; mark with comment `// Deprecated: WS path. Will be removed once all nodes are migrated.`)

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/subscription/ -v
git add internal/subscription/subscription.go internal/subscription/subscription_test.go
git commit -m "feat(subscription): add Reality + XHTTP URI builders"
```

---

## Phase 2: State Schema (cfvpn.env)

Add new env keys without consuming them yet. Keeps state migrations atomic and reversible.

### Task 2.1: Document new env keys

**Files:**
- Modify: `internal/state/store.go` (header comment only — store.go is generic kv)
- Create: `internal/state/keys.go`

- [ ] **Step 1: Create keys.go with named constants**

```go
package state

// Keys persisted in /etc/cfvpn/cfvpn.env. Adding a key here does NOT migrate
// existing files; callers must default-on-missing.
const (
	KeyMode             = "MODE"             // "direct" | "cloudflare"
	KeyDomain           = "DOMAIN"
	KeyPublicIP         = "PUBLIC_IP"
	KeyAdminHost        = "ADMIN_HOST"
	KeyAdminTunnelUUID  = "ADMIN_TUNNEL_UUID"
	KeyNodeID           = "NODE_ID"

	// Reality (direct mode)
	KeyRealityPriv      = "REALITY_PRIVATE_KEY"
	KeyRealityPub       = "REALITY_PUBLIC_KEY"
	KeyRealityShortID   = "REALITY_SHORT_ID"
	KeyRealityDest      = "REALITY_DEST"        // "www.microsoft.com:443"
	KeyRealitySNI       = "REALITY_SNI"         // "www.microsoft.com"

	// XHTTP (cloudflare mode)
	KeyXHTTPPath        = "XHTTP_PATH"          // "/api/v1/sync"

	// Hysteria (existing)
	KeyHy2Host          = "HY2_HOST"
	KeyHy2Port          = "HY2_PORT"
	KeyHy2ObfsPW        = "HY2_OBFS_PW"
)
```

- [ ] **Step 2: Sanity test**

```go
// internal/state/keys_test.go
package state

import "testing"

func TestKeysAreUnique(t *testing.T) {
	all := []string{KeyMode, KeyDomain, KeyPublicIP, KeyAdminHost, KeyAdminTunnelUUID, KeyNodeID,
		KeyRealityPriv, KeyRealityPub, KeyRealityShortID, KeyRealityDest, KeyRealitySNI,
		KeyXHTTPPath, KeyHy2Host, KeyHy2Port, KeyHy2ObfsPW}
	seen := map[string]bool{}
	for _, k := range all {
		if seen[k] { t.Errorf("dup key %q", k) }
		seen[k] = true
	}
}
```

Run: `go test ./internal/state/ -v` → PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/state/keys.go internal/state/keys_test.go
git commit -m "feat(state): document Reality + XHTTP env keys"
```

---

## Phase 3: x25519 + shortId Helpers

Wrap `xray x25519` for keypair gen, use `crypto/rand` for shortId. No external deps beyond xray binary already shipped.

### Task 3.1: Reality params helper

**Files:**
- Create: `internal/xray/reality.go`
- Create: `internal/xray/reality_test.go`

- [ ] **Step 1: Write failing test (mocks `xray x25519`)**

```go
package xray

import (
	"strings"
	"testing"
)

func TestGenerateRealityParamsShape(t *testing.T) {
	p, err := GenerateRealityParams(GenerateRealityOptions{
		XrayBin: "/bin/echo",
		// stub: echo prints args; we'll bypass keygen and accept seeded values
		StubKeypair: &Keypair{Private: "priv", Public: "pub"},
	})
	if err != nil { t.Fatal(err) }
	if p.PrivateKey != "priv" { t.Errorf("priv: %q", p.PrivateKey) }
	if p.PublicKey != "pub"   { t.Errorf("pub: %q",  p.PublicKey)  }
	if len(p.ShortID) != 16 || !isHex(p.ShortID) {
		t.Errorf("shortid not 16 hex chars: %q", p.ShortID)
	}
	if p.Dest != "www.microsoft.com:443" { t.Errorf("dest: %q", p.Dest) }
	if p.SNI  != "www.microsoft.com"     { t.Errorf("sni: %q",  p.SNI)  }
	_ = strings.Contains
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r>='0'&&r<='9')||(r>='a'&&r<='f')) { return false }
	}
	return true
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `go test ./internal/xray/ -run TestGenerateRealityParamsShape -v`

- [ ] **Step 3: Implement**

```go
package xray

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

type Keypair struct{ Private, Public string }

type RealityParams struct {
	PrivateKey, PublicKey, ShortID, Dest, SNI string
}

type GenerateRealityOptions struct {
	XrayBin     string   // path to xray binary; default "xray"
	Dest        string   // default "www.microsoft.com:443"
	SNI         string   // default "www.microsoft.com"
	StubKeypair *Keypair // tests only; if non-nil bypass `xray x25519`
}

func GenerateRealityParams(opts GenerateRealityOptions) (RealityParams, error) {
	if opts.XrayBin == "" { opts.XrayBin = "xray" }
	if opts.Dest    == "" { opts.Dest    = "www.microsoft.com:443" }
	if opts.SNI     == "" { opts.SNI     = "www.microsoft.com" }

	var kp Keypair
	if opts.StubKeypair != nil {
		kp = *opts.StubKeypair
	} else {
		out, err := exec.Command(opts.XrayBin, "x25519").Output()
		if err != nil { return RealityParams{}, fmt.Errorf("xray x25519: %w", err) }
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Private key:"):
				kp.Private = strings.TrimSpace(strings.TrimPrefix(line, "Private key:"))
			case strings.HasPrefix(line, "Public key:"):
				kp.Public  = strings.TrimSpace(strings.TrimPrefix(line, "Public key:"))
			}
		}
		if kp.Private == "" || kp.Public == "" {
			return RealityParams{}, fmt.Errorf("xray x25519: could not parse output:\n%s", out)
		}
	}

	var sid [8]byte
	if _, err := rand.Read(sid[:]); err != nil {
		return RealityParams{}, err
	}
	return RealityParams{
		PrivateKey: kp.Private,
		PublicKey:  kp.Public,
		ShortID:    hex.EncodeToString(sid[:]),
		Dest:       opts.Dest,
		SNI:        opts.SNI,
	}, nil
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/xray/ -v
git add internal/xray/reality.go internal/xray/reality_test.go
git commit -m "feat(xray): GenerateRealityParams (x25519 + 8-byte shortId)"
```

---

## Phase 4: Port 443 Reachability Helper

Used by install to default mode. "Reachable" means: outbound 443 to `1.1.1.1:443` succeeds AND no local process listens on `0.0.0.0:443`.

### Task 4.1: NetCheck helper

**Files:**
- Create: `internal/netcheck/netcheck.go`
- Create: `internal/netcheck/netcheck_test.go`

- [ ] **Step 1: Write tests using `httptest.NewServer` for the bind check and a local listener for outbound**

```go
package netcheck

import (
	"net"
	"testing"
)

func TestPort443BoundLocally_NotBound(t *testing.T) {
	bound, err := IsTCPPortBound("127.0.0.1", 0) // port 0 = ephemeral, never bound
	if err != nil { t.Fatal(err) }
	if bound { t.Error("expected not bound") }
}

func TestPort443BoundLocally_Bound(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer ln.Close()
	host, port := splitHostPort(t, ln.Addr().String())
	bound, err := IsTCPPortBound(host, port)
	if err != nil { t.Fatal(err) }
	if !bound { t.Error("expected bound") }
}
```

- [ ] **Step 2: Run, expect FAIL**

- [ ] **Step 3: Implement**

```go
package netcheck

import (
	"net"
	"strconv"
	"time"
)

// IsTCPPortBound returns true if `host:port` already has a listener locally.
// Implementation: try to listen; if EADDRINUSE → bound.
func IsTCPPortBound(host string, port int) (bool, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// crude classification: any listen error treated as "bound or blocked"
		return true, nil
	}
	_ = ln.Close()
	return false, nil
}

// CanReachOutbound443 returns true if a TCP connect to 1.1.1.1:443 succeeds.
func CanReachOutbound443(timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", timeout)
	if err != nil { return false }
	_ = c.Close()
	return true
}

// SuggestMode returns "direct" if 443 is free locally and we can reach the
// internet on 443; otherwise "cloudflare".
func SuggestMode() string {
	bound, _ := IsTCPPortBound("0.0.0.0", 443)
	if bound { return "cloudflare" }
	if !CanReachOutbound443(3 * time.Second) { return "cloudflare" }
	return "direct"
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/netcheck/ -v
git add internal/netcheck/
git commit -m "feat(netcheck): IsTCPPortBound + SuggestMode for install auto-detect"
```

---

## Phase 5: Install Path (Greenfield)

Wire the new helpers into install. Auto-detect mode unless `--mode` overrides; generate Reality params or XHTTP path; render correct template; persist all keys.

### Task 5.1: Install mode auto-detect

**Files:**
- Modify: `internal/commands/install.go` (around line 638 where Mode is validated)

- [ ] **Step 1: Add auto-detect block before mode validation**

In `runInstall` (or equivalent function), before the `in.Mode != "direct" && in.Mode != "cloudflare"` check, add:
```go
if in.Mode == "" || in.Mode == "auto" {
	in.Mode = netcheck.SuggestMode()
	fmt.Fprintf(stdout, "auto-detected mode: %s\n", in.Mode)
}
```
Add import `"github.com/kulinh/cf-vpn/internal/netcheck"`.

- [ ] **Step 2: Update CLI help text**

In `cmd/cfvpn/main.go` (or wherever the `--mode` flag is registered), document accepted values: `direct | cloudflare | auto` (default `auto`).

- [ ] **Step 3: Run install tests, fix any default-mode breakage**

Run: `go test ./internal/commands/ -run TestInstall -v`
Expected: existing tests still pass (they pass `--mode=direct` or `--mode=cloudflare` explicitly).

- [ ] **Step 4: Commit**

```bash
git add internal/commands/install.go cmd/cfvpn/main.go
git commit -m "feat(install): auto-detect mode based on port 443 availability"
```

### Task 5.2: Install renders Reality (direct) or XHTTP (cloudflare)

**Files:**
- Modify: `internal/commands/install.go` (line ~796–822 — the mode-branched render)
- Modify: `internal/commands/install.go` (line ~853 — env persistence)

- [ ] **Step 1: Generate Reality params for direct mode**

Inside `if in.Mode == "direct"` block (around line 738), after Lego cert issuance is skipped (see step 3 below), add:
```go
realityParams, err := xray.GenerateRealityParams(xray.GenerateRealityOptions{XrayBin: paths.XrayBin})
if err != nil {
	return fmt.Errorf("generate reality params: %w", err)
}
```

- [ ] **Step 2: Replace render call**

Replace the existing branch:
```go
if in.Mode == "direct" {
    xrayRendered, err = templates.RenderXrayDirect(...)
} else {
    xrayRendered, err = templates.RenderXray(...)
}
```
with:
```go
if in.Mode == "direct" {
    xrayRendered, err = templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
        Users:       []templates.XrayUser{{Name: in.User1Name, UUID: userUUID}},
        PrivateKey:  realityParams.PrivateKey,
        ShortIDs:    []string{realityParams.ShortID},
        Dest:        realityParams.Dest,
        ServerNames: []string{realityParams.SNI},
    })
} else {
    xrayRendered, err = templates.RenderXrayCloudflareXHTTP(
        []templates.XrayUser{{Name: in.User1Name, UUID: userUUID}},
        domain,
    )
}
```

- [ ] **Step 3: Skip Let's Encrypt for direct mode (Reality has no cert)**

Replace the `if in.Mode == "direct" { deps.Cert.Issue(...) }` block with: nothing. Reality requires no Let's Encrypt cert — TLS is camouflaged from `dest`. Keep cert issuance only for HY2 (which still needs it).

- [ ] **Step 4: Persist Reality + XHTTP env keys**

In the `state.SaveAtomic(envFilePath, map[string]string{...})` call near line 853, conditionally extend:
```go
envMap := map[string]string{
    state.KeyMode: in.Mode, /* ...existing keys... */
}
if in.Mode == "direct" {
    envMap[state.KeyRealityPriv]    = realityParams.PrivateKey
    envMap[state.KeyRealityPub]     = realityParams.PublicKey
    envMap[state.KeyRealityShortID] = realityParams.ShortID
    envMap[state.KeyRealityDest]    = realityParams.Dest
    envMap[state.KeyRealitySNI]     = realityParams.SNI
} else {
    envMap[state.KeyXHTTPPath] = templates.VLESSPath
}
state.SaveAtomic(envFilePath, envMap, 0o600)
```

- [ ] **Step 5: Disable cert renew timer for direct (Reality) installs**

In the systemd enable loop near line 879, branch:
```go
servicesToEnable := []string{"cfvpn-xray.service", "cfvpn-cloudflared.service", "cfvpn-agent.service", "cfvpn-hysteria.service"}
if in.Mode == "cloudflare" {
    servicesToEnable = append(servicesToEnable, "cfvpn-cert-renew.timer")
}
```
(Hysteria still needs a cert; renew timer needs to run iff any LE cert is in use. Reality direct still uses HY2 cert, so keep timer in BOTH modes — adjust to: keep cert-renew.timer enabled in both modes.) Re-read the existing code carefully before changing this; the safe move is **leave cert-renew.timer alone** because HY2 cert needs it in both modes.

- [ ] **Step 6: Build subscription URI per mode**

Replace line 894 (`sub := base64.StdEncoding.EncodeToString(...)`) with:
```go
var vlessURI string
if in.Mode == "direct" {
    vlessURI = subscription.BuildVLESSRealityURI(in.User1Name, userUUID, domain,
        realityParams.SNI, realityParams.PublicKey, realityParams.ShortID)
} else {
    vlessURI = subscription.BuildVLESSXHTTPURI(in.User1Name, userUUID, domain, templates.VLESSPath)
}
sub := base64.StdEncoding.EncodeToString([]byte(vlessURI))
```

- [ ] **Step 7: Run install tests**

Run: `go test ./internal/commands/ -run TestInstall -v -count=1`
Expected: PASS. Any failures likely from tests asserting on old WS URI format — update those test fixtures.

- [ ] **Step 8: Commit**

```bash
git add internal/commands/install.go
git commit -m "feat(install): render Reality (direct) or XHTTP (cloudflare); generate keys; emit correct sub URI"
```

---

## Phase 6: Upgrade Path

Same render-branch logic as install, plus migrate-from-WS-to-XHTTP detection (so a CF node previously on WS rolls forward on next upgrade) and migrate-from-WS-to-Reality (direct).

### Task 6.1: Upgrade renders new templates

**Files:**
- Modify: `internal/commands/install.go` (the `runUpgrade` function around line 178–493)

- [ ] **Step 1: Mirror Phase 5 changes inside `runUpgrade`**

Around line 376 (`if in.Mode == "direct"` rotate-mode branch) replace the render:
```go
if in.Mode == "direct" {
    // Detect existing reality params; only generate new if missing OR -rotate-keys flag set.
    rp, ok := loadRealityFromEnv(env)
    if !ok || in.RotateRealityKeys {
        rp, err = xray.GenerateRealityParams(xray.GenerateRealityOptions{XrayBin: paths.XrayBin})
        if err != nil { return fail(err) }
    }
    xrayRendered, err = templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
        Users:       users,
        PrivateKey:  rp.PrivateKey,
        ShortIDs:    []string{rp.ShortID},
        Dest:        rp.Dest,
        ServerNames: []string{rp.SNI},
    })
    // remove Lego cert issuance for direct mode
} else {
    xrayRendered, err = templates.RenderXrayCloudflareXHTTP(users, newHost)
}
```

Helper:
```go
func loadRealityFromEnv(env map[string]string) (xray.RealityParams, bool) {
    p := xray.RealityParams{
        PrivateKey: env[state.KeyRealityPriv],
        PublicKey:  env[state.KeyRealityPub],
        ShortID:    env[state.KeyRealityShortID],
        Dest:       env[state.KeyRealityDest],
        SNI:        env[state.KeyRealitySNI],
    }
    return p, p.PrivateKey != "" && p.ShortID != ""
}
```

- [ ] **Step 2: Add `RotateRealityKeys` opt to `UpgradeIn`**

Surface a `--rotate-reality-keys` flag (default false) so a future-session operator can force-rotate without redoing install.

- [ ] **Step 3: Persist updated env**

Append same env keys as Phase 5 step 4 to the env-save call in upgrade.

- [ ] **Step 4: Update sub URI emission in upgrade output**

Mirror Phase 5 step 6 in upgrade's subscription emission path (search `BuildVLESSURI` in `install.go`'s upgrade function).

- [ ] **Step 5: Run upgrade tests**

Run: `go test ./internal/commands/ -run TestUpgrade -v -count=1`
Update fixtures for new URI/format as needed.

- [ ] **Step 6: Commit**

```bash
git add internal/commands/install.go
git commit -m "feat(upgrade): preserve/rotate Reality params; render XHTTP for CF mode"
```

### Task 6.2: Rotate domain — same migration

**Files:**
- Modify: `internal/commands/rotate.go`

- [ ] **Step 1: Mirror Phase 6.1 render branch inside rotate**

Around `internal/commands/rotate.go:83` (`subscription.BuildVLESSURI(name, uuid, domain)`) replace with mode-aware URI construction; replace any `RenderXrayCloudflare` / `RenderXrayDirect` calls with the new template functions.

- [ ] **Step 2: Run rotate tests**

Run: `go test ./internal/commands/ -run TestRotate -v -count=1`

- [ ] **Step 3: Commit**

```bash
git add internal/commands/rotate.go
git commit -m "feat(rotate): emit Reality/XHTTP URIs and re-render template per mode"
```

---

## Phase 7: Agent Sync API

Expose Reality params + xhttp_path via existing `/admin/v1/sync` and `/admin/v1/status` so panel can rebuild URIs.

### Task 7.1: Extend agent response shapes

**Files:**
- Modify: `cmd/cfvpn-agent/main.go`

- [ ] **Step 1: Find sync handler, extend response struct**

Locate the type that backs `/admin/v1/sync`. Add fields:
```go
type SyncResponse struct {
    OK         bool   `json:"ok"`
    VPNHost    string `json:"vpn_host"`
    PublicIP   string `json:"public_ip"`
    Hy2Host    string `json:"hy2_host"`
    Hy2Port    int    `json:"hy2_port,omitempty"`
    Hy2ObfsPW  string `json:"hy2_obfs_pw,omitempty"`
    Users      int    `json:"users"`

    // NEW
    Mode            string `json:"mode,omitempty"`
    RealityPubKey   string `json:"reality_pubkey,omitempty"`
    RealityShortID  string `json:"reality_sid,omitempty"`
    RealitySNI      string `json:"reality_sni,omitempty"`
    RealityDest     string `json:"reality_dest,omitempty"`
    XHTTPPath       string `json:"xhttp_path,omitempty"`
}
```
Populate fields from env on the agent side.

- [ ] **Step 2: Same for status**

Find `AgentStatusResponse`-equivalent in agent code, extend with the same fields.

- [ ] **Step 3: Test against running JPY-02**

```bash
ssh -i /tmp/rwl01 root@185.200.65.215 'curl -s -H "Authorization: Bearer $(cat /etc/cfvpn/agent.token)" http://127.0.0.1:6788/admin/v1/sync | jq'
```
Expected: JSON includes `mode:"direct"`, `reality_pubkey`, `reality_sid`, `reality_sni`, `reality_dest`. (Cannot test until deployed — note this for acceptance testing.)

- [ ] **Step 4: Commit**

```bash
git add cmd/cfvpn-agent/main.go
git commit -m "feat(agent): expose Reality params + xhttp_path in /sync and /status"
```

---

## Phase 8: D1 Schema Migration

### Task 8.1: Add columns to `nodes`

**Files:**
- Create: `panel/worker/migrations/0010_nodes_reality_xhttp.sql`

- [ ] **Step 1: Write migration**

```sql
-- 0010_nodes_reality_xhttp.sql
ALTER TABLE nodes ADD COLUMN reality_pubkey TEXT;
ALTER TABLE nodes ADD COLUMN reality_sid    TEXT;
ALTER TABLE nodes ADD COLUMN reality_sni    TEXT;
ALTER TABLE nodes ADD COLUMN reality_dest   TEXT;
ALTER TABLE nodes ADD COLUMN xhttp_path     TEXT;
```

- [ ] **Step 2: Apply locally**

```bash
cd panel/worker
wrangler d1 execute cfvpn --local --file migrations/0010_nodes_reality_xhttp.sql
```

- [ ] **Step 3: Apply remote (production)**

```bash
wrangler d1 execute cfvpn --remote --file migrations/0010_nodes_reality_xhttp.sql
```
**Manual confirmation required before this step** — schema changes are blast-radius=large.

- [ ] **Step 4: Commit**

```bash
git add panel/worker/migrations/0010_nodes_reality_xhttp.sql
git commit -m "feat(d1): 0010 add Reality + XHTTP columns to nodes"
```

---

## Phase 9: Panel TS

### Task 9.1: Update types

**Files:**
- Modify: `panel/worker/src/types.ts`

- [ ] **Step 1: Extend `AgentSyncResponse`, `AgentStatusResponse`, `NodeRow`**

```ts
export interface AgentSyncResponse {
  ok: boolean;
  vpn_host: string;
  public_ip: string;
  hy2_host: string;
  hy2_port?: number;
  hy2_obfs_pw?: string;
  users: number;

  mode?: string;
  reality_pubkey?: string;
  reality_sid?: string;
  reality_sni?: string;
  reality_dest?: string;
  xhttp_path?: string;
}
```
Mirror in `AgentStatusResponse`. Add to `NodeRow`:
```ts
reality_pubkey: string | null;
reality_sid:    string | null;
reality_sni:    string | null;
reality_dest:   string | null;
xhttp_path:     string | null;
```

- [ ] **Step 2: Run typecheck**

Run: `cd panel/worker && npm run typecheck` (or `tsc --noEmit`)
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add panel/worker/src/types.ts
git commit -m "feat(panel): extend types for Reality + XHTTP fields"
```

### Task 9.2: Subscription URI builder per mode

**Files:**
- Modify: `panel/worker/src/lib/subscription.ts`
- Modify: `panel/worker/src/routes/sub.test.ts`

- [ ] **Step 1: Write failing tests**

```ts
import { buildVLESSRealityURI, buildVLESSXHTTPURI } from "./subscription";
import { describe, it, expect } from "vitest";

describe("buildVLESSRealityURI", () => {
  it("emits reality params", () => {
    const u = buildVLESSRealityURI("alice", "uuid-a", "node1.example.com",
      "www.microsoft.com", "pubkey-x25519", "d3cbbc0b4c5bc5f9");
    expect(u).toContain("vless://uuid-a@node1.example.com:443");
    expect(u).toContain("security=reality");
    expect(u).toContain("flow=xtls-rprx-vision");
    expect(u).toContain("sni=www.microsoft.com");
    expect(u).toContain("pbk=pubkey-x25519");
    expect(u).toContain("sid=d3cbbc0b4c5bc5f9");
    expect(u).toContain("#alice-Reality");
  });
});

describe("buildVLESSXHTTPURI", () => {
  it("emits xhttp params", () => {
    const u = buildVLESSXHTTPURI("alice", "uuid-a", "vpn.example.com", "/api/v1/sync");
    expect(u).toContain("type=xhttp");
    expect(u).toContain("mode=stream-up");
    expect(u).toContain("path=%2Fapi%2Fv1%2Fsync");
    expect(u).toContain("#alice-XHTTP");
  });
});
```

- [ ] **Step 2: Run, expect FAIL**

Run: `cd panel/worker && npm test -- subscription`

- [ ] **Step 3: Implement**

```ts
export function buildVLESSRealityURI(
  name: string, uuid: string, host: string,
  sni: string, pbk: string, sid: string,
): string {
  const enc = encodeURIComponent;
  return `vless://${uuid}@${host}:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=${enc(sni)}&pbk=${enc(pbk)}&sid=${enc(sid)}&fp=chrome#${enc(name)}-Reality`;
}

export function buildVLESSXHTTPURI(
  name: string, uuid: string, domain: string, path: string,
): string {
  const enc = encodeURIComponent;
  const encPath = path.split("/").map(enc).join("%2F");
  return `vless://${uuid}@${domain}:443?encryption=none&security=tls&type=xhttp&host=${enc(domain)}&path=${encPath}&mode=stream-up&sni=${enc(domain)}#${enc(name)}-XHTTP`;
}
```

- [ ] **Step 4: Update subscription assembler to pick per node**

In `buildSubscription` (or whichever fn iterates nodes near line 30 of subscription.ts), branch on node mode:
```ts
const tag = `${user.name}-${node.label}`;
let uri: string;
if (node.mode === "direct" && node.reality_pubkey && node.reality_sid && node.reality_sni) {
  uri = buildVLESSRealityURI(tag, r.vless_uuid, r.vpn_host,
    node.reality_sni, node.reality_pubkey, node.reality_sid);
} else if (node.mode === "cloudflare" && node.xhttp_path) {
  uri = buildVLESSXHTTPURI(tag, r.vless_uuid, r.vpn_host, node.xhttp_path);
} else {
  // legacy WS — kept transitionally for un-migrated nodes
  uri = buildVLESSURI(tag, r.vless_uuid, r.vpn_host);
}
lines.push(uri);
```

(Field shape on `r` may need broadening — extend the SQL select to include the new columns.)

- [ ] **Step 5: Run + commit**

```bash
cd panel/worker && npm test
git add panel/worker/src/lib/subscription.ts panel/worker/src/routes/sub.test.ts
git commit -m "feat(panel): build Reality/XHTTP URIs based on node mode"
```

### Task 9.3: Sync handler persists new fields

**Files:**
- Modify: `panel/worker/src/routes/agent.ts` (or wherever `/agent/sync` is handled — search `agent_sync`)

- [ ] **Step 1: Locate sync handler, extend INSERT/UPDATE on `nodes`**

Add `reality_pubkey, reality_sid, reality_sni, reality_dest, xhttp_path` to the SQL UPDATE that mirrors agent state.

- [ ] **Step 2: Run typecheck + tests**

```bash
cd panel/worker && npm run typecheck && npm test
```

- [ ] **Step 3: Commit**

```bash
git add panel/worker/src/routes/agent.ts
git commit -m "feat(panel): persist Reality + XHTTP fields on agent sync"
```

---

## Phase 10: Cleanup

Done last — only after all nodes have re-synced and panel emits new URIs successfully.

### Task 10.1: Deprecate WS-only paths

**Files:**
- Modify: `internal/templates/render.go` (mark `RenderXray`, `RenderXrayCloudflare`, `RenderXrayDirect` as deprecated; keep for now)
- Modify: `internal/subscription/subscription.go` (`BuildVLESSURI` deprecated)
- Modify: `panel/worker/src/lib/subscription.ts` (`buildVLESSURI` deprecated)

- [ ] **Step 1: Add `// Deprecated: …` comments referencing migration completion**

- [ ] **Step 2: Add a panel admin endpoint or stat that lists nodes still on WS** (any node with both reality_* and xhttp_path empty AND created before today is a leftover)

- [ ] **Step 3: Commit**

```bash
git commit -m "chore: mark legacy WS render/URI builders as deprecated"
```

### Task 10.2: Remove WS branch (FUTURE — DO NOT EXECUTE UNTIL ALL NODES MIGRATED)

- [ ] **Hold for confirmation.** Once `SELECT count(*) FROM nodes WHERE reality_pubkey IS NULL AND xhttp_path IS NULL` returns 0, schedule a follow-up PR removing legacy code paths.

---

## Phase 11: Acceptance Tests on Real Nodes

### Task 11.1: JPY-02 idempotent re-sync

JPY-02 was hand-migrated. Running the new agent build against it must produce identical config (no service flap).

- [ ] **Step 1: Build agent for linux/amd64**

```bash
cd /opt/cf-vpn
GOOS=linux GOARCH=amd64 go build -o /tmp/cfvpn-agent-new ./cmd/cfvpn-agent
```

- [ ] **Step 2: Stage on JPY-02**

```bash
scp -i /tmp/rwl01 /tmp/cfvpn-agent-new root@185.200.65.215:/usr/local/bin/cfvpn-agent.new
ssh -i /tmp/rwl01 root@185.200.65.215 'sha256sum /etc/cfvpn/xray/config.json > /tmp/before.sha; mv /usr/local/bin/cfvpn-agent.new /usr/local/bin/cfvpn-agent && systemctl restart cfvpn-agent.service && sleep 5 && sha256sum /etc/cfvpn/xray/config.json > /tmp/after.sha && diff /tmp/before.sha /tmp/after.sha && echo IDEMPOTENT_OK'
```
Expected: `IDEMPOTENT_OK` printed (config hash unchanged after agent restart).

- [ ] **Step 3: Verify subscription**

Pull subscription via panel, confirm vless URI for JPY-02 is `vless://...?security=reality&...&pbk=RiBO...&sid=d3cbbc0b4c5bc5f9...`.

### Task 11.2: One CF-mode node end-to-end

- [ ] **Step 1: Pick a low-traffic CF-mode node**

- [ ] **Step 2: Run upgrade with new agent build → expect WS→XHTTP migration**

- [ ] **Step 3: Verify connectivity from a client (Shadowrocket on iOS, v2rayN on Windows)**

- [ ] **Step 4: Record latency, throughput baseline vs WS**

### Task 11.3: Greenfield install on a fresh VPS

- [ ] **Step 1: Provision a new VPS with port 443 free**

- [ ] **Step 2: Run `cfvpn install --mode=auto`**

Expected: `auto-detected mode: direct` printed, install completes, subscription URI is Reality, `xray -test -c /etc/cfvpn/xray/config.json` passes.

- [ ] **Step 3: Run `cfvpn install --mode=cloudflare` on a fresh VPS**

Expected: install completes with XHTTP, cloudflared ingress path = `/api/v1/sync`.

- [ ] **Step 4: Tear down test VPS**

---

## Resumption Notes for Future Sessions

- Scan for first unchecked `- [ ]` from top of file. That's where work resumes.
- Each completed task should leave a `<!-- DONE @ <sha> on <date>: <note> -->` line under its task header so the audit trail survives.
- If a Phase-0 verdict says XHTTP fails through cloudflared, swap all `RenderXrayCloudflareXHTTP` references for an `HTTPUpgrade` variant (similar shape, `network: "httpupgrade"`). The structure of the rest of the plan does not change.
- D1 migration 0010 (Phase 8) is the only step that touches production state irreversibly. Confirm with user before running `wrangler d1 execute --remote`.
- JPY-02 is the byte-equivalence baseline. If the golden test in Task 1.2 ever drifts, do NOT fix the test — figure out why the renderer changed.

<!-- PROGRESS LOG (append below as work happens) -->
<!-- 2026-05-02: plan written. Phase 0 spike not yet run. JPY-02 manually at Reality. -->
<!-- 2026-05-02 (session 2): Phase 0 XHTTP spike completed — XHTTP FAILED through cloudflared (stream-up: 404, stream-one: 403). Decision: use HTTPUpgrade instead for cloudflare mode. -->
<!-- 2026-05-02 (session 2): Phases 1-10 CODE COMPLETE. All Go templates, URI builders, state keys, keygen, netcheck, install, upgrade, rotate, agent sync, D1 migration, panel TS done. Build passes. Tests pass (3 pre-existing failures: binary/install_test.go x2, commands/users_test.go restart count). -->
<!-- 2026-05-02 (session 2): Phase 11 binaries built: cfvpn-agent (10MB), cfvpnctl (12MB) for linux/amd64. -->
<!-- REMAINING: D1 prod migration (needs user approval), Phase 11 acceptance tests on real nodes (JPY-02 idempotent sync, CF-node HTTPUpgrade e2e, greenfield install). -->
