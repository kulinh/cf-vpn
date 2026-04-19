# Design Spec — cf-vpn Standalone Systemd (Go Control Plane)

**Date:** 2026-04-20
**Status:** Draft, pending user review
**Owner:** Đại ca
**Author:** Tiểu Long

## 1. Summary

Replace the current Docker-based runtime with a standalone systemd-based deployment on Debian/Ubuntu, while keeping upstream `xray` and `cloudflared` binaries as the data plane.

The new control plane is a Go CLI (`cfvpnctl`) that manages installation, config generation, user lifecycle, domain rotation, health checks, and operational status.

## 2. Scope

### In Scope (Phase 1)
- Go CLI-only control plane (no web UI)
- Debian/Ubuntu + systemd support only
- Keep upstream binaries:
  - `xray` (service: `cfvpn-xray.service`)
  - `cloudflared` (service: `cfvpn-cloudflared.service`)
- Clean install workflow only
- User management (max 5 users)
- Subscription generation
- Domain rotation and cleanup flow
- Healthcheck timer installation

### Out of Scope (Phase 1)
- Migration from existing Docker stack
- Re-implementation of Cloudflare tunnel protocol
- Re-implementation of proxy core (xray)
- Web UI/API panel
- Non-systemd platforms
- RHEL-family distro support

## 3. Key Decisions

1. **Control plane language:** Go
   - Fast implementation and maintenance for ops-heavy CLI.
2. **Data plane:** Upstream binaries unchanged
   - Use official `xray` + `cloudflared` executables.
3. **Install behavior with existing binaries:**
   - Detect whether binaries already exist.
   - Reuse existing binaries if present (record version).
   - Install missing binaries only.
4. **Tunnel behavior:**
   - `install` always creates a new dedicated tunnel for cf-vpn.
5. **Install mode:**
   - Clean install only in phase 1.

## 4. Architecture

## 4.1 Components
- `cfvpnctl` (Go CLI): orchestration and lifecycle commands.
- `xray` binary: VLESS/Trojan proxy runtime.
- `cloudflared` binary: Cloudflare Tunnel connector and ingress routing.
- `systemd`: service and timer management.

## 4.2 Runtime boundaries
- `cfvpnctl` writes configs and service files, then starts/restarts services.
- `xray` and `cloudflared` run as independent systemd units.
- `cfvpnctl` never proxies traffic itself.

## 4.3 Filesystem layout
- `/etc/cfvpn/cfvpn.env`
- `/etc/cfvpn/xray/config.json`
- `/etc/cfvpn/cloudflared/config.yml`
- `/etc/cfvpn/cloudflared/<tunnel-uuid>.json`
- `/var/lib/cfvpn/subscriptions/<user>.txt`
- `/var/lib/cfvpn/state/healthcheck.state`
- `/etc/systemd/system/cfvpn-xray.service`
- `/etc/systemd/system/cfvpn-cloudflared.service`
- `/etc/systemd/system/cfvpn-healthcheck.service`
- `/etc/systemd/system/cfvpn-healthcheck.timer`

## 5. CLI Surface

- `cfvpnctl install`
- `cfvpnctl status`
- `cfvpnctl add-user <name>`
- `cfvpnctl remove-user <name>`
- `cfvpnctl gen-sub [name]`
- `cfvpnctl rotate-domain <new-domain>`
- `cfvpnctl rotate-domain --cleanup <old-tunnel-uuid>`
- `cfvpnctl healthcheck install`

### Validation rules
- User name: `^[A-Za-z0-9_-]{1,32}$`
- Max users: 5
- Domain must resolve to a zone available in configured Cloudflare account.

## 6. Install flow (`cfvpnctl install`)

1. Preflight checks:
   - Root privileges
   - Debian/Ubuntu detection
   - systemd available and running
   - required commands present (`curl`, `jq`, `openssl`, `uuidgen`, `qrencode`)
