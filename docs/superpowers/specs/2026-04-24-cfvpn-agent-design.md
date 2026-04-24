---
name: cfvpn-agent-design
description: Design for cfvpn-agent HTTP server with systemd, callable from Cloudflare panel
type: project
---

# cfvpn-agent Design

## Context

cf-vpn hiện tại có 2 phần:
- **CLI** (`cfvpnctl`) — chạy trên VPS, quản lý install/users/xray
- **Panel** (Cloudflare Worker + React) — control panel trên Cloudflare

Panel muốn quản lý nhiều VPS nodes từ giao diện web. Mỗi node cần expose HTTP API để panel gọi.

## Requirements

- Build Go binary trên máy hiện tại (không Docker)
- Agent chạy dưới systemd service, tự start khi boot
- Port 6788, localhost only (cloudflared forward traffic)
- Share config với CLI qua `/etc/cfvpn/cfvpn.env`
- Auth bằng Cloudflare Access Service Token
- Panel sync được users, healthcheck, rotate domain

## Architecture

```
Panel (Cloudflare Worker)
    ↓ HTTPS (CF Access headers)
cloudflared tunnel (VPS)
    ↓ localhost:8080
cfvpn-agent (systemd)
    ↓ exec CLI tools
xray / cloudflared
```

### API Endpoints

| Method | Path | Handler | Response |
|--------|------|---------|----------|
| GET | `/admin/v1/status` | StatusHandler | `{xray, cloudflared, vpn_host, tunnel_uuid}` |
| POST | `/admin/v1/healthcheck` | HealthcheckHandler | `{latency_ms, xray_ok, tunnel_ok}` |
| POST | `/admin/v1/rotate-domain` | RotateDomainHandler | `{vpn_host, tunnel_uuid}` |
| POST | `/admin/v1/sync` | SyncHandler | `{ok, users_synced}` |

### Auth

Headers: `CF-Access-Client-Id` + `CF-Access-Client-Secret`
Matched against env vars `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET`

## Implementation

### New Files

1. `cmd/cfvpnctl/main.go` — thêm case `"agent"`
2. `internal/cli/dispatch.go` — thêm `case "agent"`
3. `internal/agent/server.go` — HTTP server với graceful shutdown
4. `internal/agent/handlers.go` — các handlers cho 4 endpoints
5. `internal/agent/auth.go` — middleware verify CF Access headers

### Modified Files

1. `internal/systemd/units.go` — thêm template `cfvpn-agent.service`
2. `scripts/bootstrap-vps.sh` — cài agent unit khi bootstrap

### Dependencies

- Go stdlib: `net/http`, `context`, `os/exec`
- Existing: `internal/state`, `internal/cloudflare`, `internal/systemd`, `internal/xray/config`

## Update Workflow

```bash
# Dev loop
make build
sudo systemctl restart cfvpn-agent
```

Binary đổi → systemd restart → agent reload.

## Testing Strategy

1. Unit test cho handlers (mock systemd calls)
2. Integration test: start agent, call endpoints, verify responses