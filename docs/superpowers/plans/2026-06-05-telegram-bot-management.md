# Telegram Bot Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Telegram-bot control surface to the cfvpn Worker so the operator can manage users/nodes and view status from one private Telegram group, reusing the existing management logic and D1 audit log.

**Architecture:** New routes in the existing `cfvpn-panel-api` Worker. `POST /telegram/webhook` verifies a secret header + the allowed group id, parses the Telegram `Update`, and dispatches slash commands / inline-button callbacks to the existing management core (`createUserByName`, `deleteUser`, `nodeHealthcheck`, …). Slow operations return `200` immediately, send `⏳ Đang xử lý…`, run in `ctx.waitUntil`, then `editMessageText` with the result.

**Tech Stack:** TypeScript, Cloudflare Workers (`wrangler`), D1, Vitest. Telegram Bot API over HTTPS (`api.telegram.org`).

---

## File Structure

| File | Responsibility |
|---|---|
| `panel/worker/src/types.ts` (modify) | Add Telegram fields to `Env`. |
| `panel/worker/wrangler.toml` (modify) | Add `TELEGRAM_GROUP_ID` var. |
| `panel/worker/src/lib/telegram.ts` (create) | Telegram Bot API client, update types, HTML escape, keyboard helper. |
| `panel/worker/src/lib/telegram.test.ts` (create) | Tests for escape + client request bodies. |
| `panel/worker/src/routes/users.ts` (modify) | Extract `createUserByName` core from `createUser`. |
| `panel/worker/src/routes/nodes.ts` (modify) | Extract `nodeRotateCore` / `nodeSyncCore` cores. |
| `panel/worker/src/lib/telegram-commands.ts` (create) | Dispatcher: parse commands/callbacks, call core, format replies, 2-step confirm, background runner. |
| `panel/worker/src/lib/telegram-commands.test.ts` (create) | Tests for parsing, auth-independent dispatch, confirm gating. |
| `panel/worker/src/routes/telegram.ts` (create) | Webhook handler: verify secret + group id, parse `Update`, delegate to dispatcher. |
| `panel/worker/src/routes/telegram.test.ts` (create) | Tests for webhook auth (secret header + group id). |
| `panel/worker/src/index.ts` (modify) | Add `ctx` param to `fetch`; route `POST /telegram/webhook`. |
| `scripts/telegram-setup.sh` (create) | One-time `setWebhook` (+ `secret_token`) and `setMyCommands`. |
| `README.md` (modify) | Document the Telegram bot setup + commands. |

---

## Task 1: Telegram configuration on `Env` and vars

**Files:**
- Modify: `panel/worker/src/types.ts`
- Modify: `panel/worker/wrangler.toml`

- [ ] **Step 1: Add Telegram fields to `Env`**

In `panel/worker/src/types.ts`, extend the `Env` interface (add after `AGENT_SHARED_SECRET?: string;`):

```ts
export interface Env {
  DB: D1Database;
  CF_ACCESS_CLIENT_ID?: string;
  CF_ACCESS_CLIENT_SECRET?: string;
  SERVICE_TOKEN_HEADER_ID?: string;
  SERVICE_TOKEN_HEADER_SECRET?: string;
  ADMIN_HOST_ALLOWED_SUFFIXES?: string;
  CF_API_TOKEN?: string;
  CF_ACCOUNT_ID?: string;
  AGENT_SHARED_SECRET?: string;
  TELEGRAM_BOT_TOKEN?: string;
  TELEGRAM_WEBHOOK_SECRET?: string;
  TELEGRAM_GROUP_ID?: string;
}
```

- [ ] **Step 2: Add `TELEGRAM_GROUP_ID` var**

In `panel/worker/wrangler.toml`, add the var to the existing `[vars]` block:

```toml
[vars]
SERVICE_TOKEN_HEADER_ID = "CF-Access-Client-Id"
SERVICE_TOKEN_HEADER_SECRET = "CF-Access-Client-Secret"
ADMIN_HOST_ALLOWED_SUFFIXES = "rwl265.com,888vn.net,dongnat247.com,rwl247.dev,rwl265.org"
TELEGRAM_GROUP_ID = "-1003806233980"
```

(`TELEGRAM_BOT_TOKEN` and `TELEGRAM_WEBHOOK_SECRET` are NOT placed here — they are set via `wrangler secret put` in Task 9. Putting them in `[vars]` would commit secrets to git.)

- [ ] **Step 3: Verify the Worker still type-checks**

Run: `npm --prefix panel/worker run check`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add panel/worker/src/types.ts panel/worker/wrangler.toml
git commit -m "feat(telegram): add Telegram config to Env and vars"
```

---

## Task 2: Telegram Bot API client (`lib/telegram.ts`)

**Files:**
- Create: `panel/worker/src/lib/telegram.ts`
- Test: `panel/worker/src/lib/telegram.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `panel/worker/src/lib/telegram.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { escapeHtml, sendMessage, answerCallbackQuery, type InlineKeyboard } from "./telegram";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("escapeHtml", () => {
  it("escapes &, < and >", () => {
    expect(escapeHtml('a & b < c > d')).toBe("a &amp; b &lt; c &gt; d");
  });
});

describe("sendMessage", () => {
  it("POSTs to the bot sendMessage endpoint with HTML parse mode and keyboard", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true, result: { message_id: 7 } }), { status: 200 })
    );
    const keyboard: InlineKeyboard = [[{ text: "OK", callback_data: "x:y:z" }]];
    const result = await sendMessage("TOKEN", -100, "hello", { keyboard });

    expect(result.message_id).toBe(7);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("https://api.telegram.org/botTOKEN/sendMessage");
    const body = JSON.parse((init as RequestInit).body as string);
    expect(body).toMatchObject({
      chat_id: -100,
      text: "hello",
      parse_mode: "HTML",
      disable_web_page_preview: true,
      reply_markup: { inline_keyboard: keyboard }
    });
  });

  it("throws when Telegram returns ok:false", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: false, description: "chat not found" }), { status: 400 })
    );
    await expect(sendMessage("TOKEN", -100, "hi")).rejects.toThrow("chat not found");
  });
});

describe("answerCallbackQuery", () => {
  it("POSTs the callback query id", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true, result: true }), { status: 200 })
    );
    await answerCallbackQuery("TOKEN", "cbid", "done");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("https://api.telegram.org/botTOKEN/answerCallbackQuery");
    const body = JSON.parse((init as RequestInit).body as string);
    expect(body).toMatchObject({ callback_query_id: "cbid", text: "done" });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix panel/worker test -- telegram.test.ts`