2. Load and validate config inputs:
   - `CF_API_TOKEN`, `CF_ACCOUNT_ID`, `DOMAIN`
3. Binary detection and install:
   - If `xray` exists: reuse
   - If missing: install `xray`
   - If `cloudflared` exists: reuse
   - If missing: install `cloudflared`
4. Create a new tunnel in Cloudflare account.
5. Write tunnel credentials JSON locally.
6. Upsert DNS CNAME:
   - `DOMAIN -> <TUNNEL_UUID>.cfargotunnel.com` (proxied)
7. Ensure initial user secrets:
   - `USER1_NAME` default `user1`
   - generate UUID/password if absent
8. Render/write configs atomically.
9. Install/refresh systemd units.
10. `systemctl daemon-reload`
11. `systemctl enable --now cfvpn-xray cfvpn-cloudflared`
12. Probe `https://DOMAIN/vless` via GET.
13. Success if code is `400` or `426`.
14. Generate and print subscription for `user1`.

## 7. Service definitions

## 7.1 `cfvpn-xray.service`
- Type: simple
- Restart: `on-failure`
- ExecStart: `xray -config /etc/cfvpn/xray/config.json`
- User: dedicated least-privileged service account if available; fallback root in phase 1 if required by packaging constraints.

## 7.2 `cfvpn-cloudflared.service`
- Type: simple
- Restart: `on-failure`
- ExecStart: `cloudflared tunnel --config /etc/cfvpn/cloudflared/config.yml run`
- Reads credentials from `/etc/cfvpn/cloudflared/<tunnel-uuid>.json`.

## 7.3 Healthcheck timer
- `cfvpn-healthcheck.timer`: every 5 minutes
- `cfvpn-healthcheck.service`: execute health probe and restart services on threshold breach

## 8. Domain rotation

`cfvpnctl rotate-domain <new-domain>`:
1. Validate `new-domain` in same Cloudflare account.
2. Persist old domain and old tunnel UUID in operation output.
3. Create new tunnel.
4. Upsert new DNS record.
5. Update env/config with new domain/tunnel.
6. Restart services.
7. Regenerate all subscriptions.
8. Print cleanup command for old tunnel:
   - `cfvpnctl rotate-domain --cleanup <old-tunnel-uuid>`

`cfvpnctl rotate-domain --cleanup <uuid>`:
- Delete old tunnel via Cloudflare API.
- Remove old credentials JSON locally.

## 9. Error handling and recovery

- All config/env writes are atomic (`tmp` + rename).
- On failure before services are restarted:
  - keep currently-running services untouched.
- On failure after tunnel/DNS mutation:
  - print explicit resume and cleanup commands.
- No hidden rollback logic that risks deleting active resources unexpectedly.

## 10. Security model

- Never log secrets (API token, passwords, full subscription payload by default logs).
- Credentials files created with restrictive permissions.
- Input validation at command boundary.
- Explicit confirmation for destructive operations unless `--yes` is supplied.
- Keep xray private-IP egress blocking rule in generated config.

## 11. Testing strategy

### Unit tests (Go)
- Env parsing/validation
- Config rendering
- User CRUD logic
- URI/subscription generation
- Domain/tunnel input validation

### Integration tests (VM)
- Fresh install on Debian/Ubuntu
- systemd units start and stay healthy
- probe returns `400`/`426`

### Operational tests
- add/remove user idempotency
- rotate + cleanup flow
- reboot persistence
- service failure recovery (xray/cloudflared restart paths)

### Manual client tests
- Shadowrocket (iOS)
- v2rayNG (Android)

## 12. Rollout plan

1. Release CLI phase 1 + docs.
2. Deploy on fresh VPS only.
3. Run checklist and 24h bake.
4. Move production usage to standalone stack.

## 13. Future phases (not phase 1)

- Docker-to-systemd migration command
- Web UI/API
- Multi-distro support
- Policy-based automated rotation
