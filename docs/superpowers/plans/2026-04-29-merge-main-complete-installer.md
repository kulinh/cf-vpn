# Merge Main Complete Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge the current base repo and all useful worktree changes into one clean main branch that can install a fresh VLESS + Hysteria2 VPS and upgrade existing VPS nodes safely.

**Architecture:** Use `.claude/worktrees/direct-domain-routing` as the runtime and installer source of truth, because it contains the direct data-plane implementation, Hysteria2 renderer, agent support, and install/upgrade commands. Preserve the dirty base repo's Worker/public subscription and user upgrade work, then selectively adapt UI improvements from `users-upgrade-nodes-session` and `ui-redesign`. The final system keeps Cloudflare Tunnel for admin/control traffic only and uses DNS-only A records for VLESS TCP 443 and Hysteria2 UDP.

**Tech Stack:** Go CLI/agent, Xray VLESS, Hysteria2, cloudflared, systemd, Cloudflare DNS/Tunnel APIs, Cloudflare Worker, D1, React/Vite, Vitest.

---

## Source Map

**Primary source to merge from:**
- `.claude/worktrees/direct-domain-routing` — direct VLESS + Hysteria2 installer/runtime/agent/Worker subscription base.

**Current base branch source to preserve:**
- `/opt/cf-vpn` dirty `master` — public `/sub/:token`, `users.sub_token`, `userUpgradeNodes`, current panel API and tests.

**Selective UI sources:**
- `.claude/worktrees/users-upgrade-nodes-session` — Quick Add tab, Check/Edit/Copy/QR buttons, user upgrade UX.
- `.worktrees/ui-redesign` — Command Center/status strip/node-card layout patterns.

**Reference-only sources:**
- `.worktrees/plan1-agent` — compare only for missing agent build/systemd/test ideas.
- `.worktrees/users-upgrade-nodes` — older duplicate of user upgrade work; do not merge wholesale.
- `.claude/worktrees/agent-*` — mostly duplicates; only compare `agent-a8d316c5` for Worker test package scaffold if missing.

## Final File Responsibilities

- `cmd/cfvpnctl/main.go` — CLI entrypoint for fresh install, upgrade, rotate, cert-renew commands.
- `cmd/cfvpn-agent/main.go` — local admin agent listening on `127.0.0.1:6788`, exposing status, healthcheck, rotate, sync, and user operations.
- `internal/commands/install.go` — fresh install and in-place upgrade orchestration for direct mode.
- `internal/commands/rotate.go` — direct domain rotation for VLESS and Hysteria2 hosts.
- `internal/templates/render.go` — Xray direct config, Hysteria2 config, admin-only cloudflared config.
- `internal/hysteria/hysteria.go` — Hysteria2 config/user rendering and service reload.
- `internal/systemd/units.go` — systemd unit definitions for Xray, Hysteria2, cloudflared, agent, cert renewal.
- `internal/paths/paths.go` — canonical runtime filesystem paths.
- `internal/subscription/subscription.go` — local VLESS/HY2 subscription URI generation for installer output.
- `panel/worker/migrations/*.sql` — D1 schema for users, nodes, zones, events, sub tokens, and HY2 fields.
- `panel/worker/src/index.ts` — Worker route table, including unauthenticated `/sub/:token` and authenticated `/api/*`.
- `panel/worker/src/routes/nodes.ts` — node CRUD/status/healthcheck/rotate/sync API.
- `panel/worker/src/routes/users.ts` — user CRUD, upgrade-to-new-nodes, subscription-token API.
- `panel/worker/src/routes/sub.ts` — public token-based subscription endpoint.
- `panel/worker/src/lib/subscription.ts` — Worker-side VLESS + Hysteria2 URI generation.
- `panel/web/src/lib/api.ts` — typed frontend API client.
- `panel/web/src/lib/subscriptionLinks.ts` — browser-safe public subscription URL builder.
- `panel/web/src/pages/UsersPage.tsx` — user operations UI including subscription copy/QR and node upgrade.
- `panel/web/src/pages/NodesPage.tsx` or current node page component — node status/health/edit/rotate UI.
- `README.md` — final operator docs for fresh deploy and upgrade.

