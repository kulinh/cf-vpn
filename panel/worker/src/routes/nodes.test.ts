import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../lib/agent-client", async (orig) => {
  const actual = await orig<typeof import("../lib/agent-client")>();
  return { ...actual, callAgent: vi.fn() };
});

vi.mock("../lib/events", async (orig) => {
  const actual = await orig<typeof import("../lib/events")>();
  return { ...actual, logEvent: vi.fn().mockResolvedValue(undefined) };
});

vi.mock("../lib/cf-api", async (orig) => {
  const actual = await orig<typeof import("../lib/cf-api")>();
  return {
    ...actual,
    deleteDnsRecordByName: vi.fn().mockResolvedValue(true),
    deleteTunnel: vi.fn().mockResolvedValue(undefined),
    hasCfCredentials: vi.fn().mockReturnValue(true)
  };
});

import { AgentHttpError, callAgent } from "../lib/agent-client";
import { logEvent } from "../lib/events";
import { deleteDnsRecordByName, deleteTunnel, hasCfCredentials } from "../lib/cf-api";
import { deleteNode, getNode, nodeHealthcheck, nodeRotate, nodeSyncCore, patchNode, sweepNodesHealth } from "./nodes";
import type { Env, NodeRow } from "../types";

type ZoneRow = { name: string; cf_zone_id: string; enabled?: number };

function makeEnv(seed: {
  node: NodeRow;
  zones: ZoneRow[];
  failRunSql?: RegExp;
  batches?: unknown[][];
}): Env {
  const node = { ...seed.node };
  const zones = seed.zones.slice();

  const makePrepared = (sql: string): D1PreparedStatement => {
    const state: { args: unknown[] } = { args: [] };

    const stmt: D1PreparedStatement = {
      bind(...args: unknown[]) {
        state.args = args;
        return stmt;
      },
      async first() {
        if (/FROM nodes WHERE id = \?/.test(sql)) {
          return (state.args[0] === node.id ? node : null) as never;
        }
        if (/FROM zones WHERE name = \?/.test(sql)) {
          const name = state.args[0] as string;
          return (zones.find((z) => z.name === name) ?? null) as never;
        }
        return null as never;
      },
      async all() {
        if (/FROM zones WHERE name IN \(/.test(sql)) {
          const names = state.args as string[];
          return { results: zones.filter((z) => names.includes(z.name)) } as never;
        }
        if (/FROM zones WHERE enabled = 1 AND name != \?/.test(sql)) {
          const excluded = state.args[0] as string;
          return { results: zones.filter((z) => z.enabled !== 0 && z.name !== excluded) } as never;
        }
        return { results: [] } as never;
      },
      async run() {
        if (seed.failRunSql?.test(sql)) {
          throw new Error("D1_ERROR: database is locked");
        }
        if (/UPDATE nodes SET vpn_host=\?, hy2_host=\?/.test(sql)) {
          const [vpn_host, hy2_host, hy2_port, hy2_obfs_pw, public_ip, zone] = state.args as [string, string, number, string, string, string];
          node.vpn_host = vpn_host;
          node.hy2_host = hy2_host;
          node.hy2_port = hy2_port;
          node.hy2_obfs_pw = hy2_obfs_pw;
          node.public_ip = public_ip;
          node.zone = zone;
        }
        if (/UPDATE nodes SET last_seen_at=\?, latency_ms=\?/.test(sql)) {
          const [, latency_ms] = state.args as [number, number, string];
          node.latency_ms = latency_ms;
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
    async batch(stmts: unknown[]) {
      seed.batches?.push(stmts);
      return stmts.map(() => ({ success: true })) as never;
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

describe("nodeRotate", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  it("uses the direct agent rotate payload contract", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      vpn_host: "cdn-new.example.net",
      public_ip: "203.0.113.10",
      hy2_host: "hy-new.example.net",
      hy2_port: 23456,
      hy2_obfs_pw: "obfs"
    });

    const env = makeEnv({
      node: {
        id: "sin-01",
        label: "SIN 01",
        admin_host: "sin-01.rwl247.dev",
        vpn_host: "cdn-old.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: "hy-old.example.com",
        hy2_port: 22333,
        hy2_obfs_pw: "old-obfs",
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: [
        { name: "example.com", cf_zone_id: "old-zone", enabled: 1 },
        { name: "example.net", cf_zone_id: "new-zone", enabled: 1 }
      ]
    });

    const res = await nodeRotate(env, "sin-01", new Request("https://panel.test/api/nodes/sin-01/rotate", { method: "POST" }), "operator@example.com");

    expect(res.status).toBe(200);
    const init = vi.mocked(callAgent).mock.calls[0]?.[3] as RequestInit;
    const body = JSON.parse(init.body as string) as Record<string, unknown>;
    expect(body).toMatchObject({
      new_zone_id: "new-zone",
      new_hy2_zone: "example.net",
      old_host: "cdn-old.example.com",
      old_zone_id: "old-zone",
      old_hy2_host: "hy-old.example.com",
      old_hy2_zone_id: "old-zone"
    });
    expect(body.new_host).toEqual(expect.stringMatching(/\.example\.net$/));
    expect(body.new_hy2_host).toEqual(expect.stringMatching(/\.example\.net$/));
    expect(body).not.toHaveProperty("new_vpn_host");
    expect(body).not.toHaveProperty("new_vpn_zone_id");
  });
});

describe("nodeHealthcheck", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  it("stores Worker-to-agent round-trip latency instead of agent loopback latency", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000);
    vi.mocked(callAgent).mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 42));
      return { ok: true, code: 200, latency_ms: 0 };
    });

    const env = makeEnv({
      node: {
        id: "sin-01",
        label: "SIN 01",
        admin_host: "sin-01.rwl247.dev",
        vpn_host: "cdn-old.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "cloudflare",
        hy2_host: "hy-old.example.com",
        hy2_port: 22333,
        hy2_obfs_pw: "old-obfs",
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: []
    });

    const responsePromise = nodeHealthcheck(env, "sin-01", "operator@example.com");
    await vi.advanceTimersByTimeAsync(42);
    const res = await responsePromise;

    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({ ok: true, code: 200, latency_ms: 42 });

    vi.useRealTimers();
  });
});

