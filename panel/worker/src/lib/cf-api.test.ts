import { afterEach, describe, expect, it, vi } from "vitest";
import {
  assertCfId,
  cleanupTunnelConnections,
  deleteDnsRecordByName,
  deleteTunnel,
  isCfId,
  isCfTunnelId
} from "./cf-api";
import type { Env } from "../types";

const env = { CF_API_TOKEN: "token", CF_ACCOUNT_ID: "acct" } as Env;

// Payloads verified in the audit to escape the CF API path template: fetch()
// normalises "../", so an unvalidated id turns a scoped DELETE into an
// arbitrary one against anything CF_API_TOKEN can reach.
const TRAVERSAL_PAYLOADS = [
  "../../../zones/VICTIMZONE",
  "abc/../../../../zones/Z",
  "x?foo=1",
  "..%2F..%2Fzones%2FZ",
  "",
  "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"
];

function mockFetch(...responses: unknown[]) {
  const fn = vi.spyOn(globalThis, "fetch");
  for (const r of responses) {
    fn.mockResolvedValueOnce(new Response(JSON.stringify(r), { status: 200 }));
  }
  return fn;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("cf id validation", () => {
  it("accepts a 32-hex id and rejects everything else", () => {
    expect(isCfId("a".repeat(32))).toBe(true);
    for (const bad of TRAVERSAL_PAYLOADS) {
      expect(isCfId(bad)).toBe(false);
    }
    expect(isCfId("A".repeat(32))).toBe(false); // uppercase is not a CF id
    expect(assertCfId.bind(null, "zone", "a".repeat(32))).not.toThrow();
    expect(() => assertCfId("zone", "../../x")).toThrow("invalid_cf_zone_id");
  });

  it("accepts dashed UUIDs and bare hex for tunnel ids only", () => {
    expect(isCfTunnelId("f70ff985-a4ef-4643-bbbc-4a0ed4fc8415")).toBe(true);
    expect(isCfTunnelId("b".repeat(32))).toBe(true);
    for (const bad of TRAVERSAL_PAYLOADS) {
      expect(isCfTunnelId(bad)).toBe(false);
    }
    expect(isCfTunnelId("../f70ff985-a4ef-4643-bbbc-4a0ed4fc8415")).toBe(false);
  });
});

describe("deleteDnsRecordByName", () => {
  it("refuses a traversal zone id before issuing any request", async () => {
    const fetchMock = mockFetch();
    for (const bad of TRAVERSAL_PAYLOADS) {
      await expect(deleteDnsRecordByName(env, bad, "x.example.com", "A")).rejects.toThrow("invalid_cf_zone_id");
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("refuses a traversal record id returned by the API", async () => {
    const zone = "a".repeat(32);
    mockFetch({
      success: true,
      result: [{ id: "../../../zones/VICTIM", name: "x.example.com", type: "A" }]
    });
    await expect(deleteDnsRecordByName(env, zone, "x.example.com", "A")).rejects.toThrow("invalid_cf_record_id");
  });

  it("skips records whose name/type do not match the requested pair", async () => {
    const zone = "a".repeat(32);
    const fetchMock = mockFetch({
      success: true,
      result: [
        { id: "b".repeat(32), name: "other.example.com", type: "A" },
        { id: "c".repeat(32), name: "X.Example.com", type: "A" }
      ]
    });
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ success: true, result: {} }), { status: 200 }));

    await expect(deleteDnsRecordByName(env, zone, "x.example.com", "A")).resolves.toBe(true);

    const deleteCalls = fetchMock.mock.calls.filter((c) => (c[1] as RequestInit)?.method === "DELETE");
    expect(deleteCalls).toHaveLength(1);
    expect(String(deleteCalls[0][0])).toContain(`/zones/${zone}/dns_records/${"c".repeat(32)}`);
  });
});

describe("deleteTunnel / cleanupTunnelConnections", () => {
  it("refuses a traversal tunnel id before issuing any request", async () => {
    const fetchMock = mockFetch();
    for (const bad of TRAVERSAL_PAYLOADS) {
      await expect(deleteTunnel(env, bad)).rejects.toThrow("invalid_cf_tunnel_id");
      await expect(cleanupTunnelConnections(env, bad)).rejects.toThrow("invalid_cf_tunnel_id");
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("targets the account-scoped tunnel path for a valid uuid", async () => {
    const fetchMock = mockFetch({ success: true, result: {} });
    await deleteTunnel(env, "f70ff985-a4ef-4643-bbbc-4a0ed4fc8415");
    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "https://api.cloudflare.com/client/v4/accounts/acct/cfd_tunnel/f70ff985-a4ef-4643-bbbc-4a0ed4fc8415"
    );
  });
});
