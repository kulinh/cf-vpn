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
import { createNode, deleteNode, getNode, nodeHealthcheck, nodeRotate, nodeStatus, nodeSyncCore, patchNode, sweepNodesHealth } from "./nodes";
import type { Env, NodeRow } from "../types";

type ZoneRow = { name: string; cf_zone_id: string; enabled?: number };

type RunWrite = { sql: string; args: unknown[] };

// Column order of the persistNodeRuntime UPDATE in routes/nodes.ts — the test
// reads its bound args by name instead of by index.
const PERSIST_COLUMNS = [
  "vpn_host",
  "zone",
  "public_ip",
  "mode",
  "hy2_host",
  "hy2_port",
  "hy2_obfs_pw",
  "last_seen_at",
  "latency_ms",
  "reality_pubkey",
  "reality_sid",
  "reality_sni",
  "reality_dest",
  "xhttp_path",
  "xhttp_enabled",
  "xhttp_direct_host",
  "xhttp_direct_path",
  "xhttp_h3_host",
  "xhttp_h3_path",
  "naive_host",
  "naive_user",
  "naive_pass",
  "tunnel_uuid",
  "id"
] as const;

function persistedRuntime(writes: RunWrite[]): Record<string, unknown> {
  const w = writes.find((x) => /UPDATE nodes SET status='active'/.test(x.sql));
  if (!w) throw new Error("no runtime UPDATE was issued");
  return Object.fromEntries(PERSIST_COLUMNS.map((c, i) => [c, w.args[i]]));
}

