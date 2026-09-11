# Fleet Refresh (September 2026) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repair JPY-01/HKG-01, keep Hysteria2 only on JPY-01/HKG-01, refresh Reality keys and per-node dests, ship IP-addressed Reality URIs plus a Shadowrocket AUTO group, add a fleet probe with Telegram alerts, and prepare (not deploy) NaiveProxy / XHTTP / DERP.

**Architecture:** All node-side behaviour lives in the Go `cfvpnctl` binary and is driven by keys in `/etc/cfvpn/cfvpn.env`; new behaviour is added as env keys (`CLOUDFLARED_PROTOCOL`, `HY2_ENABLED`) and one new subcommand (`rotate-reality`) so a future `cfvpnctl upgrade` cannot undo it. Client output is built twice (Go + Worker) and must stay byte-identical; both sides change together with golden tests. Fleet operations are run from VNM-01 over SSH, one node at a time, each followed by the end-to-end probe.

**Tech Stack:** Go 1.26 (`go test ./...`), Cloudflare Worker + D1 (`npm --prefix panel/worker test`, `npx wrangler deploy`), Python 3 for the probe, systemd, ufw, xray 26.3.27, hysteria 2, cloudflared 2026.8.3.

**Spec:** `docs/superpowers/specs/2026-09-12-fleet-refresh-design.md`

## Global Constraints

- Backup first into `/root/cfvpn-backups/<UTC timestamp>/` on VNM-01. No node is modified before Task 0 completes.
- One node at a time; after each node run `python3 scripts/fleet-probe.py --once` (Task 5 ships it; until then use the diagnostic copy in the scratchpad) and require `204` for every route of that node.
- Never stop a working route before its replacement is verified.
- VNM-01 is only modified by: repo files, `/etc/cron.d/cfvpn-fleet-probe`, `/etc/cfvpn/fleet-probe.env`, `/var/log/cfvpn-fleet-probe.log`, `/var/lib/cfvpn/fleet-probe.state`.
- Node SSH: `ssh -i /root/rwl01.key root@<tailscale-ip>`; targets in `/etc/cfvpn/fleet-hosts`.
- Xray stays at 26.3.27 (latest stable). No `upgrade --binaries`.
- Reality dest per node: JPY-02 `www.amazon.co.jp`, SIN-01 `www.singaporeair.com`, HKG-01 `www.cathaypacific.com`, USA-01 `www.tesla.com`, HAN-01 `vtv.vn`, VNM-02 `www.samsung.com`. SNI = dest host.
- HY2 stays on JPY-01 and HKG-01 only. Off on JPY-02, SIN-01, HAN-01, USA-01, VNM-02, OR-001. VNM-01 untouched.
- AUTO group members, in order: `kulinh@JPY-02-Reality`, `kulinh@SIN-01-Reality`, `kulinh@JPY-01-HY2`, `kulinh@HKG-01-HY2`, `kulinh@OR-001-HTTPUpgrade`; `url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8`.
- One commit per task on branch `feat/fleet-refresh-2026-09`, message prefixed with the item (`feat(node):`, `feat(panel):`, `ops:`...). Commit trailer per repo convention.
- Build binaries with `go build -o bin/cfvpnctl ./cmd/cfvpnctl && go build -o bin/cfvpn-agent ./cmd/cfvpn-agent` (all nodes are x86_64). Deploy a binary with `scp -i /root/rwl01.key bin/<name> root@<ip>:/tmp/<name>.new && ssh ... 'install -m 0755 /tmp/<name>.new /usr/local/bin/<name>'`. Restart `cfvpn-agent` after deploying the agent.

---

## File structure

| File | Responsibility |
|---|---|
| `scripts/fleet-backup.sh` (new) | Pull `/etc/cfvpn` from every node in `fleet-hosts`, dump D1, save all subscription formats into one timestamped dir |
| `internal/state/keys.go` | New keys `CLOUDFLARED_PROTOCOL`, `HY2_ENABLED` |
| `internal/templates/render.go` | cloudflared templates gain an optional `protocol:` line |
| `internal/commands/install.go` | pass `CLOUDFLARED_PROTOCOL` into renderers; pass env `REALITY_DEST/SNI` into `GenerateRealityOptions`; skip HY2 ufw/enable when disabled; `UFWRunner.Delete` |
| `internal/commands/hy2.go` (new) | `Hy2Enabled(env)`, `RunHy2Set(ctx, enable bool, ...)` |
| `internal/commands/reconcile.go` | canonical units exclude hysteria when disabled; disable-now the unit |
| `internal/commands/subscription.go` | no HY2 line when disabled; Reality URI host = `PUBLIC_IP` |
| `internal/commands/cert_renew.go` | skip HY2 cert when disabled |
| `internal/commands/reality_rotate.go` (new) | `RunRotateReality` |
| `internal/systemd/manager.go` | `DisableNow` |
| `internal/cli/dispatch.go` | `hy2 enable|disable`, `rotate-reality` |
| `cmd/cfvpn-agent/main.go` | skip hysteria SetUsers/Reload when disabled |
| `scripts/d1-set-node.sh` (new) | UPDATE one node's reality_* or hy2_* columns in D1 from the node's live env |
| `panel/worker/src/lib/subscription.ts`, `clash.ts`, `shadowrocket.ts` (new), `routes/sub.ts` | `public_ip` in Reality output; `?format=shadowrocket` |
| `scripts/fleet-probe.py`, `scripts/fleet-probe.env.example`, `scripts/fleet-probe.cron` (new) | Health probe + Telegram |
| `docs/prep/naiveproxy-jpy-01-or-001.md`, `docs/prep/xhttp-cloudflare-nodes.md`, `docs/prep/tailscale-derp.md` (new) | Prepared configs |
| `REPORT.md` (new) | Final report |

---

### Task 0: Fleet backup

**Files:**
- Create: `scripts/fleet-backup.sh`

