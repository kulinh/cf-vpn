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
