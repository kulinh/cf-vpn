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

// Telegram's message cap is 4096 chars; stay well under it so the HTML tags
// re-closed below and the suffix always fit.
export const TELEGRAM_TEXT_MAX = 4000;
const TRUNCATION_SUFFIX = "\n… (cắt bớt)";

// Re-close whatever <b>/<code>/… tags the cut left open — Telegram rejects the
// whole message ("can't parse entities") otherwise, which is exactly the
// silent-bot failure truncation is meant to prevent.
function closeOpenTags(html: string): string {
  const open: string[] = [];
  for (const m of html.matchAll(/<(\/?)([a-z]+)[^>]*>/g)) {
    if (m[1]) {
      const i = open.lastIndexOf(m[2]);
      if (i >= 0) open.splice(i, 1);
    } else {
      open.push(m[2]);
    }
  }
  return html + open.reverse().map((t) => `</${t}>`).join("");
}

export function truncateForTelegram(text: string, max = TELEGRAM_TEXT_MAX): string {
  if (text.length <= max) {
    return text;
  }
  let cut = text.slice(0, Math.max(0, max - TRUNCATION_SUFFIX.length));
  // Never split a surrogate pair (emoji) — half of one is invalid UTF-8 to
  // Telegram — nor a half-written tag or entity.
  const last = cut.charCodeAt(cut.length - 1);
  if (last >= 0xd800 && last <= 0xdbff) {
    cut = cut.slice(0, -1);
  }
  cut = cut.replace(/<[^>]*$/, "").replace(/&[^;\s]*$/, "");
  return closeOpenTags(cut) + TRUNCATION_SUFFIX;
}

// Only honour a short retry_after: a long one means we are being rate limited
// hard, and parking a Worker invocation for a minute is worse than losing one
// message.
const MAX_RETRY_AFTER_S = 10;

async function tgCall<T>(token: string, method: string, body: unknown, retried = false): Promise<T> {
  const res = await fetch(`${API_BASE}/bot${token}/${method}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body)
  });
  const data = (await res.json().catch(() => null)) as
    | { ok?: boolean; result?: T; description?: string; parameters?: { retry_after?: number } }
    | null;
  if (!data?.ok) {
    const retryAfter = data?.parameters?.retry_after;
    if (
      res.status === 429 &&
      !retried &&
      typeof retryAfter === "number" &&
      retryAfter > 0 &&
      retryAfter <= MAX_RETRY_AFTER_S
    ) {
      await new Promise((resolve) => setTimeout(resolve, retryAfter * 1000));
      return tgCall<T>(token, method, body, true);
    }
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
    text: truncateForTelegram(text),
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
    text: truncateForTelegram(text),
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