---

### Task 1: Preserve Current Work Before Merging

**Files:**
- Inspect: repository status in `/opt/cf-vpn`
- Preserve: all modified and untracked project files except generated artifacts

- [ ] **Step 1: Record current repository state**

Run:

```bash
git status --short
git branch --show-current
git worktree list --porcelain
```

Expected: current branch is `master`; dirty files include `README.md`, `panel/web/*`, `panel/worker/*`, docs, migrations, and tests.

- [ ] **Step 2: Create a safety branch from current master**

Run:

```bash
git switch -c merge/vless-hy2-complete-installer
```

Expected: new branch `merge/vless-hy2-complete-installer` with the same dirty working tree.

- [ ] **Step 3: Exclude generated artifacts from the merge branch**

Run:

```bash
git status --short | grep -E '(^.. panel/worker/node_modules/|^.. panel/worker/.wrangler/|^.. .wrangler/|^.. .playwright-mcp/)' || true
```

Expected: generated directories may appear, but they must not be staged or committed.

- [ ] **Step 4: Stage only source, tests, docs, package files, and migrations**

Run:

```bash
git add README.md docs/superpowers panel/web/package.json panel/web/package-lock.json panel/web/src panel/worker/package.json panel/worker/package-lock.json panel/worker/src panel/worker/migrations
```

Expected: generated `.wrangler`, `.playwright-mcp`, and `node_modules` remain unstaged.

- [ ] **Step 5: Commit the base preservation snapshot**

Run:

```bash
git commit -m "chore: preserve panel subscription and user upgrade work"
```

Expected: a new commit containing the useful dirty base work.

---

### Task 2: Import Direct Routing Runtime and Installer

**Files:**
- Replace/adapt from `.claude/worktrees/direct-domain-routing`: `cmd/`, `internal/`, root Go module files if present
- Modify: root installer/runtime files in `/opt/cf-vpn`

- [ ] **Step 1: Copy direct-routing Go runtime files into the merge branch**

Run:

```bash
rsync -a --delete --exclude '.git' --exclude 'node_modules' --exclude '.wrangler' /opt/cf-vpn/.claude/worktrees/direct-domain-routing/cmd/ /opt/cf-vpn/cmd/
rsync -a --delete --exclude '.git' --exclude 'node_modules' --exclude '.wrangler' /opt/cf-vpn/.claude/worktrees/direct-domain-routing/internal/ /opt/cf-vpn/internal/
```

Expected: `cmd/cfvpn-agent`, `cmd/cfvpnctl`, `internal/commands`, `internal/hysteria`, `internal/templates`, `internal/systemd`, and related packages match the direct-routing implementation.

- [ ] **Step 2: Copy Go module/build metadata if direct-routing has newer files**

Run:

```bash
cp /opt/cf-vpn/.claude/worktrees/direct-domain-routing/go.mod /opt/cf-vpn/go.mod
cp /opt/cf-vpn/.claude/worktrees/direct-domain-routing/go.sum /opt/cf-vpn/go.sum
```

Expected: root Go module can build both `cfvpnctl` and `cfvpn-agent`.

- [ ] **Step 3: Verify Go package list resolves**

Run:

```bash
go list ./...
```

Expected: all Go packages list successfully; no missing imports.

- [ ] **Step 4: Commit imported runtime**

Run:

```bash
git add cmd internal go.mod go.sum
git commit -m "feat: import direct VLESS and Hysteria2 runtime"
```

Expected: one commit containing the direct-routing Go runtime base.

---

### Task 3: Fix Mandatory Agent and Installer Bugs

**Files:**
- Modify: `cmd/cfvpn-agent/main.go`
- Modify: `internal/systemd/units.go`
- Modify: `internal/commands/install.go`
- Modify: `internal/commands/rotate.go` if needed
- Test: existing or new Go tests under `internal/commands`, `internal/systemd`, `cmd/cfvpn-agent`

- [ ] **Step 1: Fix agent listen address mismatch**

In `cmd/cfvpn-agent/main.go`, change the default agent listen address from `127.0.0.1:8787` to `127.0.0.1:6788`.

Expected code shape:

