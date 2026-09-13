import { describe, expect, it } from "vitest";
import { buildSubscriptionURIs, type SubscriptionRow } from "./subscription";
import { AUTO_URL_TEST_OPTS, buildShadowrocketConfig } from "./shadowrocket";
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
      "JPY-03-Reality",
      "JPY-03-Reality-v6",
      "JPY-03-HY2",
      "JPY-03-HY2-v6"
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
    expect(names).toEqual(["JPY-01-HTTPUpgrade", "JPY-01-HY2", "JPY-01-HY2-v6"]);
  });
});

describe("IPv6 routes in the grouped formats", () => {
  it("Shadowrocket: PROXY lists the twins, AUTO and HY2-BACKUP do not", () => {
    const conf = buildShadowrocketConfig("kulinh", [jpy01, jpy03]).split("\n");
    const line = (p: string) => conf.find((l) => l.startsWith(p))!;
    expect(line("PROXY = ")).toContain("JPY-03-Reality-v6");
    expect(line("PROXY = ")).toContain("JPY-01-HY2-v6");
    expect(line("AUTO = ")).not.toContain("-v6");
    expect(line("HY2-BACKUP = ")).toBe("HY2-BACKUP = select, JPY-01-HY2, JPY-03-HY2");
  });

  it("Clash: the twins dial the bare address and stay out of Auto", () => {
    const yaml = buildClashConfig("kulinh", [jpy03]);
    expect(yaml).toContain(`"JPY-03-Reality-v6"`);
    expect(yaml).toContain(`server: "${V6}"`);
    const auto = yaml.slice(yaml.indexOf("proxy-groups:"), yaml.indexOf(`name: "Proxy"`));
    expect(auto).not.toContain("-v6");
    const select = yaml.slice(yaml.indexOf(`name: "Proxy"`));
    expect(select).toContain("JPY-03-HY2-v6");
  });

  it("sing-box: outbounds for both twins, PROXY only", () => {
    const cfg = buildSingboxConfig("kulinh", [jpy01, jpy03], { final: "proxy" }) as { outbounds: Array<Record<string, unknown>> };
    const byTag = (t: string) => cfg.outbounds.find((o) => o.tag === t)!;
    expect(byTag("JPY-03-Reality-v6").server).toBe(V6);
    expect(byTag("JPY-03-HY2-v6").server).toBe(V6);
    expect(byTag("PROXY").outbounds).toContain("JPY-01-HY2-v6");
    expect(byTag("HY2-BACKUP").outbounds).toEqual(["JPY-01-HY2", "JPY-03-HY2"]);
    expect(byTag("AUTO").outbounds as string[]).not.toContain("JPY-03-Reality-v6");
  });
});

// Same fixture and golden as TestBuildUserURIsIPv6TwinsMatchWorker in
// internal/commands/ipv6_test.go: panel and cfvpnctl must emit identical lines.
describe("IPv6 twins match the Go builder", () => {
  it("matches byte for byte", () => {
    const row: SubscriptionRow = {
      vless_uuid: "2f8a1c3e-1111-4222-8333-abcdefabcdef", hy2_pw: "Zm9vYmFy_-abc", vpn_host: "edge-64b43148.dongnat247.com",
      public_ip: "129.225.185.197", public_ipv6: "2603:c023:19:9800:0:f882:7490:be7a",
      hy2_host: "quic-b55170f3.dongnat247.com", hy2_port: 32443, hy2_obfs_pw: "kQ3x", node_id: "JPY-03", mode: "direct",
      reality_pubkey: "XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl", reality_sid: "2441ae2d78da98bb", reality_sni: "www.sony.jp",
      xhttp_path: null, xhttp_h3_host: "quic-b55170f3.dongnat247.com", xhttp_h3_path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10"
    };
    expect(buildSubscriptionURIs("kulinh", [row]).split("\n")).toEqual([
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@129.225.185.197:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.sony.jp&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=2441ae2d78da98bb&fp=chrome#JPY-03-Reality",
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@[2603:c023:19:9800:0:f882:7490:be7a]:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.sony.jp&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=2441ae2d78da98bb&fp=chrome#JPY-03-Reality-v6",
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@quic-b55170f3.dongnat247.com:443?encryption=none&security=tls&type=xhttp&host=quic-b55170f3.dongnat247.com&path=%2F3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10&mode=stream-one&alpn=h3&sni=quic-b55170f3.dongnat247.com#JPY-03-XHTTP-H3",
      "hysteria2://kulinh:Zm9vYmFy_-abc@129.225.185.197:32443/?obfs=salamander&obfs-password=kQ3x&sni=quic-b55170f3.dongnat247.com&insecure=0#JPY-03-HY2",
      "hysteria2://kulinh:Zm9vYmFy_-abc@[2603:c023:19:9800:0:f882:7490:be7a]:32443/?obfs=salamander&obfs-password=kQ3x&sni=quic-b55170f3.dongnat247.com&insecure=0#JPY-03-HY2-v6",
    ]);
  });
});