function makeEnv(seed: {
  node: NodeRow;
  zones: ZoneRow[];
  failRunSql?: RegExp;
  batches?: unknown[][];
  writes?: RunWrite[];
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
        if (/FROM zones WHERE enabled = 1$/.test(sql)) {
          return { results: zones.filter((z) => z.enabled !== 0) } as never;
        }
        if (/FROM zones WHERE enabled = 1 AND name != \?/.test(sql)) {
          const excluded = state.args[0] as string;
          return { results: zones.filter((z) => z.enabled !== 0 && z.name !== excluded) } as never;
        }
        return { results: [] } as never;
      },
      async run() {
        seed.writes?.push({ sql, args: state.args.slice() });
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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
    last_sweep_outcome?: string | null;
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
            // Emulate the SELECT's own filter so a disabled node is genuinely
            // never handed to the sweep.
            const visible = /status != 'disabled'/.test(sql)
              ? nodes.filter((n) => n.status !== "disabled")
              : nodes;
            return { results: visible.map((n) => ({ consecutive_failures: 0, last_sweep_outcome: null, ...n })) };
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
    expect(env.writes[0].sql).toContain("status='active', last_seen_at=?, latency_ms=?, consecutive_failures=0, last_sweep_outcome='ok'");
    expect(env.writes[0].sql).toContain("AND status != 'disabled'");
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
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "unreachable", consecutive_failures: 4, last_sweep_outcome: "transport" }
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
    expect(env.writes[0].sql).toBe(
      "UPDATE nodes SET consecutive_failures=COALESCE(consecutive_failures, 0) + 1, last_sweep_outcome='transport' WHERE id=? AND status != 'disabled'"
    );
    // Incremented in SQL so overlapping sweeps cannot lose a failure (review L6).
    expect(env.writes[0].args).toEqual(["a"]);
    expect(eventInserts(env.writes)).toHaveLength(0);
  });

  it("marks a node unreachable on the second consecutive transport failure and logs once", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1, last_sweep_outcome: "transport" }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new Error("fetch failed"));

    await sweepNodesHealth(env.env);

    const update = env.writes.find((w) => /UPDATE nodes/.test(w.sql))!;
    expect(update.sql).toContain("status='unreachable', consecutive_failures=COALESCE(consecutive_failures, 0) + 1, last_sweep_outcome='transport'");
    expect(update.args).toEqual(["a"]);
    const events = eventInserts(env.writes);
    expect(events).toHaveLength(1);
    expect(eventAction(events[0])).toBe("node.healthcheck");
  });

  it("does not re-mark or re-log an already-unreachable node", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "unreachable", consecutive_failures: 5, last_sweep_outcome: "transport" }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new Error("fetch failed"));

    await sweepNodesHealth(env.env);

    expect(env.writes).toHaveLength(1);
    expect(env.writes[0].sql).toBe(
      "UPDATE nodes SET consecutive_failures=COALESCE(consecutive_failures, 0) + 1, last_sweep_outcome='transport' WHERE id=? AND status != 'disabled'"
    );
    expect(eventInserts(env.writes)).toHaveLength(0);
  });

  it("leaves status alone on a 4xx config error and logs on the outcome change", async () => {
    const first = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 0, last_sweep_outcome: "ok" }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new AgentHttpError(403, "agent_http_403: forbidden"));

    await sweepNodesHealth(first.env);

    expect(first.writes.some((w) => /status=/.test(w.sql))).toBe(false);
    const events = eventInserts(first.writes);
    expect(events).toHaveLength(1);
    expect(eventDetail(events[0])).toContain("config_error");

    // Still a config error next tick: counted, but silent.
    const second = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1, last_sweep_outcome: "config" }
    ]);
    await sweepNodesHealth(second.env);

    expect(second.writes.some((w) => /status=/.test(w.sql))).toBe(false);
    expect(eventInserts(second.writes)).toHaveLength(0);
  });

  it("logs the config error after a preceding transport miss (counter would have been 1)", async () => {
    // Regression: keying "log once" on consecutive_failures === 1 logged NOTHING
    // here, because the transport miss on the previous tick already set it to 1.
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1, last_sweep_outcome: "transport" }
    ]);
    vi.mocked(callAgent).mockRejectedValue(new AgentHttpError(401, "agent_http_401: unauthorized"));

    await sweepNodesHealth(env.env);

    const events = eventInserts(env.writes);
    expect(events).toHaveLength(1);
    expect(eventDetail(events[0])).toContain("config_error");
    expect(env.writes.some((w) => /status=/.test(w.sql))).toBe(false);
  });

  it("logs a recover when a config error clears, and stays silent after an unannounced transport miss", async () => {
    vi.mocked(callAgent).mockResolvedValue({ ok: true });

    const afterConfig = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 3, last_sweep_outcome: "config" }
    ]);
    await sweepNodesHealth(afterConfig.env);
    const recovered = eventInserts(afterConfig.writes);
    expect(recovered).toHaveLength(1);
    expect(eventAction(recovered[0])).toBe("node.healthcheck.recover");

    // One transport miss that never reached the flip threshold was never
    // announced, so its recovery must not be announced either.
    const afterQuietMiss = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1, last_sweep_outcome: "transport" }
    ]);
    await sweepNodesHealth(afterQuietMiss.env);
    expect(eventInserts(afterQuietMiss.writes)).toHaveLength(0);
  });

  it("never probes or rewrites a disabled node", async () => {
    const disabled: SweepNode = {
      id: "off",
      admin_host: "off.example.com",
      agent_secret: "s",
      status: "disabled",
      consecutive_failures: 0,
      last_sweep_outcome: "ok"
    };

    // Successful sweep.
    vi.mocked(callAgent).mockResolvedValue({ ok: true });
    const onSuccess = makeSweepEnv([disabled]);
    await sweepNodesHealth(onSuccess.env);
    expect(vi.mocked(callAgent)).not.toHaveBeenCalled();
    expect(onSuccess.writes).toHaveLength(0);

    // Failing sweep.
    vi.mocked(callAgent).mockRejectedValue(new Error("fetch failed"));
    const onFailure = makeSweepEnv([{ ...disabled, consecutive_failures: 5 }]);
    await sweepNodesHealth(onFailure.env);
    expect(onFailure.writes).toHaveLength(0);

    // And every status write the sweep can emit carries the guard, so a node
    // disabled mid-sweep is still not overwritten.
    vi.mocked(callAgent).mockReset();
    vi.mocked(callAgent).mockResolvedValue({ ok: true });
    const active = makeSweepEnv([{ ...disabled, id: "on", status: "active" }]);
    await sweepNodesHealth(active.env);
    expect(active.writes.every((w) => !/status=/.test(w.sql) || /AND status != 'disabled'/.test(w.sql))).toBe(true);
  });

  it("classifies a 4xx carrying a JSON body as a config error (H13 regression)", async () => {
    const env = makeSweepEnv([
      { id: "a", admin_host: "a.example.com", agent_secret: "s", status: "active", consecutive_failures: 1, last_sweep_outcome: "ok" }
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
    xhttp_enabled: 0,
    xhttp_direct_host: null,
    xhttp_direct_path: null,
    xhttp_h3_host: null,
    xhttp_h3_path: null,
    naive_host: null,
    naive_user: null,
    naive_pass: null,
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

  it("returns rotate_unknown_state (not rotate_failed) when the rotate call times out", async () => {
    const abort = new Error("The operation was aborted");
    abort.name = "AbortError";
    vi.mocked(callAgent).mockRejectedValue(abort);
    const env = makeEnv({ node: rotateNode, zones });

    const res = await nodeRotate(
      env,
      "rot-01",
      new Request("https://panel.test/api/nodes/rot-01/rotate", { method: "POST" }),
      "operator@example.com"
    );

    // 55s can expire while the agent completes the rotation; a retryable
    // rotate_failed here means a second rotation and orphaned subscriptions.
    expect(res.status).toBe(500);
    const body = (await res.json()) as { error: string; attempted_host: string; detail: string };
    expect(body.error).toBe("rotate_unknown_state");
    expect(body.attempted_host).toEqual(expect.stringMatching(/\.example\.net$/));
    expect(body.detail).toContain("do NOT retry");

    const ev = vi.mocked(logEvent).mock.calls.find((c) => c[2] === "node.rotate");
    expect(ev?.[3]).toBe("partial");
    expect(JSON.stringify(ev?.[4])).toContain("attempted_host");
  });

  it("still returns rotate_failed for a non-timeout agent error", async () => {
    vi.mocked(callAgent).mockRejectedValue(new Error("connection refused"));
    const env = makeEnv({ node: rotateNode, zones });

    const res = await nodeRotate(
      env,
      "rot-01",
      new Request("https://panel.test/api/nodes/rot-01/rotate", { method: "POST" }),
      "operator@example.com"
    );

    expect(res.status).toBe(502);
    expect((await res.json() as { error: string }).error).toBe("rotate_failed");
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
    xhttp_enabled: 0,
    xhttp_direct_host: null,
    xhttp_direct_path: null,
    xhttp_h3_host: null,
    xhttp_h3_path: null,
    naive_host: null,
    naive_user: null,
    naive_pass: null,
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
        xhttp_enabled: 0,
        xhttp_direct_host: null,
        xhttp_direct_path: null,
        xhttp_h3_host: null,
        xhttp_h3_path: null,
        naive_host: null,
        naive_user: null,
        naive_pass: null,
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

// ---------------------------------------------------------------------------
// Code-review fixes (2026-09): node id whitelist, real-zone resolution, XHTTP
// persistence, the agent's confirm_empty guard and the PATCH `host` alias.
// ---------------------------------------------------------------------------

const reviewRow: NodeRow = {
  id: "JPY-03",
  label: "JPY 03",
  admin_host: "jpy-03.rwl247.dev",
  vpn_host: "edge.rwl247.dev",
  zone: "rwl247.dev",
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
  xhttp_enabled: 0,
  xhttp_direct_host: null,
  xhttp_direct_path: null,
  xhttp_h3_host: null,
  xhttp_h3_path: null,
  naive_host: null,
  naive_user: null,
  naive_pass: null,
  agent_secret: null,
  tunnel_uuid: null
};

describe("createNode node id whitelist", () => {
  beforeEach(() => {
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  const freshEnv = () =>
    makeEnv({
      node: { ...reviewRow, id: "SOMETHING-ELSE" },
      zones: [{ name: "rwl247.dev", cf_zone_id: "zone-rwl", enabled: 1 }]
    });

  const post = (env: Env, body: unknown) =>
    createNode(
      env,
      new Request("https://panel.test/api/nodes", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(body)
      }),
      "operator@example.com"
    );

  it("rejects ids that would corrupt the Shadowrocket AUTO/PROXY lines", async () => {
    // A comma or newline here lands verbatim in every user's .conf.
    for (const id of ["JPY-03,VNM-01", "JPY\n03", "JPY 03", "jpy_03", "x".repeat(52)]) {
      const res = await post(freshEnv(), { id, label: "X" });
      expect(res.status).toBe(400);
      expect((await res.json() as { error: string }).error).toBe("invalid_node_id");
    }
  });

  it("accepts the uppercase ids D1 actually holds", async () => {
    for (const id of ["JPY-03", "OR-001", "VNM-01", "x".repeat(51)]) {
      const res = await post(freshEnv(), { id, label: id });
      expect(res.status).toBe(201);
    }
  });
});

describe("zone resolution for a 3-label host", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  it("writes the real zone, not a fixed label count", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      ok: true,
      vpn_host: "foo.bar.rwl247.dev",
      public_ip: "203.0.113.9",
      hy2_host: "",
      users: 1,
      mode: "direct"
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({
      node: { ...reviewRow, zone: "bar.rwl247.dev" },
      // Both the apex and the delegated 3-label zone are in the zones table, so
      // only a longest-suffix match picks the one that owns the host.
      zones: [
        { name: "rwl247.dev", cf_zone_id: "zone-apex", enabled: 1 },
        { name: "bar.rwl247.dev", cf_zone_id: "zone-sub", enabled: 1 }
      ],
      writes
    });

    const res = await nodeSyncCore(env, "JPY-03", [{ name: "alice", vless_uuid: "u", hy2_pw: "p" }], "tg:1");

    expect(res.status).toBe(200);
    const persisted = persistedRuntime(writes);
    expect(persisted.vpn_host).toBe("foo.bar.rwl247.dev");
    // A fixed split(".").slice(-2) stored "rwl247.dev" here — the wrong zone id
    // for every later rotate / DNS cleanup and for the zone_in_use check.
    expect(persisted.zone).toBe("bar.rwl247.dev");
  });

  it("keeps the stored zone when the host belongs to no known zone", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      ok: true,
      vpn_host: "edge.unknown-zone.test",
      public_ip: "203.0.113.9",
      hy2_host: "",
      users: 1,
      mode: "direct"
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({
      node: { ...reviewRow, zone: "rwl247.dev" },
      zones: [{ name: "rwl247.dev", cf_zone_id: "zone-rwl", enabled: 1 }],
      writes
    });

    await nodeSyncCore(env, "JPY-03", [{ name: "alice", vless_uuid: "u", hy2_pw: "p" }], "tg:1");

    expect(persistedRuntime(writes).zone).toBe("rwl247.dev");
  });
});

describe("nodeSyncCore confirm_empty guard", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  const agentOk = () =>
    vi.mocked(callAgent).mockResolvedValue({
      ok: true,
      vpn_host: "edge.rwl247.dev",
      public_ip: "203.0.113.9",
      hy2_host: "",
      users: 0,
      mode: "direct"
    } as never);

  const syncBody = () => JSON.parse((vi.mocked(callAgent).mock.calls[0]?.[3] as RequestInit).body as string);

  it("sends confirm_empty only when the user list is genuinely empty", async () => {
    agentOk();
    const env = makeEnv({ node: reviewRow, zones: [{ name: "rwl247.dev", cf_zone_id: "z", enabled: 1 }] });

    await nodeSyncCore(env, "JPY-03", [], "tg:1");

    expect(syncBody()).toEqual({ users: [], confirm_empty: true });
  });

  it("omits confirm_empty when there are users to push", async () => {
    agentOk();
    const env = makeEnv({ node: reviewRow, zones: [{ name: "rwl247.dev", cf_zone_id: "z", enabled: 1 }] });

    await nodeSyncCore(env, "JPY-03", [{ name: "alice", vless_uuid: "u", hy2_pw: "p" }], "tg:1");

    const body = syncBody();
    expect(body.users).toHaveLength(1);
    expect(body.confirm_empty).toBeUndefined();
  });
});

describe("XHTTP runtime persistence", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  const cfRow: NodeRow = {
    ...reviewRow,
    mode: "cloudflare",
    xhttp_path: "/old-path",
    xhttp_enabled: 1,
    xhttp_direct_host: "direct.rwl247.dev",
    xhttp_direct_path: "/old-direct"
  };

  it("clears xhttp_enabled when the agent reports XHTTP off", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      xray: "active",
      cloudflared: "active",
      hysteria: "inactive",
      vpn_host: "edge.rwl247.dev",
      mode: "cloudflare",
      tunnel_uuid: "",
      last_rotate_at: 0,
      xhttp_path: "/new-path",
      xhttp_enabled: false,
      // Go omitempty drops these when unset, so the row must be cleared by the
      // enabled flag, not by an explicit "".
      xhttp_direct_host: "",
      xhttp_direct_path: ""
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: cfRow, zones: [], writes });

    const res = await nodeStatus(env, "JPY-03", "operator@example.com");

    expect(res.status).toBe(200);
    const persisted = persistedRuntime(writes);
    expect(persisted.xhttp_path).toBe("/new-path");
    // Stale 1 here is the D1 drift the review flagged.
    expect(persisted.xhttp_enabled).toBe(0);
    expect(persisted.xhttp_direct_host).toBeNull();
    expect(persisted.xhttp_direct_path).toBeNull();
  });

  it("persists the direct-route pair the agent reports", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      xray: "active",
      cloudflared: "active",
      hysteria: "inactive",
      vpn_host: "edge.rwl247.dev",
      mode: "cloudflare",
      tunnel_uuid: "",
      last_rotate_at: 0,
      xhttp_path: "/p",
      xhttp_enabled: true,
      xhttp_direct_host: "new-direct.rwl247.dev",
      xhttp_direct_path: "/new-direct"
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: { ...cfRow, xhttp_enabled: 0 }, zones: [], writes });

    await nodeStatus(env, "JPY-03", "operator@example.com");

    const persisted = persistedRuntime(writes);
    expect(persisted.xhttp_enabled).toBe(1);
    expect(persisted.xhttp_direct_host).toBe("new-direct.rwl247.dev");
    expect(persisted.xhttp_direct_path).toBe("/new-direct");
  });
});

