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
  row("OR-001", "cloudflare", false),
  row("SIN-01", "direct", false),
  row("USA-01", "direct", false)
];

describe("buildShadowrocketConfig", () => {
  it("emits AUTO with the fixed members in the fixed order and the operator's url-test options", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    expect(conf).toContain(
      "[Proxy Group]\nAUTO = url-test, kulinh@JPY-02-Reality, kulinh@SIN-01-Reality, kulinh@JPY-01-HY2, kulinh@HKG-01-HY2, kulinh@OR-001-HTTPUpgrade, url = http://cp.cloudflare.com/generate_204, interval = 600, tolerance = 500, timeout = 8\n"
    );
    expect(conf).toContain("\n[Rule]\nFINAL,AUTO\n");
    expect(conf).toMatch(/^\[General\]\n/);
  });

  it("lists every node in PROXY with AUTO first, HY2 only where the node has it", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    const proxyLine = conf.split("\n").find((l) => l.startsWith("PROXY = select, "));
    expect(proxyLine).toBe(
      "PROXY = select, AUTO, HY2-BACKUP, kulinh@HAN-01-Reality, kulinh@HKG-01-Reality, kulinh@HKG-01-HY2, kulinh@JPY-01-HTTPUpgrade, kulinh@JPY-01-HY2, kulinh@JPY-02-Reality, kulinh@OR-001-HTTPUpgrade, kulinh@SIN-01-Reality, kulinh@USA-01-Reality"
    );
  });

  it("emits HY2-BACKUP as a select over every HY2 route, and omits it when there is none", () => {
    const conf = buildShadowrocketConfig("kulinh", fleet);
    expect(conf).toContain("\nHY2-BACKUP = select, kulinh@HKG-01-HY2, kulinh@JPY-01-HY2\n");
    const none = buildShadowrocketConfig("kulinh", [row("SIN-01", "direct", false)]);
    expect(none).not.toContain("HY2-BACKUP");
    expect(none).toContain("PROXY = select, AUTO, kulinh@SIN-01-Reality");
  });

  it("skips AUTO members the user does not have", () => {
    const conf = buildShadowrocketConfig("kulinh", [row("SIN-01", "direct", false), row("USA-01", "direct", false)]);
    expect(conf).toContain("AUTO = url-test, kulinh@SIN-01-Reality, url = ");
  });

  it("falls back to a select-only group when no AUTO member exists", () => {
    const conf = buildShadowrocketConfig("kulinh", [row("USA-01", "direct", false)]);
    expect(conf).not.toContain("AUTO");
    expect(conf).toContain("PROXY = select, kulinh@USA-01-Reality");
    expect(conf).toContain("FINAL,PROXY");
  });

  it("routes DIRECT for a user with no nodes", () => {
    const conf = buildShadowrocketConfig("kulinh", []);
    expect(conf).toContain("PROXY = select, DIRECT");
    expect(conf).toContain("FINAL,PROXY");
  });

  it("never names a broken direct node", () => {
    const broken = { ...row("SIN-01", "direct", true), reality_pubkey: null };
    expect(availableNames("kulinh", [broken])).toEqual([]);
  });
});

describe("XHTTP names", () => {
  it("adds <node>-XHTTP to PROXY for cloudflare rows with xhttp_enabled, never to AUTO", () => {
    const conf = buildShadowrocketConfig("kulinh", [{ ...row("OR-001", "cloudflare", false), xhttp_enabled: 1 }, row("SIN-01", "direct", false)]);
    expect(conf).toContain("PROXY = select, AUTO, kulinh@OR-001-HTTPUpgrade, kulinh@OR-001-XHTTP, kulinh@SIN-01-Reality");
    expect(conf).toContain("AUTO = url-test, kulinh@SIN-01-Reality, kulinh@OR-001-HTTPUpgrade, url = ");
  });
});
