import { describe, expect, it } from "vitest";
import { publicSubscription } from "./sub";
import { buildSubscriptionURIs, encodeSubscriptionBody } from "../lib/subscription";
import type { Env } from "../types";

type FirstResult = Record<string, unknown> | null;
type AllResult = unknown[];

type StubSpec = {
  userByToken?: Record<string, FirstResult>;
  nodesByUser?: Record<string, AllResult>;
};

function makeDB(spec: StubSpec): D1Database {
  const makePrepared = (sql: string): D1PreparedStatement => {
    const state: { args: unknown[] } = { args: [] };
    const stmt: D1PreparedStatement = {
      bind(...args: unknown[]) {
        state.args = args;
        return stmt;
      },
      async first() {
        if (/FROM users WHERE sub_token=\?/.test(sql)) {
          const token = state.args[0] as string;
          return (spec.userByToken?.[token] ?? null) as never;
        }
        return null as never;
      },
      async all() {
        if (/FROM user_nodes un JOIN nodes n/.test(sql)) {
          const userId = state.args[0] as string;
          return { results: (spec.nodesByUser?.[userId] ?? []) as unknown[] } as never;
        }
        return { results: [] } as never;
      },
      async run() {
        return { success: true } as never;
      }
    } as unknown as D1PreparedStatement;
    return stmt;
  };

  return {
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
}

function makeEnv(db: D1Database): Env {
  return { DB: db, ADMIN_HOST_ALLOWED_SUFFIXES: "example.com" };
}

describe("publicSubscription", () => {
  it("rejects malformed tokens with 404", async () => {
    const env = makeEnv(makeDB({}));
    const res = await publicSubscription(env, "bogus");
    expect(res.status).toBe(404);
    expect(res.headers.get("cache-control")).toBe("no-store");
  });

  it("returns 404 when token not found", async () => {
    const env = makeEnv(makeDB({ userByToken: { ["a".repeat(32)]: null } }));
    const res = await publicSubscription(env, "b".repeat(32));
    expect(res.status).toBe(404);
  });

  it("returns base64 body with vless+hy2 per node (hy2 only when present)", async () => {
    const token = "c".repeat(32);
    const env = makeEnv(
      makeDB({
        userByToken: { [token]: { id: "kulinh" } },
        nodesByUser: {
          kulinh: [
            { vless_uuid: "u1", hy2_pw: "p1", vpn_host: "sg.example.com", node_id: "SG", hy2_host: "udp-sg.example.com", hy2_port: 30000, hy2_obfs_pw: "obfs1" },
            { vless_uuid: "u2", hy2_pw: "p2", vpn_host: "jp.example.com", node_id: "JP1", hy2_host: null, hy2_port: null, hy2_obfs_pw: null }
          ]
        }
      })
    );
    const res = await publicSubscription(env, token);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/text\/plain/);
    expect(res.headers.get("cache-control")).toBe("no-store, private");

    const body = await res.text();
    const expected = encodeSubscriptionBody(
      buildSubscriptionURIs("kulinh", [
        { vless_uuid: "u1", hy2_pw: "p1", vpn_host: "sg.example.com", node_id: "SG", hy2_host: "udp-sg.example.com", hy2_port: 30000, hy2_obfs_pw: "obfs1" },
        { vless_uuid: "u2", hy2_pw: "p2", vpn_host: "jp.example.com", node_id: "JP1", hy2_host: null, hy2_port: null, hy2_obfs_pw: null }
      ]),
      "RWL8899"
    );
    expect(body).toBe(expected);

    const decoded = atob(body).split("\n");
    expect(decoded).toHaveLength(4);
    expect(decoded[0]).toBe("REMARKS=RWL8899");
    expect(decoded[1]).toMatch(/^vless:\/\/u1@sg\.example\.com:443/);
    expect(decoded[2]).toMatch(/^hysteria2:\/\/kulinh:p1@udp-sg\.example\.com:30000/);
    expect(decoded[3]).toMatch(/^vless:\/\/u2@jp\.example\.com:443/);
  });

  it("emits only the REMARKS line when user has no nodes", async () => {
    const token = "d".repeat(32);
    const env = makeEnv(
      makeDB({
        userByToken: { [token]: { id: "orphan" } },
        nodesByUser: { orphan: [] }
      })
    );
    const res = await publicSubscription(env, token);
    expect(res.status).toBe(200);
    expect(atob(await res.text())).toBe("REMARKS=RWL8899");
  });
});

describe("encodeSubscriptionBody", () => {
  it("joins uris with newline and base64-encodes", () => {
    const out = encodeSubscriptionBody(["a", "b"]);
    expect(atob(out)).toBe("a\nb");
  });

  it("prepends REMARKS= line when remarks is provided", () => {
    const out = encodeSubscriptionBody(["a", "b"], "RWL8899");
    expect(atob(out)).toBe("REMARKS=RWL8899\na\nb");
  });

  it("encodes large subscription payloads without throwing", () => {
    const chunk = "x".repeat(200000);
    const uris = [`vless://${chunk}`, `hysteria2://${chunk}`];

    const out = encodeSubscriptionBody(uris, "RWL8899");

    expect(atob(out)).toBe(`REMARKS=RWL8899\n${uris.join("\n")}`);
  });
});