**Interfaces:**
- Produces: `/root/cfvpn-backups/<ts>/{nodes/<NODE>/etc-cfvpn.tgz, d1/{nodes,users,user_nodes}.json, sub/{base64.txt,decoded.txt,clash.yaml,shadowrocket.txt}}`. `<ts>` printed on stdout.

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# fleet-backup.sh — snapshot every node's /etc/cfvpn, the D1 tables and the
# served subscriptions into one timestamped directory before touching the fleet.
#   bash scripts/fleet-backup.sh [--out DIR]     # default /root/cfvpn-backups
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_ROOT=/root/cfvpn-backups
[ "${1:-}" = "--out" ] && OUT_ROOT="$2"
HOSTS_FILE="${CFVPN_FLEET_HOSTS:-/etc/cfvpn/fleet-hosts}"
SSH_KEY="${CFVPN_SSH_KEY:-/root/rwl01.key}"
SSH_OPTS=(-i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR)
set -a; . /etc/cfvpn/cfvpn.env; set +a
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"
# shellcheck source=lib/cfvpn-d1.sh
. "$ROOT/scripts/lib/cfvpn-d1.sh"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
DIR="$OUT_ROOT/$TS"
mkdir -p "$DIR/nodes" "$DIR/d1" "$DIR/sub"
chmod 700 "$OUT_ROOT" "$DIR"

fail=0
while read -r node target; do
  case "$node" in ''|'#'*) continue ;; esac
  mkdir -p "$DIR/nodes/$node"
  if [ "$target" = "root@100.78.174.15" ]; then
    tar -C /etc -czf "$DIR/nodes/$node/etc-cfvpn.tgz" cfvpn
  elif ! ssh "${SSH_OPTS[@]}" "$target" 'tar -C /etc -czf - cfvpn' > "$DIR/nodes/$node/etc-cfvpn.tgz"; then
    echo "FAIL $node: could not pull /etc/cfvpn" >&2; fail=1
  fi
  ssh "${SSH_OPTS[@]}" "$target" 'ufw status numbered; systemctl list-units "cfvpn-*" --no-pager --all' \
    > "$DIR/nodes/$node/system.txt" 2>&1 || true
  echo "ok $node"
done < "$HOSTS_FILE"

for t in nodes users user_nodes; do
  d1_query "{\"sql\":\"SELECT * FROM $t\"}" > "$DIR/d1/$t.json"
done

TOKEN="$(jq -r '.result[0].results[0].sub_token' "$DIR/d1/users.json")"
BASE="https://cp.rwl265.com/sub/$TOKEN"
curl -fsS "$BASE" > "$DIR/sub/base64.txt"
base64 -d "$DIR/sub/base64.txt" > "$DIR/sub/decoded.txt"
curl -fsS "$BASE?format=clash" > "$DIR/sub/clash.yaml"
curl -fsS "$BASE?target=shadowrocket" > "$DIR/sub/shadowrocket.txt" || true
chmod -R go-rwx "$DIR"
echo "$DIR"
exit $fail
```

- [ ] **Step 2: Run it and check the tree**

Run: `bash scripts/fleet-backup.sh && ls -R /root/cfvpn-backups/$(ls /root/cfvpn-backups | tail -1) | head -40`
Expected: `ok <node>` for all 9 nodes, three D1 json files with `"success":true`, `decoded.txt` with 17 URIs. The final line is the directory path; record it as `$BK` for later tasks.

- [ ] **Step 3: Commit**

```bash
git add scripts/fleet-backup.sh
git commit -m "ops: add fleet-backup.sh — snapshot node configs, D1 and subscriptions before fleet changes"
```

---

### Task 1: JPY-01 — cloudflared on http2

**Files:**
- Modify: `internal/state/keys.go`
- Modify: `internal/templates/render.go:45-63` (templates), `RenderCloudflaredAdmin`, `RenderCloudflaredWithAdmin`
- Modify: `internal/commands/install.go` (both call sites of the two renderers, in `runUpgradeCore`, `reRenderInPlace`, `RunInstall`)
- Test: `internal/templates/cloudflared_test.go`

**Interfaces:**
- Produces: `templates.RenderCloudflaredAdmin(tunnelUUID, adminHost, protocol string)`, `templates.RenderCloudflaredWithAdmin(tunnelUUID, domain, adminHost, protocol string)`. `protocol` is `""`, `"quic"` or `"http2"`; anything else is an error. `state.KeyCloudflaredProtocol = "CLOUDFLARED_PROTOCOL"`.

- [ ] **Step 1: Failing test**

Append to `internal/templates/cloudflared_test.go`:

```go
func TestRenderCloudflaredProtocolLine(t *testing.T) {
	uuid := "823f4c15-950e-40c2-bf9b-ea2cd11e9fce"
	got, err := RenderCloudflaredWithAdmin(uuid, "edge.example.com", "jpy-01.example.com", "http2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\nprotocol: http2\n") {
		t.Fatalf("missing protocol line:\n%s", got)
	}
	got, err = RenderCloudflaredAdmin(uuid, "jpy-01.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "protocol:") {
		t.Fatalf("empty protocol must render no line:\n%s", got)
	}
	if _, err := RenderCloudflaredAdmin(uuid, "jpy-01.example.com", "h3\ningress: x"); err == nil {
		t.Fatal("bad protocol must be rejected")
	}
}
```

- [ ] **Step 2: Run, expect compile failure** — `go test ./internal/templates/ -run TestRenderCloudflaredProtocolLine`

- [ ] **Step 3: Implement**

`internal/state/keys.go`: add `KeyCloudflaredProtocol = "CLOUDFLARED_PROTOCOL"` and `KeyHy2Enabled = "HY2_ENABLED"` (used by Task 2).

`internal/templates/render.go`: insert `{{if .Protocol}}protocol: {{.Protocol}}\n{{end}}` right after the `credentials-file:` line in both templates; add

```go
func validateCloudflaredProtocol(p string) error {
	switch p {
	case "", "quic", "http2":
		return nil
	}
	return fmt.Errorf("cloudflared config: protocol %q is not one of quic, http2", p)
}
```

and give both renderers a trailing `protocol string` parameter, validated before parsing, passed as `"Protocol": protocol` in the template data.

`internal/commands/install.go`: every `RenderCloudflaredAdmin(...)` / `RenderCloudflaredWithAdmin(...)` call gets the extra argument `env[state.KeyCloudflaredProtocol]` (upgrade paths) or `""` (fresh install, `RunInstall`). Update the existing tests that call the renderers with the old arity.

- [ ] **Step 4: Tests pass** — `go test ./internal/templates/ ./internal/commands/`

- [ ] **Step 5: Deploy to JPY-01 and verify**

```bash
go build -o bin/cfvpnctl ./cmd/cfvpnctl
J=100.84.34.23; S="ssh -i /root/rwl01.key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@$J"
scp -i /root/rwl01.key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null bin/cfvpnctl root@$J:/tmp/cfvpnctl.new
$S 'install -m 0755 /tmp/cfvpnctl.new /usr/local/bin/cfvpnctl && echo CLOUDFLARED_PROTOCOL=http2 >> /etc/cfvpn/cfvpn.env && cfvpnctl upgrade --mode cloudflare'
$S 'grep -n protocol /etc/cfvpn/cloudflared/config.yml; sleep 20; journalctl -u cfvpn-cloudflared --no-pager -n 12 | grep -E "Registered|protocol="'
```
Expected: `protocol: http2` in config; four `Registered tunnel connection ... protocol=http2` lines. Then `python3 <scratch>/probe.py` shows `JPY-01-HTTPUpgrade OK`. Wait 10 minutes, re-check `journalctl -u cfvpn-cloudflared --since -10m | grep -c "Connection terminated"` is 0.

- [ ] **Step 6: Commit** — `git commit -am "feat(node): CLOUDFLARED_PROTOCOL env key; JPY-01 tunnel moved to http2 to stop QUIC drops"`

---

### Task 2: HY2_ENABLED switch + disable on six nodes

**Files:**
- Create: `internal/commands/hy2.go`, `internal/commands/hy2_test.go`
- Modify: `internal/systemd/manager.go` (add `DisableNow`), `internal/commands/reconcile.go`, `internal/commands/subscription.go`, `internal/commands/install.go` (`UFWRunner`), `internal/commands/cert_renew.go`, `internal/cli/dispatch.go`, `cmd/cfvpn-agent/main.go`
- Create: `scripts/d1-set-node.sh`

**Interfaces:**
- Produces: `commands.Hy2Enabled(env map[string]string) bool` (true unless `HY2_ENABLED` is `0`/`false`/`no`); `commands.RunHy2Set(ctx, enable bool, deps InstallDeps, stdout, stderr) error`; `systemd.DisableNow(ctx, r, unit)`; `UFWRunner.Delete(ctx, rule)`; CLI `cfvpnctl hy2 enable|disable`.

- [ ] **Step 1: Failing tests** (`internal/commands/hy2_test.go`)

```go
func TestHy2EnabledDefaultsOn(t *testing.T) {
	for _, tc := range []struct{ v string; want bool }{{"", true}, {"1", true}, {"0", false}, {"false", false}, {"no", false}} {
		if got := Hy2Enabled(map[string]string{"HY2_ENABLED": tc.v}); got != tc.want {
			t.Fatalf("HY2_ENABLED=%q: got %v", tc.v, got)
		}
	}
}

