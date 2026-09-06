import { describe, expect, it, vi } from "vitest";

const { dispatchMock } = vi.hoisted(() => ({
  dispatchMock: vi.fn().mockResolvedValue(undefined)
}));
vi.mock("../lib/telegram-commands", () => ({ dispatch: dispatchMock }));

import { handleTelegramWebhook } from "./telegram";
import type { Env } from "../types";

function fakeCtx(): ExecutionContext {
  return { waitUntil: () => {}, passThroughOnException: () => {} } as unknown as ExecutionContext;
}

const env = {
  TELEGRAM_BOT_TOKEN: "T",
  TELEGRAM_WEBHOOK_SECRET: "S",
  TELEGRAM_GROUP_ID: "-100"
} as Env;

// The webhook is registered against *.workers.dev in production (wrangler.toml),
// so the tests must post there too — the old "https://panel.example" URL let the
// origin-echo bug (every bot /sub link 404ing) pass unnoticed.
const WEBHOOK_URL = "https://cfvpn-panel-api.acct.workers.dev/telegram/webhook";

function req(body: unknown, secret?: string, url = WEBHOOK_URL): Request {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (secret !== undefined) headers["X-Telegram-Bot-Api-Secret-Token"] = secret;
  return new Request(url, {
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

  it("rejects a secret that shares a prefix but differs in length", async () => {
    dispatchMock.mockClear();
    const res = await handleTelegramWebhook(env, req({ update_id: 1 }, "SS"), fakeCtx());
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

  const update = { update_id: 1, message: { message_id: 1, chat: { id: -100, type: "group" }, text: "/help" } };

  it("builds subscription links from PANEL_PUBLIC_ORIGIN, not the workers.dev request origin", async () => {
    dispatchMock.mockClear();
    const configured = { ...env, PANEL_PUBLIC_ORIGIN: "https://cp.rwl265.com" } as Env;

    const res = await handleTelegramWebhook(configured, req(update, "S"), fakeCtx());

    expect(res.status).toBe(200);
    expect(dispatchMock).toHaveBeenCalledTimes(1);
    // Echoing the request origin here yields .../sub/<token> on workers.dev,
    // which index.ts 404s — every link the bot handed out was dead.
    expect(dispatchMock.mock.calls[0][3]).toBe("https://cp.rwl265.com");
  });

  it("falls back to the request origin when PANEL_PUBLIC_ORIGIN is unset", async () => {
    dispatchMock.mockClear();
    const res = await handleTelegramWebhook(env, req(update, "S", "https://cp.rwl265.com/telegram/webhook"), fakeCtx());
    expect(res.status).toBe(200);
    expect(dispatchMock.mock.calls[0][3]).toBe("https://cp.rwl265.com");
  });
});