Expected: FAIL — `Cannot find module './telegram'`.

- [ ] **Step 3: Implement `lib/telegram.ts`**

Create `panel/worker/src/lib/telegram.ts`:

```ts
const API_BASE = "https://api.telegram.org";

export interface TgUser {
  id: number;
  is_bot: boolean;
  username?: string;
  first_name?: string;
}

export interface TgChat {
  id: number;
  type: string;
}

export interface TgMessage {
  message_id: number;
  chat: TgChat;
  from?: TgUser;
  text?: string;
}

export interface TgCallbackQuery {
  id: string;
  from: TgUser;
  message?: TgMessage;
  data?: string;
}

export interface TgUpdate {
  update_id: number;
  message?: TgMessage;
  callback_query?: TgCallbackQuery;
}

export interface InlineButton {
  text: string;
  callback_data: string;
}

export type InlineKeyboard = InlineButton[][];

export interface BotCommand {
  command: string;
  description: string;
}

// Telegram HTML parse mode only requires escaping these three characters in
// text nodes. We use HTML (not MarkdownV2) because its escaping rules are far
// simpler and less error-prone.
export function escapeHtml(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

async function tgCall<T>(token: string, method: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}/bot${token}/${method}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body)
  });
  const data = (await res.json().catch(() => null)) as
    | { ok?: boolean; result?: T; description?: string }
    | null;
  if (!data?.ok) {
    throw new Error(data?.description || `telegram_http_${res.status}`);
  }
  return data.result as T;
}

interface SendOpts {
  keyboard?: InlineKeyboard;
}

export function sendMessage(
  token: string,
  chatId: number,
  text: string,
  opts: SendOpts = {}
): Promise<TgMessage> {
  return tgCall<TgMessage>(token, "sendMessage", {
    chat_id: chatId,
    text,
    parse_mode: "HTML",
    disable_web_page_preview: true,
    ...(opts.keyboard ? { reply_markup: { inline_keyboard: opts.keyboard } } : {})
  });
}

export function editMessageText(
  token: string,
  chatId: number,
  messageId: number,
  text: string,
  opts: SendOpts = {}
): Promise<TgMessage | boolean> {
  return tgCall<TgMessage | boolean>(token, "editMessageText", {
    chat_id: chatId,
    message_id: messageId,
    text,
    parse_mode: "HTML",
    disable_web_page_preview: true,
    ...(opts.keyboard ? { reply_markup: { inline_keyboard: opts.keyboard } } : {})
  });
}

export function answerCallbackQuery(
  token: string,
  callbackQueryId: string,
  text?: string
): Promise<boolean> {
  return tgCall<boolean>(token, "answerCallbackQuery", {
    callback_query_id: callbackQueryId,
    ...(text ? { text } : {})
  });
}

export function setMyCommands(token: string, commands: BotCommand[]): Promise<boolean> {
  return tgCall<boolean>(token, "setMyCommands", { commands });
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix panel/worker test -- telegram.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add panel/worker/src/lib/telegram.ts panel/worker/src/lib/telegram.test.ts
git commit -m "feat(telegram): Telegram Bot API client and types"
```

---

## Task 3: Extract management cores for reuse

The bot must call the same logic the HTTP routes use. Three handlers parse their
input from a `Request`; extract the post-parse logic into cores that take plain
parameters. The existing HTTP handlers become thin wrappers, so existing tests
stay green.

**Files:**
- Modify: `panel/worker/src/routes/users.ts`
- Modify: `panel/worker/src/routes/nodes.ts`

- [ ] **Step 1: Extract `createUserByName` in `users.ts`**

In `panel/worker/src/routes/users.ts`, replace the `createUser` function with a
thin wrapper plus an exported core. Keep all logic identical — only the
name-parsing prologue stays in the wrapper:

```ts
export async function createUser(env: Env, request: Request, actor: string): Promise<Response> {
  let body: { name: string };
  try {
    body = await readJSON<{ name: string }>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_user", detail: "request body must be a JSON object" });
  }
  return createUserByName(env, body.name, actor);
}

export async function createUserByName(env: Env, rawName: string | undefined, actor: string): Promise<Response> {
  const name = rawName?.trim();
  if (!name) {
    return error(400, { error: "invalid_user", detail: "name is required" });
  }
  const id = userIDFromName(name);
  if (!id) {
    return error(400, { error: "invalid_user", detail: "name is invalid" });
  }

  const existing = await one<{ id: string }>(env.DB.prepare("SELECT id FROM users WHERE id=?").bind(id));
  if (existing) {
    return error(409, { error: "user_exists", detail: id });
  }

  const subToken = randomHex(16);
  await env.DB.prepare("INSERT INTO users (id,name,created_at,sub_token) VALUES (?, ?, ?, ?)")
    .bind(id, name, nowTs(), subToken)
    .run();
  const nodes = await all<NodeMini>(
    env.DB.prepare("SELECT id,admin_host,agent_secret FROM nodes WHERE status='active' ORDER BY id")
  );

  const results = await Promise.allSettled(
    nodes.map(async (node) => {
      const creds = await callAgent<AgentAddUserResponse>(
        env,
        { adminHost: node.admin_host, agentSecret: node.agent_secret },
        "/admin/v1/users",
        { method: "POST", body: JSON.stringify({ name: id }) },
        120000
      );
      await env.DB.prepare(
        "INSERT OR REPLACE INTO user_nodes (user_id,node_id,vless_uuid,hy2_pw,created_at) VALUES (?, ?, ?, ?, ?)"
      )
        .bind(id, node.id, creds.vless_uuid, creds.hy2_pw, nowTs())
        .run();
      return { node_id: node.id, ok: true };
    })
  );

  const summary = results.map((r, i) => {
    if (r.status === "fulfilled") {
      return r.value;
    }
    return { node_id: nodes[i]?.id, ok: false, error: String(r.reason) };
  });
  const failed = summary.some((x) => !x.ok);
  const succeeded = summary.some((x) => x.ok);
  const outcome = failed ? (succeeded ? "partial" : "error") : "ok";
  await logEvent(env, actor, "user.add", outcome, { user_id: id, results: summary }, undefined, id);

  return json({ id, name, results: summary }, failed ? 207 : 201);
}
```

- [ ] **Step 2: Extract `nodeRotateCore` in `nodes.ts`**

In `panel/worker/src/routes/nodes.ts`, replace `nodeRotate` so the body parsing
stays in the wrapper and the rest moves to an exported core that takes an
already-parsed `override`:

