# Telegram Bot Management — Design

**Date:** 2026-06-05
**Status:** Approved (brainstorming) → ready for implementation plan

## Goal

Add Telegram-bot management to cfvpn as a second control surface alongside the
existing React panel. The bot lets the operator manage users and nodes, and view
status, from a single private Telegram group. It reuses the existing Worker
(`cfvpn-panel-api`) logic and D1 binding so there is one source of truth and the
`events` audit log captures bot-driven actions automatically.

## Scope

In scope (confirmed with operator):

- **Read-only status** — list nodes + health + latency, list users, per-node status, subscription links.
- **User management** — add user, delete user, get subscription link, upgrade user onto new nodes.
- **Node operations** — healthcheck, sync, rotate-domain, per-node status.

Out of scope:

- Viewing the `events` audit log via the bot.
- Zone management via the bot.
- Update-deduplication store (see Known Limitations — YAGNI for now).

## Authorization & Configuration

The bot operates in exactly one private group whose only members are the
operator and his own bots; group membership is therefore the security boundary.

- **Secrets (`wrangler secret`):**
  - `TELEGRAM_BOT_TOKEN` — bot token `8825953715:AAH…Sw09I`.
  - `TELEGRAM_WEBHOOK_SECRET` — random string, registered as the
    `secret_token` on `setWebhook` and sent back by Telegram in the
    `X-Telegram-Bot-Api-Secret-Token` header.
- **Vars (`wrangler.toml [vars]`):**
  - `TELEGRAM_GROUP_ID` = `-1003806233980`.
- **Per-update checks (all must pass, else respond `200 OK` silently):**
  1. `X-Telegram-Bot-Api-Secret-Token` header equals `TELEGRAM_WEBHOOK_SECRET`.
  2. `chat.id === Number(TELEGRAM_GROUP_ID)` (for messages and callback queries).
  3. Ignore updates whose sender is a bot, where applicable.
- **Audit actor:** mutations log to `events` with actor `tg:<telegram_user_id>`
  (e.g. `tg:12345678`), distinct from the panel's email actors.

A failed check returns a bare `200 OK` with no body — no reply, no information leak.

## Architecture

Chosen approach: **integrate into the existing Worker** (rejected alternatives:
a second Worker calling `/api/*` via service token — duplicates config and adds
latency; a Go long-polling bot on a node — breaks the serverless model and needs
node-level D1/agent access).

```
Telegram  ──webhook POST──▶  cfvpn-panel-api Worker
                               /telegram/webhook
                                 ├─ verify (secret header + group id)
                                 ├─ parse Update (message | callback_query)
                                 ├─ dispatch command/callback
                                 │     └─ calls existing management core
                                 │          (createUserByName, deleteUser,
                                 │           nodeHealthcheck, …) → D1 + agents
                                 ├─ return 200 fast
                                 └─ ctx.waitUntil(run + sendMessage/editMessageText)
```

Slow operations (e.g. add-user fans out to all ~7 nodes, each up to 120 s) can
exceed Telegram's webhook timeout. The handler therefore returns `200`
immediately, sends `⏳ Đang xử lý…`, runs the work in `ctx.waitUntil`, and edits
that message with the result. This avoids Telegram retries that would duplicate
commands.

## Components

### New files

- **`panel/worker/src/lib/telegram.ts`** — Telegram Bot API client and types.
  - Functions: `sendMessage`, `editMessageText`, `answerCallbackQuery`,
    `setMyCommands`.
  - Types: `Update`, `Message`, `CallbackQuery`, `InlineKeyboardMarkup`.
  - Helpers: inline-keyboard builder; MarkdownV2/HTML text escaping.
  - All calls go to `https://api.telegram.org/bot<token>/<method>`.

- **`panel/worker/src/routes/telegram.ts`** — webhook handler.
  - `handleTelegramWebhook(env, request, ctx)`:
    verify → parse → dispatch → fast `200` + `waitUntil` for heavy work.
  - Dispatcher maps slash commands and `callback_data` to actions.

### Modified files

- **`panel/worker/src/index.ts`**
  - Add `ctx: ExecutionContext` to the `fetch` signature (currently absent) so
    `ctx.waitUntil` is available.
  - Add `POST /telegram/webhook` branch **before** the `/api/*` block, since the
    Telegram route does its own auth and must not go through `requireActorEmail`.

