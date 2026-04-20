# Multi-VPS Control Panel — Design

**Status:** Draft for review
**Date:** 2026-04-20
**Supersedes / extends:** [2026-04-19-cf-vpn-design.md](2026-04-19-cf-vpn-design.md)

## Goal

Extend the existing single-VPS cf-vpn project to support a fleet of VPS
nodes across multiple geographies (Singapore, Hong Kong, Japan, Vietnam,
etc.), managed from a single mobile-friendly web panel reachable even
when the operator is behind a restrictive firewall.

Constraints:
- Keep the existing `cfvpnctl` Go CLI and its per-VPS state unchanged.
- No new attack surface on VPS public IPs; all traffic keeps flowing
  through Cloudflare Tunnel outbound connections.
- Panel must be reachable from locations where common platforms
  (Telegram, SSH to arbitrary IPs) are blocked.
- Fleet size is small (≤10 VPS, ≤5 VPN users).

## Non-goals

- Live log streaming, real-time metrics dashboards, bulk scheduled
  operations, or paid-tier CF features.
- Multi-tenant panel (single operator).
- Proactive monitoring / alerting (lazy refresh on app open is enough
  for MVP).

## Architecture overview

```
Mobile/Web (operator) ──HTTPS──► Cloudflare Pages (panel SPA, static)
                                        │ fetch /api/*
                                        ▼
                                 Cloudflare Worker (control logic)
                                  │ bindings: D1, KV (cache)
                                  │ auth: CF Access (email/OAuth)
                                  ▼
              ┌──────────── outbound HTTPS + CF Access Service Token ────────────┐
              ▼                                   ▼                              ▼
   admin-sg.<zone>                     admin-hk.<zone>                admin-jp.<zone>
   (CF Access policy:                  (same)                         (same)
    service token only)
              │ cloudflared ingress /admin/* → 127.0.0.1:8787
              ▼
   ┌──────────────────────┐
   │ VPS (any region)     │
   │                      │
   │ cfvpn-agent (Go)     │  new — HTTP API wrapping internal/commands
   │ cfvpnctl (Go)        │  unchanged
   │ xray                 │  unchanged
   │ cloudflared          │  config gains /admin ingress
   └──────────────────────┘
```

**New components:**
1. `cfvpn-agent` — Go binary on each VPS, listens on `127.0.0.1:8787`,
   reuses `internal/commands`. Runs as `cfvpn-agent.service`.
2. `/admin/*` ingress in `cloudflared` config on each VPS.
3. Stable admin hostname per node (`admin-<label>.<zone>`), separate
   from the rotating VPN hostname.
4. Cloudflare Worker + D1 + Pages project (in a new `panel/`
   subdirectory of this repo, or a sibling repo).

**Unchanged components:** `cfvpnctl` CLI surface, all `/etc/cfvpn/*`
state files, xray config layout, healthcheck timer.

**Key design choice — stable admin hostname:** the VPN hostname (e.g.
`sg.vpn.example.com`) rotates freely; the admin hostname stays put
across rotations. Admin hostname is protected by CF Access (service
token) and not shared with VPN users, so censors cannot find it to
block it. This lets the panel retain a stable identity for each node
independent of VPN domain rotation.

## Decisions (from brainstorming)

| # | Topic | Decision |
|---|---|---|
| 1 | Panel ↔ VPS transport | Agent on VPS, reachable via existing cloudflared tunnel (path `/admin/*`) |
| 2 | Panel hosting | Cloudflare Pages (SPA) + Workers (API) + D1 (state) |
| 3 | Operator login to panel | Cloudflare Access (email OTP or OAuth) |
| 4 | Panel → Agent auth | CF Access Service Token on each admin hostname |
| 5 | VPS registration flow | Manual — operator adds a VPS via the panel UI |
| 6 | VPN user model | Hybrid — global user identity in panel, per-node unique credentials |
| 7 | MVP scope | Minimal + audit log |
| 8a | Rotate-domain zone source | Pool of zones, configurable in panel Settings; random zone chosen per rotation |
| 8b | Rotate-domain subdomain pattern | Random hex, 6–8 chars |
| 9 | UI language | English only |