func TestCanonicalUnitsDropHysteriaWhenDisabled(t *testing.T) {
	if _, ok := canonicalUnitsFor(false)["cfvpn-hysteria.service"]; ok {
		t.Fatal("hysteria unit must be absent when HY2 is disabled")
	}
	if _, ok := canonicalUnitsFor(true)["cfvpn-hysteria.service"]; !ok {
		t.Fatal("hysteria unit must be present when HY2 is enabled")
	}
}

func TestBuildUserURIsNoHy2WhenDisabled(t *testing.T) {
	env := map[string]string{"MODE": "cloudflare", "NODE_ID": "OR-001", "HY2_HOST": "h.example", "HY2_PORT": "5331", "HY2_OBFS_PW": "o", "HY2_ENABLED": "0", "PUBLIC_IP": "1.2.3.4"}
	lines := buildUserURIs("kulinh", "uuid", "d.example", "pw", env, nil)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "vless://") {
		t.Fatalf("expected only the VLESS line, got %v", lines)
	}
}
```

- [ ] **Step 2: Run, expect failures** — `go test ./internal/commands/ -run 'Hy2|CanonicalUnits|NoHy2'`

- [ ] **Step 3: Implement**

`internal/commands/hy2.go`:

```go
package commands

// Hy2Enabled reports whether this node should run Hysteria2. Absent key = on,
// so every existing node keeps its HY2 until an operator turns it off.
func Hy2Enabled(env map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(env[state.KeyHy2Enabled])) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// RunHy2Set flips HY2_ENABLED, reconciles units (which stops/disables or
// re-enables cfvpn-hysteria), fixes the ufw rule and regenerates the
// node-side subscription files. Disabling keeps hysteria/config.yaml and the
// HY2_* env keys so enable is a pure reversal.
func RunHy2Set(ctx context.Context, enable bool, deps InstallDeps, stdout, stderr io.Writer) error {
	unlock, err := AcquireConfigLock(ctx)
	if err != nil { return fmt.Errorf("acquire config lock: %w", err) }
	defer unlock()
	env, err := state.Load(envFilePath)
	if err != nil { return fmt.Errorf("load env: %w", err) }
	if enable { env[state.KeyHy2Enabled] = "1" } else { env[state.KeyHy2Enabled] = "0" }
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil { return fmt.Errorf("save env: %w", err) }
	runner := resolveRunner(deps.SystemdRunner)
	if err := runReconcileUnitsLocked(ctx, runner, stdout); err != nil { return err }
	if port := env[state.KeyHy2Port]; port != "" {
		ufw := deps.UFW
		if ufw == nil { ufw = NewExecUFW() }
		var uerr error
		if enable { uerr = ufw.Allow(ctx, port+"/udp") } else { uerr = ufw.Delete(ctx, port+"/udp") }
		if uerr != nil && stderr != nil { fmt.Fprintf(stderr, "warning: ufw %s/udp: %v\n", port, uerr) }
	}
	if err := RegenerateSubscriptionsTo(env[state.KeyDomain], stderr); err != nil { return err }
	fmt.Fprintf(stdout, "hysteria2 %s\n", map[bool]string{true: "enabled", false: "disabled"}[enable])
	return nil
}
```

`internal/commands/reconcile.go`: rename `canonicalUnits()` to `canonicalUnitsFor(hy2 bool)` (omit the hysteria entry when `!hy2`) and keep `canonicalUnits()` as `canonicalUnitsFor(Hy2Enabled(loadEnvOrEmpty()))` where `loadEnvOrEmpty` is `state.Load(envFilePath)` falling back to an empty map. In `runReconcileUnitsLocked`, when HY2 is disabled and `/etc/systemd/system/cfvpn-hysteria.service` exists: `systemd.DisableNow(ctx, r, "cfvpn-hysteria.service")`, remove the file, daemon-reload, print `reconciled cfvpn-hysteria.service (disabled)`.

`internal/systemd/manager.go`: `func DisableNow(ctx, r, unit) error { return r.Run(ctx, "systemctl", "disable", "--now", unit) }`.

`internal/commands/subscription.go` `buildHy2Line`: first line `if !Hy2Enabled(env) { return "", false }`.

`internal/commands/install.go`: `UFWRunner` gains `Delete(ctx, rule string) error`; `execUFW.Delete` runs `ufw delete allow <rule>`; update every fake UFW in tests. In `RunInstall` keep behaviour (fresh installs are HY2-on).

`internal/commands/cert_renew.go`: skip the HY2 certificate when `!Hy2Enabled(env)` (print `hy2 disabled; skipping HY2 cert`).

`cmd/cfvpn-agent/main.go` `applyUsers`: wrap `hysteria.SetUsers` + `hysteria.ReloadService` in `if commands.Hy2Enabled(env) { ... }`; in `handleStatus` report `Hy2Host`/`Hy2Port`/`Hy2ObfsPW` only when enabled (the Worker's `mergeHy2Runtime` keeps the row otherwise, so D1 is nulled explicitly in Step 6).

`internal/cli/dispatch.go`: add

```go
case "hy2":
	if len(args) != 2 || (args[1] != "enable" && args[1] != "disable") {
		fmt.Fprintln(stderr, "usage: cfvpnctl hy2 {enable|disable}")
		return 2
	}
	env, err := state.Load(envFile)
	if err != nil { fmt.Fprintf(stderr, "cannot read env file %s: %v\n", envFile, err); return 1 }
	if err := commands.RunHy2Set(ctx, args[1] == "enable", buildInstallDeps(env), stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err); return 1
	}
	return 0