```go
addr := os.Getenv("CFVPN_AGENT_ADDR")
if addr == "" {
	addr = "127.0.0.1:6788"
}
```

- [ ] **Step 2: Fix cert-renew binary name**

In `internal/systemd/units.go`, ensure the cert-renew unit starts `cfvpnctl`, not `cfvpn`.

Expected unit line:

```ini
ExecStart=/usr/local/bin/cfvpnctl cert-renew
```

- [ ] **Step 3: Fix effective domain handling in install/upgrade**

In `internal/commands/install.go`, ensure all DNS, cert, host-generation, and saved-env logic uses the effective resolved domain variable, not raw `in.Domain` after defaults/auto-detection.

Expected pattern:

```go
domain := strings.TrimSpace(in.Domain)
if domain == "" {
	domain = env.Domain
}
if domain == "" {
	return errors.New("domain is required")
}
```

All later references in that function should use `domain` unless intentionally reading the original input.

- [ ] **Step 4: Run Go formatting**

Run:

```bash
gofmt -w cmd internal
```

Expected: no output; files are formatted.

- [ ] **Step 5: Run Go tests/build**

Run:

```bash
go test ./...
go build ./cmd/cfvpnctl ./cmd/cfvpn-agent
```

Expected: all tests pass and both binaries build.

- [ ] **Step 6: Commit mandatory runtime fixes**

Run:

```bash
git add cmd internal
git commit -m "fix: align direct installer and agent runtime defaults"
```

Expected: commit contains only bug fixes, not unrelated UI/Worker changes.

---

### Task 4: Normalize D1 Schema and Migrations

**Files:**
- Modify/create: `panel/worker/migrations/0001_initial.sql`
- Modify/create: `panel/worker/migrations/0002_direct_nodes.sql`
- Modify/create: `panel/worker/migrations/0003_users_sub_token.sql`
- Modify if needed: `panel/worker/src/types.ts`

- [ ] **Step 1: Compare all migration sources**

Run:

```bash
find panel/worker/migrations .claude/worktrees/direct-domain-routing/panel/worker/migrations .claude/worktrees/users-upgrade-nodes-session/panel/worker/migrations .worktrees/ui-redesign/panel/worker/migrations .worktrees/users-upgrade-nodes/panel/worker/migrations -maxdepth 1 -type f -name '*.sql' -print | sort
```

Expected: all migration files across base and worktrees are listed.

- [ ] **Step 2: Ensure final nodes schema supports direct VLESS + HY2**

The final `nodes` table must include these columns:

```sql
id TEXT PRIMARY KEY,
label TEXT NOT NULL,
admin_host TEXT NOT NULL,
vpn_host TEXT NOT NULL,
zone TEXT NOT NULL,
status TEXT NOT NULL DEFAULT 'unknown',
last_seen_at INTEGER,
latency_ms INTEGER,
created_at INTEGER NOT NULL,
public_ip TEXT,
mode TEXT NOT NULL DEFAULT 'direct',
hy2_host TEXT,
hy2_port INTEGER,
hy2_obfs_pw TEXT
```

If `0001_initial.sql` already exists in deployed systems, add missing direct-mode columns in a later migration with `ALTER TABLE` statements.

- [ ] **Step 3: Ensure final users schema supports public subscription tokens**

The final `users` table must include:

```sql
sub_token TEXT UNIQUE
```

The migration must support existing users by leaving `sub_token` nullable and letting `userSubscription` backfill on demand.

- [ ] **Step 4: Ensure final user_nodes schema stores both protocol credentials**

The final `user_nodes` table must include:

```sql
user_id TEXT NOT NULL,
node_id TEXT NOT NULL,
vless_uuid TEXT NOT NULL,
hy2_pw TEXT NOT NULL,
created_at INTEGER NOT NULL,
PRIMARY KEY (user_id, node_id)
```

Do not merge older scaffold code that inserts `user_nodes` without credentials.

- [ ] **Step 5: Ensure TypeScript NodeRow matches schema**

In `panel/worker/src/types.ts`, `NodeRow` must include:

```ts
public_ip: string | null;
mode: string;
hy2_host: string | null;
hy2_port: number | null;
hy2_obfs_pw: string | null;
```

