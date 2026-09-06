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