```

`scripts/d1-set-node.sh`:

```bash
#!/usr/bin/env bash
# d1-set-node.sh — push one node's live env values into D1.
#   bash scripts/d1-set-node.sh <NODE_ID> hy2-off        # null hy2_host/port/obfs
#   bash scripts/d1-set-node.sh <NODE_ID> reality        # copy REALITY_* + PUBLIC_IP from the node
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODE="$1"; ACTION="$2"
HOSTS_FILE="${CFVPN_FLEET_HOSTS:-/etc/cfvpn/fleet-hosts}"
SSH_KEY="${CFVPN_SSH_KEY:-/root/rwl01.key}"
set -a; . /etc/cfvpn/cfvpn.env; set +a
D1_DB_ID="${CFVPN_D1_DATABASE_ID:-0649f07f-e2c0-47f3-b84a-273f7f67332e}"
. "$ROOT/scripts/lib/cfvpn-d1.sh"
TARGET="$(awk -v n="$NODE" '$1==n{print $2}' "$HOSTS_FILE")"
[ -n "$TARGET" ] || { echo "no ssh target for $NODE in $HOSTS_FILE" >&2; exit 2; }
case "$ACTION" in
  hy2-off)
    payload=$(jq -cn --arg id "$NODE" '{sql:"UPDATE nodes SET hy2_host=NULL, hy2_port=NULL, hy2_obfs_pw=NULL WHERE id=?", params:[$id]}') ;;
  reality)
    if [ "$TARGET" = "root@100.78.174.15" ]; then envtxt="$(cat /etc/cfvpn/cfvpn.env)"; else
      envtxt="$(ssh -i "$SSH_KEY" -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR "$TARGET" cat /etc/cfvpn/cfvpn.env)"; fi
    g() { printf '%s\n' "$envtxt" | awk -F= -v k="$1" '$1==k{print substr($0, length(k)+2)}'; }
    payload=$(jq -cn --arg id "$NODE" --arg pk "$(g REALITY_PUBLIC_KEY)" --arg sid "$(g REALITY_SHORT_ID)" --arg sni "$(g REALITY_SNI)" --arg dest "$(g REALITY_DEST)" --arg ip "$(g PUBLIC_IP)" \
      '{sql:"UPDATE nodes SET reality_pubkey=?, reality_sid=?, reality_sni=?, reality_dest=?, public_ip=? WHERE id=?", params:[$pk,$sid,$sni,$dest,$ip,$id]}') ;;
  *) echo "unknown action $ACTION" >&2; exit 2 ;;