describe("sing-box AUTO without xhttp", () => {
  it("puts JPY-03-HY2 where Shadowrocket has JPY-03-XHTTP-H3", () => {
    const h3 = { ...jpy03, xhttp_h3_host: "quic.example.com", xhttp_h3_path: "/p" };
    const sr = buildShadowrocketConfig("kulinh", [h3]).split("\n").find((l) => l.startsWith("AUTO = "))!;
    expect(sr).toContain("AUTO = url-test, JPY-03-XHTTP-H3, JPY-03-Reality,");
    const cfg = buildSingboxConfig("kulinh", [h3], { final: "proxy" }) as { outbounds: Array<Record<string, unknown>> };
    const auto = cfg.outbounds.find((o) => o.tag === "AUTO")!;
    expect(auto.outbounds).toEqual(["JPY-03-HY2", "JPY-03-Reality"]);
  });

  it("does not add the HY2 fallback when the node has no HY2", () => {
    const noHy2 = { ...jpy03, hy2_host: null, hy2_port: null, hy2_obfs_pw: null, xhttp_h3_host: "q", xhttp_h3_path: "/p" };
    const cfg = buildSingboxConfig("kulinh", [noHy2], { final: "proxy" }) as { outbounds: Array<Record<string, unknown>> };
    expect(cfg.outbounds.find((o) => o.tag === "AUTO")!.outbounds).toEqual(["JPY-03-Reality"]);
  });
});

describe("public_ipv6 must be a real IPv6 literal (review L1, same rule as Go publicIPv6)", () => {
  it("emits no twins for bracketed, zoned, mapped or malformed values, and trims whitespace", () => {
    for (const v of ["[2001:db8::1]", "fe80::1%eth0", "::ffff:1.2.3.4", "a:b", "129.225.185.197"]) {
      const lines = buildSubscriptionURIs("kulinh", [{ ...jpy03, public_ipv6: v }]).split("\n");
      expect(lines.some((l) => l.includes("-v6"))).toBe(false);
    }
    const lines = buildSubscriptionURIs("kulinh", [{ ...jpy03, public_ipv6: `  ${V6}  ` }]).split("\n");
    expect(lines[1]).toContain(`@[${V6}]:443?`);
    const cfg = buildSingboxConfig("kulinh", [{ ...jpy03, public_ipv6: `  ${V6}\n` }], { final: "proxy" }) as { outbounds: Array<Record<string, unknown>> };
    expect(cfg.outbounds.find((o) => o.tag === "JPY-03-Reality-v6")!.server).toBe(V6);
  });
});

describe("RU profile AUTO (rules=ru)", () => {
  const han = { ...jpy03, node_id: "HAN-01", vless_uuid: "u-han" };
  const naiveJpy01 = { ...jpy01, naive_host: "cdn.example.com", naive_user: "u", naive_pass: "p" };
  it("Shadowrocket: AUTO is REALITY on the RU list only — no HY2, H3, or Cloudflare routes", () => {
    const rows = [han, jpy01, { ...jpy03, xhttp_h3_host: "q.example.com", xhttp_h3_path: "/p" }];
    const ru = buildShadowrocketConfig("kulinh", rows, { rules: "ru" }).split("\n").find((l) => l.startsWith("AUTO = "))!;
    expect(ru).toBe("AUTO = url-test, HAN-01-Reality, " + AUTO_URL_TEST_OPTS);
    const cn = buildShadowrocketConfig("kulinh", rows, { rules: "cn" }).split("\n").find((l) => l.startsWith("AUTO = "))!;
    expect(cn).toContain("JPY-03-XHTTP-H3");
    expect(cn).toContain("JPY-01-HY2");
  });
  it("sing-box: RU AUTO adds JPY-01-Naive after the REALITY members", () => {
    const cfg = buildSingboxConfig("kulinh", [han, naiveJpy01], { final: "proxy", rules: "ru" }) as { outbounds: Array<Record<string, unknown>> };
    expect(cfg.outbounds.find((o) => o.tag === "AUTO")!.outbounds).toEqual(["HAN-01-Reality", "JPY-01-Naive"]);
    const cn = buildSingboxConfig("kulinh", [han, naiveJpy01], { final: "proxy" }) as { outbounds: Array<Record<string, unknown>> };
    expect(cn.outbounds.find((o) => o.tag === "AUTO")!.outbounds).toEqual(["JPY-01-HY2"]);
  });
});