describe("deleteNode", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(deleteDnsRecordByName).mockReset();
    vi.mocked(deleteDnsRecordByName).mockResolvedValue(true);
    vi.mocked(deleteTunnel).mockReset();
    vi.mocked(deleteTunnel).mockResolvedValue(undefined);
    vi.mocked(hasCfCredentials).mockReset();
    vi.mocked(hasCfCredentials).mockReturnValue(true);
  });

  it("cleans up tunnel + DNS records via CF API before removing the row", async () => {
    vi.mocked(callAgent).mockImplementation(async (_env, _host, path) => {
      if (path === "/admin/v1/status") {
        return {
          xray: "ok",
          cloudflared: "ok",
          hysteria: "ok",
          vpn_host: "edge-old.example.com",
          tunnel_uuid: "f70ff985-a4ef-4643-bbbc-4a0ed4fc8415",
          last_rotate_at: 0
        } as never;
      }
      if (path === "/admin/v1/shutdown-tunnel") {
        return { ok: true } as never;
      }
      throw new Error(`unexpected agent call ${path}`);
    });

    const env = makeEnv({
      node: {
        id: "del-01",
        label: "Del 01",
        admin_host: "del-01.rwl247.dev",
        vpn_host: "edge-old.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: "hy-old.example.net",
        hy2_port: 22333,
        hy2_obfs_pw: "obfs",
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: [
        { name: "example.com", cf_zone_id: "zone-com", enabled: 1 },
        { name: "example.net", cf_zone_id: "zone-net", enabled: 1 },
        { name: "rwl247.dev", cf_zone_id: "zone-admin", enabled: 1 }
      ]
    });

    const res = await deleteNode(env, "del-01");

    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok: boolean; warnings: string[] };
    expect(body.ok).toBe(true);
    expect(body.warnings).toEqual([]);

    expect(vi.mocked(deleteDnsRecordByName)).toHaveBeenCalledWith(env, "zone-com", "edge-old.example.com", "A");
    expect(vi.mocked(deleteDnsRecordByName)).toHaveBeenCalledWith(env, "zone-net", "hy-old.example.net", "A");
    expect(vi.mocked(deleteDnsRecordByName)).toHaveBeenCalledWith(env, "zone-admin", "del-01.rwl247.dev", "CNAME");
    expect(vi.mocked(deleteTunnel)).toHaveBeenCalledWith(env, "f70ff985-a4ef-4643-bbbc-4a0ed4fc8415");
  });

  it("ignores a path-traversal tunnel_uuid reported by a compromised agent", async () => {
    vi.mocked(callAgent).mockImplementation(async (_env, _host, path) => {
      if (path === "/admin/v1/status") {
        return {
          xray: "ok",
          cloudflared: "ok",
          hysteria: "ok",
          vpn_host: "edge-old.example.com",
          tunnel_uuid: "../../../zones/VICTIMZONE",
          last_rotate_at: 0
        } as never;
      }
      return { ok: true } as never;
    });

    const env = makeEnv({
      node: {
        id: "del-05",
        label: "Del 05",
        admin_host: "del-05.rwl247.dev",
        vpn_host: "edge-old.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: [
        { name: "example.com", cf_zone_id: "zone-com", enabled: 1 },
        { name: "rwl247.dev", cf_zone_id: "zone-admin", enabled: 1 }
      ]
    });

    const res = await deleteNode(env, "del-05");

    expect(res.status).toBe(200);
    const body = (await res.json()) as { warnings: string[] };
    expect(vi.mocked(deleteTunnel)).not.toHaveBeenCalled();
    expect(body.warnings.some((w) => /malformed tunnel_uuid/i.test(w))).toBe(true);
  });

  it("still deletes the row when agent is unreachable, surfacing a warning", async () => {
    vi.mocked(callAgent).mockRejectedValue(new Error("agent down"));

    const env = makeEnv({
      node: {
        id: "del-02",
        label: "Del 02",
        admin_host: "del-02.rwl247.dev",
        vpn_host: "edge.example.com",
        zone: "example.com",
        status: "down",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: [
        { name: "example.com", cf_zone_id: "zone-com", enabled: 1 },
        { name: "rwl247.dev", cf_zone_id: "zone-admin", enabled: 1 }
      ]
    });

    const res = await deleteNode(env, "del-02");

    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok: boolean; warnings: string[] };
    expect(body.ok).toBe(true);
    expect(body.warnings.some((w) => /Agent unreachable/i.test(w))).toBe(true);
    expect(vi.mocked(deleteTunnel)).not.toHaveBeenCalled();
    expect(vi.mocked(deleteDnsRecordByName)).toHaveBeenCalledWith(env, "zone-com", "edge.example.com", "A");
    expect(vi.mocked(deleteDnsRecordByName)).toHaveBeenCalledWith(env, "zone-admin", "del-02.rwl247.dev", "CNAME");
  });

  it("falls back to the persisted tunnel_uuid to delete the tunnel when the agent is unreachable", async () => {
    vi.mocked(callAgent).mockRejectedValue(new Error("agent down"));

    const env = makeEnv({
      node: {
        id: "del-04",
        label: "Del 04",
        admin_host: "del-04.rwl247.dev",
        vpn_host: "edge.example.com",
        zone: "example.com",
        status: "down",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: "persisted-tunnel-xyz"
      },
      zones: [
        { name: "example.com", cf_zone_id: "zone-com", enabled: 1 },
        { name: "rwl247.dev", cf_zone_id: "zone-admin", enabled: 1 }
      ]
    });

    const res = await deleteNode(env, "del-04");

    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok: boolean; warnings: string[] };
    expect(vi.mocked(deleteTunnel)).toHaveBeenCalledWith(env, "persisted-tunnel-xyz");
    expect(body.warnings.some((w) => /persisted tunnel_uuid/i.test(w))).toBe(true);
  });

  it("skips CF cleanup with a warning when credentials are missing", async () => {
    vi.mocked(hasCfCredentials).mockReturnValue(false);

    const env = makeEnv({
      node: {
        id: "del-03",
        label: "Del 03",
        admin_host: "del-03.rwl247.dev",
        vpn_host: "edge.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: []
    });

    const res = await deleteNode(env, "del-03");

    expect(res.status).toBe(200);
    const body = (await res.json()) as { ok: boolean; warnings: string[] };
    expect(body.warnings).toEqual([expect.stringMatching(/CF cleanup skipped/i)]);
    expect(vi.mocked(callAgent)).not.toHaveBeenCalled();
    expect(vi.mocked(deleteDnsRecordByName)).not.toHaveBeenCalled();
    expect(vi.mocked(deleteTunnel)).not.toHaveBeenCalled();
  });

  it("returns 404 when node does not exist", async () => {
    const env = makeEnv({
      node: {
        id: "exists",
        label: "x",
        admin_host: "x.rwl247.dev",
        vpn_host: "x.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: []
    });

    const res = await deleteNode(env, "missing");
    expect(res.status).toBe(404);
  });
});

