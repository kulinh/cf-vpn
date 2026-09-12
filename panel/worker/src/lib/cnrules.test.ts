import { describe, expect, it } from "vitest";
import { DEFAULT_CN_RULES_URL, fetchCNRules, parseModuleRules } from "./cnrules";

const moduleText = `#!name=GFW Optimized Proxy List (CN) 2026
#!desc=...
# ============================================================================
#  HƯỚNG DẪN DÙNG
# ============================================================================

[Rule]
# --- Google ---
DOMAIN-SUFFIX,google.com,PROXY
DOMAIN-SUFFIX, googleapis.com , PROXY
domain-suffix,gstatic.com,Proxy
DOMAIN,one.one.one.one,PROXY
DOMAIN-KEYWORD,google,PROXY
IP-CIDR,8.8.8.0/24,PROXY,no-resolve
IP-CIDR,2001:4860::/32,PROXY,no-resolve
IP-CIDR,1.1.1.1/32,SomeOtherPolicy
DOMAIN-SUFFIX,google.com,PROXY
USER-AGENT,Telegram*,PROXY
RULE-SET,https://example.com/x.list,PROXY
FINAL,DIRECT
`;

describe("parseModuleRules", () => {
  it("keeps only rule lines, retargets every policy to PROXY, keeps no-resolve and drops duplicates", () => {
    expect(parseModuleRules(moduleText)).toEqual([
      "DOMAIN-SUFFIX,google.com,PROXY",
      "DOMAIN-SUFFIX,googleapis.com,PROXY",
      "DOMAIN-SUFFIX,gstatic.com,PROXY",
      "DOMAIN,one.one.one.one,PROXY",
      "DOMAIN-KEYWORD,google,PROXY",
      "IP-CIDR,8.8.8.0/24,PROXY,no-resolve",
      "IP-CIDR,2001:4860::/32,PROXY,no-resolve",
      "IP-CIDR,1.1.1.1/32,PROXY",
      "USER-AGENT,Telegram*,PROXY"
    ]);
  });
  it("never lets a FINAL or RULE-SET from the module into the config", () => {
    const rules = parseModuleRules(moduleText).join("\n");
    expect(rules).not.toContain("FINAL");
    expect(rules).not.toContain("RULE-SET");
  });
  it("handles CRLF and an empty file", () => {
    expect(parseModuleRules("[Rule]\r\nDOMAIN,a.b,PROXY\r\n")).toEqual(["DOMAIN,a.b,PROXY"]);
    expect(parseModuleRules("")).toEqual([]);
  });
});

describe("fetchCNRules", () => {
  it("fetches the module through the edge cache and describes the result", async () => {
    let seen: { url: string; init?: RequestInit } | undefined;
    const got = await fetchCNRules(DEFAULT_CN_RULES_URL, async (url, init) => {
      seen = { url, init };
      return new Response(moduleText, { status: 200, headers: { etag: 'W/"0123456789abcdef"' } });
    });
    expect(seen?.url).toBe(DEFAULT_CN_RULES_URL);
    expect((seen?.init as { cf?: { cacheTtl?: number } })?.cf?.cacheTtl).toBe(3600);
    expect(got?.rules.length).toBe(9);
    expect(got?.comment).toMatch(/^# sr_proxy_list_CN from https:\/\/raw\.githubusercontent\.com\/.* \(9 rules, etag 0123456789ab, fetched \d{4}-\d{2}-\d{2}\)$/);
  });
  it("returns null on a non-2xx, on an empty module, and on a thrown fetch", async () => {
    expect(await fetchCNRules("u", async () => new Response("nope", { status: 404 }))).toBeNull();
    expect(await fetchCNRules("u", async () => new Response("# only comments\n[Rule]\n"))).toBeNull();
    expect(
      await fetchCNRules("u", async () => {
        throw new Error("boom");
      })
    ).toBeNull();
  });
});
