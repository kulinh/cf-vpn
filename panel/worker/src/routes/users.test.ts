import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../lib/agent-client", () => ({
  callAgent: vi.fn()
}));

vi.mock("../lib/events", () => ({
  logEvent: vi.fn().mockResolvedValue(undefined)
}));

import { callAgent } from "../lib/agent-client";
import { logEvent } from "../lib/events";
import { userUpgradeNodes } from "./users";
import type { Env } from "../types";

type UserRow = { id: string; name: string };
type NodeRow = { id: string; admin_host: string; status: string; agent_secret?: string | null };
type UserNodeRow = { user_id: string; node_id: string; vless_uuid: string; hy2_pw: string; created_at: number };

function makeEnv(seed?: {
  users?: UserRow[];
  nodes?: NodeRow[];
  userNodes?: UserNodeRow[];
}): Env {
  const users = new Map((seed?.users ?? []).map((u) => [u.id, u]));
  const nodes = (seed?.nodes ?? []).slice();
  const userNodes: UserNodeRow[] = (seed?.userNodes ?? []).slice();

  const makePrepared = (sql: string): D1PreparedStatement => {
    const state: { args: unknown[] } = { args: [] };

    const stmt: D1PreparedStatement = {
      bind(...args: unknown[]) {
        state.args = args;
        return stmt;
      },
      async first() {
        if (/SELECT id,name FROM users WHERE id=\?/.test(sql)) {
          const id = state.args[0] as string;
          return (users.get(id) ?? null) as never;
        }
        return null as never;
      },
      async all() {
        if (/SELECT id,admin_host,agent_secret FROM nodes WHERE status='active' ORDER BY id/.test(sql)) {
          return {
            results: nodes.filter((n) => n.status === "active").map((n) => ({ id: n.id, admin_host: n.admin_host, agent_secret: n.agent_secret ?? null }))
          } as never;
        }

        if (/SELECT node_id FROM user_nodes WHERE user_id=\?/.test(sql)) {
          const userId = state.args[0] as string;
          return {
            results: userNodes.filter((r) => r.user_id === userId).map((r) => ({ node_id: r.node_id }))
          } as never;
        }

        return { results: [] } as never;
      },
      async run() {
        if (/INSERT OR REPLACE INTO user_nodes/.test(sql)) {
          const [user_id, node_id, vless_uuid, hy2_pw, created_at] = state.args as [string, string, string, string, number];
          const idx = userNodes.findIndex((r) => r.user_id === user_id && r.node_id === node_id);
          const row = { user_id, node_id, vless_uuid, hy2_pw, created_at };
          if (idx >= 0) userNodes[idx] = row;
          else userNodes.push(row);
        }

        return { success: true } as never;
      }
    } as unknown as D1PreparedStatement;

    return stmt;
  };

  const db = {
    prepare(sql: string) {
      return makePrepared(sql);
    },
    async batch() {
      return [] as never;
    },
    async exec() {
      return { count: 0, duration: 0 } as never;
    },
    withSession() {
      return this as never;
    },
    async dump() {
      return new ArrayBuffer(0);
    }
  } as unknown as D1Database;

  return { DB: db };
}

describe("userUpgradeNodes", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  it("logs upgrade event with authenticated actor", async () => {
    vi.mocked(callAgent).mockResolvedValue({ vless_uuid: "u1", hy2_pw: "p1" });

    const env = makeEnv({
      users: [{ id: "kulinh", name: "kulinh" }],
      nodes: [{ id: "VN", admin_host: "admin-vn.example.com", status: "active" }],
      userNodes: []
    });

    const res = await userUpgradeNodes(env, "kulinh", "operator@example.com");

    expect(res.status).toBe(200);
    expect(logEvent).toHaveBeenCalled();
    expect(vi.mocked(logEvent).mock.calls[0]?.[1]).toBe("operator@example.com");
  });

  it("returns 207 and partial outcome when some nodes fail", async () => {
    vi.mocked(callAgent)
      .mockResolvedValueOnce({ vless_uuid: "u1", hy2_pw: "p1" })
      .mockRejectedValueOnce(new Error("agent down"));

    const env = makeEnv({
      users: [{ id: "kulinh", name: "kulinh" }],
      nodes: [
        { id: "JP1", admin_host: "admin-jp1.example.com", status: "active" },
        { id: "SG", admin_host: "admin-sg.example.com", status: "active" }
      ],
      userNodes: []
    });

    const res = await userUpgradeNodes(env, "kulinh", "operator@example.com");
    const body = (await res.json()) as { addedCount: number; failedCount?: number };

    expect(res.status).toBe(207);
    expect(body.addedCount).toBe(1);
    expect(body.failedCount).toBe(1);
    expect(vi.mocked(logEvent).mock.calls[0]?.[3]).toBe("partial");
  });

  it("returns 502 and error outcome when all node upgrades fail", async () => {
    vi.mocked(callAgent).mockRejectedValue(new Error("all down"));

    const env = makeEnv({
      users: [{ id: "kulinh", name: "kulinh" }],
      nodes: [{ id: "HK", admin_host: "admin-hk.example.com", status: "active" }],
      userNodes: []
    });

    const res = await userUpgradeNodes(env, "kulinh", "operator@example.com");
    const body = (await res.json()) as { error?: string; failedCount?: number };

    expect(res.status).toBe(502);
    expect(body.error).toBe("upgrade_failed");
    expect(body.failedCount).toBe(1);
    expect(vi.mocked(logEvent).mock.calls[0]?.[3]).toBe("error");
  });
});