esac
out="$(d1_query "$payload")"
jq -e '.success' <<<"$out" >/dev/null || { echo "$out" >&2; exit 1; }
echo "D1 updated: $NODE $ACTION"
```

- [ ] **Step 4: All tests pass** — `go test ./...`

- [ ] **Step 5: Commit code** — `git add -A && git commit -m "feat(node): HY2_ENABLED switch — cfvpnctl hy2 enable|disable, honored by reconcile, upgrade, cert-renew, agent and subscriptions"`

- [ ] **Step 6: Roll out, one node at a time** (order: OR-001, HAN-01, USA-01, VNM-02, JPY-02, SIN-01)

For each `<NODE> <IP>`:

```bash
go build -o bin/cfvpnctl ./cmd/cfvpnctl && go build -o bin/cfvpn-agent ./cmd/cfvpn-agent
S="ssh -i /root/rwl01.key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@$IP"
scp -i /root/rwl01.key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null bin/cfvpnctl bin/cfvpn-agent root@$IP:/tmp/
$S 'install -m 0755 /tmp/cfvpnctl /usr/local/bin/cfvpnctl && install -m 0755 /tmp/cfvpn-agent /usr/local/bin/cfvpn-agent && systemctl restart cfvpn-agent && cfvpnctl hy2 disable'
$S 'systemctl is-active cfvpn-hysteria; systemctl is-enabled cfvpn-hysteria; ss -lunp | grep -c hysteria; ufw status | grep -c udp; systemctl is-active cfvpn-xray cfvpn-agent'
bash scripts/d1-set-node.sh $NODE hy2-off
```
Expected on the node: `inactive`, `disabled` (or "not-found"), `0`, `0`, `active active`. Then probe: the node's VLESS route `OK`, its HY2 route absent from a fresh `curl $SUB | base64 -d`. Then `bash scripts/check-fleet-drift.sh` → in sync.

- [ ] **Step 7: Commit rollout note** — add the six nodes and timestamps to `REPORT.md` draft (created in Task 7; keep notes in the scratchpad until then).

---

### Task 3: rotate-reality + per-node dest

**Files:**
- Create: `internal/commands/reality_rotate.go`, `internal/commands/reality_rotate_test.go`
- Modify: `internal/commands/install.go` (`GenerateRealityOptions{}` at both call sites), `internal/cli/dispatch.go`

**Interfaces:**
- Produces: `commands.RotateRealityInputs{Dest, SNI string}`; `commands.RunRotateReality(ctx, in RotateRealityInputs, runner systemd.Runner, keygen func(xray.GenerateRealityOptions) (xray.RealityParams, error), stdout, stderr io.Writer) error`; CLI `cfvpnctl rotate-reality --dest <host:port> [--sni <host>]`.

- [ ] **Step 1: Failing test**

```go
func TestRunRotateRealityWritesNewParams(t *testing.T) {
	dir := t.TempDir()
	withTestPaths(t, dir) // existing helper that redirects envFilePath / xrayConfigPath into dir
	writeEnv(t, map[string]string{"MODE": "direct", "DOMAIN": "d.example", "PUBLIC_IP": "1.2.3.4",
		"REALITY_PRIVATE_KEY": "oldpriv", "REALITY_PUBLIC_KEY": "oldpub", "REALITY_SHORT_ID": "0011223344556677",
		"REALITY_DEST": "www.apple.com:443", "REALITY_SNI": "www.apple.com"})
	writeXrayDirectConfig(t, "kulinh", "uuid-1")
	runner := &fakeRunner{}
	keygen := func(o xray.GenerateRealityOptions) (xray.RealityParams, error) {
		return xray.RealityParams{PrivateKey: "newpriv", PublicKey: "newpub", ShortID: "aabbccddeeff0011", Dest: o.Dest, SNI: o.SNI}, nil
	}
	err := RunRotateReality(context.Background(), RotateRealityInputs{Dest: "vtv.vn:443", SNI: "vtv.vn"}, runner, keygen, io.Discard, io.Discard)
	if err != nil { t.Fatal(err) }
	env, _ := state.Load(envFilePath)
	if env["REALITY_PUBLIC_KEY"] != "newpub" || env["REALITY_DEST"] != "vtv.vn:443" || env["REALITY_SNI"] != "vtv.vn" {
		t.Fatalf("env not updated: %v", env)
	}
	cfg, _ := os.ReadFile(xrayConfigPath)
	if !strings.Contains(string(cfg), `"dest": "vtv.vn:443"`) || !strings.Contains(string(cfg), "newpriv") {
		t.Fatalf("xray config not re-rendered:\n%s", cfg)
	}
	if !runner.sawRestart("cfvpn-xray.service") { t.Fatal("xray not restarted") }
}