## Data model (Cloudflare D1)

```sql
CREATE TABLE nodes (
  id           TEXT PRIMARY KEY,          -- slug: 'sg', 'hk', 'jp'
  label        TEXT NOT NULL,             -- 'Singapore'
  admin_host   TEXT NOT NULL,             -- 'admin-sg.example.com' (stable)
  vpn_host     TEXT NOT NULL,             -- 'k7wz3r2a.example.com' (rotates)
  zone         TEXT NOT NULL,             -- zone of vpn_host
  status       TEXT NOT NULL DEFAULT 'active',   -- active | disabled | unreachable
  last_seen_at INTEGER,
  created_at   INTEGER NOT NULL
);

CREATE TABLE users (
  id         TEXT PRIMARY KEY,            -- slug: 'alice'
  name       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE user_nodes (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  node_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  vless_uuid TEXT NOT NULL,
  trojan_pw  TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, node_id)
);

CREATE TABLE zones (
  name       TEXT PRIMARY KEY,            -- 'example.com'
  cf_zone_id TEXT NOT NULL,               -- CF API zone id
  enabled    INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

CREATE TABLE events (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  ts       INTEGER NOT NULL,
  actor    TEXT NOT NULL,                 -- email from CF Access header
  action   TEXT NOT NULL,                 -- 'user.add', 'node.rotate', ...
  node_id  TEXT,
  user_id  TEXT,
  outcome  TEXT NOT NULL,                 -- 'ok' | 'partial' | 'error'
  detail   TEXT                           -- JSON blob
);
CREATE INDEX idx_events_ts ON events(ts DESC);
```

**Consistency rule:** D1 is the declared *intent*; the VPS state files
are runtime *truth*. When they drift, reconciliation is a manual,
explicit operator action (see `/api/nodes/{id}/sync`). Credentials are
stored plaintext in D1 (required to assemble subscriptions) and are
only accessible through the Worker, which is gated by CF Access.

CF API tokens are **not** stored in D1. They stay in `/etc/cfvpn/cfvpn.env`
on each VPS where they already live. The panel never handles them.

## Agent API (cfvpn-agent)

Binary: new `cmd/cfvpn-agent/main.go`, imports
`internal/commands`, `internal/cloudflare`, etc.

Binding: `127.0.0.1:8787` (never exposed directly to the internet;
cloudflared proxies `/admin/*` to it).

Auth: none at agent level — CF Access at the edge rejects
unauthenticated callers before they reach the VPS. Agent only trusts
its loopback binding.

### Endpoints

```
POST   /admin/v1/users              body: {name}
       → 200 {name, vless_uuid, trojan_pw}
       Calls commands.AddUser and returns the freshly generated creds
       so the Worker can persist them to D1.

DELETE /admin/v1/users/{name}       → 200 {ok: true}

GET    /admin/v1/users              → 200 [{name, vless_uuid, trojan_pw}]
       Used by /sync reconciliation.

GET    /admin/v1/status             → 200 {
                                        xray: "active"|"inactive",
                                        cloudflared: "active"|"inactive",
                                        vpn_host, tunnel_uuid,
                                        last_rotate_at
                                      }

POST   /admin/v1/healthcheck        → 200 {ok: true|false, code, latency_ms}

POST   /admin/v1/rotate-domain      body: {new_host, new_zone_id}
       → 200 {tunnel_uuid, vpn_host}
       Wraps commands.RotateDomain. New endpoint accepts an explicit
       zone id (see §"Required cfvpnctl changes").

POST   /admin/v1/rotate-cleanup     body: {old_tunnel_uuid} → 200 {ok}

POST   /admin/v1/sync               body: {users: [{name, vless_uuid, trojan_pw}]}
       → 200 {added: [...], removed: [...]}
       Reconciles VPS state to match the list provided by the panel.
```

