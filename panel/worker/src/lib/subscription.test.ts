import { describe, expect, it } from "vitest";
import { buildSubscriptionURIs, buildVLESSXHTTPH3URI, type SubscriptionRow } from "./subscription";

// Byte-for-byte the same string as TestGoldenXHTTPH3URIMatchesWorker in
// internal/subscription/subscription_test.go. A user provisioned from the node
// CLI and one provisioned from the panel must get identical links.
//
// alpn=h3 is load-bearing: xray binds the UDP port only when the TLS alpn list
// is exactly ["h3"], and a client that omits it dials TCP, where this route
// has no listener.
describe("buildVLESSXHTTPH3URI", () => {
  it("matches the Go builder byte for byte", () => {
    const got = buildVLESSXHTTPH3URI(
      "kulinh@JPY-03",
      "2f8a1c3e-1111-4222-8333-abcdefabcdef",
      "quic-b55170f3.dongnat247.com",
      "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10",
      "stream-one",
    );
    expect(got).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@quic-b55170f3.dongnat247.com:443?encryption=none&security=tls&type=xhttp&host=quic-b55170f3.dongnat247.com&path=%2F3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10&mode=stream-one&alpn=h3&sni=quic-b55170f3.dongnat247.com#kulinh%40JPY-03-XHTTP-H3",
    );
  });
});

// The order of the lines is part of the contract with the Go side
// (internal/commands/subscription.go buildUserURIs): REALITY, then H3, then
// HY2. A user whose client keeps node order would otherwise see the same
// endpoints in a different sequence depending on where they were provisioned.
describe("buildSubscriptionURIs with an H3 route", () => {
  const directH3Row: SubscriptionRow = {
    vless_uuid: "2f8a1c3e-1111-4222-8333-abcdefabcdef",
    hy2_pw: "Zm9vYmFy_-abc",
    vpn_host: "edge-64b43148.dongnat247.com",
    public_ip: "129.225.185.197",
    hy2_host: "quic-b55170f3.dongnat247.com",
    hy2_port: 32443,
    hy2_obfs_pw: "kQ3x",
    node_id: "JPY-03",
    mode: "direct",
    reality_pubkey: "XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl",
    reality_sid: "2441ae2d78da98bb",
    reality_sni: "www.sony.jp",
    xhttp_path: null,
    xhttp_h3_host: "quic-b55170f3.dongnat247.com",
    xhttp_h3_path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10",
  };

  it("emits REALITY, then H3, then HY2", () => {
    const lines = buildSubscriptionURIs("kulinh", [directH3Row]).split("\n");
    expect(lines).toHaveLength(3);
    expect(lines[0]).toContain("#kulinh%40JPY-03-Reality");
    expect(lines[1]).toContain("#kulinh%40JPY-03-XHTTP-H3");
    expect(lines[2]).toContain("#kulinh%40JPY-03-HY2");
  });

  it("dials the certificate hostname, not the public IP", () => {
    const h3 = buildSubscriptionURIs("kulinh", [directH3Row]).split("\n")[1];
    expect(h3).toContain("@quic-b55170f3.dongnat247.com:443");
    expect(h3).not.toContain("129.225.185.197");
  });

  it("emits nothing extra when only one of host/path is set", () => {
    const half = { ...directH3Row, xhttp_h3_path: null };
    const lines = buildSubscriptionURIs("kulinh", [half]).split("\n");
    expect(lines.some((l) => l.includes("XHTTP-H3"))).toBe(false);
  });

  it("ignores H3 columns on a cloudflare-mode row", () => {
    const cf = { ...directH3Row, mode: "cloudflare" };
    const lines = buildSubscriptionURIs("kulinh", [cf]).split("\n");
    expect(lines.some((l) => l.includes("XHTTP-H3"))).toBe(false);
  });
});
