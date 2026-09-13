import { describe, expect, it } from "vitest";
import { buildSubscriptionURIs, type SubscriptionRow } from "./subscription";
import { buildShadowrocketConfig } from "./shadowrocket";
import { buildClashConfig } from "./clash";
import { buildSingboxConfig } from "./singbox";

// public_ipv6 (migration 0025) adds IPv6 twins of the Reality and HY2 routes.
// They are manual picks: listed in PROXY, never in AUTO or HY2-BACKUP.
const V6 = "2603:c023:19:9800:0:f882:7490:be7a";

const jpy03: SubscriptionRow = {
  vless_uuid: "uuid-jpy03",
  hy2_pw: "pw",
  vpn_host: "edge.example.com",
  public_ip: "129.225.185.197",
  public_ipv6: V6,
  hy2_host: "quic.example.com",
  hy2_port: 32443,
  hy2_obfs_pw: "obfs",
  node_id: "JPY-03",
  mode: "direct",
  reality_pubkey: "pk",
  reality_sid: "sid",
  reality_sni: "www.sony.jp",
  xhttp_path: null
};

// Cloudflare-mode node with HY2 (JPY-01): only the HY2 route gets a twin.
const jpy01: SubscriptionRow = {
  ...jpy03,
  vless_uuid: "uuid-jpy01",
  node_id: "JPY-01",
  mode: "cloudflare",
  public_ip: "45.143.131.36",
  public_ipv6: "2a12:a304:4:8f3::a",
  reality_pubkey: null,
  reality_sid: null,
  reality_sni: null,
  xhttp_path: "/api/v1/sync"
};

describe("IPv6 routes in the base64 list", () => {
  it("adds a bracketed -Reality-v6 after Reality and -HY2-v6 after HY2", () => {
    const lines = buildSubscriptionURIs("kulinh", [jpy03]).split("\n");
    expect(lines.map((l) => decodeURIComponent(l.split("#")[1]))).toEqual([
      "kulinh@JPY-03-Reality",
      "kulinh@JPY-03-Reality-v6",
      "kulinh@JPY-03-HY2",
      "kulinh@JPY-03-HY2-v6"
    ]);
    expect(lines[1]).toMatch(new RegExp(`^vless://uuid-jpy03@\\[${V6}\\]:443\\?`));
    expect(lines[1]).toContain("sni=www.sony.jp");
    expect(lines[3]).toContain(`@[${V6}]:32443/?`);
    expect(lines[3]).toContain("sni=quic.example.com");
    // The IPv4 lines are unchanged.
    expect(lines[0]).toContain("@129.225.185.197:443?");
    expect(lines[2]).toContain("@129.225.185.197:32443/?");
  });

  it("emits nothing extra without public_ipv6", () => {
    for (const v of [null, undefined, ""]) {
      const lines = buildSubscriptionURIs("kulinh", [{ ...jpy03, public_ipv6: v }]).split("\n");
      expect(lines.some((l) => l.includes("-v6"))).toBe(false);
      expect(lines).toHaveLength(2);
    }
  });

  it("gives a cloudflare node only an HY2 twin", () => {
    const names = buildSubscriptionURIs("kulinh", [jpy01]).split("\n").map((l) => decodeURIComponent(l.split("#")[1]));
    expect(names).toEqual(["kulinh@JPY-01-HTTPUpgrade", "kulinh@JPY-01-HY2", "kulinh@JPY-01-HY2-v6"]);
  });
});

describe("IPv6 routes in the grouped formats", () => {
  it("Shadowrocket: PROXY lists the twins, AUTO and HY2-BACKUP do not", () => {
    const conf = buildShadowrocketConfig("kulinh", [jpy01, jpy03]).split("\n");
    const line = (p: string) => conf.find((l) => l.startsWith(p))!;
    expect(line("PROXY = ")).toContain("kulinh@JPY-03-Reality-v6");
    expect(line("PROXY = ")).toContain("kulinh@JPY-01-HY2-v6");
    expect(line("AUTO = ")).not.toContain("-v6");
    expect(line("HY2-BACKUP = ")).toBe("HY2-BACKUP = select, kulinh@JPY-01-HY2, kulinh@JPY-03-HY2");
  });

  it("Clash: the twins dial the bare address and stay out of Auto", () => {
    const yaml = buildClashConfig("kulinh", [jpy03]);
    expect(yaml).toContain(`"kulinh@JPY-03-Reality-v6"`);
    expect(yaml).toContain(`server: "${V6}"`);
    const auto = yaml.slice(yaml.indexOf("proxy-groups:"), yaml.indexOf(`name: "Proxy"`));
    expect(auto).not.toContain("-v6");
    const select = yaml.slice(yaml.indexOf(`name: "Proxy"`));
    expect(select).toContain("kulinh@JPY-03-HY2-v6");
  });

  it("sing-box: outbounds for both twins, PROXY only", () => {
    const cfg = buildSingboxConfig("kulinh", [jpy01, jpy03], { final: "proxy" }) as { outbounds: Array<Record<string, unknown>> };
    const byTag = (t: string) => cfg.outbounds.find((o) => o.tag === t)!;
    expect(byTag("kulinh@JPY-03-Reality-v6").server).toBe(V6);
    expect(byTag("kulinh@JPY-03-HY2-v6").server).toBe(V6);
    expect(byTag("PROXY").outbounds).toContain("kulinh@JPY-01-HY2-v6");
    expect(byTag("HY2-BACKUP").outbounds).toEqual(["kulinh@JPY-01-HY2", "kulinh@JPY-03-HY2"]);
    expect(byTag("AUTO").outbounds as string[]).not.toContain("kulinh@JPY-03-Reality-v6");
  });
});
