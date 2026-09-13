import { describe, expect, it, vi } from "vitest";
import { publicSubscription } from "./sub";
import { buildSubscriptionURIs, buildVLESSRealityURI, buildVLESSHTTPUpgradeURI, encodeSubscriptionBody } from "../lib/subscription";
import { buildClashConfig } from "../lib/clash";
import type { Env } from "../types";

type FirstResult = Record<string, unknown> | null;
type AllResult = unknown[];

type StubSpec = {
  userByToken?: Record<string, FirstResult>;
  nodesByUser?: Record<string, AllResult>;
  settings?: Record<string, string>;
  settingsThrow?: boolean;
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
        if (/FROM settings WHERE key=\?/.test(sql)) {
          if (spec.settingsThrow) throw new Error("no such table: settings");
          const v = spec.settings?.[state.args[0] as string];
          return (v == null ? null : { value: v }) as never;
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

  it("serves mihomo YAML for ?format=clash", async () => {
    const token = "1".repeat(32);
    const row = {
      vless_uuid: "u1",
      hy2_pw: "p1",
      vpn_host: "sg.example.com",
      node_id: "SG",
      hy2_host: "udp-sg.example.com",
      hy2_port: 30000,
      hy2_obfs_pw: "obfs1",
      mode: "direct",
      reality_pubkey: "pk",
      reality_sid: "sid",
      reality_sni: "www.apple.com",
      xhttp_path: null
    };
    const env = makeEnv(makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [row] } }));

    const res = await publicSubscription(env, token, "clash");

    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toBe("text/yaml; charset=utf-8");
    const body = await res.text();
    expect(body).toBe(buildClashConfig("kulinh", [row]));
    expect(body).toContain('type: "url-test"');
  });

  it("rejects an unknown ?format= with 400 instead of serving base64", async () => {
    const token = "2".repeat(32);
    const env = makeEnv(makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] } }));

    const res = await publicSubscription(env, token, "surge");

    expect(res.status).toBe(400);
    expect((await res.json() as { error: string }).error).toBe("invalid_format");
    expect((await res.json().catch(() => ({})) as { detail?: string }).detail ?? "supported: clash, shadowrocket").toContain("shadowrocket");
  });

  it("keeps the default (no format) body byte-identical", async () => {
    const token = "3".repeat(32);
    const rows = [
      {
        vless_uuid: "u1",
        hy2_pw: "p1",
        vpn_host: "sg.example.com",
        node_id: "SG",
        hy2_host: "udp-sg.example.com",
        hy2_port: 30000,
        hy2_obfs_pw: "obfs1",
        mode: "direct",
        reality_pubkey: "pk",
        reality_sid: "sid",
        reality_sni: "www.apple.com",
        xhttp_path: null
      }
    ];
    const spec = { userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: rows } };

    const bare = await publicSubscription(makeEnv(makeDB(spec)), token);
    const empty = await publicSubscription(makeEnv(makeDB(spec)), token, "");
    const nulled = await publicSubscription(makeEnv(makeDB(spec)), token, null);
    const expected = encodeSubscriptionBody(buildSubscriptionURIs("kulinh", rows), "RWL8899");

    expect(await bare.text()).toBe(expected);
    expect(await empty.text()).toBe(expected);
    expect(await nulled.text()).toBe(expected);
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

describe("Reality URIs address the node by public IP", () => {
  const base = {
    vless_uuid: "u1", hy2_pw: "p1", vpn_host: "assets-b7e69185.rwl.one", node_id: "SIN-01",
    hy2_host: null, hy2_port: null, hy2_obfs_pw: null,
    mode: "direct" as const, reality_pubkey: "pk", reality_sid: "sid", reality_sni: "www.singaporeair.com", xhttp_path: null as string | null,
  };
  it("uses public_ip when D1 has it", () => {
    const lines = buildSubscriptionURIs("kulinh", [{ ...base, public_ip: "96.9.231.74" }]).split("\n");
    expect(lines[0].startsWith("vless://u1@96.9.231.74:443?")).toBe(true);
    expect(lines[0]).toContain("sni=www.singaporeair.com");
  });
  it("falls back to vpn_host when public_ip is null", () => {
    const lines = buildSubscriptionURIs("kulinh", [{ ...base, public_ip: null }]).split("\n");
    expect(lines[0].startsWith("vless://u1@assets-b7e69185.rwl.one:443?")).toBe(true);
  });
  it("keeps the hostname for cloudflare routes even when public_ip is set", () => {
    const lines = buildSubscriptionURIs("kulinh", [{ ...base, mode: "cloudflare" as const, node_id: "OR-001", vpn_host: "static-df60bd79.duylinh.org", public_ip: "51.81.245.144", xhttp_path: "/api/v1/sync" }]).split("\n");
    expect(lines[0].startsWith("vless://u1@static-df60bd79.duylinh.org:443?")).toBe(true);
  });
});

describe("HY2 URIs dial the public IP and keep the hostname as sni", () => {
  const base = {
    vless_uuid: "u1", hy2_pw: "p1", vpn_host: "media.example.com", node_id: "HKG-01",
    hy2_host: "hy-c36ca6bd.dongnat247.com", hy2_port: 31300, hy2_obfs_pw: "obfs",
    mode: "direct" as const, reality_pubkey: "pk", reality_sid: "sid", reality_sni: "www.cathaypacific.com", xhttp_path: null as string | null,
  };
  it("uses public_ip in the authority and the hostname in sni", () => {
    const lines = buildSubscriptionURIs("kulinh", [{ ...base, public_ip: "96.9.228.81" }]).split("\n");
    expect(lines[1]).toBe("hysteria2://kulinh:p1@96.9.228.81:31300/?obfs=salamander&obfs-password=obfs&sni=hy-c36ca6bd.dongnat247.com&insecure=0#kulinh%40HKG-01-HY2");
  });
  it("falls back to the hostname without public_ip", () => {
    const lines = buildSubscriptionURIs("kulinh", [{ ...base, public_ip: null }]).split("\n");
    expect(lines[1].startsWith("hysteria2://kulinh:p1@hy-c36ca6bd.dongnat247.com:31300/?")).toBe(true);
  });
});

describe("XHTTP line for cloudflare nodes with xhttp_enabled", () => {
  const base = {
    vless_uuid: "2f8a1c3e-1111-4222-8333-abcdefabcdef", hy2_pw: "p1", vpn_host: "static-df60bd79.duylinh.org", node_id: "or-001",
    hy2_host: null, hy2_port: null, hy2_obfs_pw: null, public_ip: "51.81.245.144",
    mode: "cloudflare" as const, reality_pubkey: null, reality_sid: null, reality_sni: null, xhttp_path: "/api/v1/sync",
  };
  it("matches the Go golden string byte for byte", () => {
    const lines = buildSubscriptionURIs("alice", [{ ...base, xhttp_enabled: 1 }]).split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[1]).toBe("vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@static-df60bd79.duylinh.org:443?encryption=none&security=tls&type=xhttp&host=static-df60bd79.duylinh.org&path=%2Fapi%2Fv2%2Fstream&mode=packet-up&sni=static-df60bd79.duylinh.org#alice%40or-001-XHTTP");
  });
  it("emits nothing extra when disabled or for direct nodes", () => {
    expect(buildSubscriptionURIs("alice", [{ ...base, xhttp_enabled: 0 }]).split("\n")).toHaveLength(1);
    expect(buildSubscriptionURIs("alice", [{ ...base, xhttp_enabled: null }]).split("\n")).toHaveLength(1);
  });
});