```ts
export async function nodeRotate(env: Env, id: string, request: Request, actor: string): Promise<Response> {
  // Optional override body — empty body is fine (auto path).
  let override: { host?: string; zone?: string } = {};
  const raw = await request.text();
  if (raw.trim() !== "") {
    try {
      const parsed = JSON.parse(raw);
      if (isRecord(parsed)) {
        override = parsed as { host?: string; zone?: string };
      }
    } catch {
      override = {};
    }
  }
  return nodeRotateCore(env, id, override, actor);
}

export async function nodeRotateCore(
  env: Env,
  id: string,
  override: { host?: string; zone?: string },
  actor: string
): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }

  const hasHost = typeof override.host === "string" && override.host.length > 0;
  const hasZone = typeof override.zone === "string" && override.zone.length > 0;
  if (hasHost !== hasZone) {
    return error(400, { error: "invalid_request", detail: "host and zone must be provided together" });
  }

  let newHost: string;
  let newZoneID: string;
  let newZoneName: string;
  const rng: (n: number) => Uint8Array = (n) => crypto.getRandomValues(new Uint8Array(n));

  if (hasHost && hasZone) {
    const overrideZone = await one<ZoneRow>(
      env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(override.zone)
    );
    if (!overrideZone) {
      return error(400, { error: "zone_not_found", detail: override.zone! });
    }
    newHost = override.host!;
    newZoneID = overrideZone.cf_zone_id;
    newZoneName = overrideZone.name;
  } else {
    const candidates = await all<ZoneRow>(
      env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE enabled = 1 AND name != ?").bind(row.zone)
    );
    if (candidates.length === 0) {
      return error(400, {
        error: "rotate_requires_multi_zone",
        detail: "enable at least one other zone before rotating"
      });
    }
    const picked = pickZone(rng, candidates, "");
    newHost = generateHost(rng, picked.name);
    newZoneID = picked.cf_zone_id;
    newZoneName = picked.name;
  }

  const newHy2Host = generateHy2Host(rng, newZoneName);
  const oldZone = await one<ZoneRow>(env.DB.prepare("SELECT name, cf_zone_id FROM zones WHERE name = ?").bind(row.zone));
  try {
    const out = await agentCall<AgentRotateResponse>(
      env,
      { adminHost: row.admin_host, agentSecret: row.agent_secret },
      "/admin/v1/rotate-domain",
      {
        method: "POST",
        body: JSON.stringify({
          new_host: newHost,
          new_zone_id: newZoneID,
          old_host: row.vpn_host,
          old_zone_id: oldZone?.cf_zone_id ?? "",
          new_hy2_host: newHy2Host,
          new_hy2_zone: newZoneName,
          new_hy2_zone_id: newZoneID,
          old_hy2_host: row.hy2_host ?? "",
          old_hy2_zone_id: oldZone?.cf_zone_id ?? ""
        })
      },
      120000
    );
    await env.DB.prepare("UPDATE nodes SET vpn_host=?, hy2_host=?, hy2_port=?, hy2_obfs_pw=?, public_ip=?, zone=?, status='active', last_seen_at=? WHERE id=?")
      .bind(out.vpn_host, out.hy2_host, out.hy2_port, out.hy2_obfs_pw, out.public_ip, newZoneName, nowTs(), id)
      .run();
    await logEvent(env, actor, "node.rotate", "ok", { old_host: row.vpn_host, new_host: out.vpn_host, public_ip: out.public_ip }, id);
    return json({ vpn_host: out.vpn_host, hy2_host: out.hy2_host, public_ip: out.public_ip });
  } catch (e) {
    await logEvent(env, actor, "node.rotate", "error", { message: String(e) }, id);
    return error(502, { error: "rotate_failed", detail: String(e) });
  }
}
```

> Note: the original `nodeRotate` fetched the node row first, then parsed the
> body. The refactor parses the body first, then `nodeRotateCore` fetches the
> row. This is behaviorally equivalent for callers (a missing node still returns
> 404; a malformed body is still ignored).

- [ ] **Step 3: Extract `nodeSyncCore` in `nodes.ts`**

Replace `nodeSync` so the request parsing/validation stays in the wrapper and
the agent call + persistence move to an exported core taking a validated `users`
array. Find the remainder of `nodeSync` (the `try { const out = await agentCall<AgentSyncResponse>(...` block through the end of the function) and move it verbatim into `nodeSyncCore`:

```ts
export async function nodeSync(env: Env, id: string, request: Request, actor: string): Promise<Response> {
  let body: { users: Array<{ name: string; vless_uuid: string; hy2_pw: string }> };
  try {
    body = await readJSON<{ users: Array<{ name: string; vless_uuid: string; hy2_pw: string }> }>(request);
  } catch {
    return error(400, { error: "invalid_json", detail: "request body must be valid JSON" });
  }
  if (!isRecord(body)) {
    return error(400, { error: "invalid_sync_payload", detail: "request body must be a JSON object" });
  }
  if (!Array.isArray(body.users)) {
    return error(400, { error: "invalid_sync_payload", detail: "users must be an array" });
  }
  const invalid = body.users.some(
    (u) => !u || typeof u.name !== "string" || typeof u.vless_uuid !== "string" || typeof u.hy2_pw !== "string"
  );
  if (invalid) {
    return error(400, { error: "invalid_sync_payload", detail: "each user must include name, vless_uuid, hy2_pw" });
  }
  return nodeSyncCore(env, id, body.users, actor);
}

export async function nodeSyncCore(
  env: Env,
  id: string,
  users: Array<{ name: string; vless_uuid: string; hy2_pw: string }>,
  actor: string
): Promise<Response> {
  const row = await getNodeOr404(env, id);
  if (row instanceof Response) {
    return row;
  }
  // (paste the original `try { const out = await agentCall<AgentSyncResponse>(...)`
  //  block from the old nodeSync here verbatim, replacing `body.users` with `users`.)
}
```

> When pasting, change the single reference `body: JSON.stringify({ users: body.users })` to `body: JSON.stringify({ users })`. Everything else in that block is unchanged.

- [ ] **Step 4: Run the existing Worker tests**

Run: `npm --prefix panel/worker test`
Expected: PASS — all pre-existing tests still green (the refactor is behavior-preserving).

- [ ] **Step 5: Type-check**

Run: `npm --prefix panel/worker run check`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add panel/worker/src/routes/users.ts panel/worker/src/routes/nodes.ts
git commit -m "refactor(routes): extract createUserByName, nodeRotateCore, nodeSyncCore for reuse"
```

---

## Task 4: Command dispatcher (`lib/telegram-commands.ts`)

This module owns parsing, formatting, confirmation gating, and the background
runner. It is the largest unit; build it with tests for the pure-logic parts.

**Files:**
- Create: `panel/worker/src/lib/telegram-commands.ts`
- Test: `panel/worker/src/lib/telegram-commands.test.ts`

- [ ] **Step 1: Write failing tests for parsing + confirm gating**

Create `panel/worker/src/lib/telegram-commands.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { parseCommand, parseCallback } from "./telegram-commands";

describe("parseCommand", () => {
  it("parses a bare command", () => {
    expect(parseCommand("/nodes")).toEqual({ cmd: "nodes", arg: "" });
  });
  it("parses a command with an argument", () => {
    expect(parseCommand("/adduser alice")).toEqual({ cmd: "adduser", arg: "alice" });
  });
  it("strips an @botname suffix", () => {
    expect(parseCommand("/help@cfvpn_bot")).toEqual({ cmd: "help", arg: "" });
  });
  it("returns null for non-command text", () => {
    expect(parseCommand("hello there")).toBeNull();
  });
  it("trims surrounding whitespace in the argument", () => {
    expect(parseCommand("/sub   bob  ")).toEqual({ cmd: "sub", arg: "bob" });
  });
});

describe("parseCallback", () => {
  it("parses entity:action:id", () => {
    expect(parseCallback("u:del:alice")).toEqual({ entity: "u", action: "del", id: "alice", confirmed: false });
  });
  it("marks confirmed when the :yes suffix is present", () => {
    expect(parseCallback("u:del:alice:yes")).toEqual({ entity: "u", action: "del", id: "alice", confirmed: true });
  });
  it("parses node callbacks", () => {
    expect(parseCallback("n:health:hk-01")).toEqual({ entity: "n", action: "health", id: "hk-01", confirmed: false });
  });
  it("returns null for malformed data", () => {
    expect(parseCallback("garbage")).toBeNull();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix panel/worker test -- telegram-commands.test.ts`
Expected: FAIL — `Cannot find module './telegram-commands'`.

- [ ] **Step 3: Implement the parsers and dispatcher skeleton**

Create `panel/worker/src/lib/telegram-commands.ts`:

```ts
import type { Env } from "../types";
import { all, userIDFromName } from "./db";
import {
  answerCallbackQuery,
  editMessageText,
  escapeHtml,
  sendMessage,
  type InlineKeyboard,
  type TgUpdate
} from "./telegram";
import { createUserByName, deleteUser, listUsers, userSubscription, userUpgradeNodes } from "../routes/users";
import { nodeHealthcheck, nodeRotateCore, nodeStatus, nodeSyncCore } from "../routes/nodes";

export interface ParsedCommand {
  cmd: string;
  arg: string;
}

export interface ParsedCallback {
  entity: string;
  action: string;
  id: string;
  confirmed: boolean;
}

export function parseCommand(text: string): ParsedCommand | null {
  const trimmed = text.trim();
  if (!trimmed.startsWith("/")) {
    return null;
  }
  const sp = trimmed.indexOf(" ");
  const head = sp === -1 ? trimmed.slice(1) : trimmed.slice(1, sp);
  const arg = sp === -1 ? "" : trimmed.slice(sp + 1).trim();
  const cmd = head.split("@")[0].toLowerCase();
  if (!cmd) {
    return null;
  }
  return { cmd, arg };
}

export function parseCallback(data: string): ParsedCallback | null {
  const parts = data.split(":");
  if (parts.length < 3) {
    return null;
  }
  const [entity, action, id, suffix] = parts;
  if (!entity || !action || !id) {
    return null;
  }
  return { entity, action, id, confirmed: suffix === "yes" };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix panel/worker test -- telegram-commands.test.ts`
Expected: PASS (9 tests).

- [ ] **Step 5: Commit**

```bash
git add panel/worker/src/lib/telegram-commands.ts panel/worker/src/lib/telegram-commands.test.ts
git commit -m "feat(telegram): command/callback parsers"
```

---

## Task 5: Read-only command handlers (`/help`, `/nodes`, `/users`, `/sub`, `/status`, `/health`)

**Files:**
- Modify: `panel/worker/src/lib/telegram-commands.ts`

- [ ] **Step 1: Add helpers — handler-result reader, formatters, node/user keyboards**

Append to `panel/worker/src/lib/telegram-commands.ts`:

```ts
const HELP_TEXT = [
  "<b>cfvpn bot</b>",
  "",
  "/nodes — danh sách node",
  "/status &lt;node&gt; — trạng thái chi tiết",
  "/health &lt;node&gt; — chạy healthcheck",
  "/sync &lt;node&gt; — đồng bộ user lên node",
  "/rotate &lt;node&gt; — đổi domain (xác nhận)",
  "/users — danh sách user",
  "/adduser &lt;tên&gt; — thêm user",
  "/deluser &lt;tên&gt; — xóa user (xác nhận)",
  "/sub &lt;tên&gt; — link subscription",
  "/upgrade &lt;tên&gt; — thêm user vào node mới"
].join("\n");

async function readHandler(p: Promise<Response>): Promise<{ status: number; body: any }> {
  const res = await p;
  const body = (await res.json().catch(() => null)) as any;
  return { status: res.status, body };
}

interface NodeListRow {
  id: string;
  label: string;
  status: string;
  latency_ms: number | null;
}

function statusDot(status: string): string {
  if (status === "active") return "🟢";
  if (status === "unreachable") return "🔴";
  return "⚪";
}

async function formatNodes(env: Env): Promise<{ text: string; keyboard: InlineKeyboard }> {
  const rows = await all<NodeListRow>(
    env.DB.prepare("SELECT id,label,status,latency_ms FROM nodes ORDER BY id")
  );
  if (rows.length === 0) {
    return { text: "Chưa có node nào.", keyboard: [] };
  }
  const lines = rows.map((n) => {
    const lat = n.latency_ms != null ? ` · ${n.latency_ms}ms` : "";
    return `${statusDot(n.status)} <b>${escapeHtml(n.id)}</b> ${escapeHtml(n.label || "")}${lat}`;
  });
  const keyboard: InlineKeyboard = rows.map((n) => [
    { text: `🔄 ${n.id}`, callback_data: `n:health:${n.id}` },
    { text: `📊 ${n.id}`, callback_data: `n:status:${n.id}` }
  ]);
  return { text: lines.join("\n"), keyboard };
}

async function formatUsers(env: Env): Promise<{ text: string; keyboard: InlineKeyboard }> {
  const { body } = await readHandler(listUsers(env));
  const users = Array.isArray(body) ? body : [];
  if (users.length === 0) {
    return { text: "Chưa có user nào.", keyboard: [] };
  }
  const lines = users.map((u: any) => `👤 <b>${escapeHtml(u.id)}</b> · ${u.nodes?.length ?? 0} node`);
  const keyboard: InlineKeyboard = users.map((u: any) => [
    { text: `🔗 ${u.id}`, callback_data: `u:sub:${u.id}` },
    { text: `⬆️ ${u.id}`, callback_data: `u:upg:${u.id}` },
    { text: `🗑 ${u.id}`, callback_data: `u:del:${u.id}` }
  ]);
  return { text: lines.join("\n"), keyboard };
}

async function formatSub(env: Env, name: string, baseUrl: string): Promise<string> {
  const id = userIDFromName(name);
  if (!id) return "❌ Tên user không hợp lệ.";
  const { status, body } = await readHandler(userSubscription(env, id));
  if (status === 404) return `❌ Không tìm thấy user <b>${escapeHtml(id)}</b>.`;
  if (status >= 400) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  const link = `${baseUrl}/sub/${body.sub_token}`;
  return `🔗 <b>${escapeHtml(id)}</b>\n<code>${escapeHtml(link)}</code>`;
}

async function formatStatus(env: Env, nodeId: string): Promise<string> {
  const { status, body } = await readHandler(nodeStatus(env, nodeId, actorLabel.current));
  if (status === 404) return `❌ Không tìm thấy node <b>${escapeHtml(nodeId)}</b>.`;
  if (status >= 400) return `❌ <b>${escapeHtml(nodeId)}</b>: ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return [
    `📊 <b>${escapeHtml(nodeId)}</b>`,
    `mode: ${escapeHtml(String(body.mode ?? "?"))}`,
    `host: ${escapeHtml(String(body.vpn_host ?? "?"))}`,
    `xray: ${escapeHtml(String(body.xray ?? "?"))}`,
    `hysteria: ${escapeHtml(String(body.hysteria ?? "?"))}`,
    `cloudflared: ${escapeHtml(String(body.cloudflared ?? "?"))}`
  ].join("\n");
}

async function formatHealth(env: Env, nodeId: string): Promise<string> {
  const { status, body } = await readHandler(nodeHealthcheck(env, nodeId, actorLabel.current));
  if (status === 404) return `❌ Không tìm thấy node <b>${escapeHtml(nodeId)}</b>.`;
  if (status >= 400) return `🔴 <b>${escapeHtml(nodeId)}</b>: ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🟢 <b>${escapeHtml(nodeId)}</b> ok · ${body.latency_ms}ms (HTTP ${body.code})`;
}

// The core handlers log to the events table keyed on an actor string. The bot
// sets this per-update (tg:<telegram_user_id>) before dispatching.
const actorLabel = { current: "tg:unknown" };
export function setActor(actor: string): void {
  actorLabel.current = actor;
}
```

- [ ] **Step 2: Type-check**

Run: `npm --prefix panel/worker run check`
Expected: no errors. (No new behavior to unit-test here beyond the formatters, which are exercised via the dispatch test in Task 7.)

- [ ] **Step 3: Commit**

```bash
git add panel/worker/src/lib/telegram-commands.ts
git commit -m "feat(telegram): read-only command formatters"
```

---

## Task 6: Mutating handlers + background runner + 2-step confirm

**Files:**
- Modify: `panel/worker/src/lib/telegram-commands.ts`

- [ ] **Step 1: Add the sync-payload builder, mutating formatters, and the `dispatch` entrypoint**

Append to `panel/worker/src/lib/telegram-commands.ts`:

```ts
interface SyncUserRow {
  name: string;
  vless_uuid: string;
  hy2_pw: string;
}

async function buildNodeSyncUsers(env: Env, nodeId: string): Promise<SyncUserRow[]> {
  return all<SyncUserRow>(
    env.DB.prepare(
      "SELECT u.id AS name, un.vless_uuid, un.hy2_pw FROM user_nodes un JOIN users u ON u.id=un.user_id WHERE un.node_id=? ORDER BY u.id"
    ).bind(nodeId)
  );
}

function summarize(results: Array<{ node_id?: string; ok: boolean; error?: string }> | undefined): string {
  if (!results || results.length === 0) return "(không có node active)";
  return results
    .map((r) => (r.ok ? `✅ ${escapeHtml(r.node_id ?? "?")}` : `⚠️ ${escapeHtml(r.node_id ?? "?")}: ${escapeHtml(r.error ?? "lỗi")}`))
    .join("\n");
}

async function formatAddUser(env: Env, name: string): Promise<string> {
  const { status, body } = await readHandler(createUserByName(env, name, actorLabel.current));
  if (status === 409) return `⚠️ User <b>${escapeHtml(body?.detail || name)}</b> đã tồn tại.`;
  if (status >= 400 && status !== 207) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `👤 Thêm <b>${escapeHtml(body.id)}</b>:\n${summarize(body.results)}`;
}

async function formatDelUser(env: Env, id: string): Promise<string> {
  const { status, body } = await readHandler(deleteUser(env, id, actorLabel.current));
  if (status === 404) return `❌ Không tìm thấy user <b>${escapeHtml(id)}</b>.`;
  if (status >= 400 && status !== 207) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🗑 Xóa <b>${escapeHtml(id)}</b>:\n${summarize(body.results)}`;
}

async function formatUpgrade(env: Env, id: string): Promise<string> {
  const { status, body } = await readHandler(userUpgradeNodes(env, id, actorLabel.current));
  if (status === 404) return `❌ Không tìm thấy user <b>${escapeHtml(id)}</b>.`;
  if (status >= 400 && status !== 207) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  if ((body.addedCount ?? 0) === 0) return `ℹ️ <b>${escapeHtml(id)}</b> đã có trên mọi node.`;
  return `⬆️ <b>${escapeHtml(id)}</b> thêm vào: ${body.addedNodes.map((n: string) => escapeHtml(n)).join(", ")}`;
}

async function formatRotate(env: Env, nodeId: string): Promise<string> {
  const { status, body } = await readHandler(nodeRotateCore(env, nodeId, {}, actorLabel.current));
  if (status === 404) return `❌ Không tìm thấy node <b>${escapeHtml(nodeId)}</b>.`;
  if (status >= 400) return `❌ <b>${escapeHtml(nodeId)}</b>: ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🔁 <b>${escapeHtml(nodeId)}</b> → <code>${escapeHtml(body.vpn_host)}</code> (${escapeHtml(body.public_ip)})`;
}

async function formatSync(env: Env, nodeId: string): Promise<string> {
  const users = await buildNodeSyncUsers(env, nodeId);
  const { status, body } = await readHandler(nodeSyncCore(env, nodeId, users, actorLabel.current));
  if (status === 404) return `❌ Không tìm thấy node <b>${escapeHtml(nodeId)}</b>.`;
  if (status >= 400) return `❌ <b>${escapeHtml(nodeId)}</b>: ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🔃 <b>${escapeHtml(nodeId)}</b> đồng bộ ${users.length} user.`;
}

// Run a slow action in the background: send "processing", do the work, edit the
// placeholder with the result. Errors become a ❌ reply instead of a webhook 5xx.
function runBackground(
  env: Env,
  ctx: ExecutionContext,
  chatId: number,
  work: () => Promise<string>
): void {
  ctx.waitUntil(
    (async () => {
      const token = env.TELEGRAM_BOT_TOKEN!;
      const placeholder = await sendMessage(token, chatId, "⏳ Đang xử lý…");
      try {
        const text = await work();
        await editMessageText(token, chatId, placeholder.message_id, text);
      } catch (e) {
        await editMessageText(token, chatId, placeholder.message_id, `❌ ${escapeHtml(String(e))}`);
      }
    })()
  );
}

export async function dispatch(env: Env, ctx: ExecutionContext, update: TgUpdate, baseUrl: string): Promise<void> {
  const token = env.TELEGRAM_BOT_TOKEN!;

  if (update.callback_query) {
    const cq = update.callback_query;
    setActor(`tg:${cq.from.id}`);
    await answerCallbackQuery(token, cq.id);
    const chatId = cq.message?.chat.id;
    if (chatId == null || !cq.data) return;
    const parsed = parseCallback(cq.data);
    if (!parsed) return;

    // Destructive callbacks require a confirm step.
    if (parsed.entity === "u" && parsed.action === "del" && !parsed.confirmed) {
      await sendMessage(token, chatId, `⚠️ Xóa user <b>${escapeHtml(parsed.id)}</b>?`, {
        keyboard: [[
          { text: "✅ Có", callback_data: `u:del:${parsed.id}:yes` },
          { text: "❌ Không", callback_data: `x:noop:0` }
        ]]
      });
      return;
    }
    if (parsed.entity === "n" && parsed.action === "rotate" && !parsed.confirmed) {
      await sendMessage(token, chatId, `⚠️ Rotate domain node <b>${escapeHtml(parsed.id)}</b>?`, {
        keyboard: [[
          { text: "✅ Có", callback_data: `n:rotate:${parsed.id}:yes` },
          { text: "❌ Không", callback_data: `x:noop:0` }
        ]]
      });
      return;
    }

    if (parsed.entity === "x") return; // noop / cancel
    if (parsed.entity === "n" && parsed.action === "health") {
      runBackground(env, ctx, chatId, () => formatHealth(env, parsed.id));
    } else if (parsed.entity === "n" && parsed.action === "status") {
      runBackground(env, ctx, chatId, () => formatStatus(env, parsed.id));
    } else if (parsed.entity === "n" && parsed.action === "rotate" && parsed.confirmed) {
      runBackground(env, ctx, chatId, () => formatRotate(env, parsed.id));
    } else if (parsed.entity === "u" && parsed.action === "del" && parsed.confirmed) {
      runBackground(env, ctx, chatId, () => formatDelUser(env, parsed.id));
    } else if (parsed.entity === "u" && parsed.action === "sub") {
      runBackground(env, ctx, chatId, () => formatSub(env, parsed.id, baseUrl));
    } else if (parsed.entity === "u" && parsed.action === "upg") {
      runBackground(env, ctx, chatId, () => formatUpgrade(env, parsed.id));
    }
    return;
  }

  const msg = update.message;
  if (!msg?.text) return;
  setActor(`tg:${msg.from?.id ?? "unknown"}`);
  const chatId = msg.chat.id;
  const parsed = parseCommand(msg.text);
  if (!parsed) return;

  switch (parsed.cmd) {
    case "start":
    case "help":
      await sendMessage(token, chatId, HELP_TEXT);
      return;
    case "nodes": {
      const { text, keyboard } = await formatNodes(env);
      await sendMessage(token, chatId, text, { keyboard });
      return;
    }
    case "users": {
      const { text, keyboard } = await formatUsers(env);
      await sendMessage(token, chatId, text, { keyboard });
      return;
    }
    case "sub":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /sub &lt;tên&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatSub(env, parsed.arg, baseUrl));
      return;
    case "status":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /status &lt;node&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatStatus(env, parsed.arg));
      return;
    case "health":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /health &lt;node&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatHealth(env, parsed.arg));
      return;
    case "sync":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /sync &lt;node&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatSync(env, parsed.arg));
      return;
    case "adduser":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /adduser &lt;tên&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatAddUser(env, parsed.arg));
      return;
    case "upgrade": {
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /upgrade &lt;tên&gt;"); return; }
      const upId = userIDFromName(parsed.arg);
      if (!upId) { await sendMessage(token, chatId, "❌ Tên user không hợp lệ."); return; }
      runBackground(env, ctx, chatId, () => formatUpgrade(env, upId));
      return;
    }
    case "deluser": {
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /deluser &lt;tên&gt;"); return; }
      const delId = userIDFromName(parsed.arg);
      if (!delId) { await sendMessage(token, chatId, "❌ Tên user không hợp lệ."); return; }
      await sendMessage(token, chatId, `⚠️ Xóa user <b>${escapeHtml(delId)}</b>?`, {
        keyboard: [[
          { text: "✅ Có", callback_data: `u:del:${delId}:yes` },
          { text: "❌ Không", callback_data: `x:noop:0` }
        ]]
      });
      return;
    }
    case "rotate": {
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /rotate &lt;node&gt;"); return; }
      await sendMessage(token, chatId, `⚠️ Rotate domain node <b>${escapeHtml(parsed.arg)}</b>?`, {
        keyboard: [[
          { text: "✅ Có", callback_data: `n:rotate:${parsed.arg}:yes` },
          { text: "❌ Không", callback_data: `x:noop:0` }
        ]]
      });
      return;
    }
    default:
      await sendMessage(token, chatId, "Lệnh không rõ. Gõ /help.");
  }
}
```

- [ ] **Step 2: Type-check**

Run: `npm --prefix panel/worker run check`
Expected: no errors.

- [ ] **Step 3: Write a dispatch confirm-gating test**

Append to `panel/worker/src/lib/telegram-commands.test.ts`:

```ts
import { vi } from "vitest";

vi.mock("./telegram", async (orig) => {
  const actual = await orig<typeof import("./telegram")>();
  return {
    ...actual,
    sendMessage: vi.fn().mockResolvedValue({ message_id: 1, chat: { id: -1, type: "group" } }),
    editMessageText: vi.fn().mockResolvedValue(true),
    answerCallbackQuery: vi.fn().mockResolvedValue(true)
  };
});

vi.mock("../routes/users", () => ({
  createUserByName: vi.fn(),
  deleteUser: vi.fn(),
  listUsers: vi.fn(),
  userSubscription: vi.fn(),
  userUpgradeNodes: vi.fn()
}));
vi.mock("../routes/nodes", () => ({
  listNodes: vi.fn(),
  nodeHealthcheck: vi.fn(),
  nodeRotateCore: vi.fn(),
  nodeStatus: vi.fn(),
  nodeSyncCore: vi.fn()
}));

import { dispatch } from "./telegram-commands";
import { sendMessage } from "./telegram";
import { deleteUser } from "../routes/users";
import type { Env } from "../types";

function fakeCtx(): ExecutionContext {
  return { waitUntil: (_p: Promise<unknown>) => {}, passThroughOnException: () => {} } as ExecutionContext;
}

describe("dispatch confirm gating", () => {
  it("/deluser only prompts, does not delete", async () => {
    const env = { TELEGRAM_BOT_TOKEN: "T" } as Env;
    await dispatch(env, fakeCtx(), {
      update_id: 1,
      message: { message_id: 2, chat: { id: -100, type: "group" }, from: { id: 9, is_bot: false }, text: "/deluser alice" }
    }, "https://panel.example");
    expect(deleteUser).not.toHaveBeenCalled();
    const lastCall = (sendMessage as any).mock.calls.at(-1);
    expect(lastCall[2]).toContain("Xóa user");
  });
});
```

- [ ] **Step 4: Run the dispatch test**

Run: `npm --prefix panel/worker test -- telegram-commands.test.ts`
Expected: PASS (parser tests + the new confirm-gating test).

- [ ] **Step 5: Commit**

```bash
git add panel/worker/src/lib/telegram-commands.ts panel/worker/src/lib/telegram-commands.test.ts
git commit -m "feat(telegram): mutating handlers, background runner, 2-step confirm"
```

---

## Task 7: Webhook route with auth (`routes/telegram.ts`)

**Files:**
- Create: `panel/worker/src/routes/telegram.ts`
- Test: `panel/worker/src/routes/telegram.test.ts`

- [ ] **Step 1: Write failing auth tests**

Create `panel/worker/src/routes/telegram.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";

const dispatchMock = vi.fn().mockResolvedValue(undefined);
vi.mock("../lib/telegram-commands", () => ({ dispatch: dispatchMock }));

import { handleTelegramWebhook } from "./telegram";
import type { Env } from "../types";

function fakeCtx(): ExecutionContext {
  return { waitUntil: () => {}, passThroughOnException: () => {} } as ExecutionContext;
}

const env = {
  TELEGRAM_BOT_TOKEN: "T",
  TELEGRAM_WEBHOOK_SECRET: "S",
  TELEGRAM_GROUP_ID: "-100"
} as Env;

function req(body: unknown, secret?: string): Request {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (secret !== undefined) headers["X-Telegram-Bot-Api-Secret-Token"] = secret;
  return new Request("https://panel.example/telegram/webhook", {
    method: "POST",
    headers,
    body: JSON.stringify(body)
  });
}

describe("handleTelegramWebhook", () => {
  it("rejects a missing/wrong secret header without dispatching", async () => {
    dispatchMock.mockClear();
    const res = await handleTelegramWebhook(env, req({ update_id: 1 }, "WRONG"), fakeCtx());
    expect(res.status).toBe(200);
    expect(dispatchMock).not.toHaveBeenCalled();
  });

  it("ignores updates from the wrong chat id", async () => {
    dispatchMock.mockClear();
    const update = { update_id: 1, message: { message_id: 1, chat: { id: -999, type: "group" }, text: "/help" } };
    const res = await handleTelegramWebhook(env, req(update, "S"), fakeCtx());
    expect(res.status).toBe(200);
    expect(dispatchMock).not.toHaveBeenCalled();
  });

  it("dispatches a valid update from the allowed chat", async () => {
    dispatchMock.mockClear();
    const update = { update_id: 1, message: { message_id: 1, chat: { id: -100, type: "group" }, text: "/help" } };
    const res = await handleTelegramWebhook(env, req(update, "S"), fakeCtx());
    expect(res.status).toBe(200);
    expect(dispatchMock).toHaveBeenCalledTimes(1);
    expect(dispatchMock.mock.calls[0][3]).toBe("https://panel.example");
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm --prefix panel/worker test -- routes/telegram.test.ts`
Expected: FAIL — `Cannot find module './telegram'`.

- [ ] **Step 3: Implement the webhook handler**

Create `panel/worker/src/routes/telegram.ts`:

```ts
import type { Env } from "../types";
import { dispatch } from "../lib/telegram-commands";
import { type TgUpdate } from "../lib/telegram";

// Telegram always receives 200 — even on rejection or internal error — so it
// does not retry. Rejections are silent (no body) to avoid leaking which check
// failed.
const OK = new Response("ok", { status: 200 });

function chatIdOf(update: TgUpdate): number | null {
  if (update.message) return update.message.chat.id;
  if (update.callback_query?.message) return update.callback_query.message.chat.id;
  return null;
}

export async function handleTelegramWebhook(
  env: Env,
  request: Request,
  ctx: ExecutionContext
): Promise<Response> {
  if (!env.TELEGRAM_BOT_TOKEN || !env.TELEGRAM_WEBHOOK_SECRET || !env.TELEGRAM_GROUP_ID) {
    return OK; // bot not configured — ignore
  }
  const secret = request.headers.get("X-Telegram-Bot-Api-Secret-Token");
  if (secret !== env.TELEGRAM_WEBHOOK_SECRET) {
    return OK;
  }

  let update: TgUpdate;
  try {
    update = (await request.json()) as TgUpdate;
  } catch {
    return OK;
  }

  const allowedChat = Number(env.TELEGRAM_GROUP_ID);
  if (chatIdOf(update) !== allowedChat) {
    return OK;
  }

  const baseUrl = new URL(request.url).origin;
  try {
    await dispatch(env, ctx, update, baseUrl);
  } catch {
    // Swallow — never return non-200 to Telegram.
  }
  return OK;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm --prefix panel/worker test -- routes/telegram.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add panel/worker/src/routes/telegram.ts panel/worker/src/routes/telegram.test.ts
git commit -m "feat(telegram): webhook route with secret + group-id auth"
```

---

## Task 8: Wire the route into the Worker entry

**Files:**
- Modify: `panel/worker/src/index.ts`

- [ ] **Step 1: Import the handler**

In `panel/worker/src/index.ts`, add to the imports near the top:

```ts
import { handleTelegramWebhook } from "./routes/telegram";
```

- [ ] **Step 2: Add `ctx` to `fetch` and route the webhook**

Change the `fetch` signature and add the telegram branch immediately after the
`subToken` block (before the `if (!pathname.startsWith("/api/"))` guard):

```ts
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    const { pathname } = url;

    const subToken = parseSubToken(pathname);
    if (subToken && request.method === "GET") {
      return publicSubscription(env, subToken);
    }

    if (pathname === "/telegram/webhook" && request.method === "POST") {
      return handleTelegramWebhook(env, request, ctx);
    }

    if (!pathname.startsWith("/api/")) {
      return notFound(pathname);
    }
```

- [ ] **Step 3: Type-check and run the full Worker test suite**

Run: `npm --prefix panel/worker run check && npm --prefix panel/worker test`
Expected: no type errors; all tests pass (telegram + pre-existing).

- [ ] **Step 4: Commit**

```bash
git add panel/worker/src/index.ts
git commit -m "feat(telegram): route POST /telegram/webhook in Worker entry"
```

---

## Task 9: Setup script + README

**Files:**
- Create: `scripts/telegram-setup.sh`
- Modify: `README.md`

- [ ] **Step 1: Write the setup script**

Create `scripts/telegram-setup.sh`:

```bash
#!/usr/bin/env bash
# Registers the Telegram webhook + command menu for the cfvpn bot.
# Usage:
#   TELEGRAM_BOT_TOKEN=... TELEGRAM_WEBHOOK_SECRET=... PANEL_HOST=panel.rwl247.dev \
#     bash scripts/telegram-setup.sh
set -euo pipefail

: "${TELEGRAM_BOT_TOKEN:?set TELEGRAM_BOT_TOKEN}"
: "${TELEGRAM_WEBHOOK_SECRET:?set TELEGRAM_WEBHOOK_SECRET}"
: "${PANEL_HOST:?set PANEL_HOST (e.g. panel.rwl247.dev)}"

API="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"

echo "Setting webhook -> https://${PANEL_HOST}/telegram/webhook"
curl -fsS "${API}/setWebhook" \
  --data-urlencode "url=https://${PANEL_HOST}/telegram/webhook" \
  --data-urlencode "secret_token=${TELEGRAM_WEBHOOK_SECRET}" \
  --data-urlencode 'allowed_updates=["message","callback_query"]'
echo

echo "Setting command menu"
curl -fsS "${API}/setMyCommands" \
  -H 'content-type: application/json' \
  -d '{"commands":[
    {"command":"help","description":"Danh sách lệnh"},
    {"command":"nodes","description":"Danh sách node"},
    {"command":"status","description":"Trạng thái node: /status <node>"},
    {"command":"health","description":"Healthcheck: /health <node>"},
    {"command":"sync","description":"Đồng bộ user lên node: /sync <node>"},
    {"command":"rotate","description":"Đổi domain node: /rotate <node>"},
    {"command":"users","description":"Danh sách user"},
    {"command":"adduser","description":"Thêm user: /adduser <tên>"},
    {"command":"deluser","description":"Xóa user: /deluser <tên>"},
    {"command":"sub","description":"Link subscription: /sub <tên>"},
    {"command":"upgrade","description":"Thêm user vào node mới: /upgrade <tên>"}
  ]}'
echo
echo "Done."
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x scripts/telegram-setup.sh`

- [ ] **Step 3: Document in README**

In `README.md`, add a new section after the "Panel (React) and Worker" section:

````markdown
## Telegram bot

A Telegram bot provides a second control surface (manage users/nodes, view
status) from one private group. It runs inside the `cfvpn-panel-api` Worker as
`POST /telegram/webhook`.

Setup:

```bash
# 1. Secrets (never commit these)
wrangler --config panel/worker/wrangler.toml secret put TELEGRAM_BOT_TOKEN
wrangler --config panel/worker/wrangler.toml secret put TELEGRAM_WEBHOOK_SECRET   # any random string

# 2. TELEGRAM_GROUP_ID is already set in wrangler.toml [vars] (-1003806233980)

# 3. Deploy, then register the webhook + command menu
npm --prefix panel/worker run deploy
TELEGRAM_BOT_TOKEN=...  TELEGRAM_WEBHOOK_SECRET=...  PANEL_HOST=panel.rwl247.dev \
  bash scripts/telegram-setup.sh
```

Security: the Worker rejects any webhook whose `X-Telegram-Bot-Api-Secret-Token`
header does not equal `TELEGRAM_WEBHOOK_SECRET`, and ignores any update whose
`chat.id` is not `TELEGRAM_GROUP_ID`. Mutations are logged to the `events` table
with actor `tg:<telegram_user_id>`.

Commands: `/help`, `/nodes`, `/status <node>`, `/health <node>`, `/sync <node>`,
`/rotate <node>`, `/users`, `/adduser <name>`, `/deluser <name>`, `/sub <name>`,
`/upgrade <name>`. Destructive actions (`/deluser`, `/rotate`) ask for
confirmation via inline buttons.
````

- [ ] **Step 4: Commit**

```bash
git add scripts/telegram-setup.sh README.md
git commit -m "docs(telegram): setup script and README section"
```

---

## Final verification

- [ ] **Run the full Worker suite and type-check**

Run: `npm --prefix panel/worker run check && npm --prefix panel/worker test`
Expected: no type errors; all tests pass.

- [ ] **Confirm the Go build is unaffected**

Run: `go build ./cmd/cfvpnctl ./cmd/cfvpn-agent`
Expected: builds clean (no Go files changed).

- [ ] **Manual smoke test (after deploy + setup script)**

In the Telegram group, send `/help`, `/nodes`, then tap a `📊` button and run
`/adduser smoketest` followed by `/deluser smoketest` (confirm). Verify replies
and that rows appear in the `events` table with actor `tg:<id>`.

---

## Notes for the implementer

- **Do not** put `TELEGRAM_BOT_TOKEN` / `TELEGRAM_WEBHOOK_SECRET` in `wrangler.toml` — they are `wrangler secret`s.
- The `actorLabel` module-global in `telegram-commands.ts` is set per-update via `setActor` at the top of `dispatch`. It is acceptable here because each Worker invocation handles one update; do not parallelize updates within one invocation.
- `userIDFromName` is imported from `../lib/db` (already used by `routes/users.ts`).
- The `events` retention cron and all existing routes are untouched.