- [ ] **Step 6: Commit migration normalization**

Run:

```bash
git add panel/worker/migrations panel/worker/src/types.ts
git commit -m "fix: normalize D1 schema for direct nodes and subscriptions"
```

Expected: migrations and Worker types agree with all SQL queries.

---

### Task 5: Merge Worker Node APIs With Agent Contract

**Files:**
- Modify: `panel/worker/src/routes/nodes.ts`
- Modify: `panel/worker/src/index.ts` if route order changed
- Test: `panel/worker/src/routes/nodes.test.ts`

- [ ] **Step 1: Keep direct-routing node SELECT fields**

In `panel/worker/src/routes/nodes.ts`, ensure the node select list includes all direct fields:

```ts
const NODE_SELECT_FIELDS = "id,label,admin_host,vpn_host,zone,status,last_seen_at,latency_ms,created_at,public_ip,mode,hy2_host,hy2_port,hy2_obfs_pw";
```

- [ ] **Step 2: Fix rotate payload sent to agent**

In `nodeRotate`, send the field names expected by `cmd/cfvpn-agent/main.go`:

```ts
{
  new_host: newVpnHost,
  new_zone_id: zone.cf_zone_id,
  old_host: row.vpn_host,
  old_zone_id: row.zone,
  new_hy2_host: newHy2Host,
  new_hy2_zone_id: zone.cf_zone_id,
  old_hy2_host: row.hy2_host,
  old_hy2_zone_id: row.zone
}
```

Do not use `new_vpn_host`, `new_vpn_zone_id`, `old_vpn_host`, or `old_vpn_zone_id`.

- [ ] **Step 3: Persist direct fields from rotate response**

After successful rotate, update:

```sql
vpn_host=?, hy2_host=?, hy2_port=?, hy2_obfs_pw=?, zone=?, status='active', last_seen_at=?
```

using agent response fields where present and preserving existing HY2 port/obfs when the agent does not rotate them.

- [ ] **Step 4: Persist direct fields from status response**

After successful status, update `public_ip`, `mode`, `hy2_host`, `hy2_port`, and `hy2_obfs_pw` when returned by the agent, alongside `status`, `last_seen_at`, and `latency_ms`.

- [ ] **Step 5: Keep admin host validation from base repo**

Ensure `createNode` and `patchNode` still call `validateAdminHost` before writing `admin_host`.

- [ ] **Step 6: Add or update Worker route tests**

In `panel/worker/src/routes/nodes.test.ts`, include a rotate test that asserts the outgoing mocked agent request body has `new_host` and `new_zone_id`, not `new_vpn_host`.

Expected test assertion shape:

```ts
expect(JSON.parse(agentRequest.body as string)).toMatchObject({
  new_host: expect.any(String),
  new_zone_id: "zone-id"
});
expect(JSON.parse(agentRequest.body as string)).not.toHaveProperty("new_vpn_host");
```

- [ ] **Step 7: Run Worker tests for node routes**

Run:

```bash
cd panel/worker && npm test -- src/routes/nodes.test.ts
```

Expected: node route tests pass.

- [ ] **Step 8: Commit Worker node API merge**

Run:

```bash
git add panel/worker/src/routes/nodes.ts panel/worker/src/routes/nodes.test.ts panel/worker/src/index.ts
git commit -m "fix: align Worker node APIs with direct agent contract"
```

Expected: node API can drive direct VLESS + HY2 nodes.

---

### Task 6: Merge Worker User, Upgrade, and Subscription APIs

**Files:**
- Modify: `panel/worker/src/routes/users.ts`
- Modify: `panel/worker/src/routes/sub.ts`
- Modify: `panel/worker/src/lib/subscription.ts`
- Modify: `panel/worker/src/index.ts`
- Test: `panel/worker/src/routes/users.test.ts`
- Test: `panel/worker/src/routes/sub.test.ts`

- [ ] **Step 1: Preserve token-based public subscription route**

In `panel/worker/src/index.ts`, keep this route before `/api/*` auth enforcement:

```ts
const subToken = parseSubToken(pathname);
if (subToken && request.method === "GET") {
  return publicSubscription(env, subToken);
}
```

