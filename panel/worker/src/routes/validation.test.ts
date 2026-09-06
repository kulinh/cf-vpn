import { describe, expect, it } from "vitest";
import { validateAdminHost } from "../lib/hosts";
import { createNode, nodeSync } from "./nodes";
import { createZone } from "./zones";
import type { Env } from "../types";

function makeDBStub(overrides?: {
  first?: unknown;
  runSuccess?: boolean;
  allResults?: unknown[];
}): D1Database {
  const prepared = {
    bind() {
      return this;
    },
    async first() {
      return (overrides?.first ?? null) as any;
    },
    async run() {
      return { success: overrides?.runSuccess ?? true } as any;
    },
    async all() {
      return { results: overrides?.allResults ?? [] } as any;
    }
  } as unknown as D1PreparedStatement;

  return {
    prepare() {
      return prepared;
    },
    async batch() {
      return [] as any;
    },
    async exec() {
      return { count: 0, duration: 0 } as any;
    },
    withSession() {
      return this as any;
    },
    async dump() {
      return new ArrayBuffer(0);
    }
  } as unknown as D1Database;
}

function makeEnv(db?: D1Database): Env {
  return {
    DB: db ?? makeDBStub(),
    ADMIN_HOST_ALLOWED_SUFFIXES: "example.com"
  };
}

describe("validateAdminHost", () => {
  it("rejects localhost and ip literals", () => {
    const env = makeEnv();
    expect(validateAdminHost("localhost", env)).toBeTruthy();
    expect(validateAdminHost("127.0.0.1", env)).toBeTruthy();
    expect(validateAdminHost("10.0.0.1", env)).toBeTruthy();
  });

  it("accepts allowlisted suffix", () => {
    const env = makeEnv();
    expect(validateAdminHost("admin-sg.example.com", env)).toBeNull();
  });

  it("rejects non-allowlisted host", () => {
    const env = makeEnv();
    expect(validateAdminHost("evil.attacker.com", env)).toBeTruthy();
  });
});

describe("route payload validation", () => {
  it("returns 400 on non-object node create body", async () => {
    const env = makeEnv();
    const req = new Request("https://panel.test/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "null"
    });
    const res = await createNode(env, req);
    expect(res.status).toBe(400);
  });

  it("returns 409 on duplicate node", async () => {
    const env = makeEnv(makeDBStub({ first: { id: "sg" } }));
    const req = new Request("https://panel.test/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        id: "sg",
        label: "Singapore",
        admin_host: "admin-sg.example.com",
        vpn_host: "sg.vpn.example.com",
        zone: "example.com"
      })
    });
    const res = await createNode(env, req);
    expect(res.status).toBe(409);
  });

  it("returns 400 on invalid sync payload shape", async () => {
    const env = makeEnv(makeDBStub({ first: { id: "sg", label: "SG", admin_host: "admin-sg.example.com", vpn_host: "sg.vpn.example.com", zone: "example.com", status: "active", last_seen_at: null, created_at: Date.now() } }));
    const req = new Request("https://panel.test/api/nodes/sg/sync", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ users: null })
    });
    const res = await nodeSync(env, "sg", req, "operator@example.com");
    expect(res.status).toBe(400);
  });

  it("returns 400 on non-object zone create body", async () => {
    const env = makeEnv();
    const req = new Request("https://panel.test/api/zones", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "[]"
    });
    const res = await createZone(env, req);
    expect(res.status).toBe(400);
  });

  it("returns 409 on duplicate zone", async () => {
    const env = makeEnv(makeDBStub({ first: { name: "example.com" } }));
    const req = new Request("https://panel.test/api/zones", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ name: "example.com", cf_zone_id: "a".repeat(32) })
    });
    const res = await createZone(env, req);
    expect(res.status).toBe(409);
  });
});