func TestRunRotateRealityRefusesCloudflareMode(t *testing.T) { /* MODE=cloudflare → error mentioning "direct" */ }
```

Use the existing test helpers in `internal/commands/*_test.go` (look for how `rotate_test.go` redirects paths and fakes the runner; reuse those names exactly).

- [ ] **Step 2: Run, expect failure**

- [ ] **Step 3: Implement** `internal/commands/reality_rotate.go`:

```go
type RotateRealityInputs struct{ Dest, SNI string }

func RunRotateReality(ctx context.Context, in RotateRealityInputs, runner systemd.Runner,
	keygen func(xray.GenerateRealityOptions) (xray.RealityParams, error), stdout, stderr io.Writer) error {
	in.Dest = strings.TrimSpace(in.Dest); in.SNI = strings.TrimSpace(in.SNI)
	if in.Dest == "" { return fmt.Errorf("--dest host:port is required") }
	host, port, err := net.SplitHostPort(in.Dest)
	if err != nil || host == "" || port == "" { return fmt.Errorf("--dest must be host:port, got %q", in.Dest) }
	if in.SNI == "" { in.SNI = host }
	if err := validate.Hostname(in.SNI); err != nil { return fmt.Errorf("--sni: %w", err) }
	if keygen == nil { keygen = xray.GenerateRealityParams }

	unlock, err := AcquireConfigLock(ctx)
	if err != nil { return fmt.Errorf("acquire config lock: %w", err) }
	defer unlock()
	env, err := state.Load(envFilePath)
	if err != nil { return fmt.Errorf("load env: %w", err) }
	if env[state.KeyMode] != "direct" { return fmt.Errorf("rotate-reality needs MODE=direct (this node is %q)", env[state.KeyMode]) }

	params, err := keygen(xray.GenerateRealityOptions{Dest: in.Dest, SNI: in.SNI})
	if err != nil { return fmt.Errorf("generate reality params: %w", err) }
	users, err := usersFromCurrentXray()
	if err != nil { return err }
	rendered, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
		Users: users, PrivateKey: params.PrivateKey, ShortIDs: []string{params.ShortID},
		Dest: params.Dest, ServerNames: []string{params.SNI}, DNSServers: xrayDNSServersFromEnv(env)})
	if err != nil { return fmt.Errorf("render xray reality config: %w", err) }

	old, oldErr := os.ReadFile(xrayConfigPath)
	if err := writeXrayConfigChecked(ctx, xrayConfigPath, []byte(rendered), 0o600); err != nil { return fmt.Errorf("write xray config: %w", err) }
	r := resolveRunner(runner)
	if err := systemd.Restart(ctx, r, "cfvpn-xray.service"); err != nil {
		if oldErr == nil { _ = writeAtomicFile(xrayConfigPath, old, 0o600); _ = systemd.Restart(ctx, r, "cfvpn-xray.service") }
		return fmt.Errorf("restart xray (old config restored): %w", err)
	}
	env[state.KeyRealityPriv] = params.PrivateKey; env[state.KeyRealityPub] = params.PublicKey
	env[state.KeyRealityShortID] = params.ShortID; env[state.KeyRealityDest] = params.Dest; env[state.KeyRealitySNI] = params.SNI
	if err := state.SaveAtomic(envFilePath, env, 0o600); err != nil { return fmt.Errorf("save env: %w", err) }
	if err := RegenerateSubscriptionsTo(env[state.KeyDomain], stderr); err != nil { return err }
	fmt.Fprintf(stdout, "reality rotated: dest=%s sni=%s pbk=%s sid=%s\n", params.Dest, params.SNI, params.PublicKey, params.ShortID)
	return nil
}
```

`install.go`: both `xray.GenerateRealityParams(xray.GenerateRealityOptions{})` become `xray.GenerateRealityOptions{Dest: env[state.KeyRealityDest], SNI: env[state.KeyRealitySNI]}` (upgrade) and `{Dest: in.RealityDest, SNI: in.RealitySNI}` (install) with `InstallInputs.RealityDest/RealitySNI` filled from env in `installFromEnv`.

`dispatch.go`:

```go
case "rotate-reality":
	var dest, sni string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--dest": if i+1 < len(args) { dest = args[i+1]; i++ }
		case "--sni": if i+1 < len(args) { sni = args[i+1]; i++ }
		default: fmt.Fprintln(stderr, "usage: cfvpnctl rotate-reality --dest <host:443> [--sni <host>]"); return 2
		}
	}
	if err := commands.RunRotateReality(ctx, commands.RotateRealityInputs{Dest: dest, SNI: sni}, systemd.ExecRunner{}, nil, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err); return 1
	}
	return 0
```

- [ ] **Step 4: Tests pass** — `go test ./...`
- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(node): cfvpnctl rotate-reality — new x25519 key, shortId and per-node dest; env dest honored on install/upgrade"`

- [ ] **Step 6: Roll out** (order HAN-01, VNM-02, USA-01, HKG-01, JPY-02, SIN-01). For each:

```bash
scp -i /root/rwl01.key ... bin/cfvpnctl root@$IP:/tmp/cfvpnctl.new
$S 'install -m 0755 /tmp/cfvpnctl.new /usr/local/bin/cfvpnctl && cfvpnctl rotate-reality --dest <DEST>:443'
$S 'systemctl is-active cfvpn-xray; ss -lntp | grep ":443 "; timeout 6 openssl s_client -connect 127.0.0.1:443 -servername <DEST> </dev/null 2>/dev/null | grep -E "subject=|Protocol"'
bash scripts/d1-set-node.sh $NODE reality
```
Expected: active, xray on :443, the probe certificate subject is the dest's real cert (Reality forwarded the handshake). Refresh the subscription (`curl $SUB | base64 -d`) and run the probe: `<NODE>-Reality OK`. Then `bash scripts/check-fleet-drift.sh`. Only after `OK` move to the next node. HKG-01 is re-verified here (item 1b).

---

### Task 4: IP-addressed Reality URIs + Shadowrocket format

**Files:**
- Modify: `internal/commands/subscription.go`, `internal/subscription/subscription_test.go`, `internal/commands/subscription_test.go`
- Modify: `panel/worker/src/lib/subscription.ts`, `panel/worker/src/lib/clash.ts`, `panel/worker/src/routes/sub.ts`, `panel/worker/src/routes/sub.test.ts`, `panel/worker/src/lib/clash.test.ts`
- Create: `panel/worker/src/lib/shadowrocket.ts`, `panel/worker/src/lib/shadowrocket.test.ts`

**Interfaces:**
- `SubscriptionRow` gains `public_ip: string | null`; the SQL in `sub.ts` selects `n.public_ip`.
- Direct rows: `host = r.public_ip ?? r.vpn_host` (Worker) / `env["PUBLIC_IP"]` falling back to `domain` (Go). Cloudflare rows unchanged.
- `buildShadowrocketConfig(username: string, rows: SubscriptionRow[]): string`.
- `GET /sub/<token>?format=shadowrocket` → `text/plain`, header `content-disposition: attachment; filename="RWL8899.conf"`.

- [ ] **Step 1: Failing tests**

Go (`internal/commands/subscription_test.go`):
```go
func TestDirectURIUsesPublicIP(t *testing.T) {
	env := map[string]string{"MODE": "direct", "NODE_ID": "SIN-01", "PUBLIC_IP": "96.9.231.74",
		"REALITY_PUBLIC_KEY": "pbk", "REALITY_SHORT_ID": "sid", "REALITY_SNI": "www.singaporeair.com", "HY2_ENABLED": "0"}
	lines := buildUserURIs("kulinh", "uuid", "assets-b7e69185.rwl.one", "", env, nil)
	if !strings.HasPrefix(lines[0], "vless://uuid@96.9.231.74:443?") { t.Fatalf("got %s", lines[0]) }
}
```
Worker (`sub.test.ts`): a direct row with `public_ip: "96.9.231.74"` yields a line starting `vless://<uuid>@96.9.231.74:443?`; a direct row with `public_ip: null` still uses `vpn_host`. `shadowrocket.test.ts`:
```ts
it("emits AUTO with only the members the user has, in the fixed order", () => {
  const conf = buildShadowrocketConfig("kulinh", rowsFor(["JPY-02", "SIN-01", "JPY-01", "HKG-01", "OR-001", "USA-01"]));
  expect(conf).toContain("[Proxy Group]\nAUTO = url-test, kulinh@JPY-02-Reality, kulinh@SIN-01-Reality, kulinh@JPY-01-HY2, kulinh@HKG-01-HY2, kulinh@OR-001-HTTPUpgrade, url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8");
  expect(conf).toContain("[Rule]\nFINAL,AUTO");
});
it("falls back to a select group when no AUTO member exists", ...)
```
`rowsFor` builds `SubscriptionRow`s with `mode`, `hy2_*` per the fleet (JPY-02/SIN-01 direct, HY2 off; JPY-01/HKG-01 HY2 on; OR-001 cloudflare).

- [ ] **Step 2: Run, expect failures** — `go test ./internal/commands/ ./internal/subscription/`; `npm --prefix panel/worker test`

- [ ] **Step 3: Implement**

Go `buildUserURIs` direct branch: `host := env[state.KeyPublicIP]; if host == "" { host = domain }` and pass `host`. Update `RunInstall`'s final URI print to pass `ip`.

Worker `subscription.ts`: add `public_ip` to the interface; direct branch `const host = r.public_ip && r.public_ip.length > 0 ? r.public_ip : r.vpn_host;`. `clash.ts` Reality proxy `server: host` with the same rule. `sub.ts` SQL adds `n.public_ip`.

`shadowrocket.ts`:
```ts
import type { SubscriptionRow } from "./subscription";
import { realityName, httpUpgradeName, hy2Name } from "./clash";

// Fixed AUTO membership (operator decision, 2026-09-12). Missing members are
// skipped so a user without one of these nodes still gets a valid group.
const AUTO_MEMBERS: Array<[string, (u: string, n: string) => string]> = [
  ["JPY-02", realityName], ["SIN-01", realityName], ["JPY-01", hy2Name], ["HKG-01", hy2Name], ["OR-001", httpUpgradeName],
];
const AUTO_OPTS = "url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8";

export function availableNames(username: string, rows: SubscriptionRow[]): Set<string> { /* same gating as buildProxies */ }