Expected: `/sub/:token` remains unauthenticated.

- [ ] **Step 2: Preserve create-user credential storage**

In `panel/worker/src/routes/users.ts`, `createUser` must call each active node agent and store both fields:

```ts
.bind(id, node.id, creds.vless_uuid, creds.hy2_pw, nowTs())
```

Expected: no code path creates a `user_nodes` row without VLESS and HY2 credentials.

- [ ] **Step 3: Preserve idempotent user node upgrade**

Keep `POST /api/users/:id/upgrade-nodes` behavior:
- return `404` if user does not exist
- compute active nodes missing from `user_nodes`
- call `/admin/v1/users` only for missing nodes
- store `vless_uuid` and `hy2_pw`
- return `200` on full success, `207` on partial success, `502` only when every attempted node fails

- [ ] **Step 4: Ensure admin subscription endpoint returns body and token**

`GET /api/users/:id/subscription` must return:

```json
{
  "subscription_url": "<base64 body or URI text depending existing API>",
  "sub_token": "<32 hex token>"
}
```

Use the current base repo response shape so the frontend `getUserSubscription` keeps working.

- [ ] **Step 5: Ensure public subscription emits VLESS + HY2 lines**

In `panel/worker/src/lib/subscription.ts`, `buildSubscriptionURIs` must emit:
- one VLESS URI per user-node row
- one Hysteria2 URI per row when `hy2_host`, `hy2_port`, `hy2_obfs_pw`, and `hy2_pw` are present

Expected HY2 URI shape:

```text
hysteria2://<username>:<password>@<host>:<port>/?obfs=salamander&obfs-password=<obfsPw>&insecure=0#<tag>
```

- [ ] **Step 6: Run Worker user/subscription tests**

Run:

```bash
cd panel/worker && npm test -- src/routes/users.test.ts src/routes/sub.test.ts
```

Expected: tests pass and confirm VLESS + HY2 subscription output.

- [ ] **Step 7: Commit user/subscription merge**

Run:

```bash
git add panel/worker/src/index.ts panel/worker/src/routes/users.ts panel/worker/src/routes/sub.ts panel/worker/src/lib/subscription.ts panel/worker/src/routes/users.test.ts panel/worker/src/routes/sub.test.ts
git commit -m "feat: unify user upgrades and VLESS HY2 subscriptions"
```

Expected: Worker user lifecycle is complete for direct nodes.

---

### Task 7: Merge Frontend API Client and Subscription Helpers

**Files:**
- Modify: `panel/web/src/lib/api.ts`
- Modify: `panel/web/src/lib/types.ts`
- Modify/create: `panel/web/src/lib/subscriptionLinks.ts`
- Test: `panel/web/src/lib/subscriptionLinks.test.ts`

- [ ] **Step 1: Extend frontend Node type for direct fields**

In `panel/web/src/lib/types.ts`, ensure `Node` includes:

```ts
publicIp?: string | null
mode?: string
hy2Host?: string | null
hy2Port?: number | null
hy2ObfsPw?: string | null
```

- [ ] **Step 2: Parse direct fields in API client**

In `panel/web/src/lib/api.ts`, extend `NodeApiResponse` and `parseNode` to map:

```ts
publicIp: raw.public_ip ?? null,
mode: raw.mode ?? 'direct',
hy2Host: raw.hy2_host ?? null,
hy2Port: raw.hy2_port ?? null,
hy2ObfsPw: raw.hy2_obfs_pw ?? null,
```

- [ ] **Step 3: Preserve rotate response compatibility**

Keep `parseRotateNodeResponse` accepting both `new_host` and `vpn_host`:

```ts
const vpnHost = raw.new_host ?? raw.vpn_host
```

Expected: frontend works with both current Worker response variants while final Worker should return `new_host`.

- [ ] **Step 4: Preserve public subscription URL builder**

Keep `buildPublicSubscriptionUrl(window.location.origin, data.sub_token)` in `getUserSubscription` and tests for origin/path correctness.

- [ ] **Step 5: Run frontend helper tests**

Run:

```bash
cd panel/web && npm test -- src/lib/subscriptionLinks.test.ts
```

Expected: subscription link helper tests pass.