describe("XHTTP-Direct line", () => {
  const base = {
    vless_uuid: "2f8a1c3e-1111-4222-8333-abcdefabcdef", hy2_pw: "p1", vpn_host: "edge-fd34b370.rwl247.dev", node_id: "JPY-01",
    hy2_host: null, hy2_port: null, hy2_obfs_pw: null, public_ip: "45.143.131.36",
    mode: "cloudflare" as const, reality_pubkey: null, reality_sid: null, reality_sni: null, xhttp_path: "/api/v1/sync", xhttp_enabled: 0,
  };
  it("matches the Go golden string and uses the hostname, not the IP", () => {
    const lines = buildSubscriptionURIs("kulinh", [{ ...base, xhttp_direct_host: "cdn-82169439.duylinh.net", xhttp_direct_path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10" }]).split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[1]).toBe("vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-82169439.duylinh.net:443?encryption=none&security=tls&type=xhttp&host=cdn-82169439.duylinh.net&path=%2F3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10&mode=stream-one&sni=cdn-82169439.duylinh.net#kulinh%40JPY-01-XHTTP-Direct");
  });
  it("needs both host and path", () => {
    expect(buildSubscriptionURIs("kulinh", [{ ...base, xhttp_direct_host: "cdn.example.com", xhttp_direct_path: null }]).split("\n")).toHaveLength(1);
  });
});

describe("?format=shadowrocket&final=", () => {
  it("rejects an unknown final value", async () => {
    const env = makeEnv(makeDB({}));
    const res = await publicSubscription(env, "a".repeat(32), "shadowrocket", "maybe");
    expect(res.status).toBe(400);
    expect((await res.json() as { error: string }).error).toBe("invalid_final");
  });
});

describe("?format=shadowrocket&rules=", () => {
  const token = "c".repeat(32);
  const db = () => makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] } });
  const moduleText = "#!name=x\n[Rule]\n# c\nDOMAIN-SUFFIX,google.com,PROXY\nIP-CIDR,8.8.8.0/24,PROXY,no-resolve\n";

  it("rejects an unknown rules value before touching the database", async () => {
    const res = await publicSubscription(makeEnv(makeDB({})), token, "shadowrocket", null, "bogus");
    expect(res.status).toBe(400);
    expect((await res.json() as { error: string }).error).toBe("invalid_rules");
  });

  it("inlines the module fetched at the edge by default", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", async (input: string) => {
      calls.push(input);
      return new Response(moduleText, { status: 200, headers: { etag: '"abc"' } });
    });
    try {
      const res = await publicSubscription(makeEnv(db()), token, "shadowrocket", null, null);
      expect(res.status).toBe(200);
      const body = await res.text();
      expect(calls).toEqual(["https://raw.githubusercontent.com/kulinh/shadowrocket-vietnamese/master/sr_proxy_list_CN.module"]);
      expect(body).toContain("\nDOMAIN-SUFFIX,google.com,PROXY\n");
      expect(body).toContain("\nIP-CIDR,8.8.8.0/24,PROXY,no-resolve\n");
      expect(body).toContain("(2 rules, etag abc");
      expect(body).not.toContain("RULE-SET,");
      expect(body).toMatch(/FINAL,DIRECT\n$/);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("falls back to a RULE-SET line pointing at the .list when GitHub is unreachable", async () => {
    vi.stubGlobal("fetch", async () => {
      throw new Error("connect timeout");
    });
    try {
      const res = await publicSubscription(makeEnv(db()), token, "shadowrocket", null, "cn");
      const body = await res.text();
      expect(body).toContain("RULE-SET,https://raw.githubusercontent.com/kulinh/shadowrocket-vietnamese/master/sr_proxy_list_CN.list,PROXY\n");
      expect(body).not.toContain("google.com");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("rules=uae inlines the UAE module instead", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", async (input: string) => {
      calls.push(input);
      return new Response("[Rule]\nDOMAIN-SUFFIX,whatsapp.net,PROXY\n");
    });
    try {
      const body = await (await publicSubscription(makeEnv(db()), token, "shadowrocket", null, "uae")).text();
      expect(calls).toEqual(["https://raw.githubusercontent.com/kulinh/shadowrocket-vietnamese/master/sr_proxy_list_UAE.module"]);
      expect(body).toContain("# sr_proxy_list_UAE from ");
      expect(body).toContain("\nDOMAIN-SUFFIX,whatsapp.net,PROXY\n");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("follows the stored rules_mode setting when the link carries no ?rules=", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", async (input: string) => {
      calls.push(input);
      return new Response("[Rule]\nDOMAIN-SUFFIX,whatsapp.net,PROXY\n");
    });
    try {
      const uaeDB = makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] }, settings: { rules_mode: "uae" } });
      const body = await (await publicSubscription(makeEnv(uaeDB), token, "shadowrocket", null, null)).text();
      expect(calls.at(-1)).toContain("sr_proxy_list_UAE.module");
      expect(body).toContain("# sr_proxy_list_UAE from ");

      // An explicit ?rules= on the link still wins over the stored mode.
      await publicSubscription(makeEnv(uaeDB), token, "shadowrocket", null, "cn");
      expect(calls.at(-1)).toContain("sr_proxy_list_CN.module");

      // rules_mode=none: bare tail, nothing fetched.
      const n = calls.length;
      const noneDB = makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] }, settings: { rules_mode: "none" } });
      const bare = await (await publicSubscription(makeEnv(noneDB), token, "shadowrocket", null, null)).text();
      expect(bare).toContain("load the sr_proxy_list_CN (or _UAE) module");
      expect(calls.length).toBe(n);

      // Garbage or a missing settings table fall back to CN.
      for (const db2 of [
        makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] }, settings: { rules_mode: "mars" } }),
        makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] }, settingsThrow: true })
      ]) {
        await publicSubscription(makeEnv(db2), token, "shadowrocket", null, null);
        expect(calls.at(-1)).toContain("sr_proxy_list_CN.module");
      }
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("rules=none and final=proxy skip the fetch entirely", async () => {
    let calls = 0;
    vi.stubGlobal("fetch", async () => {
      calls++;
      return new Response(moduleText);
    });
    try {
      const none = await (await publicSubscription(makeEnv(db()), token, "shadowrocket", null, "none")).text();
      expect(none).toContain("load the sr_proxy_list_CN (or _UAE) module above this config");
      const full = await (await publicSubscription(makeEnv(db()), token, "shadowrocket", "proxy", null)).text();
      expect(full).toMatch(/FINAL,PROXY\n$/);
      expect(full).not.toContain("RULE-SET,");
      expect(calls).toBe(0);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe("NaiveProxy and ?format=singbox", () => {
  const token = "d".repeat(32);
  const jpy01 = {
    vless_uuid: "2f8a1c3e-1111-4222-8333-abcdefabcdef", hy2_pw: "p1", vpn_host: "edge-fd34b370.rwl247.dev", node_id: "JPY-01",
    hy2_host: "quic.example.net", hy2_port: 31565, hy2_obfs_pw: "obfs", public_ip: "45.143.131.36",
    mode: "cloudflare", reality_pubkey: null, reality_sid: null, reality_sni: null, xhttp_path: "/api/v1/sync", xhttp_enabled: 0,
    naive_host: "cdn-82169439.duylinh.net", naive_user: "u1", naive_pass: "p@ss"
  };
  const jpy02 = {
    vless_uuid: "3f8a1c3e-1111-4222-8333-abcdefabcdef", hy2_pw: "p2", vpn_host: "edge-2.example.net", node_id: "JPY-02",
    hy2_host: null, hy2_port: null, hy2_obfs_pw: null, public_ip: "96.9.228.81",
    mode: "direct", reality_pubkey: "pk", reality_sid: "ab", reality_sni: "www.amazon.co.jp", xhttp_path: null, xhttp_enabled: 0
  };
  const db = () => makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [jpy01, jpy02] } });
  const moduleText = "[Rule]\nDOMAIN-SUFFIX,google.com,PROXY\nDOMAIN,one.one.one.one,PROXY\nDOMAIN-KEYWORD,telegram,PROXY\nIP-CIDR,8.8.8.0/24,PROXY,no-resolve\nIP-CIDR6,2001:4860::/32,PROXY,no-resolve\n";

  it("adds naive:// lines to the base64 list only for Hiddify", async () => {
    const plain = atob(await (await publicSubscription(makeEnv(db()), token, null, null, null, "Shadowrocket/2070")).text());
    expect(plain).not.toContain("naive://");
    const hiddify = atob(await (await publicSubscription(makeEnv(db()), token, null, null, null, "HiddifyNext/4.1.1 (android) like ClashMeta v2ray sing-box")).text());
    expect(hiddify.split("\n")).toContain("naive://u1:p%40ss@cdn-82169439.duylinh.net:443?security=tls&sni=cdn-82169439.duylinh.net&uot=false#kulinh%40JPY-01-Naive");
  });

  it("leaves naive:// out for Hiddify on iOS, where a naive outbound kills the core", async () => {
    for (const ua of ["HiddifyNext/4.0.0 (ios) like ClashMeta v2ray sing-box", "HiddifyNextX/4.0.0 (iOS) like ClashMeta v2ray sing-box"]) {
      const body = atob(await (await publicSubscription(makeEnv(db()), token, null, null, null, ua)).text());
      expect(body).not.toContain("naive://");
      expect(body).toContain("kulinh%40JPY-01-HY2");
    }
    const mac = atob(await (await publicSubscription(makeEnv(db()), token, null, null, null, "HiddifyNext/4.1.1 (macos) like ClashMeta v2ray sing-box")).text());
    expect(mac).toContain("naive://");
  });

  it("serves a split sing-box config with the module inlined as a rule set", async () => {
    vi.stubGlobal("fetch", async () => new Response(moduleText, { status: 200 }));
    try {
      const res = await publicSubscription({ ...makeEnv(db()), PANEL_PUBLIC_ORIGIN: "https://cp.rwl265.com" }, token, "singbox", null, null);
      expect(res.status).toBe(200);
      expect(res.headers.get("content-type")).toContain("application/json");
      const cfg = await res.json() as { outbounds: Array<Record<string, unknown>>; route: Record<string, unknown>; dns: Record<string, unknown> };
      const byTag = Object.fromEntries(cfg.outbounds.map((o) => [o.tag, o]));
      expect(byTag["kulinh@JPY-01-Naive"]).toMatchObject({ type: "naive", server: "cdn-82169439.duylinh.net", server_port: 443, username: "u1", password: "p@ss" });
      expect(byTag["kulinh@JPY-01-HY2"]).toMatchObject({ type: "hysteria2", server: "45.143.131.36", password: "kulinh:p1", obfs: { type: "salamander", password: "obfs" } });
      expect(byTag["kulinh@JPY-02-Reality"]).toMatchObject({ type: "vless", server: "96.9.228.81", flow: "xtls-rprx-vision" });
      expect(byTag["AUTO"]).toMatchObject({ type: "urltest", outbounds: ["kulinh@JPY-02-Reality", "kulinh@JPY-01-HY2"] });
      expect((byTag["PROXY"].outbounds as string[]).slice(0, 2)).toEqual(["AUTO", "HY2-BACKUP"]);
      expect(cfg.route.final).toBe("DIRECT");
      expect(cfg.route.rule_set).toEqual([
        { type: "inline", tag: "blocked-domain", rules: [{ domain: ["one.one.one.one"], domain_suffix: ["google.com"], domain_keyword: ["telegram"] }] },
        { type: "inline", tag: "blocked-ip", rules: [{ ip_cidr: ["8.8.8.0/24", "2001:4860::/32"] }] }
      ]);
      expect(cfg.route.rules).toContainEqual({ rule_set: ["blocked-domain", "blocked-ip"], action: "route", outbound: "PROXY" });
      // sing-box 1.14 deprecates DNS rules that reach an ip_cidr rule set.
      expect(cfg.dns.rules).toContainEqual({ rule_set: ["blocked-domain"], action: "route", server: "remote" });
      expect(JSON.stringify(cfg.dns.rules)).not.toContain("blocked-ip");
      expect(cfg.route.rules).toContainEqual({ domain: ["cp.rwl265.com"], domain_suffix: ["cloudflareaccess.com"], action: "route", outbound: "PROXY" });
      expect(cfg.dns.final).toBe("local");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("final=proxy is a full tunnel with no rule set and no fetch", async () => {
    let fetched = false;
    vi.stubGlobal("fetch", async () => { fetched = true; return new Response(moduleText); });
    try {
      const cfg = await (await publicSubscription(makeEnv(db()), token, "singbox", "proxy", null)).json() as { route: Record<string, unknown>; dns: Record<string, unknown> };
      expect(fetched).toBe(false);
      expect(cfg.route.final).toBe("PROXY");
      expect(cfg.route.rule_set).toBeUndefined();
      expect(cfg.dns.final).toBe("remote");
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("answers 503 instead of a split config without its list", async () => {
    vi.stubGlobal("fetch", async () => { throw new Error("timeout"); });
    try {
      const res = await publicSubscription(makeEnv(db()), token, "singbox", null, "cn");
      expect(res.status).toBe(503);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe("disabled nodes (review M1)", () => {
  it("leaves nodes the operator disabled out of the subscription query", async () => {
    const token = "9".repeat(32);
    const base = makeDB({ userByToken: { [token]: { id: "kulinh" } }, nodesByUser: { kulinh: [] } });
    const seen: string[] = [];
    const db = { ...base, prepare(sql: string) { seen.push(sql); return base.prepare(sql); } } as unknown as D1Database;
    expect((await publicSubscription(makeEnv(db), token)).status).toBe(200);
    const q = seen.find((x) => /FROM user_nodes un JOIN nodes n/.test(x))!;
    expect(q).toContain("n.status != 'disabled'");
    expect(q).not.toContain("unreachable");
  });
});