describe("patchNode host alias", () => {
  beforeEach(() => {
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  const patchedVpnHost = (writes: RunWrite[]): unknown => {
    const w = writes.find((x) => /UPDATE nodes SET label=\?, admin_host=\?, vpn_host=\?/.test(x.sql));
    if (!w) throw new Error("no node UPDATE was issued");
    return w.args[2];
  };

  const patch = (env: Env, body: unknown) =>
    patchNode(env, "JPY-03", new Request("https://panel.test/api/nodes/JPY-03", {
      method: "PATCH",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body)
    }), "operator@example.com");

  it("honours the documented `host` alias instead of dropping it", async () => {
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: reviewRow, zones: [], writes });

    const res = await patch(env, { host: "new.rwl247.dev" });

    expect(res.status).toBe(200);
    expect(patchedVpnHost(writes)).toBe("new.rwl247.dev");
  });

  it("lets `host` win over vpn_host, as createNode does", async () => {
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: reviewRow, zones: [], writes });

    await patch(env, { host: "alias.rwl247.dev", vpn_host: "canonical.rwl247.dev" });

    expect(patchedVpnHost(writes)).toBe("alias.rwl247.dev");
  });

  it("400s on an empty `host` rather than writing the old value back", async () => {
    const env = makeEnv({ node: reviewRow, zones: [] });
    const res = await patch(env, { host: "   " });
    expect(res.status).toBe(400);
    expect((await res.json() as { error: string }).error).toBe("invalid_node");
  });
});

