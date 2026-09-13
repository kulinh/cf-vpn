import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import { requireActorEmail, resetAccessKeyCache, verifyAccessJwt } from "./auth";
import type { Env } from "../types";

const env = (over: Partial<Env> = {}) => ({ ...over } as Env);
const basic = (u: string, p: string) => `Basic ${btoa(`${u}:${p}`)}`;

const TEAM = "team.cloudflareaccess.com";
const AUD = "aud-tag-123";
const accessEnv = (over: Partial<Env> = {}) => env({ ACCESS_TEAM_DOMAIN: TEAM, ACCESS_AUD: AUD, ...over });

// A real RS256 key pair, so the tests exercise the actual signature check.
let privateKey: CryptoKey;
let jwks: { keys: JsonWebKey[] };

function b64url(bytes: Uint8Array | string): string {
  const raw = typeof bytes === "string" ? new TextEncoder().encode(bytes) : bytes;
  let bin = "";
  for (const b of raw) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function sign(payload: Record<string, unknown>, header: Record<string, unknown> = { alg: "RS256", kid: "k1" }, key = privateKey): Promise<string> {
  const input = `${b64url(JSON.stringify(header))}.${b64url(JSON.stringify(payload))}`;
  const sig = new Uint8Array(await crypto.subtle.sign("RSASSA-PKCS1-v1_5", key, new TextEncoder().encode(input)));
  return `${input}.${b64url(sig)}`;
}

const now = () => Math.floor(Date.now() / 1000);
const goodClaims = () => ({ aud: [AUD], iss: `https://${TEAM}`, exp: now() + 300, iat: now(), email: "someone@example.com" });

let certFetches = 0;
const fetcher = (async (url: string) => {
  certFetches += 1;
  expect(url).toBe(`https://${TEAM}/cdn-cgi/access/certs`);
  return new Response(JSON.stringify(jwks), { status: 200 });
}) as unknown as typeof fetch;

beforeAll(async () => {
  const pair = await crypto.subtle.generateKey(
    { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
    true, ["sign", "verify"]
  ) as CryptoKeyPair;
  privateKey = pair.privateKey;
  const pub = await crypto.subtle.exportKey("jwk", pair.publicKey) as JsonWebKey;
  jwks = { keys: [{ ...pub, kid: "k1", alg: "RS256", use: "sig" } as JsonWebKey] };
});

beforeEach(() => {
  resetAccessKeyCache();
  certFetches = 0;
});

const accessReq = (jwt: string, extra: Record<string, string> = {}) =>
  new Request("https://cp.example.com/api/nodes", {
    headers: { "CF-Access-Jwt-Assertion": jwt, "Cf-Access-Authenticated-User-Email": "header-email@example.com", ...extra }
  });

describe("requireActorEmail with Cloudflare Access configured", () => {
  it("accepts a verified token and takes the email from the token, not the header", async () => {
    expect(await requireActorEmail(accessReq(await sign(goodClaims())), accessEnv(), fetcher)).toBe("someone@example.com");
  });

  it("rejects a forged token signed by another key", async () => {
    const other = await crypto.subtle.generateKey(
      { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
      true, ["sign", "verify"]
    ) as CryptoKeyPair;
    const out = await requireActorEmail(accessReq(await sign(goodClaims(), undefined, other.privateKey)), accessEnv(), fetcher);
    expect((out as Response).status).toBe(401);
  });

  it("rejects a junk assertion", async () => {
    for (const jwt of ["x", "a.b.c", "jwt"]) {
      expect(await requireActorEmail(accessReq(jwt), accessEnv(), fetcher)).toBeInstanceOf(Response);
    }
  });

  it("rejects a wrong audience, a wrong issuer, an expired token and a missing email", async () => {
    const cases = [
      { ...goodClaims(), aud: ["other-app"] },
      { ...goodClaims(), iss: "https://evil.cloudflareaccess.com" },
      { ...goodClaims(), exp: now() - 3600 },
      { ...goodClaims(), nbf: now() + 3600 },
      { ...goodClaims(), email: undefined }
    ];
    for (const claims of cases) {
      expect(await verifyAccessJwt(await sign(claims), accessEnv(), fetcher)).toBeNull();
    }
  });

  it("rejects alg=none and HS256 headers", async () => {
    const [, p] = (await sign(goodClaims())).split(".");
    expect(await verifyAccessJwt(`${b64url(JSON.stringify({ alg: "none", kid: "k1" }))}.${p}.`, accessEnv(), fetcher)).toBeNull();
    expect(await verifyAccessJwt(await sign(goodClaims(), { alg: "HS256", kid: "k1" }), accessEnv(), fetcher)).toBeNull();
  });

  it("caches the key set and refetches once for an unknown kid", async () => {
    const jwt = await sign(goodClaims());
    await verifyAccessJwt(jwt, accessEnv(), fetcher);
    await verifyAccessJwt(jwt, accessEnv(), fetcher);
    expect(certFetches).toBe(1);
    expect(await verifyAccessJwt(await sign(goodClaims(), { alg: "RS256", kid: "rotated" }), accessEnv(), fetcher)).toBeNull();
    expect(certFetches).toBe(2);
  });

  it("fails closed when the key set cannot be fetched", async () => {
    const down = (async () => new Response("nope", { status: 503 })) as unknown as typeof fetch;
    expect(await requireActorEmail(accessReq(await sign(goodClaims())), accessEnv(), down)).toBeInstanceOf(Response);
  });

  it("does not fall back to basic auth when an Access token is presented but invalid", async () => {
    const req = accessReq("forged", { Authorization: basic("admin", "s3cret") });
    const out = await requireActorEmail(req, accessEnv({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }), fetcher);
    expect((out as Response).status).toBe(401);
  });
});

describe("requireActorEmail without Access configured", () => {
  it("ignores forged Access headers and requires basic auth (review C1)", async () => {
    const forged = accessReq("anything");
    const out = await requireActorEmail(forged, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }), fetcher);
    expect((out as Response).status).toBe(401);
    expect((out as Response).headers.get("WWW-Authenticate")).toMatch(/^Basic /);
    expect(certFetches).toBe(0);
  });

  it("ignores forged Access headers when only one Access var is set", async () => {
    for (const e of [env({ ACCESS_TEAM_DOMAIN: TEAM }), env({ ACCESS_AUD: AUD })]) {
      expect(await requireActorEmail(accessReq("anything"), e, fetcher)).toBeInstanceOf(Response);
    }
  });

  it("uses basic auth even when Access headers ride along", async () => {
    const req = accessReq("anything", { Authorization: basic("admin", "s3cret") });
    expect(await requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }), fetcher)).toBe("admin");
  });

  it("rejects everything when no auth method is configured", async () => {
    // Access removed and no basic credentials set must fail closed. Falling
    // through to "allow" here would publish the whole admin API.
    const out = await requireActorEmail(new Request("https://cp.example.com/api/nodes"), env());
    expect(out).toBeInstanceOf(Response);
    expect((out as Response).status).toBe(401);
  });

  it("accepts correct basic credentials when they are configured", async () => {
    const req = new Request("https://cp.example.com/api/nodes", { headers: { Authorization: basic("admin", "s3cret") } });
    expect(await requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBe("admin");
  });

  it("rejects a wrong password and asks the browser to authenticate", async () => {
    const req = new Request("https://cp.example.com/api/nodes", { headers: { Authorization: basic("admin", "wrong") } });
    const out = await requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" })) as Response;
    expect(out.status).toBe(401);
    // Without this header the browser never prompts and the panel is unusable.
    expect(out.headers.get("WWW-Authenticate")).toMatch(/^Basic /);
  });

  it("rejects a wrong username", async () => {
    const req = new Request("https://cp.example.com/api/nodes", { headers: { Authorization: basic("root", "s3cret") } });
    expect(await requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBeInstanceOf(Response);
  });

  it("ignores a malformed Authorization header instead of throwing", async () => {
    for (const value of ["Basic", "Basic !!!not-base64!!!", "Bearer token", "Basic " + btoa("no-colon")]) {
      const req = new Request("https://cp.example.com/api/nodes", { headers: { Authorization: value } });
      expect(await requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBeInstanceOf(Response);
    }
  });

  it("does not accept basic credentials when only one half is configured", async () => {
    // A half-configured secret must never turn into "any password works".
    const req = new Request("https://cp.example.com/api/nodes", { headers: { Authorization: basic("admin", "") } });
    expect(await requireActorEmail(req, env({ PANEL_BASIC_USER: "admin" }))).toBeInstanceOf(Response);
    expect(await requireActorEmail(req, env({ PANEL_BASIC_PASS: "s3cret" }))).toBeInstanceOf(Response);
  });
});
