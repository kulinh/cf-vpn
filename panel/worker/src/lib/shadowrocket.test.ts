import { describe, expect, it } from "vitest";
import { buildShadowrocketConfig, availableNames } from "./shadowrocket";
import type { SubscriptionRow } from "./subscription";

// Fleet shape after the September 2026 refresh: JPY-02/SIN-01 Reality without
// HY2, JPY-01/HKG-01 keep HY2, OR-001 is a cloudflare route without HY2.
function row(node_id: string, mode: "direct" | "cloudflare", hy2: boolean): SubscriptionRow {
  return {
    vless_uuid: `uuid-${node_id}`,
    hy2_pw: `pw-${node_id}`,
    vpn_host: `${node_id.toLowerCase()}.example.com`,
    public_ip: mode === "direct" ? "203.0.113.10" : null,
    node_id,
    mode,
    hy2_host: hy2 ? `hy-${node_id.toLowerCase()}.example.com` : null,
    hy2_port: hy2 ? 31300 : null,
    hy2_obfs_pw: hy2 ? "obfs" : null,
    reality_pubkey: mode === "direct" ? "pk" : null,
    reality_sid: mode === "direct" ? "sid" : null,
    reality_sni: mode === "direct" ? "www.example.com" : null,
    xhttp_path: mode === "cloudflare" ? "/api/v1/sync" : null
  };
}

const fleet: SubscriptionRow[] = [
  row("HAN-01", "direct", false),
  row("HKG-01", "direct", true),
  row("JPY-01", "cloudflare", true),
  row("JPY-02", "direct", false),
  row("JPY-03", "direct", true),
  row("OR-001", "cloudflare", false),
  row("SIN-01", "direct", false),
  row("USA-01", "direct", false)
];

describe("buildShadowrocketConfig", () => {
  it("emits AUTO with the fixed members in the fixed order and the operator's url-test options", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    expect(conf).toContain(
      "[Proxy Group]\nAUTO = url-test, JPY-02-Reality, SIN-01-Reality, JPY-03-Reality, JPY-01-HY2, HKG-01-HY2, OR-001-HTTPUpgrade, url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8\n"
    );
    expect(conf).toMatch(/\[Rule\]\n(#.*\n)*FINAL,DIRECT\n$/);
    expect(conf).toMatch(/^\[General\]\n/);
  });

  it("lists every node in PROXY with AUTO first, HY2 only where the node has it", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    const proxyLine = conf.split("\n").find((l) => l.startsWith("PROXY = select, "));
    expect(proxyLine).toBe(
      "PROXY = select, AUTO, HY2-BACKUP, HAN-01-Reality, HKG-01-Reality, HKG-01-HY2, JPY-01-HTTPUpgrade, JPY-01-HY2, JPY-02-Reality, JPY-03-Reality, JPY-03-HY2, OR-001-HTTPUpgrade, SIN-01-Reality, USA-01-Reality"
    );
  });

  it("emits HY2-BACKUP as a select over every HY2 route, and omits it when there is none", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    expect(conf).toContain("\nHY2-BACKUP = select, HKG-01-HY2, JPY-01-HY2, JPY-03-HY2\n");
    const none = buildShadowrocketConfig("kulinh", [row("SIN-01", "direct", false)]);
    expect(none).not.toContain("HY2-BACKUP");
    expect(none).toContain("PROXY = select, AUTO, SIN-01-Reality");
  });

  it("skips AUTO members the user does not have", () => {
    const conf = buildShadowrocketConfig("kulinh", [row("SIN-01", "direct", false), row("USA-01", "direct", false)]);
    expect(conf).toContain("AUTO = url-test, SIN-01-Reality, url = ");
  });

  it("falls back to a select-only group when no AUTO member exists", () => {
    const conf = buildShadowrocketConfig("kulinh", [row("USA-01", "direct", false)]);
    expect(conf).not.toContain("AUTO");
    expect(conf).toContain("PROXY = select, USA-01-Reality");
    expect(conf).toMatch(/FINAL,DIRECT\n$/);
  });

  it("routes DIRECT for a user with no nodes", () => {
    const conf = buildShadowrocketConfig("kulinh", []);
    expect(conf).toContain("PROXY = select, DIRECT");
    expect(conf).toMatch(/FINAL,DIRECT\n$/);
  });

  it("never names a broken direct node", () => {
    const broken = { ...row("SIN-01", "direct", true), reality_pubkey: null };
    expect(availableNames("kulinh", [broken])).toEqual([]);
  });
});