// Reads a persisted column by name, deriving the bind index from the SQL's
// own SET clause rather than a positional constant, so this block cannot be
// silently invalidated by a column added elsewhere in the statement.
function persistedByName(writes: RunWrite[], column: string): unknown {
  const w = writes.find((x) => /UPDATE nodes SET status='active'/.test(x.sql));
  if (!w) throw new Error("no runtime UPDATE was issued");
  const set = w.sql.slice(w.sql.indexOf("SET ") + 4, w.sql.indexOf(" WHERE "));
  const cols = set.split(",").map((c) => c.trim().split("=")[0].trim());
  // status='active' is a literal, not a bind, so it consumes no argument.
  const binds = cols.filter((c) => c !== "status");
  const i = binds.indexOf(column);
  if (i < 0) throw new Error(`the runtime UPDATE does not persist ${column}: ${set}`);
  return w.args[i];
}

// The H3 route lives on a DIRECT node, so it rides the same gate as the other
// direct-mode runtime fields (reality_*), not the cloudflare gate that
// xhttp_path / xhttp_direct_* use.
describe("nodeStatus XHTTP-H3 runtime", () => {
  const directRow: NodeRow = {
    id: "JPY-03",
    label: "JPY 03",
    admin_host: "jpy-03.rwl247.dev",
    vpn_host: "edge-64b43148.dongnat247.com",
    zone: "dongnat247.com",
    status: "active",
    last_seen_at: null,
    latency_ms: null,
    created_at: 1,
    public_ip: "129.225.185.197",
    mode: "direct",
    hy2_host: "quic-b55170f3.dongnat247.com",
    hy2_port: 32443,
    hy2_obfs_pw: "obfs",
    reality_pubkey: "pk",
    reality_sid: "2441ae2d78da98bb",
    reality_sni: "www.sony.jp",
    reality_dest: "www.sony.jp:443",
    xhttp_path: null,
    xhttp_enabled: 0,
    xhttp_direct_host: null,
    xhttp_direct_path: null,
    xhttp_h3_host: null,
    xhttp_h3_path: null,
    naive_host: null,
    naive_user: null,
    naive_pass: null,
    agent_secret: null,
    tunnel_uuid: null
  };

  it("persists the H3 pair a direct node reports", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      xray: "active", cloudflared: "active", hysteria: "active",
      vpn_host: "edge-64b43148.dongnat247.com", mode: "direct",
      tunnel_uuid: "", last_rotate_at: 0,
      xhttp_h3_host: "quic-b55170f3.dongnat247.com",
      xhttp_h3_path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10"
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: directRow, zones: [], writes });

    await nodeStatus(env, "JPY-03", "operator@example.com");

    expect(persistedByName(writes, "xhttp_h3_host")).toBe("quic-b55170f3.dongnat247.com");
    expect(persistedByName(writes, "xhttp_h3_path")).toBe("/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10");
  });

  // `cfvpnctl xhttp-h3 disable` writes empty strings, which Go's omitempty
  // then drops from the payload entirely — so "absent" must keep the row and
  // only an explicit "" may clear it, exactly like xhttp_direct_*.
  it("clears the pair when the node reports empty strings", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      xray: "active", cloudflared: "active", hysteria: "active",
      vpn_host: "edge-64b43148.dongnat247.com", mode: "direct",
      tunnel_uuid: "", last_rotate_at: 0,
      xhttp_h3_host: "", xhttp_h3_path: ""
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({
      node: { ...directRow, xhttp_h3_host: "quic-b55170f3.dongnat247.com", xhttp_h3_path: "/old" },
      zones: [], writes
    });

    await nodeStatus(env, "JPY-03", "operator@example.com");

    expect(persistedByName(writes, "xhttp_h3_host")).toBeNull();
    expect(persistedByName(writes, "xhttp_h3_path")).toBeNull();
  });

  it("keeps the stored pair when the node does not report it at all", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      xray: "active", cloudflared: "active", hysteria: "active",
      vpn_host: "edge-64b43148.dongnat247.com", mode: "direct",
      tunnel_uuid: "", last_rotate_at: 0
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({
      node: { ...directRow, xhttp_h3_host: "quic-b55170f3.dongnat247.com", xhttp_h3_path: "/keep" },
      zones: [], writes
    });

    await nodeStatus(env, "JPY-03", "operator@example.com");

    expect(persistedByName(writes, "xhttp_h3_host")).toBe("quic-b55170f3.dongnat247.com");
    expect(persistedByName(writes, "xhttp_h3_path")).toBe("/keep");
  });

  // A cloudflare-mode node has no H3 inbound; anything it claims about one is
  // not applied, the same way its reality_* claims are not.
  it("ignores an H3 pair claimed by a cloudflare-mode node", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      xray: "active", cloudflared: "active", hysteria: "inactive",
      vpn_host: "edge.rwl247.dev", mode: "cloudflare",
      tunnel_uuid: "", last_rotate_at: 0,
      xhttp_h3_host: "evil.example.com", xhttp_h3_path: "/whatever"
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: { ...directRow, mode: "cloudflare" }, zones: [], writes });

    await nodeStatus(env, "JPY-03", "operator@example.com");

    expect(persistedByName(writes, "xhttp_h3_host")).toBeNull();
  });
});

