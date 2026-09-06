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

import { dispatch, isCallbackId } from "./telegram-commands";
import { editMessageText, sendMessage } from "./telegram";
import { deleteUser, listUsers, userSubscription } from "../routes/users";
import { nodeHealthcheck, nodeStatus } from "../routes/nodes";
import type { Env } from "../types";

function fakeCtx(): ExecutionContext {
  return { waitUntil: (_p: Promise<unknown>) => {}, passThroughOnException: () => {} } as ExecutionContext;
}

// Collects the background work so a test can await it (production hands it to
// ctx.waitUntil, which the fake ctx above drops on the floor).
function collectingCtx(): { ctx: ExecutionContext; done: () => Promise<unknown> } {
  const pending: Promise<unknown>[] = [];
  return {
    ctx: {
      waitUntil: (p: Promise<unknown>) => {
        pending.push(p);
      },
      passThroughOnException: () => {}
    } as unknown as ExecutionContext,
    done: () => Promise.all(pending)
  };
}

const env = { TELEGRAM_BOT_TOKEN: "T" } as Env;
const jsonRes = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

function message(text: string, fromId: number) {
  return {
    update_id: fromId,
    message: { message_id: 2, chat: { id: -100, type: "group" }, from: { id: fromId, is_bot: false }, text }
  };
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

describe("actor isolation (M-W3)", () => {
  it("keeps each concurrent update's actor with its own command", async () => {
    const seen: string[] = [];
    vi.mocked(nodeStatus).mockImplementation(async (_env, _id, actor) => {
      // Resolve out of order so a shared global would be observably wrong.
      await new Promise((r) => setTimeout(r, seen.length === 0 ? 20 : 0));
      seen.push(actor);
      return jsonRes({ mode: "direct", vpn_host: "h", xray: "ok", hysteria: "ok", cloudflared: "ok" });
    });

    const a = collectingCtx();
    const b = collectingCtx();
    await Promise.all([
      dispatch(env, a.ctx, message("/status SIN-01", 111), "https://cp.example"),
      dispatch(env, b.ctx, message("/status SIN-02", 222), "https://cp.example")
    ]);
    await Promise.all([a.done(), b.done()]);

    expect(seen.sort()).toEqual(["tg:111", "tg:222"]);
  });
});

describe("callback_data id validation (M-W5)", () => {
  it("accepts ordinary ids and rejects colons, emptiness and over-long values", () => {
    expect(isCallbackId("SIN-01")).toBe(true);
    expect(isCallbackId("kulinh")).toBe(true);
    // A colon would make parseCallback address a different node than the label.
    expect(isCallbackId("n:1")).toBe(false);
    expect(isCallbackId("")).toBe(false);
    // The bound is 64 bytes minus the longest wrapper ("u:del:" + ":yes").
    expect(isCallbackId("x".repeat(54))).toBe(true);
    expect(`u:del:${"x".repeat(54)}:yes`.length).toBe(64);
    expect(isCallbackId("x".repeat(55))).toBe(false);
    expect(isCallbackId("a b")).toBe(false);
  });

  it("omits buttons for a user id that cannot be put in callback_data", async () => {
    vi.mocked(listUsers).mockResolvedValue(
      jsonRes([{ id: "good", nodes: [] }, { id: "x".repeat(70), nodes: [] }])
    );
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

    await dispatch(env, fakeCtx(), message("/users", 9), "https://cp.example");

    const lastCall = (sendMessage as any).mock.calls.at(-1);
    const keyboard = lastCall[3].keyboard as Array<Array<{ callback_data: string }>>;
    expect(keyboard).toHaveLength(1);
    expect(keyboard[0][0].callback_data).toBe("u:sub:good");
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it("refuses /rotate with an id that would break callback_data", async () => {
    await dispatch(env, fakeCtx(), message("/rotate n:1:yes", 9), "https://cp.example");
    const lastCall = (sendMessage as any).mock.calls.at(-1);
    expect(lastCall[2]).toContain("Node id không hợp lệ");
    expect(lastCall[3]).toBeUndefined();
  });
});

describe("runBackground resilience", () => {
  it("still delivers the result when the placeholder send fails", async () => {
    (sendMessage as any).mockRejectedValueOnce(new Error("chat not found"));
    (sendMessage as any).mockResolvedValueOnce({ message_id: 5 });
    vi.mocked(nodeHealthcheck).mockResolvedValue(jsonRes({ latency_ms: 12, code: 200 }));
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const bg = collectingCtx();

    await dispatch(env, bg.ctx, message("/health SIN-01", 9), "https://cp.example");
    // Must resolve, not reject: this promise goes to ctx.waitUntil.
    await expect(bg.done()).resolves.toBeDefined();

    const lastCall = (sendMessage as any).mock.calls.at(-1);
    expect(lastCall[2]).toContain("SIN-01");
    errorSpy.mockRestore();
  });

  it("does not reject when editMessageText fails", async () => {
    (editMessageText as any).mockRejectedValueOnce(new Error("message is too long"));
    vi.mocked(userSubscription).mockResolvedValue(jsonRes({ sub_token: "t".repeat(32) }));
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const bg = collectingCtx();

    await dispatch(env, bg.ctx, message("/sub alice", 9), "https://cp.example");

    await expect(bg.done()).resolves.toBeDefined();
    expect(errorSpy).toHaveBeenCalled();
    errorSpy.mockRestore();
  });
});