export function buildShadowrocketConfig(username: string, rows: SubscriptionRow[]): string {
  const have = availableNames(username, rows);
  const members = AUTO_MEMBERS.map(([id, f]) => f(username, id)).filter((n) => have.has(n));
  const all = [...have];
  const out = ["[General]", "bypass-system = true", "skip-proxy = 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local", "dns-server = system", ""];
  out.push("[Proxy Group]");
  if (members.length > 0) out.push(`AUTO = url-test, ${members.join(", ")}, ${AUTO_OPTS}`);
  out.push(`PROXY = select, ${members.length > 0 ? "AUTO, " : ""}${all.join(", ")}`, "");
  out.push("[Rule]", members.length > 0 ? "FINAL,AUTO" : "FINAL,PROXY", "");
  return out.join("\n");
}
```
`sub.ts`: accept `format === "shadowrocket"`; respond `text/plain; charset=utf-8`, `content-disposition: attachment; filename="RWL8899.conf"`, same cache headers. Update the 400 detail string to `supported: clash, shadowrocket (omit for base64)` and its test.

- [ ] **Step 4: Tests pass** — `go test ./...`; `npm --prefix panel/worker test`
- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(sub): Reality URIs address the node by public IP; ?format=shadowrocket with AUTO url-test group"`

- [ ] **Step 6: Deploy Worker and capture outputs**

```bash
cd panel/worker && set -a && . /etc/cfvpn/cfvpn.env && set +a && CLOUDFLARE_API_TOKEN=$CF_API_TOKEN CLOUDFLARE_ACCOUNT_ID=$CF_ACCOUNT_ID npx wrangler deploy; cd -
SUB="https://cp.rwl265.com/sub/<token>"; mkdir -p $BK/after
curl -fsS "$SUB" | tee $BK/after/base64.txt | base64 -d > $BK/after/decoded.txt
curl -fsS "$SUB?format=shadowrocket" > $BK/after/RWL8899.conf
curl -fsS "$SUB?format=clash" > $BK/after/clash.yaml
python3 scripts/fleet-probe.py --sub-file $BK/after/decoded.txt --once
```
Expected: every direct line is `@<ip>:443`, exactly 2 HY2 lines (JPY-01, HKG-01), `RWL8899.conf` has the AUTO line, probe all `OK`. Also regenerate node-side files on every direct node: `cfvpnctl gen-sub kulinh` (they now embed the IP).

---

### Task 5: Fleet probe + Telegram alert on VNM-01

**Files:**
- Create: `scripts/fleet-probe.py`, `scripts/fleet-probe.env.example`, `scripts/fleet-probe.cron`, `scripts/tests/test_fleet_probe.py`

**Interfaces:**
- CLI: `fleet-probe.py [--once] [--sub-file PATH] [--env /etc/cfvpn/fleet-probe.env]`. Env: `SUB_URL`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`, `STATE_FILE` (default `/var/lib/cfvpn/fleet-probe.state`), `LOG_FILE` (default `/var/log/cfvpn-fleet-probe.log`), `FAIL_THRESHOLD` (default 2).
- Pure functions to unit-test: `parse_subscription(text) -> list[Route]`, `xray_outbound(route) -> dict`, `hysteria_config(route, port) -> dict`, `next_state(prev: dict, results: dict, threshold: int) -> (state, alerts: list[str])`.

- [ ] **Step 1: Failing tests** (`scripts/tests/test_fleet_probe.py`, run with `python3 -m pytest scripts/tests` or `python3 -m unittest`):

```python
def test_parse_reality_and_hy2():
    routes = fp.parse_subscription(SAMPLE)  # 1 reality, 1 httpupgrade, 1 hysteria2
    assert [r.kind for r in routes] == ["vless", "vless", "hy2"]
    assert routes[0].name == "kulinh@SIN-01-Reality"
def test_hy2_auth_is_user_colon_password():
    cfg = fp.hysteria_config(fp.parse_subscription(SAMPLE)[2], 21003)
    assert cfg["auth"] == "kulinh:secretpw" and cfg["obfs"]["salamander"]["password"] == "obfspw"
def test_alert_on_second_consecutive_failure_and_recovery():
    s, a = fp.next_state({}, {"A": None}, 2);            assert a == [] and s["A"]["fails"] == 1
    s, a = fp.next_state(s, {"A": None}, 2);             assert a == ["DOWN A (2 consecutive failures)"]
    s, a = fp.next_state(s, {"A": None}, 2);             assert a == []          # no repeat spam
    s, a = fp.next_state(s, {"A": 120}, 2);              assert a == ["UP A (120 ms)"]
```

- [ ] **Step 2: Run, expect ImportError**

- [ ] **Step 3: Implement** — port the scratchpad `probe.py` (xray multi-SOCKS client + one hysteria client per HY2 route, `curl --socks5-hostname` to `http://cp.cloudflare.com/generate_204`, 2 tries, 10 s timeout), plus:
  - `parse_subscription` handles a base64 body or decoded text; skips `REMARKS=`.
  - append `ts node OK|FAIL latency_ms` lines to `LOG_FILE`; state JSON `{name: {"fails": n, "alerted": bool}}`.
  - Telegram via `urllib.request` POST `https://api.telegram.org/bot<token>/sendMessage` with `chat_id`, `text` (one message listing all alerts, prefixed `cfvpn fleet-probe @vnm-01`); skipped with a stderr note when the token is empty.
  - `flock` on `STATE_FILE + ".lock"` so overlapping cron runs do not race.
  - ports 21001+ on 127.0.0.1 only; kill children on exit.