All responses are JSON. Error responses are HTTP 4xx/5xx with body
`{error: "<code>", detail: "<message>"}`. Long-running operations
(rotate) must return within 120s or the Worker times out; the agent
returns the final outcome only, not progress.

### Required `cfvpnctl` changes

Small, targeted edits to support the agent without duplicating logic:

- `commands.RotateDomain` currently infers the zone from the domain
  string. Add a variant that accepts an explicit `cf_zone_id` so the
  agent can rotate to a subdomain in a different zone than the current
  one. (Needed because the zone pool can span multiple registered
  zones.)
- `commands.AddUser` already returns the generated UUID/password via
  the subscription file path; expose the struct directly so the agent
  can serialize it without re-reading files.

No changes to the CLI surface; the CLI still wraps these same
functions.

### systemd unit

```
/etc/systemd/system/cfvpn-agent.service
  ExecStart=/usr/local/bin/cfvpn-agent
  Environment=CFVPN_AGENT_ADDR=127.0.0.1:8787
  Restart=on-failure
  EnvironmentFile=/etc/cfvpn/cfvpn.env
```

### cloudflared ingress addition

Every node's `/etc/cfvpn/cloudflared/config.yml` gains one ingress
rule before the catch-all:

```yaml
ingress:
  - hostname: admin-<label>.<zone>
    path: /admin/*
    service: http://127.0.0.1:8787
  # existing /vless, /trojan, fallback 404 rules continue below
```

## Panel (Cloudflare Pages + Workers)

**Stack:** Vite + React + TypeScript + Tailwind. No global state
library; React Query (or plain `useState` + `fetch`) is enough.

**Routing:** 5 screens (mobile-portrait optimized):

1. **Home** — node status grid, user count card, quick links to add
   node / add user / view events.
2. **Node detail** — per-VPS page: status, VPN host, admin host,
   rotate button, force healthcheck, list of users on that node,
   edit/remove node.
3. **User detail** — per-user page: nodes they're on, subscription
   (QR + copy), per-node remove, global remove.
4. **Events** — last 200 audit log entries, filterable by actor /
   action / node.
5. **Settings** — zone pool CRUD.

### Worker API (internal, SPA is the only client)

```
GET    /api/me                      → {email}  (from CF Access header)
GET    /api/nodes                   → [{id, label, status, ...}]
POST   /api/nodes                   body: {id, label, admin_host, vpn_host, zone}
PATCH  /api/nodes/{id}              partial update
DELETE /api/nodes/{id}
GET    /api/nodes/{id}              → {...full node incl. live status via agent}
GET    /api/nodes/{id}/status       → live status from agent
POST   /api/nodes/{id}/rotate       → {new_host, tunnel_uuid}
POST   /api/nodes/{id}/healthcheck  → {ok, code}
POST   /api/nodes/{id}/sync         → {diff or applied ops}

GET    /api/users                   → [{id, name, nodes: [...]}]
POST   /api/users                   body: {name}
                                    → fan-out add-user to every active node
DELETE /api/users/{id}              → fan-out remove-user; delete row when all done
GET    /api/users/{id}/subscription → {text, qr_data}  (built in Worker)

GET    /api/zones                   → [{name, cf_zone_id, enabled}]
POST   /api/zones
PATCH  /api/zones/{name}
DELETE /api/zones/{name}

GET    /api/events?limit=200        → [...]
```

Worker authorization: a middleware rejects any request lacking
`Cf-Access-Authenticated-User-Email` (populated by CF Access at the
edge). The value is also used as `events.actor`.

Worker → Agent calls: every outbound request attaches the
`CF-Access-Client-Id` and `CF-Access-Client-Secret` headers from
Worker secrets.

### Add-user flow (fan-out example)