- [ ] **Step 6: Commit frontend API merge**

Run:

```bash
git add panel/web/src/lib/api.ts panel/web/src/lib/types.ts panel/web/src/lib/subscriptionLinks.ts panel/web/src/lib/subscriptionLinks.test.ts
git commit -m "feat(panel): expose direct node and subscription fields"
```

Expected: frontend API layer matches final Worker schema.

---

### Task 8: Merge Final Control Panel UI

**Files:**
- Modify: `panel/web/src/pages/UsersPage.tsx`
- Modify: `panel/web/src/pages/UsersPage.test.tsx`
- Modify: node/dashboard page files currently used by the app
- Modify: shared CSS/component files currently used by the app

- [ ] **Step 1: Import Quick Add and user action UX from session worktree**

Compare and adapt from:

```text
.claude/worktrees/users-upgrade-nodes-session/panel/web/src/pages/UsersPage.tsx
```

Preserve these behaviors:
- quick add user
- copy subscription link
- QR display if currently implemented
- upgrade user to missing nodes
- clear success/error states after relevant actions

- [ ] **Step 2: Import useful visual layout from UI redesign**

Compare and adapt from:

```text
.worktrees/ui-redesign/panel/web/src
```

Preserve only production-compatible UI improvements:
- command/status summary
- clearer node cards
- improved users/events layout

Do not merge mock data, demo-only state, or UI that assumes missing backend endpoints.

- [ ] **Step 3: Show direct node runtime details**

Node UI should display these fields when available:

```text
VLESS host: node.vpnHost
Hysteria2 host: node.hy2Host
Hysteria2 UDP port: node.hy2Port
Public IP: node.publicIp
Mode: node.mode
```

- [ ] **Step 4: Keep user upgrade result visibility**

After `upgradeUserNodes`, show:

```text
Added: <addedCount>, failed: <failedCount>, total after upgrade: <totalNodesAfterUpgrade>
```

- [ ] **Step 5: Run UsersPage tests**

Run:

```bash
cd panel/web && npm test -- src/pages/UsersPage.test.tsx
```

Expected: tests pass for create user, subscription copy, and upgrade nodes.

- [ ] **Step 6: Run full frontend test suite**

Run:

```bash
cd panel/web && npm test
```

Expected: all frontend tests pass.

- [ ] **Step 7: Commit UI merge**

Run:

```bash
git add panel/web/src
git commit -m "feat(panel): merge direct node and user management UI"
```

Expected: final UI is production-backed and contains no demo-only assumptions.

---

### Task 9: Remove Trojan Runtime and Documentation Drift

**Files:**
- Modify: `README.md`
- Modify: Go files under `internal/` if any Trojan runtime remains in install/upgrade paths
- Modify: docs under `docs/superpowers/specs` and `docs/superpowers/plans` only if they are presented as current docs

- [ ] **Step 1: Search for Trojan references**

Run:

```bash
grep -RIn "trojan\|Trojan\|TROJAN" README.md cmd internal panel docs --exclude-dir=node_modules --exclude-dir=.wrangler || true
```

Expected: references may remain in historical docs or migration notes, but not in current install/runtime instructions.

- [ ] **Step 2: Remove Trojan from current operator docs**

In `README.md`, describe only:
- VLESS over TCP 443 via Xray
- Hysteria2 over UDP configured port
- Cloudflare Tunnel for admin host only
- Worker/D1/panel control plane
- public `/sub/:token` subscription

- [ ] **Step 3: Remove Trojan from active install output**

In Go installer/subscription code, ensure fresh install and upgrade output does not generate Trojan links or ask operators for Trojan credentials.

Historical helper functions may remain only if unused and not visible in operator paths; prefer deleting unused Trojan helpers if tests confirm no callers.

- [ ] **Step 4: Commit Trojan cleanup**

Run:

```bash
git add README.md cmd internal docs
git commit -m "docs: align current system with VLESS and Hysteria2"
```

Expected: current docs and runtime no longer imply Trojan is active.

---

### Task 10: Verify Fresh Install Path in Dry-Run/Build Mode

**Files:**
- No source changes expected unless tests reveal bugs
- Test: Go build/test outputs

