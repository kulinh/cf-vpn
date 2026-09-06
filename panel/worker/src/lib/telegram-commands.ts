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

// callback_data is capped at 64 bytes by Telegram and is split on ":", so an id
// carrying a colon would address a different entity than the button says, and an
// over-long id makes sendMessage 400 — which used to make /nodes and /users
// silently return nothing, forever. Ids that fail this are never put on a button.
const CALLBACK_ID_RE = /^[A-Za-z0-9._-]{1,32}$/;

export function isCallbackId(id: unknown): id is string {
  return typeof id === "string" && CALLBACK_ID_RE.test(id);
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
  const keyboard: InlineKeyboard = rows
    .filter((n) => {
      if (isCallbackId(n.id)) return true;
      console.warn("node id unusable in callback_data, buttons omitted:", n.id);
      return false;
    })
    .map((n) => [
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
  const keyboard: InlineKeyboard = users
    .filter((u: any) => {
      if (isCallbackId(u.id)) return true;
      console.warn("user id unusable in callback_data, buttons omitted:", u.id);
      return false;
    })
    .map((u: any) => [
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

async function formatStatus(env: Env, nodeId: string, actor: string): Promise<string> {
  const { status, body } = await readHandler(nodeStatus(env, nodeId, actor));
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

async function formatHealth(env: Env, nodeId: string, actor: string): Promise<string> {
  const { status, body } = await readHandler(nodeHealthcheck(env, nodeId, actor));
  if (status === 404) return `❌ Không tìm thấy node <b>${escapeHtml(nodeId)}</b>.`;
  if (status >= 400) return `🔴 <b>${escapeHtml(nodeId)}</b>: ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🟢 <b>${escapeHtml(nodeId)}</b> ok · ${escapeHtml(String(body.latency_ms ?? "?"))}ms (HTTP ${escapeHtml(String(body.code ?? "?"))})`;
}

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

async function formatAddUser(env: Env, name: string, actor: string): Promise<string> {
  const { status, body } = await readHandler(createUserByName(env, name, actor));
  if (status === 409) return `⚠️ User <b>${escapeHtml(body?.detail || name)}</b> đã tồn tại.`;
  if (status >= 400 && status !== 207) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `👤 Thêm <b>${escapeHtml(body.id)}</b>:\n${summarize(body.results)}`;
}

async function formatDelUser(env: Env, id: string, actor: string): Promise<string> {
  const { status, body } = await readHandler(deleteUser(env, id, actor));
  if (status === 404) return `❌ Không tìm thấy user <b>${escapeHtml(id)}</b>.`;
  if (status >= 400 && status !== 207) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🗑 Xóa <b>${escapeHtml(id)}</b>:\n${summarize(body.results)}`;
}

async function formatUpgrade(env: Env, id: string, actor: string): Promise<string> {
  const { status, body } = await readHandler(userUpgradeNodes(env, id, actor));
  if (status === 404) return `❌ Không tìm thấy user <b>${escapeHtml(id)}</b>.`;
  if (status >= 400 && status !== 207) return `❌ ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  if ((body.addedCount ?? 0) === 0) return `ℹ️ <b>${escapeHtml(id)}</b> đã có trên mọi node.`;
  return `⬆️ <b>${escapeHtml(id)}</b> thêm vào: ${body.addedNodes.map((n: string) => escapeHtml(n)).join(", ")}`;
}

async function formatRotate(env: Env, nodeId: string, actor: string): Promise<string> {
  const { status, body } = await readHandler(nodeRotateCore(env, nodeId, {}, actor));
  if (status === 404) return `❌ Không tìm thấy node <b>${escapeHtml(nodeId)}</b>.`;
  if (status >= 400) return `❌ <b>${escapeHtml(nodeId)}</b>: ${escapeHtml(body?.detail || body?.error || "lỗi")}`;
  return `🔁 <b>${escapeHtml(nodeId)}</b> → <code>${escapeHtml(body.vpn_host)}</code> (${escapeHtml(body.public_ip)})`;
}

async function formatSync(env: Env, nodeId: string, actor: string): Promise<string> {
  const users = await buildNodeSyncUsers(env, nodeId);
  const { status, body } = await readHandler(nodeSyncCore(env, nodeId, users, actor));
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
      // Nothing in here may reject: this promise is handed to ctx.waitUntil.
      // The placeholder send can fail (chat gone, 429) and editMessageText can
      // fail too (a >4096-char result), so both are guarded.
      let placeholderId: number | null = null;
      try {
        placeholderId = (await sendMessage(token, chatId, "⏳ Đang xử lý…")).message_id;
      } catch (e) {
        console.error("telegram placeholder failed", String(e));
      }
      let text: string;
      try {
        text = await work();
      } catch (e) {
        text = `❌ ${escapeHtml(String(e))}`;
      }
      try {
        if (placeholderId == null) {
          await sendMessage(token, chatId, text);
        } else {
          await editMessageText(token, chatId, placeholderId, text);
        }
      } catch (e) {
        console.error("telegram reply failed", String(e));
      }
    })()
  );
}

export async function dispatch(env: Env, ctx: ExecutionContext, update: TgUpdate, baseUrl: string): Promise<void> {
  const token = env.TELEGRAM_BOT_TOKEN!;

  if (update.callback_query) {
    const cq = update.callback_query;
    const actor = `tg:${cq.from.id}`;
    await answerCallbackQuery(token, cq.id);
    const chatId = cq.message?.chat.id;
    if (chatId == null || !cq.data) return;
    const parsed = parseCallback(cq.data);
    if (!parsed) return;

    // Destructive callbacks require a confirm step.
    if (parsed.entity === "u" && parsed.action === "del" && !parsed.confirmed) {
      if (!isCallbackId(parsed.id)) return;
      await sendMessage(token, chatId, `⚠️ Xóa user <b>${escapeHtml(parsed.id)}</b>?`, {
        keyboard: [[
          { text: "✅ Có", callback_data: `u:del:${parsed.id}:yes` },
          { text: "❌ Không", callback_data: `x:noop:0` }
        ]]
      });
      return;
    }
    if (parsed.entity === "n" && parsed.action === "rotate" && !parsed.confirmed) {
      if (!isCallbackId(parsed.id)) return;
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
      runBackground(env, ctx, chatId, () => formatHealth(env, parsed.id, actor));
    } else if (parsed.entity === "n" && parsed.action === "status") {
      runBackground(env, ctx, chatId, () => formatStatus(env, parsed.id, actor));
    } else if (parsed.entity === "n" && parsed.action === "rotate" && parsed.confirmed) {
      runBackground(env, ctx, chatId, () => formatRotate(env, parsed.id, actor));
    } else if (parsed.entity === "u" && parsed.action === "del" && parsed.confirmed) {
      runBackground(env, ctx, chatId, () => formatDelUser(env, parsed.id, actor));
    } else if (parsed.entity === "u" && parsed.action === "sub") {
      runBackground(env, ctx, chatId, () => formatSub(env, parsed.id, baseUrl));
    } else if (parsed.entity === "u" && parsed.action === "upg") {
      runBackground(env, ctx, chatId, () => formatUpgrade(env, parsed.id, actor));
    }
    return;
  }

  const msg = update.message;
  if (!msg?.text) return;
  const actor = `tg:${msg.from?.id ?? "unknown"}`;
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
      runBackground(env, ctx, chatId, () => formatStatus(env, parsed.arg, actor));
      return;
    case "health":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /health &lt;node&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatHealth(env, parsed.arg, actor));
      return;
    case "sync":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /sync &lt;node&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatSync(env, parsed.arg, actor));
      return;
    case "adduser":
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /adduser &lt;tên&gt;"); return; }
      runBackground(env, ctx, chatId, () => formatAddUser(env, parsed.arg, actor));
      return;
    case "upgrade": {
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /upgrade &lt;tên&gt;"); return; }
      const upId = userIDFromName(parsed.arg);
      if (!upId) { await sendMessage(token, chatId, "❌ Tên user không hợp lệ."); return; }
      runBackground(env, ctx, chatId, () => formatUpgrade(env, upId, actor));
      return;
    }
    case "deluser": {
      if (!parsed.arg) { await sendMessage(token, chatId, "Cú pháp: /deluser &lt;tên&gt;"); return; }
      const delId = userIDFromName(parsed.arg);
      if (!delId || !isCallbackId(delId)) { await sendMessage(token, chatId, "❌ Tên user không hợp lệ."); return; }
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
      // parsed.arg is raw user text and goes straight into callback_data.
      if (!isCallbackId(parsed.arg)) { await sendMessage(token, chatId, "❌ Node id không hợp lệ."); return; }
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