1. Operator taps `[+ Add user]`, enters `alice`, submits.
2. Worker: `INSERT INTO users (id, name, created_at) VALUES ('alice', ...)`.
3. Worker: `SELECT id, admin_host FROM nodes WHERE status='active'`.
4. For each node in parallel (Promise.allSettled):
   `POST https://<admin_host>/admin/v1/users` body `{name: "alice"}`.
5. For each fulfilled response: `INSERT INTO user_nodes (...)`.
6. Worker records ONE event row with `outcome = ok | partial | error`
   and `detail` listing per-node results.
7. SPA surfaces any partial failures as a banner: "alice added on
   3 of 4 nodes — retry on HK?" with a one-click retry button.

### Gen-subscription flow

1. SPA hits `GET /api/users/alice/subscription`.
2. Worker `JOIN users × user_nodes × nodes` for alice, builds a merged
   subscription: one `vless://` line per node plus one `trojan://`
   line per node, each pointing to that node's current `vpn_host`.
3. Worker returns plaintext + base64 form; SPA renders QR client-side
   via `qrcode` npm lib.
4. **Note:** gen-sub is always computed from current D1 state — after
   a rotation, a re-fetch yields updated URLs automatically.

## Rotate-domain flow

Trigger: `POST /api/nodes/sg/rotate` (no body).

Worker logic:
1. `SELECT name, cf_zone_id FROM zones WHERE enabled=1 ORDER BY RANDOM() LIMIT 1`.
   If empty: return 400 "No enabled zones; add one in Settings".
2. `subdomain = crypto.getRandomValues(new Uint8Array(4))`, hex-encoded
   → 8 chars (e.g. `k7wz3r2a`).
3. `new_host = subdomain + '.' + zone.name`.
4. `POST admin-sg.<zone>/admin/v1/rotate-domain`
   body `{new_host, new_zone_id: zone.cf_zone_id}`. Timeout 120s.
5. On agent success (returns `tunnel_uuid, vpn_host`):
   `UPDATE nodes SET vpn_host = ?, zone = ? WHERE id = 'sg'`.
6. Log event `action='node.rotate'` with old/new host in `detail`.
7. SPA shows the new host and a banner:
   "Subscriptions refreshed. Send updated QR to affected users."

Rotation does **not** change `admin_host` and does **not** change any
`user_nodes` credentials (UUIDs remain valid; only the URL changes).

### Edge cases

- **Subdomain collision** (another CNAME already exists on that zone):
  agent returns an error; Worker retries once with a fresh random
  subdomain before surfacing failure.