`scripts/fleet-probe.env.example`:
```
SUB_URL=https://cp.rwl265.com/sub/<sub_token>
TELEGRAM_BOT_TOKEN=
TELEGRAM_CHAT_ID=-1003806233980
FAIL_THRESHOLD=2
```
`scripts/fleet-probe.cron`:
```
*/10 * * * * root /usr/bin/python3 /opt/cf-vpn/scripts/fleet-probe.py --env /etc/cfvpn/fleet-probe.env >/dev/null 2>>/var/log/cfvpn-fleet-probe.err
```

- [ ] **Step 4: Tests pass** — `python3 -m pytest scripts/tests -q` (or unittest)
- [ ] **Step 5: Install on VNM-01**

```bash
install -m 600 scripts/fleet-probe.env.example /etc/cfvpn/fleet-probe.env
sed -i "s|<sub_token>|$(jq -r '.result[0].results[0].sub_token' $BK/d1/users.json)|" /etc/cfvpn/fleet-probe.env
install -m 644 scripts/fleet-probe.cron /etc/cron.d/cfvpn-fleet-probe
python3 scripts/fleet-probe.py --env /etc/cfvpn/fleet-probe.env --once && tail -20 /var/log/cfvpn-fleet-probe.log
```
Expected: one log line per route, all `OK`; stderr says Telegram token empty (operator fills it). Add a `logrotate` stanza only if the log grows past 10 MB/month (it will not at 11 lines / 10 min).

- [ ] **Step 6: Commit** — `git add -A && git commit -m "ops: fleet-probe.py — 10-minute end-to-end probe of every route from VNM-01 with Telegram alerts"`

---

### Task 6: Prepared configs (not deployed)

**Files:**
- Create: `docs/prep/naiveproxy-jpy-01-or-001.md`, `docs/prep/xhttp-cloudflare-nodes.md`, `docs/prep/tailscale-derp.md`

- [ ] **Step 1: NaiveProxy doc** — for JPY-01 (45.143.131.36) and OR-001 (51.81.245.144); both are cloudflare-mode so `:443` is free. Caddy built with `xcaddy build --with github.com/caddyserver/forwardproxy@caddy2=github.com/klzgrad/forwardproxy@naive --with github.com/caddy-dns/cloudflare`; Caddyfile per node:
```
{
  order forward_proxy before file_server
  admin off
}
naive-<rand>.duylinh.net:443 {
  tls { dns cloudflare {env.CF_API_TOKEN} }   # DNS-01, no :80 needed
  forward_proxy {
    basic_auth kulinh <password>
    hide_ip
    hide_via
    probe_resistance
  }
  file_server { root /var/www/naive-decoy }
}
```
Unit `naive-caddy.service` (EnvironmentFile `/etc/cfvpn/cfvpn.env` for `CF_API_TOKEN`, `AmbientCapabilities=CAP_NET_BIND_SERVICE`), `ufw allow 443/tcp`, DNS A record command via the CF API (one hostname per node), Shadowrocket URIs `https://kulinh:<password>@naive-<rand>.duylinh.net:443#kulinh@JPY-01-Naive` and `...#kulinh@OR-001-Naive` in their own file `naive.txt` (never in the subscription or AUTO). Enable steps, verify (`curl -x https://kulinh:pw@host:443 http://cp.cloudflare.com/generate_204` → 204, and the HTTPUpgrade route still 204 afterwards), rollback (`systemctl disable --now naive-caddy; ufw delete allow 443/tcp`). Note: JPY-01 also runs the operator's personal cloudflared; confirm with `ss -lntp | grep ':443 '` that nothing else has claimed 443 right before enabling.

- [ ] **Step 2: XHTTP doc** — for OR-001, VNM-01, JPY-01: second inbound on `127.0.0.1:10002`, `network: xhttp`, `xhttpSettings: {path: "/api/v2/stream", mode: "packet-up"}`; cloudflared ingress line `hostname: <DOMAIN>, path: ^/api/v2/stream, service: http://127.0.0.1:10002` inserted before the HTTPUpgrade rule; client URI `vless://<uuid>@<DOMAIN>:443?encryption=none&security=tls&type=xhttp&host=<DOMAIN>&path=%2Fapi%2Fv2%2Fstream&mode=packet-up&sni=<DOMAIN>#kulinh@<NODE>-XHTTP`. Note the 2026-05-02 finding (`stream-up` 404 / `stream-one` 403 through cloudflared) and that `packet-up` is the mode to re-test. Enable: add inbound with `cfvpnctl` re-render disabled (hand edit + `xray run -test`), restart xray, restart cloudflared; rollback: remove both lines, restart both. HTTPUpgrade stays untouched throughout.

- [ ] **Step 3: DERP doc** — `derper` on HKG-01, `derper --hostname derp-<rand>.duylinh.net -a :8443 --stun-port 3478 --certmode manual --certdir /etc/derper --verify-clients`, LE cert via lego DNS-01 using `CF_API_TOKEN`, ufw `8443/tcp` + `3478/udp`, ACL snippet:
```json
"derpMap": {
  "OmitDefaultRegions": true,
  "Regions": { "900": { "RegionID": 900, "RegionCode": "hkg", "RegionName": "HKG-01",
    "Nodes": [{ "Name": "900a", "RegionID": 900, "HostName": "derp-<rand>.duylinh.net", "DERPPort": 8443, "STUNPort": 3478 }] } }
}
```
with the warning that `OmitDefaultRegions: true` makes this the only relay for the whole tailnet, so verify with `tailscale netcheck` from two devices before applying.

- [ ] **Step 4: Commit** — `git add docs/prep && git commit -m "docs(prep): NaiveProxy on JPY-01/OR-001:443, XHTTP second inbound for cloudflare nodes, custom DERP on HKG-01 — prepared, not deployed"`

---

### Task 7: REPORT.md

- [ ] **Step 1: Write `REPORT.md`** with: per-node table (before → after: mode, dest, HY2, cloudflared protocol, probe latency before/after from Task 0 and Task 4 runs), where secrets live (`/etc/cfvpn/cfvpn.env` on each node, backup dir), verification evidence (drift check output, probe output), decisions left to the operator (Telegram token, NaiveProxy/XHTTP/DERP go-ahead, disabling Tailscale key expiry on remaining nodes), and the xray version note.
- [ ] **Step 2: Commit** — `git add REPORT.md && git commit -m "docs: REPORT.md for the September 2026 fleet refresh"`
- [ ] **Step 3: Push branch and open the PR** — `git push -u origin feat/fleet-refresh-2026-09 && gh pr create --fill`.
