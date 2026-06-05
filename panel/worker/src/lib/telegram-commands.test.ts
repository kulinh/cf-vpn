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

import { dispatch } from "./telegram-commands";
import { sendMessage } from "./telegram";
import { deleteUser } from "../routes/users";
import type { Env } from "../types";

function fakeCtx(): ExecutionContext {
  return { waitUntil: (_p: Promise<unknown>) => {}, passThroughOnException: () => {} } as ExecutionContext;
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