- **Agent error mid-rotation** (CF API 5xx, tunnel didn't come up):
  Worker keeps D1 unchanged, logs `outcome=error`. The operator
  can retry or manually inspect via SSH.
- **Cleanup of old tunnel:** SPA's Node detail shows a "Cleanup old
  tunnel" button, enabled 5 minutes after the last rotation. It calls
  `POST /admin/v1/rotate-cleanup` with the old tunnel UUID.
- **Empty zone pool:** UI disables rotate button with a tooltip
  explaining why.

## Error handling & consistency

**Principle:** D1 is intent, VPS is truth. When they drift, surface
the diff for the operator — do not auto-reconcile.

| Failure | Response |
|---|---|
| Agent unreachable (timeout / 5xx / CF 502) | Mark `nodes.status='unreachable'`, log event, UI shows red dot. No automatic retry. |
| Fan-out add-user: partial success | Keep completed `user_nodes` rows, create `users` row, log `outcome=partial`, UI banner with per-node retry. |
| Fan-out remove-user: partial success | Keep completed deletes, log partial, banner with retry. `users` row deleted only when all `user_nodes` for that user are gone. |
| D1 write fails after agent succeeded | Log `outcome=partial_state`, UI warns operator; resolve via Sync. |
| Rotate fails mid-flight | D1 unchanged. Agent's existing `rotate.go` already rolls back on error; any leftover resources surface via a follow-up "cleanup" action. |
| Service token compromise | Rotate in CF Access dashboard, update Worker secret. No VPS-side action. |

### `/api/nodes/{id}/sync`

When triggered:
1. Worker calls `GET admin-<id>/admin/v1/users` to fetch VPS truth.
2. Compares to D1 `SELECT * FROM user_nodes WHERE node_id=?`.
3. Returns a diff to the SPA:
   - In D1 but not on node → offer "Push" (re-add to node with D1 creds).
   - On node but not in D1 → offer "Import" or "Remove from node".
4. Operator picks per-row; no auto-apply.

### Healthcheck / liveness

No proactive polling in the MVP:
- `GET /api/nodes/{id}/status` is called lazily when the SPA opens Home
  or a Node detail page.
- Worker fans out to agents with a short (5s) timeout, updates
  `nodes.last_seen_at` and `status` opportunistically.
- A future CF Cron Trigger can add proactive healthchecks; not in MVP.

### Rate limiting

Worker rejects >10 req/s per user (keyed by CF Access email). Purpose:
guard against accidental retry loops, not DoS defense (CF edge already
handles that upstream of the Worker).

## Testing strategy

### Agent (Go)

- Unit test each handler with `httptest.Server`, reusing the existing
  `internal/commands/*_test.go` harness (these already mock CF API and
  systemd).
- Integration test: start the agent on a random port, issue
  `POST /admin/v1/users`, assert the generated state on disk matches.
- Auth test: agent does not verify auth tokens (delegated to CF Access),
  so tests confirm that it binds only to `127.0.0.1`.

### Worker (TypeScript)

- Unit test routes with `@cloudflare/vitest-pool-workers` running the
  Worker in-process with mocked D1.
- Mock agent calls by monkey-patching `fetch` to return canned JSON
  per admin host.
- Happy paths: add-user, gen-sub, rotate-domain, remove-user, sync.
- Error paths: one agent returns 500 → verify `events` row marked
  `partial`, verify `user_nodes` reflects only the successful node.
- Auth: request without `Cf-Access-Authenticated-User-Email` → 401.

### SPA

Skip automated E2E for MVP. The SPA is a thin layer over the Worker
API; Worker-level tests cover behavior. If regressions appear, add
Playwright smoke tests for login + add-user + see-in-list.

### Manual smoke (documented in `docs/TESTING.md`)

1. Install agent on a test VPS, register in panel → appears on Home.
2. Add user → SSH into VPS, confirm xray config gained a user.
3. Gen-sub → paste into V2ray client → connect OK.
4. Rotate domain → verify DNS update and the refreshed subscription
   works with the new hostname.
5. Disable a zone → UI prevents rotation into it.
6. Kill agent → UI shows red dot on the next lazy refresh.

### Out of test scope

- CF Access behavior (managed service).
- D1 durability / replication.
- cloudflared tunnel behavior (already covered by existing tests).

## Code layout changes

```
/opt/cf-vpn/
  cmd/
    cfvpnctl/          (unchanged)
    cfvpn-agent/       NEW — agent binary
  internal/
    agent/             NEW — HTTP handlers, request/response types
    commands/          minor additions to expose structured return values
  panel/               NEW subdirectory (or sibling repo)
    worker/            TS Worker source + wrangler.toml + migrations/
    web/               React + Vite SPA
    README.md
  scripts/
    install-agent.sh   NEW — adds agent unit + ingress rule on a VPS
  docs/
    TESTING.md         extend with multi-VPS manual smoke
```

## Open questions

None at spec-approval time. Anything that surfaces during
implementation will be raised in the plan.

## Out of scope (revisit later)

- Live log streaming via WebSocket.
- Proactive scheduled healthchecks / alerting.
- Bulk operations (e.g., rotate all nodes at once).
- Panel for VPN users themselves (only the operator uses it in MVP).
- Multi-operator RBAC.