describe("XHTTP names", () => {
  it("adds <node>-XHTTP to PROXY for cloudflare rows with xhttp_enabled, never to AUTO", () => {
    const conf = buildShadowrocketConfig("kulinh", [{ ...row("OR-001", "cloudflare", false), xhttp_enabled: 1 }, row("SIN-01", "direct", false)]);
    expect(conf).toContain("PROXY = select, AUTO, OR-001-HTTPUpgrade, OR-001-XHTTP, SIN-01-Reality");
    expect(conf).toContain("AUTO = url-test, SIN-01-Reality, OR-001-HTTPUpgrade, url = ");
  });
});

describe("XHTTP-Direct names", () => {
  it("adds <node>-XHTTP-Direct to PROXY only, never to AUTO", () => {
    const conf = buildShadowrocketConfig("kulinh", [{ ...row("JPY-01", "cloudflare", true), xhttp_direct_host: "cdn.example.com", xhttp_direct_path: "/abc" }, row("SIN-01", "direct", false)]);
    expect(conf).toContain("PROXY = select, AUTO, HY2-BACKUP, JPY-01-HTTPUpgrade, JPY-01-XHTTP-Direct, JPY-01-HY2, SIN-01-Reality");
    expect(conf).toContain("AUTO = url-test, SIN-01-Reality, JPY-01-HY2, url = ");
  });
});

describe("[Rule] tail", () => {
  it("defaults to blacklist mode (FINAL,DIRECT) so the CN module decides what is proxied", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    expect(conf).toMatch(/FINAL,DIRECT\n$/);
    expect(conf).not.toContain("FINAL,AUTO");
    expect(conf).not.toContain("FINAL,PROXY");
  });
  it("final=proxy makes everything ride the PROXY group", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet, { final: "proxy" });
    expect(conf).toMatch(/FINAL,PROXY\n$/);
  });
  it("puts the always-proxy hosts ahead of FINAL, exact host or whole zone", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet, { alwaysProxyHosts: ["cp.rwl265.com", ".cloudflareaccess.com"] });
    expect(conf).toContain("[Rule]\nDOMAIN,cp.rwl265.com,PROXY\nDOMAIN-SUFFIX,cloudflareaccess.com,PROXY\n");
    expect(conf.indexOf("DOMAIN,cp.rwl265.com,PROXY")).toBeLessThan(conf.indexOf("FINAL,DIRECT"));
  });
  it("inlines the CN module rules between the always-proxy hosts and FINAL,DIRECT", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet, {
      alwaysProxyHosts: ["cp.rwl265.com"],
      moduleRules: { rules: ["DOMAIN-SUFFIX,google.com,PROXY", "IP-CIDR,8.8.8.0/24,PROXY,no-resolve"], comment: "# sr_proxy_list_CN from x (2 rules)" },
      moduleFallbackURL: "https://example.com/sr_proxy_list_CN.list"
    });
    const i = (s: string) => conf.indexOf(s);
    expect(i("DOMAIN,cp.rwl265.com,PROXY")).toBeGreaterThan(-1);
    expect(i("DOMAIN,cp.rwl265.com,PROXY")).toBeLessThan(i("# sr_proxy_list_CN from x (2 rules)"));
    expect(i("# sr_proxy_list_CN from x (2 rules)")).toBeLessThan(i("DOMAIN-SUFFIX,google.com,PROXY"));
    expect(i("IP-CIDR,8.8.8.0/24,PROXY,no-resolve")).toBeLessThan(i("FINAL,DIRECT"));
    expect(conf).not.toContain("RULE-SET,");
    expect(conf).not.toContain("load the sr_proxy_list_CN module");
    expect(conf).toMatch(/FINAL,DIRECT\n$/);
  });
  it("falls back to a RULE-SET line when the module could not be fetched", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet, { moduleRules: null, moduleFallbackURL: "https://example.com/sr_proxy_list_CN.list" });
    expect(conf).toContain("RULE-SET,https://example.com/sr_proxy_list_CN.list,PROXY\n");
    expect(conf.indexOf("RULE-SET,")).toBeLessThan(conf.indexOf("FINAL,DIRECT"));
  });
  it("leaves the tail bare when neither rules nor a fallback are given (user loads the module)", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    expect(conf).toContain("load the sr_proxy_list_CN (or _UAE) module above this config");
    expect(conf).not.toContain("RULE-SET,");
  });
  it("final=proxy never inlines the module: a full tunnel has no use for it", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet, {
      final: "proxy",
      moduleRules: { rules: ["DOMAIN-SUFFIX,google.com,PROXY"], comment: "# x" },
      moduleFallbackURL: "https://example.com/sr_proxy_list_CN.list"
    });
    expect(conf).not.toContain("google.com");
    expect(conf).not.toContain("RULE-SET,");
    expect(conf).toMatch(/FINAL,PROXY\n$/);
  });
  it("a user with no nodes still gets a valid tail", () => {
    const conf = buildShadowrocketConfig("kulinh", []);
    expect(conf).toContain("PROXY = select, DIRECT");
    expect(conf).toMatch(/FINAL,DIRECT\n$/);
  });
});

