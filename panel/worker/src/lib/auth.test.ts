import { describe, expect, it } from "vitest";
import { requireActorEmail } from "./auth";
import type { Env } from "../types";

const env = (over: Partial<Env> = {}) => ({ ...over } as Env);
const basic = (u: string, p: string) => `Basic ${btoa(`${u}:${p}`)}`;

describe("requireActorEmail", () => {
  it("accepts a Cloudflare Access request", () => {
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: {
        "CF-Access-Jwt-Assertion": "jwt",
        "Cf-Access-Authenticated-User-Email": "someone@example.com"
      }
    });
    expect(requireActorEmail(req, env())).toBe("someone@example.com");
  });

  it("rejects an Access request with a JWT but no email", () => {
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: { "CF-Access-Jwt-Assertion": "jwt" }
    });
    expect(requireActorEmail(req, env())).toBeInstanceOf(Response);
  });

  it("rejects everything when no auth method is configured", () => {
    // Access removed and no basic credentials set must fail closed. Falling
    // through to "allow" here would publish the whole admin API.
    const req = new Request("https://cp.example.com/api/nodes");
    const out = requireActorEmail(req, env());
    expect(out).toBeInstanceOf(Response);
    expect((out as Response).status).toBe(401);
  });

  it("accepts correct basic credentials when they are configured", () => {
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: { Authorization: basic("admin", "s3cret") }
    });
    expect(requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBe("admin");
  });

  it("rejects a wrong password and asks the browser to authenticate", async () => {
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: { Authorization: basic("admin", "wrong") }
    });
    const out = requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" })) as Response;
    expect(out.status).toBe(401);
    // Without this header the browser never prompts and the panel is unusable.
    expect(out.headers.get("WWW-Authenticate")).toMatch(/^Basic /);
  });

  it("rejects a wrong username", () => {
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: { Authorization: basic("root", "s3cret") }
    });
    expect(requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBeInstanceOf(Response);
  });

  it("ignores a malformed Authorization header instead of throwing", () => {
    for (const value of ["Basic", "Basic !!!not-base64!!!", "Bearer token", "Basic " + btoa("no-colon")]) {
      const req = new Request("https://cp.example.com/api/nodes", { headers: { Authorization: value } });
      expect(requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBeInstanceOf(Response);
    }
  });

  it("does not accept basic credentials when only one half is configured", () => {
    // A half-configured secret must never turn into "any password works".
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: { Authorization: basic("admin", "") }
    });
    expect(requireActorEmail(req, env({ PANEL_BASIC_USER: "admin" }))).toBeInstanceOf(Response);
    expect(requireActorEmail(req, env({ PANEL_BASIC_PASS: "s3cret" }))).toBeInstanceOf(Response);
  });

  it("still prefers Access when both are available", () => {
    const req = new Request("https://cp.example.com/api/nodes", {
      headers: {
        "CF-Access-Jwt-Assertion": "jwt",
        "Cf-Access-Authenticated-User-Email": "someone@example.com",
        Authorization: basic("admin", "s3cret")
      }
    });
    expect(requireActorEmail(req, env({ PANEL_BASIC_USER: "admin", PANEL_BASIC_PASS: "s3cret" }))).toBe("someone@example.com");
  });
});
