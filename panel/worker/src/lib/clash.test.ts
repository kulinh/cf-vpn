import { describe, expect, it } from "vitest";
import { buildClashConfig, yamlString } from "./clash";
import { buildSubscriptionURIs, type SubscriptionRow } from "./subscription";

const realityRow: SubscriptionRow = {
  vless_uuid: "11111111-2222-3333-4444-555555555555",
  hy2_pw: "hy2-pass",
  vpn_host: "sg.example.com",
  hy2_host: "udp-sg.example.com",
  hy2_port: 30000,
  hy2_obfs_pw: "obfs-secret",
  node_id: "SIN-01",
  mode: "direct",
  reality_pubkey: "pubkey-x25519",
  reality_sid: "abcd1234",
  reality_sni: "www.apple.com",
  xhttp_path: null
};

const cloudflareRow: SubscriptionRow = {
  vless_uuid: "66666666-7777-8888-9999-000000000000",
  hy2_pw: "hy2-pass-2",
  vpn_host: "cf.example.com",
  hy2_host: null,
  hy2_port: null,
  hy2_obfs_pw: null,
  node_id: "CHN-01",
  mode: "cloudflare",
  reality_pubkey: null,
  reality_sid: null,
  reality_sni: null,
  xhttp_path: "/api/v1/sync"
};

describe("yamlString", () => {
  it("double-quotes and escapes backslash and quote", () => {
    expect(yamlString("plain")).toBe('"plain"');
    expect(yamlString('a"b')).toBe('"a\\"b"');
    expect(yamlString("a\\b")).toBe('"a\\\\b"');
    // Values that would otherwise be misparsed bare.
    expect(yamlString("MATCH,Proxy")).toBe('"MATCH,Proxy"');
    expect(yamlString("kulinh:pw")).toBe('"kulinh:pw"');
  });

  it("escapes C0 controls so they cannot end the scalar mid-line", () => {
    // A newline inside an obfs password or node id would otherwise split the
    // scalar and produce an unparseable document.
    expect(yamlString("a\nb")).toBe('"a\\u000ab"');
    expect(yamlString("a\tb")).toBe('"a\\u0009b"');
    expect(yamlString("a\u0000b")).toBe('"a\\u0000b"');
    expect(yamlString("a\u007fb")).toBe('"a\\u007fb"');
    // Non-C0 unicode is left alone.
    expect(yamlString("Việt")).toBe('"Việt"');
  });
});

describe("buildClashConfig", () => {
  it("renders a Reality + HY2 node", () => {
    expect(buildClashConfig("kulinh", [realityRow])).toBe(`proxies:
  - name: "kulinh@SIN-01-Reality"
    type: "vless"
    server: "sg.example.com"
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    network: "tcp"
    tls: true
    udp: true
    flow: "xtls-rprx-vision"
    servername: "www.apple.com"
    client-fingerprint: "chrome"
    reality-opts:
      public-key: "pubkey-x25519"
      short-id: "abcd1234"
  - name: "kulinh@SIN-01-HY2"
    type: "hysteria2"
    server: "udp-sg.example.com"
    port: 30000
    password: "kulinh:hy2-pass"
    sni: "udp-sg.example.com"
    obfs: "salamander"
    obfs-password: "obfs-secret"
proxy-groups:
  - name: "Auto"
    type: "url-test"
    url: "http://www.gstatic.com/generate_204"
    interval: 300
    tolerance: 100
    proxies:
      - "kulinh@SIN-01-Reality"
      - "kulinh@SIN-01-HY2"
  - name: "Proxy"
    type: "select"
    proxies:
      - "Auto"
      - "kulinh@SIN-01-Reality"
      - "kulinh@SIN-01-HY2"
rules:
  - "MATCH,Proxy"
`);
  });

  it("renders an HTTPUpgrade (cloudflare mode) node", () => {
    expect(buildClashConfig("kulinh", [cloudflareRow])).toBe(`proxies:
  - name: "kulinh@CHN-01-HTTPUpgrade"
    type: "vless"
    server: "cf.example.com"
    port: 443
    uuid: "66666666-7777-8888-9999-000000000000"
    tls: true
    udp: true
    servername: "cf.example.com"
    network: "ws"
    ws-opts:
      path: "/api/v1/sync"
      headers:
        Host: "cf.example.com"
      v2ray-http-upgrade: true
proxy-groups:
  - name: "Auto"
    type: "url-test"
    url: "http://www.gstatic.com/generate_204"
    interval: 300
    tolerance: 100
    proxies:
      - "kulinh@CHN-01-HTTPUpgrade"
  - name: "Proxy"
    type: "select"
    proxies:
      - "Auto"
      - "kulinh@CHN-01-HTTPUpgrade"
rules:
  - "MATCH,Proxy"
`);
  });

  it("names every proxy exactly like the base64 URI fragment for the same row", () => {
    const yaml = buildClashConfig("kulinh", [realityRow, cloudflareRow]);
    const fragments = buildSubscriptionURIs("kulinh", [realityRow, cloudflareRow])
      .split("\n")
      .map((uri) => decodeURIComponent(uri.slice(uri.indexOf("#") + 1)));

    expect(fragments).toEqual([
      "kulinh@SIN-01-Reality",
      "kulinh@SIN-01-HY2",
      "kulinh@CHN-01-HTTPUpgrade"
    ]);
    for (const name of fragments) {
      expect(yaml).toContain(`name: ${JSON.stringify(name)}`);
    }
  });

  it("skips a direct node missing Reality params, exactly like the base64 body", () => {
    const broken: SubscriptionRow = { ...realityRow, reality_pubkey: null, mode: null };
    expect(buildSubscriptionURIs("kulinh", [broken])).toBe("");
    expect(buildClashConfig("kulinh", [broken])).toBe(`proxies: []
proxy-groups: []
rules:
  - "MATCH,DIRECT"
`);
  });
});
