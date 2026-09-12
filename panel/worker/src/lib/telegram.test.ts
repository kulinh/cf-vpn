import { afterEach, describe, expect, it, vi } from "vitest";
import {
  escapeHtml,
  sendMessage,
  editMessageText,
  answerCallbackQuery,
  truncateForTelegram,
  TELEGRAM_TEXT_MAX,
  type InlineKeyboard
} from "./telegram";

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

describe("truncateForTelegram", () => {
  const SUFFIX = "\n… (cắt bớt)";

  it("leaves a message that fits untouched", () => {
    expect(truncateForTelegram("short")).toBe("short");
    expect(truncateForTelegram("x".repeat(TELEGRAM_TEXT_MAX))).toHaveLength(TELEGRAM_TEXT_MAX);
  });

  it("cuts an over-long message and marks it in Vietnamese", () => {
    const out = truncateForTelegram("x".repeat(9000));
    expect(out.length).toBeLessThanOrEqual(TELEGRAM_TEXT_MAX);
    expect(out.endsWith(SUFFIX)).toBe(true);
  });

  it("never cuts inside a surrogate pair", () => {
    // The emoji straddles the cut point: keeping its high surrogate alone is
    // invalid UTF-8 and Telegram rejects the whole message.
    const text = `${"a".repeat(7)}😀${"b".repeat(50)}`;
    const out = truncateForTelegram(text, 20);
    expect(out).toBe(`${"a".repeat(7)}${SUFFIX}`);
    // No lone high surrogate anywhere.
    expect(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])/.test(out)).toBe(false);
  });

  it("keeps a whole surrogate pair that still fits", () => {
    const out = truncateForTelegram(`${"a".repeat(6)}😀${"b".repeat(50)}`, 20);
    expect(out).toBe(`${"a".repeat(6)}😀${SUFFIX}`);
  });

  it("does not leave a half-written tag or entity behind", () => {
    expect(truncateForTelegram(`${"a".repeat(6)}<b>bold`, 20)).not.toMatch(/<b?$/);
    expect(truncateForTelegram(`${"a".repeat(6)}&amp;more`, 20)).not.toMatch(/&[a-z]*$/);
  });
});

describe("sendMessage / editMessageText length cap", () => {
  it("truncates the text instead of letting Telegram 400 a long /nodes", async () => {
    // One Response per call: a body can only be read once.
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async () => new Response(JSON.stringify({ ok: true, result: { message_id: 1 } }), { status: 200 }));
    await sendMessage("TOKEN", -100, "x".repeat(9000));
    await editMessageText("TOKEN", -100, 5, "y".repeat(9000));

    for (const [, init] of fetchMock.mock.calls) {
      const body = JSON.parse((init as RequestInit).body as string) as { text: string };
      expect(body.text.length).toBeLessThanOrEqual(TELEGRAM_TEXT_MAX);
      expect(body.text.endsWith("\n… (cắt bớt)")).toBe(true);
    }
  });
});

describe("tgCall 429 handling", () => {
  // A Response body can be read only once, so every call needs a fresh one.
  const tooMany = (retryAfter?: number) => () =>
    new Response(
      JSON.stringify({
        ok: false,
        description: "Too Many Requests",
        parameters: retryAfter === undefined ? {} : { retry_after: retryAfter }
      }),
      { status: 429 }
    );

  it("waits out a short retry_after and retries once", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementationOnce(async () => tooMany(2)())
      .mockImplementationOnce(async () => new Response(JSON.stringify({ ok: true, result: { message_id: 9 } }), { status: 200 }));
    vi.useFakeTimers();
    try {
      const pending = sendMessage("TOKEN", -100, "hi");
      await vi.runAllTimersAsync();
      await expect(pending).resolves.toMatchObject({ message_id: 9 });
    } finally {
      vi.useRealTimers();
    }
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("surfaces the error instead of parking the Worker on a long retry_after", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async () => tooMany(60)());
    await expect(sendMessage("TOKEN", -100, "hi")).rejects.toThrow("Too Many Requests");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not retry a 429 with no retry_after, nor a second time", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async () => tooMany()());
    await expect(sendMessage("TOKEN", -100, "hi")).rejects.toThrow("Too Many Requests");
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // A 429 on the retry itself must give up, not loop.
    fetchMock.mockReset();
    fetchMock.mockImplementation(async () => tooMany(1)());
    vi.useFakeTimers();
    let thrown: unknown;
    try {
      const pending = sendMessage("TOKEN", -100, "hi").catch((e: unknown) => {
        thrown = e;
      });
      await vi.runAllTimersAsync();
      await pending;
    } finally {
      vi.useRealTimers();
    }
    expect(String(thrown)).toContain("Too Many Requests");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