describe("nodeSyncCore", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  it("does not log the hy2_obfs_pw secret into the node.sync ok event", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      ok: true,
      vpn_host: "edge.example.com",
      public_ip: "203.0.113.5",
      hy2_host: "hy.example.net",
      hy2_port: 23456,
      hy2_obfs_pw: "SUPER_SECRET_OBFS",
      users: 1,
      mode: "direct"
    } as never);

    const env = makeEnv({
      node: {
        id: "sync-01",
        label: "Sync 01",
        admin_host: "sync-01.rwl247.dev",
        vpn_host: "edge.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: [{ name: "example.com", cf_zone_id: "zone-com", enabled: 1 }]
    });

    const res = await nodeSyncCore(env, "sync-01", [{ name: "alice", vless_uuid: "uuid", hy2_pw: "pw" }], "tg:1");
    expect(res.status).toBe(200);

    const okCall = vi.mocked(logEvent).mock.calls.find((c) => c[2] === "node.sync" && c[3] === "ok");
    expect(okCall).toBeDefined();
    const detail = JSON.stringify(okCall?.[4]);
    expect(detail).not.toContain("SUPER_SECRET_OBFS");
    expect(detail).not.toContain("hy2_obfs_pw");
    expect(detail).toContain("edge.example.com");
  });
});