// JPY-03 gained an XHTTP-over-H3 route on 2026-09-13. Measured from VNM-01
// (home VNPT line) it beat the same node's REALITY route by 2-3x on
// throughput and TTFB, so it joins AUTO — ahead of that node's REALITY entry,
// which stays as the fallback for when UDP is throttled.
function jpy03WithH3(): SubscriptionRow {
  return {
    ...row("JPY-03", "direct", true),
    xhttp_h3_host: "quic-b55170f3.dongnat247.com",
    xhttp_h3_path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10"
  };
}

const fleetWithH3: SubscriptionRow[] = fleet.map((r) => (r.node_id === "JPY-03" ? jpy03WithH3() : r));

describe("XHTTP-H3 in the Shadowrocket config", () => {
  it("lists the H3 route among the user's nodes", () => {
    expect(availableNames("kulinh", fleetWithH3)).toContain("JPY-03-XHTTP-H3");
  });

  it("puts H3 in AUTO ahead of the same node's REALITY entry", () => {
    const conf = buildShadowrocketConfig("kulinh", fleetWithH3);
    const auto = conf.split("\n").find((l) => l.startsWith("AUTO = "))!;
    expect(auto).toContain("JPY-03-XHTTP-H3");
    expect(auto.indexOf("JPY-03-XHTTP-H3")).toBeLessThan(auto.indexOf("JPY-03-Reality"));
  });

  it("keeps JPY-03 REALITY in AUTO as the fallback when UDP is throttled", () => {
    const auto = buildShadowrocketConfig("kulinh", fleetWithH3).split("\n").find((l) => l.startsWith("AUTO = "))!;
    expect(auto).toContain("JPY-03-Reality");
  });

  // The group must never name a node the subscription does not contain.
  it("omits the H3 member for a user whose JPY-03 row has no H3 route", () => {
    const auto = buildShadowrocketConfig("kulinh", fleet).split("\n").find((l) => l.startsWith("AUTO = "))!;
    expect(auto).not.toContain("XHTTP-H3");
    expect(auto).toContain("JPY-03-Reality");
  });
});
