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
