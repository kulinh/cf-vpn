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
