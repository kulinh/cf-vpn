import { beforeEach, describe, expect, it, vi } from "vitest";

const { sweepMock } = vi.hoisted(() => ({ sweepMock: vi.fn().mockResolvedValue(undefined) }));
vi.mock("./routes/nodes", async (orig) => {
  const actual = await orig<typeof import("./routes/nodes")>();
  return { ...actual, sweepNodesHealth: sweepMock };
});

import worker from "./index";
import type { Env } from "./types";

function makeEnv(): { env: Env; runs: { sql: string; args: unknown[] }[] } {
  const runs: { sql: string; args: unknown[] }[] = [];
  const db = {
    prepare(sql: string) {
      const state: { args: unknown[] } = { args: [] };
      const stmt = {
        bind(...args: unknown[]) {
          state.args = args;
          return stmt;
        },
        async run() {
          runs.push({ sql, args: state.args });
          return { success: true };
        },
        async all() {
          return { results: [] };
        },
        async first() {
          return null;
        }
      };
      return stmt;
    }
  };
  return { env: { DB: db } as unknown as Env, runs };
}

const ctx = { waitUntil: () => {}, passThroughOnException: () => {} } as unknown as ExecutionContext;
const scheduled = (cron: string) => ({ cron, scheduledTime: 0, noRetry: () => {} }) as unknown as ScheduledEvent;

describe("fetch router hardening", () => {
  it("does not throw a 500 on a malformed percent escape in /sub/ (pre-auth)", async () => {
    const { env } = makeEnv();
    const res = await worker.fetch(new Request("https://panel.example/sub/%"), env, ctx);
    // "/sub/%" matches the route regex; decodeURIComponent used to throw URIError
    // here, before any auth check, giving anonymous callers a 1101 exception.
    expect(res.status).toBe(404);
  });

  it("returns a generic internal_error without leaking the exception", async () => {
    const warn = vi.spyOn(console, "error").mockImplementation(() => {});
    const env = {
      DB: {
        prepare() {
          throw new Error("D1_ERROR: near SECRET_TABLE");
        }
      }
    } as unknown as Env;

    const res = await worker.fetch(
      new Request("https://panel.example/sub/" + "a".repeat(32)),
      env,
      ctx
    );

    expect(res.status).toBe(500);
    const body = await res.text();
    expect(JSON.parse(body)).toEqual({ error: "internal_error" });
    expect(body).not.toContain("SECRET_TABLE");
    warn.mockRestore();
  });

  it("answers a rate-limited Telegram webhook with 200 so Telegram does not retry", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { env } = makeEnv();
    const post = () =>
      worker.fetch(
        new Request("https://cfvpn-panel-api.workers.dev/telegram/webhook", {
          method: "POST",
          headers: { "CF-Connecting-IP": "198.51.100.7", "content-type": "application/json" },
          body: JSON.stringify({ update_id: 1 })
        }),
        env,
        ctx
      );

    const statuses: number[] = [];
    for (let i = 0; i < 15; i += 1) {
      statuses.push((await post()).status);
    }

    // Rate limiting must never surface as 429 here: a retried u:del / n:rotate
    // callback re-runs the mutation.
    expect(statuses.every((s) => s === 200)).toBe(true);
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});

describe("scheduled cron dispatch", () => {
  beforeEach(() => {
    sweepMock.mockClear();
  });

  it("runs the fleet sweep only for the */5 trigger", async () => {
    const { env, runs } = makeEnv();
    await worker.scheduled(scheduled("*/5 * * * *"), env, ctx);
    expect(sweepMock).toHaveBeenCalledTimes(1);
    expect(runs).toHaveLength(0);
  });

  it("runs the events prune only for the daily trigger", async () => {
    const { env, runs } = makeEnv();
    await worker.scheduled(scheduled("17 3 * * *"), env, ctx);
    expect(sweepMock).not.toHaveBeenCalled();
    expect(runs).toHaveLength(1);
    expect(runs[0].sql).toContain("DELETE FROM events WHERE ts < ?");
  });

  it("does nothing (and warns) for an unknown trigger instead of falling through to the sweep", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { env, runs } = makeEnv();

    await worker.scheduled(scheduled("0 4 * * *"), env, ctx);

    expect(sweepMock).not.toHaveBeenCalled();
    expect(runs).toHaveLength(0);
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});