- **Core/HTTP split (one source of logic for panel + bot):**
  Extract body-parsing wrappers from body-bearing handlers so the core takes
  plain parameters:
  - `createUser(env, request, actor)` → thin wrapper that parses `name` then
    calls new `createUserByName(env, name, actor)`.
  - `nodeRotate`, `nodeSync` → extract cores taking already-parsed params.
  - Handlers already taking a plain `id` (`deleteUser`, `userUpgradeNodes`,
    `userSubscription`, `nodeStatus`, `nodeHealthcheck`, `listUsers`,
    `listNodes`) are called directly by the bot, which reads JSON from the
    returned `Response`.

No new D1 migration: button context rides in `callback_data` (≤64 bytes), e.g.
`n:health:hk-01`, `u:del:alice`, `u:del:alice:yes`.

## Commands & Interaction

Registered via `setMyCommands` so Telegram shows the command menu.

| Command | Action | Background? |
|---|---|---|
| `/start`, `/help` | Intro + command list | no |
| `/nodes` | List nodes (status, latency) + per-node buttons `🔄 Healthcheck`, `📊 Status` | no |
| `/status <node>` | Detailed node status (agent call) | yes |
| `/health <node>` | Run healthcheck on one node | yes |
| `/sync <node>` | Re-sync one node | yes |
| `/rotate <node>` | Rotate-domain (2-step confirm) | yes |
| `/users` | List users (+ node count) + per-user buttons `🔗 Link`, `🗑 Xóa`, `⬆️ Upgrade` | no |
| `/adduser <name>` | Add user to all active nodes | yes |
| `/deluser <name>` | Delete user (2-step confirm) | yes |
| `/sub <name>` | Get user's subscription link | no |
| `/upgrade <name>` | Add user to newly-added nodes | yes |

**Inline keyboards:** each button carries a short `callback_data`
(`<entity>:<action>:<id>[:yes]`). The handler calls `answerCallbackQuery`
immediately to clear the Telegram spinner, then processes.

**Destructive actions (delete user, rotate)** use a 2-step confirm: first press
shows `⚠️ Xóa alice? [✅ Có] [❌ Không]`; only the `…:yes` callback executes.

**Heavy-work flow:** return `200` → send `⏳ Đang xử lý…` → run in `waitUntil` →
`editMessageText` with the result (✅ / ⚠️ partial / ❌, including per-node detail).

**Formatting:** concise, status emoji (🟢 active / 🔴 down), escaped special
characters. Subscription links are sent in the group (private group → safe).

## Error Handling

- The webhook **always** returns `200` (even on internal error) so Telegram does
  not retry indefinitely; errors are caught and replied as `❌ <reason>`.
- Bad syntax / missing argument → short usage hint, no crash.
- Missing node/user → clear message (reuses the core's 404 errors).
- Partial operations → show ✅ ok-nodes and ⚠️ failed-nodes with reasons.

## Testing

Vitest with mocked `fetch`:

- **Auth:** reject wrong secret header; reject wrong `chat.id`; accept when both correct.
- **Dispatcher:** parses command + argument correctly; parses `callback_data` correctly.
- **2-step confirm:** `u:del:x` only prompts (no delete); `u:del:x:yes` calls the core.
- **Telegram client:** `sendMessage`/`editMessageText` request bodies correct; escaping correct.
- **Refactored core (`createUserByName`, …):** existing tests stay green + new tests for the extracted cores.
- Go side untouched (`go test ./...` unaffected — only Worker TypeScript changes).

## Deployment

Scripted and documented in the README:

1. `wrangler secret put TELEGRAM_BOT_TOKEN` and `wrangler secret put TELEGRAM_WEBHOOK_SECRET`.
2. Add `TELEGRAM_GROUP_ID` to `[vars]` in `wrangler.toml`.
3. `npm --prefix panel/worker run deploy`.
4. `scripts/telegram-setup.sh`: calls `setWebhook` (with `secret_token`) pointing
   at `https://<panel-host>/telegram/webhook`, and `setMyCommands` to register the
   command menu.

## Known Limitations

- The Worker does not deduplicate `update_id`. Mitigated by the fast `200` +
  background-processing pattern, which makes Telegram retries unlikely. If strict
  idempotency is later required, add a `tg_seen_updates` table or use KV — out of
  scope for this version (YAGNI).
- Best-effort: heavy operations rely on `ctx.waitUntil` completing within the
  Worker's wall-clock budget — the same constraint the panel already operates under.