describe("nodeStatus naive runtime", () => {
  const cfRow: NodeRow = {
    id: "JPY-01", label: "JPY-01", admin_host: "jpy-01.rwl247.dev", vpn_host: "edge.rwl247.dev",
    zone: "rwl247.dev", status: "active", last_seen_at: null, latency_ms: null, created_at: 1,
    public_ip: null, mode: "cloudflare", hy2_host: null, hy2_port: null, hy2_obfs_pw: null,
    reality_pubkey: null, reality_sid: null, reality_sni: null, reality_dest: null,
    xhttp_path: "/api/v1/sync", xhttp_enabled: 1, xhttp_direct_host: null, xhttp_direct_path: null,
    xhttp_h3_host: null, xhttp_h3_path: null, naive_host: null, naive_user: null, naive_pass: null,
    agent_secret: null, tunnel_uuid: null
  };
  const base = { xray: "active", cloudflared: "active", hysteria: "active", vpn_host: "edge.rwl247.dev", mode: "cloudflare", tunnel_uuid: "", last_rotate_at: 0 };

  it("persists the naive triple even on a cloudflare-mode node", async () => {
    vi.mocked(callAgent).mockResolvedValue({ ...base, naive_host: "cdn.example.net", naive_user: "u1", naive_pass: "p1" } as never);
    const writes: RunWrite[] = [];
    await nodeStatus(makeEnv({ node: cfRow, zones: [], writes }), "JPY-01", "operator@example.com");
    expect(persistedByName(writes, "naive_host")).toBe("cdn.example.net");
    expect(persistedByName(writes, "naive_user")).toBe("u1");
    expect(persistedByName(writes, "naive_pass")).toBe("p1");
  });

  it("keeps the stored triple when absent and clears it on empty strings", async () => {
    const stored = { ...cfRow, naive_host: "cdn.example.net", naive_user: "u1", naive_pass: "p1" };
    vi.mocked(callAgent).mockResolvedValue({ ...base } as never);
    let writes: RunWrite[] = [];
    await nodeStatus(makeEnv({ node: stored, zones: [], writes }), "JPY-01", "operator@example.com");
    expect(persistedByName(writes, "naive_pass")).toBe("p1");

    vi.mocked(callAgent).mockResolvedValue({ ...base, naive_host: "", naive_user: "", naive_pass: "" } as never);
    writes = [];
    await nodeStatus(makeEnv({ node: stored, zones: [], writes }), "JPY-01", "operator@example.com");
    expect(persistedByName(writes, "naive_host")).toBeNull();
    expect(persistedByName(writes, "naive_pass")).toBeNull();
  });
});

