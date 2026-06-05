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