- [ ] **Step 1: Run full Go tests**

Run:

```bash
go test ./...
```

Expected: all Go tests pass.

- [ ] **Step 2: Build final binaries**

Run:

```bash
go build -o /tmp/cfvpnctl ./cmd/cfvpnctl
go build -o /tmp/cfvpn-agent ./cmd/cfvpn-agent
```

Expected: binaries build without errors.

- [ ] **Step 3: Inspect CLI help**

Run:

```bash
/tmp/cfvpnctl --help
/tmp/cfvpnctl install --help
/tmp/cfvpnctl upgrade --help
```

Expected: help includes fresh install and upgrade commands with direct/HY2 options; no Trojan-first wording.

- [ ] **Step 4: Verify systemd unit rendering by tests or generated output**

Run existing tests, or add a minimal unit test if none exists, asserting:

```text
cfvpn-agent listens behind cloudflared at 127.0.0.1:6788
cfvpn-hysteria.service starts /usr/local/bin/hysteria server -c ...
cfvpn-cert-renew.service starts /usr/local/bin/cfvpnctl cert-renew
```

Expected: rendered units match final runtime.

- [ ] **Step 5: Commit verification fixes if any**

If code changes were needed:

```bash
git add cmd internal
git commit -m "test: verify installer runtime units"
```

Expected: no commit if no changes were needed.

---

### Task 11: Verify Worker and D1 Integration

**Files:**
- Modify only if tests reveal bugs: `panel/worker/src/**`, `panel/worker/migrations/**`

- [ ] **Step 1: Install Worker dependencies if needed**

Run:

```bash
cd panel/worker && npm install
```

Expected: `package-lock.json` is updated only if dependencies were missing or changed intentionally.

- [ ] **Step 2: Run Worker typecheck**

Run:

```bash
cd panel/worker && npm run typecheck
```

Expected: no TypeScript errors.

- [ ] **Step 3: Run Worker tests**

Run:

```bash
cd panel/worker && npm test
```

Expected: all Worker tests pass.

- [ ] **Step 4: Validate migrations against local D1 if configured**

Run:

```bash
cd panel/worker && npx wrangler d1 migrations list DB --local
```

Expected: wrangler can parse the D1 binding and migration directory. If local DB is unavailable, document that this step was not run and why.

- [ ] **Step 5: Commit Worker verification fixes if any**

If code changes were needed:

```bash
git add panel/worker/package.json panel/worker/package-lock.json panel/worker/src panel/worker/migrations
git commit -m "test(worker): verify direct subscription APIs"
```

Expected: no commit if no changes were needed.

---

### Task 12: Verify Frontend UI in Browser

**Files:**
- Modify only if browser testing reveals bugs: `panel/web/src/**`

- [ ] **Step 1: Run frontend typecheck and tests**

Run:

```bash
cd panel/web && npm run typecheck
cd panel/web && npm test
```

Expected: typecheck and tests pass.

- [ ] **Step 2: Start frontend dev server**

Run:

```bash
cd panel/web && npm run dev -- --host 127.0.0.1
```

Expected: Vite starts and prints a local URL.

- [ ] **Step 3: Open the UI in a browser**

Use Playwright or a browser to visit the local Vite URL.

Golden-path checks:
- dashboard loads without console errors
- nodes list shows VLESS/HY2/direct fields
- users list loads
- create user form validates empty names and accepts valid names
- subscription copy/QR action works
- upgrade nodes action displays result counts

- [ ] **Step 4: Check browser console**

Expected: no uncaught runtime errors.

- [ ] **Step 5: Commit frontend verification fixes if any**

If code changes were needed:

```bash
git add panel/web/src
git commit -m "fix(panel): resolve direct management UI issues"
```

Expected: no commit if no changes were needed.

---

### Task 13: Write Final Fresh Deploy and Upgrade Runbook

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document fresh VPS deploy flow**

`README.md` must include the operator sequence:

```bash
# Build locally or on target
make build

# Copy/install binaries
sudo install -m 0755 cfvpnctl /usr/local/bin/cfvpnctl
sudo install -m 0755 cfvpn-agent /usr/local/bin/cfvpn-agent

# Fresh direct install
sudo CF_API_TOKEN=... CF_ACCOUNT_ID=... cfvpnctl install \
  --mode direct \
  --domain example.com \
  --node-id sin-01 \
  --user user1
```

