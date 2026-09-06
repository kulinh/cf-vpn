import { describe, expect, it, vi } from "vitest";
import { publicSubscription } from "./sub";
import { buildSubscriptionURIs, buildVLESSRealityURI, buildVLESSHTTPUpgradeURI, encodeSubscriptionBody } from "../lib/subscription";
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
    const realityFields = {
      mode: "direct",
      reality_pubkey: "pubkey-x25519",
      reality_sid: "abcd1234",
      reality_sni: "www.microsoft.com",
      xhttp_path: null
    };
    const env = makeEnv(
      makeDB({
        userByToken: { [token]: { id: "kulinh" } },
        nodesByUser: {
          kulinh: [
            { vless_uuid: "u1", hy2_pw: "p1", vpn_host: "sg.example.com", node_id: "SG", hy2_host: "udp-sg.example.com", hy2_port: 30000, hy2_obfs_pw: "obfs1", ...realityFields },
            { vless_uuid: "u2", hy2_pw: "p2", vpn_host: "jp.example.com", node_id: "JP1", hy2_host: null, hy2_port: null, hy2_obfs_pw: null, ...realityFields }
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
        { vless_uuid: "u1", hy2_pw: "p1", vpn_host: "sg.example.com", node_id: "SG", hy2_host: "udp-sg.example.com", hy2_port: 30000, hy2_obfs_pw: "obfs1", ...realityFields },
        { vless_uuid: "u2", hy2_pw: "p2", vpn_host: "jp.example.com", node_id: "JP1", hy2_host: null, hy2_port: null, hy2_obfs_pw: null, ...realityFields }
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

  it("sends profile-title and no empty subscription-userinfo header", async () => {
    const token = "e".repeat(32);
    const env = makeEnv(makeDB({
      userByToken: { [token]: { id: "kulinh" } },
      nodesByUser: { kulinh: [] }
    }));

    const res = await publicSubscription(env, token);

    // An empty subscription-userinfo reads as upload=0/download=0/total=0 —
    // "0 B of 0 B" — and some clients treat that as an exhausted quota and stop
    // auto-updating. Absent is correct; empty is worse than nothing.
    expect(res.headers.get("subscription-userinfo")).toBeNull();
    expect(res.headers.get("profile-title")).toBe(`base64:${btoa("RWL8899")}`);
    expect(res.headers.get("profile-update-interval")).toBe("24");
  });

  it("warns once per node when hy2 is configured without an obfs password", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const token = "f".repeat(32);
    const row = {
      vless_uuid: "u1",
      hy2_pw: "p1",
      vpn_host: "sg.example.com",
      node_id: "NO-OBFS",
      hy2_host: "udp-sg.example.com",
      hy2_port: 30000,
      hy2_obfs_pw: null,
      mode: "direct",
      reality_pubkey: "pk",
      reality_sid: "sid",
      reality_sni: "www.apple.com",
      xhttp_path: null
    };
    const env = makeEnv(makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [row] } }));

    const first = await publicSubscription(env, token);
    const second = await publicSubscription(env, token);

    // Output is unchanged — the HY2 line is still dropped, just no longer silently.
    const firstBody = await first.text();
    const secondBody = await second.text();
    expect(atob(firstBody).split("\n")).toHaveLength(2); // REMARKS + the Reality URI
    expect(secondBody).toBe(firstBody);
    expect(warn.mock.calls.filter((c) => String(c[1]).includes("NO-OBFS"))).toHaveLength(1);
    warn.mockRestore();
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

describe("buildVLESSRealityURI", () => {
  it("builds a reality URI with expected params", () => {
    const uri = buildVLESSRealityURI(
      "test@SG", "uid-1", "sg.example.com",
      "discord.com", "pubkey123", "sid456",
    );
    expect(uri).toContain("vless://uid-1@sg.example.com:443");
    expect(uri).toContain("security=reality");
    expect(uri).toContain("flow=xtls-rprx-vision");
    expect(uri).toContain("type=tcp");
    expect(uri).toContain("sni=discord.com");
    expect(uri).toContain("pbk=pubkey123");
    expect(uri).toContain("sid=sid456");
    expect(uri).toContain("fp=chrome");
    expect(uri).toContain("#test%40SG-Reality");
  });
});

describe("buildVLESSHTTPUpgradeURI", () => {
  it("builds an HTTPUpgrade URI with path-encoded slashes", () => {
    const uri = buildVLESSHTTPUpgradeURI(
      "test@JP1", "uid-2", "jp.example.com", "/api/v1/sync",
    );
    expect(uri).toContain("vless://uid-2@jp.example.com:443");
    expect(uri).toContain("security=tls");
    expect(uri).toContain("type=httpupgrade");
    expect(uri).toContain("host=jp.example.com");
    expect(uri).toContain("path=%2Fapi%2Fv1%2Fsync");
    expect(uri).toContain("sni=jp.example.com");
    expect(uri).toContain("#test%40JP1-HTTPUpgrade");
  });
});

describe("buildSubscriptionURIs mode branching", () => {
  const nullFields = { mode: null, reality_pubkey: null, reality_sid: null, reality_sni: null, xhttp_path: null };

  it("emits Reality URI when mode=direct with reality fields", () => {
    const rows = [{
      vless_uuid: "u1", hy2_pw: "p1", vpn_host: "sg.example.com", node_id: "SG",
      hy2_host: null as string | null, hy2_port: null as number | null, hy2_obfs_pw: null as string | null,
      mode: "direct" as const, reality_pubkey: "pk", reality_sid: "sid", reality_sni: "discord.com", xhttp_path: null as string | null,
    }];
    const uris = buildSubscriptionURIs("kulinh", rows);
    const lines = uris.split("\n");
    expect(lines).toHaveLength(1);
    expect(lines[0]).toContain("security=reality");
    expect(lines[0]).toContain("sni=discord.com");
    expect(lines[0]).toContain("#kulinh%40SG-Reality");
  });

  it("emits HTTPUpgrade URI when mode=cloudflare", () => {
    const rows = [{
      vless_uuid: "u2", hy2_pw: "p2", vpn_host: "cf.example.com", node_id: "CF",
      hy2_host: null as string | null, hy2_port: null as number | null, hy2_obfs_pw: null as string | null,
      mode: "cloudflare" as const, reality_pubkey: null as string | null, reality_sid: null as string | null, reality_sni: null as string | null, xhttp_path: "/api/v1/sync",
    }];
    const uris = buildSubscriptionURIs("kulinh", rows);
    const lines = uris.split("\n");
    expect(lines).toHaveLength(1);
    expect(lines[0]).toContain("type=httpupgrade");
    expect(lines[0]).toContain("path=%2Fapi%2Fv1%2Fsync");
    expect(lines[0]).toContain("#kulinh%40CF-HTTPUpgrade");
  });

  it("skips nodes with no mode (legacy WS+TLS no longer supported)", () => {
    const rows = [{
      vless_uuid: "u3", hy2_pw: "p3", vpn_host: "old.example.com", node_id: "OLD",
      hy2_host: null as string | null, hy2_port: null as number | null, hy2_obfs_pw: null as string | null,
      ...nullFields,
    }];
    const uris = buildSubscriptionURIs("kulinh", rows);
    expect(uris).toBe("");
  });
});