describe("agent-reported hosts and IPs are validated before they reach D1 (review M2)", () => {
  beforeEach(() => {
    vi.mocked(callAgent).mockReset();
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });

  const stored: NodeRow = {
    ...reviewRow,
    id: "m2-01",
    admin_host: "m2-01.rwl247.dev",
    vpn_host: "edge.example.com",
    zone: "example.com",
    mode: "direct",
    public_ip: "203.0.113.10",
    hy2_host: "hy.example.com",
    hy2_port: 32443,
    hy2_obfs_pw: "obfs",
    xhttp_h3_host: "quic.example.com",
    xhttp_h3_path: "/p",
    naive_host: "naive.example.com",
    naive_user: "u",
    naive_pass: "p"
  };

  it("nodeStatus keeps the stored values when the agent reports garbage", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      mode: "direct",
      vpn_host: "evil.example.com#x",
      zone: "example.com",
      public_ip: "1.2.3.4#x",
      hy2_host: "hy.example.com/../x",
      xhttp_h3_host: "quic.example.com:443",
      naive_host: "user@naive.example.com",
      tunnel_uuid: ""
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: stored, zones: [{ name: "example.com", cf_zone_id: "z" }], writes });
    expect((await nodeStatus(env, "m2-01", "op")).status).toBe(200);
    const p = persistedRuntime(writes);
    expect(p.vpn_host).toBe("edge.example.com");
    expect(p.public_ip).toBe("203.0.113.10");
    expect(p.hy2_host).toBe("hy.example.com");
    expect(p.xhttp_h3_host).toBe("quic.example.com");
    expect(p.naive_host).toBe("naive.example.com");
  });

  it("nodeStatus still accepts well-formed values and an explicit clear", async () => {
    vi.mocked(callAgent).mockResolvedValue({
      mode: "direct",
      vpn_host: "edge2.example.com",
      zone: "example.com",
      public_ip: "198.51.100.7",
      hy2_host: "hy2.example.com",
      xhttp_h3_host: "",
      naive_host: "naive2.example.com",
      tunnel_uuid: ""
    } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: stored, zones: [{ name: "example.com", cf_zone_id: "z" }], writes });
    await nodeStatus(env, "m2-01", "op");
    const p = persistedRuntime(writes);
    expect(p.vpn_host).toBe("edge2.example.com");
    expect(p.public_ip).toBe("198.51.100.7");
    expect(p.hy2_host).toBe("hy2.example.com");
    expect(p.xhttp_h3_host).toBeNull();
    expect(p.naive_host).toBe("naive2.example.com");
  });

  it("nodeSyncCore ignores a malformed public_ip and vpn_host", async () => {
    vi.mocked(callAgent).mockResolvedValue({ ok: true, vpn_host: "a b", public_ip: "not-an-ip", hy2_host: "", users: 1 } as never);
    const writes: RunWrite[] = [];
    const env = makeEnv({ node: stored, zones: [{ name: "example.com", cf_zone_id: "z" }], writes });
    expect((await nodeSyncCore(env, "m2-01", [{ name: "a", vless_uuid: "u", hy2_pw: "p" }], "op")).status).toBe(200);
    const p = persistedRuntime(writes);
    expect(p.vpn_host).toBe("edge.example.com");
    expect(p.public_ip).toBe("203.0.113.10");
  });
});