describe("sweepNodesHealth", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockClear();
  });

  type SweepNode = {
    id: string;
    admin_host: string;
    agent_secret: string;
    status: string;
    consecutive_failures?: number;
  };
  type Write = { sql: string; args: unknown[] };

  // The sweep now folds every per-node UPDATE and event INSERT into one
  // env.DB.batch(), so the stub records what the batch received.
  function makeSweepEnv(nodes: SweepNode[]): { env: Env; writes: Write[]; batches: number } {
    const writes: Write[] = [];
    const state = { batches: 0 };
    const db = {
      prepare(sql: string) {
        const bound: { args: unknown[] } = { args: [] };
        const stmt = {
          sql,
          get args() {
            return bound.args;
          },
          bind(...args: unknown[]) {
            bound.args = args;
            return stmt;
          },
          async all() {
            return { results: nodes.map((n) => ({ consecutive_failures: 0, ...n })) };
          },
          async run() {
            writes.push({ sql, args: bound.args });
            return { success: true };
          }
        };
        return stmt;
      },
      async batch(stmts: Array<{ sql: string; args: unknown[] }>) {
        state.batches += 1;
        for (const st of stmts) writes.push({ sql: st.sql, args: st.args });
        return [];
      }
    };
    const env = { DB: db } as unknown as Env;
    return {
      env,
      writes,
      get batches() {
        return state.batches;
      }
    };
  }

  const eventInserts = (writes: Write[]) => writes.filter((w) => /INSERT INTO events/.test(w.sql));
  const eventAction = (w: Write) => w.args[2];
  const eventDetail = (w: Write) => String(w.args[6]);

  it("refreshes last_seen, latency and resets the failure counter for reachable nodes", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1 }
    ]);
    vi.mocked(callAgent).mockResolvedValue({ ok: true });

    await sweepNodesHealth(env.env);

    expect(env.writes).toHaveLength(1);
    expect(env.writes[0].sql).toContain("status='active', last_seen_at=?, latency_ms=?, consecutive_failures=0");
    expect(env.writes[0].args[2]).toBe("a");
    expect(eventInserts(env.writes)).toHaveLength(0);
  });

  it("uses a single batch for the whole fleet", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active" },
      { id: "b", admin_host: "b.example.com", agent_secret: "s", status: "active" },
      { id: "c", admin_host: "c.example.com", agent_secret: "s", status: "active" }
    ]);
    vi.mocked(callAgent).mockResolvedValue({ ok: true });

    await sweepNodesHealth(env.env);

    expect(env.batches).toBe(1);
    expect(env.writes).toHaveLength(3);
  });

  it("logs a recover event when a previously unreachable node answers", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "unreachable", consecutive_failures: 4 }
    ]);
    vi.mocked(callAgent).mockResolvedValue({ ok: true });

    await sweepNodesHealth(env.env);

    const events = eventInserts(env.writes);
    expect(events).toHaveLength(1);
    expect(eventAction(events[0])).toBe("node.healthcheck.recover");
    expect(events[0].args[1]).toBe("cron");
  });

  it("does not flip status on the first transport failure — only counts it", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 0 }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new Error("AbortError: The operation was aborted"));

    await sweepNodesHealth(env.env);

    expect(env.writes).toHaveLength(1);
    expect(env.writes[0].sql).toBe("UPDATE nodes SET consecutive_failures=? WHERE id=?");
    expect(env.writes[0].args).toEqual([1, "a"]);
    expect(eventInserts(env.writes)).toHaveLength(0);
  });

  it("marks a node unreachable on the second consecutive transport failure and logs once", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1 }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new Error("fetch failed"));

    await sweepNodesHealth(env.env);

    const update = env.writes.find((w) => /UPDATE nodes/.test(w.sql))!;
    expect(update.sql).toContain("status='unreachable', consecutive_failures=?");
    expect(update.args).toEqual([2, "a"]);
    const events = eventInserts(env.writes);
    expect(events).toHaveLength(1);
    expect(eventAction(events[0])).toBe("node.healthcheck");
  });

  it("does not re-mark or re-log an already-unreachable node", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "unreachable", consecutive_failures: 5 }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new Error("fetch failed"));

    await sweepNodesHealth(env.env);

    expect(env.writes).toHaveLength(1);
    expect(env.writes[0].sql).toBe("UPDATE nodes SET consecutive_failures=? WHERE id=?");
    expect(eventInserts(env.writes)).toHaveLength(0);
  });

  it("leaves status alone on a 4xx config error but logs it once", async () => {
    const first = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 0 }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new AgentHttpError(403, "agent_http_403: forbidden"));

    await sweepNodesHealth(first.env);

    expect(first.writes.some((w) => /status=/.test(w.sql))).toBe(false);
    const events = eventInserts(first.writes);
    expect(events).toHaveLength(1);
    expect(eventDetail(events[0])).toContain("config_error");

    // Second consecutive config failure: counted, but silent.
    const second = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1 }
    ]);
    await sweepNodesHealth(second.env);

    expect(second.writes.some((w) => /status=/.test(w.sql))).toBe(false);
    expect(eventInserts(second.writes)).toHaveLength(0);
  });

  it("classifies a 4xx carrying a JSON body as a config error (H13 regression)", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1 }
    ]);
    // The agent answers 401 with {"error":"unauthorized"} — the old regex on the
    // message text never saw a status and flipped the node to unreachable.
    vi.mocked(callAgent).mockRejectedValue(new AgentHttpError(401, "agent_http_401: unauthorized"));

    await sweepNodesHealth(env.env);

    expect(env.writes.some((w) => /status='unreachable'/.test(w.sql))).toBe(false);
  });

  it("reports a rejected per-node task instead of discarding it", async () => {
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active" }
    ]);
    vi.mocked(callAgent).mockImplementation(() => {
      throw { toString: () => { throw new Error("boom"); } };
    });

    await sweepNodesHealth(env.env);

    expect(errorSpy).toHaveBeenCalledWith("sweep failed for node", "a", expect.any(String));
    errorSpy.mockRestore();
  });
});

