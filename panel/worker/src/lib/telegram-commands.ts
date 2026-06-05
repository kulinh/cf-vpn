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