Adjust exact flags to match the actual CLI.

- [ ] **Step 2: Document existing VPS upgrade flow**

`README.md` must include:

```bash
sudo install -m 0755 cfvpnctl /usr/local/bin/cfvpnctl
sudo install -m 0755 cfvpn-agent /usr/local/bin/cfvpn-agent
sudo CF_API_TOKEN=... CF_ACCOUNT_ID=... cfvpnctl upgrade --mode direct
sudo systemctl status cfvpn-xray cfvpn-hysteria cfvpn-agent cloudflared
```

Adjust exact flags/service names to match actual code.

- [ ] **Step 3: Document Cloudflare/D1/panel setup**

Include:
- Worker deployment
- D1 migration command
- required Worker vars/secrets
- `ADMIN_HOST_ALLOWED_SUFFIXES`
- public subscription format `/sub/:token`

- [ ] **Step 4: Document smoke tests**

Include:

```bash
curl -fsS https://<admin-host>/admin/v1/status
curl -fsS https://<worker-host>/api/nodes
curl -fsS https://<worker-host>/sub/<token>
```

Also include client checks:
- import subscription into Shadowrocket or compatible client
- test VLESS TCP 443
- test Hysteria2 UDP port

- [ ] **Step 5: Commit final README**

Run:

```bash
git add README.md
git commit -m "docs: add direct deploy and upgrade runbook"
```

Expected: README is the current source of truth for deployment.

---

### Task 14: Final Full-Suite Gate and Cleanup

**Files:**
- Entire repository

- [ ] **Step 1: Run full repository status check**

Run:

```bash
git status --short
```

Expected: no generated artifacts staged; only intentional source changes remain if any.

- [ ] **Step 2: Run full backend and frontend checks**

Run:

```bash
go test ./...
cd panel/worker && npm run typecheck && npm test
cd ../web && npm run typecheck && npm test
```

Expected: all checks pass.

- [ ] **Step 3: Search for generated artifacts before final commit**

Run:

```bash
git status --short | grep -E 'node_modules|\.wrangler|\.playwright-mcp' && exit 1 || true
```

Expected: command exits successfully because no generated artifacts are tracked/staged.

- [ ] **Step 4: Review final commit history**

Run:

```bash
git log --oneline --decorate -n 20
```

Expected: commits are grouped by preservation, runtime import, bug fixes, migrations, Worker, frontend, docs.

- [ ] **Step 5: Rename branch to final integration branch if desired**

Run:

```bash
git branch -m main-vless-hy2-installer
```

Expected: branch is ready to review and merge into `master`/`main`.

- [ ] **Step 6: Prepare final merge command for the operator**

After review approval, merge into the canonical branch:

```bash
git switch master
git merge --no-ff main-vless-hy2-installer
```

Expected: `master` contains the single complete installer/runtime/control-panel implementation.

---

## Final Acceptance Criteria

- Fresh VPS install produces:
  - Xray VLESS on TCP 443 with TLS and WebSocket `/vless`
  - Hysteria2 on UDP configured port with salamander obfs
  - admin-only cloudflared tunnel to `127.0.0.1:6788`
  - systemd units enabled for Xray, Hysteria2, cloudflared, agent, cert renewal
  - Cloudflare DNS-only A records for VLESS and HY2 data-plane hosts
- Existing VPS upgrade preserves users and adds/backfills HY2 credentials/config without requiring manual user recreation.
- Worker D1 schema matches all code queries.
- `POST /api/users/:id/upgrade-nodes` adds existing users to newly added active nodes.
- `/sub/:token` is unauthenticated, token-protected, no-store, and emits both VLESS and HY2 links.
- Panel can manage nodes/users, copy subscriptions, show direct runtime fields, and upgrade users to missing nodes.
- No current docs describe Trojan as active runtime.
- Full Go, Worker, and Web test/typecheck gates pass.
- Generated directories such as `node_modules`, `.wrangler`, and `.playwright-mcp` are not committed.