describe("nodeRotate persistence split (M-W6)", () => {
  const rotateNode: NodeRow = {
    id: "rot-01",
    label: "Rot 01",
    admin_host: "rot-01.rwl247.dev",
    vpn_host: "cdn-old.example.com",
    zone: "example.com",
    status: "active",
    last_seen_at: null,
    latency_ms: null,
    created_at: 1,
    public_ip: null,
    mode: "direct",
    hy2_host: "hy-old.example.com",
    hy2_port: 22333,
    hy2_obfs_pw: "old-obfs",
    reality_pubkey: null,
    reality_sid: null,
    reality_sni: null,
    reality_dest: null,
    xhttp_path: null,
    agent_secret: null,
    tunnel_uuid: null
  };
  const zones: ZoneRow[] = [
    { name: "example.com", cf_zone_id: "old-zone", enabled: 1 },
    { name: "example.net", cf_zone_id: "new-zone", enabled: 1 }
  ];

  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  it("returns rotate_persist_failed (not rotate_failed) and logs the new host when the DB write fails", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      vpn_host: "cdn-new.example.net",
      public_ip: "203.0.113.10",
      hy2_host: "hy-new.example.net",
      hy2_port: 23456,
      hy2_obfs_pw: "obfs"
    });
    const env = makeEnv({ node: rotateNode, zones, failRunSql: /UPDATE nodes SET vpn_host=/ });

    const res = await nodeRotate(
      env,
      "rot-01",
      new Request("https://panel.test/api/nodes/rot-01/rotate", { method: "POST" }),
      "operator@example.com"
    );

    expect(res.status).toBe(500);
    const body = (await res.json()) as { error: string; vpn_host: string };
    // Distinct from rotate_failed: the node HAS moved, so a retry would rotate
    // it a second time and orphan every subscription already handed out.
    expect(body.error).toBe("rotate_persist_failed");
    expect(body.vpn_host).toBe("cdn-new.example.net");

    const ev = vi.mocked(logEvent).mock.calls.find((c) => c[2] === "node.rotate");
    expect(ev?.[3]).toBe("partial");
    expect(JSON.stringify(ev?.[4])).toContain("cdn-new.example.net");
  });

  it("keeps the stored hy2 runtime when the agent reports hy2_port=0", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      vpn_host: "cdn-new.example.net",
      public_ip: "203.0.113.10",
      hy2_host: "hy-new.example.net",
      hy2_port: 0,
      hy2_obfs_pw: ""
    });
    const env = makeEnv({ node: rotateNode, zones });

    const res = await nodeRotate(
      env,
      "rot-01",
      new Request("https://panel.test/api/nodes/rot-01/rotate", { method: "POST" }),
      "operator@example.com"
    );

    expect(res.status).toBe(200);
    // Persisting 0 / "" makes buildSubscriptionURIs drop HY2 for this node
    // entirely (falsy check), so rotate must merge like every other write path.
    const written = await getNode(env, "rot-01");
    const row = (await written.json()) as { hy2_port: number };
    expect(row.hy2_port).toBe(22333);
  });
});

