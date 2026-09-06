import type { Env } from "../types";
import { dispatch } from "../lib/telegram-commands";
import { type TgUpdate } from "../lib/telegram";

// Telegram always receives 200 — even on rejection or internal error — so it
// does not retry. Rejections are silent (no body) to avoid leaking which check
// failed. Built per-call: constructing a Response at module/global scope is
// disallowed by the Workers runtime.
function ok(): Response {
  return new Response("ok", { status: 200 });
}

// Constant-time comparison. The Workers runtime does not expose
// crypto.timingSafeEqual, so compare SHA-256 digests instead of the strings:
// the loop then always runs over a fixed 32 bytes, so neither the number of
// matching leading characters nor the *length* of the secret is observable
// (the previous version looped max(a.length, b.length) times).
export async function timingSafeEqual(a: string, b: string): Promise<boolean> {
  const enc = new TextEncoder();
  const [da, db] = await Promise.all([
    crypto.subtle.digest("SHA-256", enc.encode(a)),
    crypto.subtle.digest("SHA-256", enc.encode(b))
  ]);
  const x = new Uint8Array(da);
  const y = new Uint8Array(db);
  let diff = 0;
  for (let i = 0; i < x.length; i += 1) {
    diff |= x[i] ^ y[i];
  }
  return diff === 0;
}

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
    return ok(); // bot not configured — ignore
  }
  const secret = request.headers.get("X-Telegram-Bot-Api-Secret-Token") ?? "";
  if (!(await timingSafeEqual(secret, env.TELEGRAM_WEBHOOK_SECRET))) {
    return ok();
  }

  let update: TgUpdate;
  try {
    update = (await request.json()) as TgUpdate;
  } catch {
    return ok();
  }

  const allowedChat = Number(env.TELEGRAM_GROUP_ID);
  if (chatIdOf(update) !== allowedChat) {
    return ok();
  }

  const baseUrl = new URL(request.url).origin;
  try {
    await dispatch(env, ctx, update, baseUrl);
  } catch {
    // Swallow — never return non-200 to Telegram.
  }
  return ok();
}
