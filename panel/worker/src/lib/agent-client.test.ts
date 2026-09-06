import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentHttpError, callAgent, isConfigError, isTimeoutError, MAX_TIMEOUT_MS } from "./agent-client";
import type { Env } from "../types";

const env = { ADMIN_HOST_ALLOWED_SUFFIXES: "example.com", AGENT_SHARED_SECRET: "" } as unknown as Env;
const target = { adminHost: "node-a.example.com", agentSecret: "s".repeat(20), nodeId: "NODE-A" };

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("callAgent error classification", () => {
  it("throws AgentHttpError carrying the status even when the agent returns a JSON body", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "unauthorized", detail: "bad bearer" }), { status: 401 })
    );

    const err = await callAgent(env, target, "/admin/v1/healthcheck", { method: "POST" }).catch((e) => e);

    expect(err).toBeInstanceOf(AgentHttpError);
    expect((err as AgentHttpError).status).toBe(401);
    // The old code surfaced only the JSON detail, so `agent_http_4xx` matching
    // never fired for the exact case it was written for.
    expect(String(err)).toContain("agent_http_401");
    expect(isConfigError(err)).toBe(true);
  });

  it("classifies 5xx and transport failures as transport errors", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "bad gateway" }), { status: 502 })
    );
    const httpErr = await callAgent(env, target, "/x").catch((e) => e);
    expect(isConfigError(httpErr)).toBe(false);

    vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("AbortError: The operation was aborted"));
    const transportErr = await callAgent(env, target, "/x").catch((e) => e);
    expect(isConfigError(transportErr)).toBe(false);
  });

  it("classifies host/auth misconfiguration as a config error", async () => {
    const badHost = await callAgent(env, { adminHost: "evil.attacker.net" }, "/x").catch((e) => e);
    expect(isConfigError(badHost)).toBe(true);

    const noSecret = await callAgent(env, { adminHost: "node-a.example.com" }, "/x").catch((e) => e);
    expect(isConfigError(noSecret)).toBe(true);
  });
});

describe("callAgent timeout budget", () => {
  it("aborts a response whose body never arrives (fetch resolves on headers)", async () => {
    vi.useFakeTimers();
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
      const signal = (init as RequestInit).signal!;
      return new Response(
        new ReadableStream({
          start(controller) {
            // Body stalls forever until the deadline aborts it.
            signal.addEventListener("abort", () => controller.error(new Error("aborted")));
          }
        }),
        { status: 200, headers: { "content-type": "application/json" } }
      );
    });

    const promise = callAgent(env, target, "/admin/v1/status", {}, 1000).catch((e) => String(e));
    await vi.advanceTimersByTimeAsync(1000);

    // The body read must be inside the deadline; without it this never settles.
    await expect(promise).resolves.toContain("agent_body_timeout");
  });

  it("caps a caller budget at MAX_TIMEOUT_MS", async () => {
    const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );

    await callAgent(env, target, "/x", {}, 120000);

    const delays = setTimeoutSpy.mock.calls.map((c) => c[1]);
    expect(delays).toContain(MAX_TIMEOUT_MS);
    expect(delays).not.toContain(120000);
  });
});

describe("callAgent shared-secret fallback", () => {
  it("warns with the node id when falling back to AGENT_SHARED_SECRET", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );
    const fallbackEnv = { ...env, AGENT_SHARED_SECRET: "fleet-secret" } as Env;

    await callAgent(fallbackEnv, { adminHost: "node-a.example.com", nodeId: "NODE-A" }, "/x");

    expect(warn).toHaveBeenCalledWith("agent_secret missing for node", "NODE-A");
  });

  it("does not warn when the node has its own secret", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );

    await callAgent({ ...env, AGENT_SHARED_SECRET: "fleet-secret" } as Env, target, "/x");

    expect(warn).not.toHaveBeenCalled();
  });
});

describe("isTimeoutError", () => {
  it("recognises an aborted fetch and a stalled body read", () => {
    const abort = new Error("The operation was aborted");
    abort.name = "AbortError";
    expect(isTimeoutError(abort)).toBe(true);
    expect(isTimeoutError(new Error("agent_body_timeout: response body not read within 55000ms"))).toBe(true);
    expect(isTimeoutError("AbortError: The operation was aborted")).toBe(true);
  });

  it("does not treat an HTTP answer or a plain transport error as unknown state", () => {
    // The agent answered — we know it did not silently complete the work.
    expect(isTimeoutError(new AgentHttpError(502, "agent_http_502: bad gateway"))).toBe(false);
    expect(isTimeoutError(new AgentHttpError(401, "agent_http_401: unauthorized"))).toBe(false);
    expect(isTimeoutError(new Error("connection refused"))).toBe(false);
  });
});