describe("patchNode status whitelist", () => {
  const patchNodeRow: NodeRow = {
    id: "p-01",
    label: "P 01",
    admin_host: "p-01.rwl247.dev",
    vpn_host: "p.example.com",
    zone: "example.com",
    status: "active",
    last_seen_at: null,
    latency_ms: null,
    created_at: 1,
    public_ip: null,
    mode: "direct",
    hy2_host: null,
    hy2_port: null,
    hy2_obfs_pw: null,
    reality_pubkey: null,
    reality_sid: null,
    reality_sni: null,
    reality_dest: null,
    xhttp_path: null,
    agent_secret: null,
    tunnel_uuid: null
  };

  const patch = (env: Env, body: unknown) =>
    patchNode(env, "p-01", new Request("https://panel.test/api/nodes/p-01", {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body)
    }), "operator@example.com");

  it("rejects an unknown status instead of silently parking the node", async () => {
    const env = makeEnv({ node: patchNodeRow, zones: [] });
    const res = await patch(env, { status: "activ" });
    expect(res.status).toBe(400);
    expect((await res.json() as { error: string }).error).toBe("invalid_status");
  });

  it("accepts each allowed status", async () => {
    for (const status of ["active", "disabled", "unreachable"]) {
      const env = makeEnv({ node: patchNodeRow, zones: [] });
      const res = await patch(env, { status });
      expect(res.status).toBe(200);
    }
  });
});

describe("deleteNode row removal", () => {
  it("removes membership and node rows in one batch", async () => {
    vi.mocked(hasCfCredentials).mockReturnValue(false);
    const batches: unknown[][] = [];
    const env = makeEnv({
      node: {
        id: "batch-01",
        label: "B",
        admin_host: "b.rwl247.dev",
        vpn_host: "b.example.com",
        zone: "example.com",
        status: "active",
        last_seen_at: null,
        latency_ms: null,
        created_at: 1,
        public_ip: null,
        mode: "direct",
        hy2_host: null,
        hy2_port: null,
        hy2_obfs_pw: null,
        reality_pubkey: null,
        reality_sid: null,
        reality_sni: null,
        reality_dest: null,
        xhttp_path: null,
        agent_secret: null,
        tunnel_uuid: null
      },
      zones: [],
      batches
    });

    const res = await deleteNode(env, "batch-01");

    expect(res.status).toBe(200);
    expect(batches).toHaveLength(1);
    expect(batches[0]).toHaveLength(2);
  });
});