describe("operator-supplied hosts are validated (review M3) and label cannot be blanked (review L8)", () => {
  beforeEach(() => {
    vi.mocked(logEvent).mockReset();
    vi.mocked(logEvent).mockResolvedValue(undefined);
  });
  const req = (method: string, url: string, body: unknown) =>
    new Request(url, { method, headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  const errorOf = async (res: Response) => (await res.json() as { error: string }).error;

  it("createNode rejects a vpn_host or hy2_host that is not a plain hostname", async () => {
    for (const host of ["a b.example.com", "x@y.example.com", "example", "host.example.com/path", "1.2.3.4:443"]) {
      const env = makeEnv({ node: { ...reviewRow, id: "OTHER" }, zones: [{ name: "example.com", cf_zone_id: "z", enabled: 1 }] });
      const res = await createNode(env, req("POST", "https://panel.test/api/nodes", { id: "NEW-01", label: "n", host, zone: "example.com" }), "op");
      expect(res.status).toBe(400);
      expect(await errorOf(res)).toBe("invalid_vpn_host");
    }
    const env = makeEnv({ node: { ...reviewRow, id: "OTHER" }, zones: [{ name: "example.com", cf_zone_id: "z", enabled: 1 }] });
    const res = await createNode(env, req("POST", "https://panel.test/api/nodes", { id: "NEW-01", label: "n", hy2_host: "bad host" }), "op");
    expect(await errorOf(res)).toBe("invalid_hy2_host");
  });

  it("patchNode rejects a malformed vpn_host and an empty label", async () => {
    const env = () => makeEnv({ node: { ...reviewRow }, zones: [] });
    const url = `https://panel.test/api/nodes/${reviewRow.id}`;
    let res = await patchNode(env(), reviewRow.id, req("PATCH", url, { vpn_host: "x#y.example.com" }), "op");
    expect(await errorOf(res)).toBe("invalid_vpn_host");
    for (const label of ["", "   ", 5]) {
      res = await patchNode(env(), reviewRow.id, req("PATCH", url, { label }), "op");
      expect(res.status).toBe(400);
    }
    res = await patchNode(env(), reviewRow.id, req("PATCH", url, { label: "Renamed", vpn_host: "edge9.example.com" }), "op");
    expect(res.status).toBe(200);
  });

  it("nodeRotate rejects a malformed host override before calling the agent", async () => {
    vi.mocked(callAgent).mockReset();
    const env = makeEnv({ node: { ...reviewRow }, zones: [{ name: "example.com", cf_zone_id: "z", enabled: 1 }] });
    const res = await nodeRotate(env, reviewRow.id, req("POST", "https://panel.test/x", { host: "bad host", zone: "example.com" }), "op");
    expect(res.status).toBe(400);
    expect(callAgent).not.toHaveBeenCalled();
  });
});
