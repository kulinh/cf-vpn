import { describe, expect, it } from "vitest";
import {
  buildHy2URI,
  buildVLESSHTTPUpgradeURI,
  buildVLESSRealityURI,
  buildVLESSXHTTPDirectURI,
  buildVLESSXHTTPURI
} from "./subscription";

// The other half of internal/subscription/subscription_test.go: the same
// inputs, the same literal strings. Before this file only the XHTTP-H3 golden
// was pinned on both sides, so an escaping change in the Worker kept every Go
// test green while panel- and cfvpnctl-provisioned links drifted apart.
// Change a string here only together with its Go twin.
const UUID = "2f8a1c3e-1111-4222-8333-abcdefabcdef";

describe("Worker builders match the Go goldens byte for byte", () => {
  it("TestGoldenRealityURIMatchesWorker", () => {
    expect(buildVLESSRealityURI("alice", UUID, "cdn-a1b2.rwl.one", "www.apple.com", "XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl", "d3cbbc0b4c5bc5f9")).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.apple.com&pbk=XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl&sid=d3cbbc0b4c5bc5f9&fp=chrome#alice-Reality"
    );
  });

  it("TestGoldenRealityURIEscapesLikeWorker", () => {
    expect(buildVLESSRealityURI("alice@hkg-01", UUID, "cdn-a1b2.rwl.one", "www.apple.com", "pbk+/=x", "sid")).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=www.apple.com&pbk=pbk%2B%2F%3Dx&sid=sid&fp=chrome#alice%40hkg-01-Reality"
    );
  });

  it("TestGoldenHTTPUpgradeURIMatchesWorker", () => {
    expect(buildVLESSHTTPUpgradeURI("alice", UUID, "cdn-a1b2.rwl.one", "/api/v1/sync")).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=tls&type=httpupgrade&host=cdn-a1b2.rwl.one&path=%2Fapi%2Fv1%2Fsync&alpn=http%2F1.1&sni=cdn-a1b2.rwl.one#alice-HTTPUpgrade"
    );
  });

  it("TestGoldenHTTPUpgradeURIEscapesFullPath", () => {
    expect(buildVLESSHTTPUpgradeURI("alice@hkg-01", UUID, "cdn-a1b2.rwl.one", "/api/v1/sync?ed=2048")).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-a1b2.rwl.one:443?encryption=none&security=tls&type=httpupgrade&host=cdn-a1b2.rwl.one&path=%2Fapi%2Fv1%2Fsync%3Fed%3D2048&alpn=http%2F1.1&sni=cdn-a1b2.rwl.one#alice%40hkg-01-HTTPUpgrade"
    );
  });

  it("TestGoldenHy2URIMatchesWorker", () => {
    expect(buildHy2URI("alice@hkg-01", "alice", "Zm9vYmFy_-abc", "96.9.228.81", "hy2-c3d4.rwl.one", 24430, "kQ3x")).toBe(
      "hysteria2://alice:Zm9vYmFy_-abc@96.9.228.81:24430/?obfs=salamander&obfs-password=kQ3x&sni=hy2-c3d4.rwl.one&insecure=0#alice%40hkg-01-HY2"
    );
  });

  it("TestGoldenHy2URIEscapesLikeWorker", () => {
    expect(buildHy2URI("alice", "alice", "p@ss w/rd:1+2", "hy2-c3d4.rwl.one", "hy2-c3d4.rwl.one", 24430, "obfs_PW-1~2*3'4(5)!6")).toBe(
      "hysteria2://alice:p%40ss%20w%2Frd%3A1%2B2@hy2-c3d4.rwl.one:24430/?obfs=salamander&obfs-password=obfs_PW-1~2*3'4(5)!6&sni=hy2-c3d4.rwl.one&insecure=0#alice-HY2"
    );
  });

  it("TestGoldenXHTTPURIMatchesWorker", () => {
    expect(buildVLESSXHTTPURI("alice@or-001", UUID, "static-df60bd79.duylinh.org", "/api/v2/stream", "packet-up")).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@static-df60bd79.duylinh.org:443?encryption=none&security=tls&type=xhttp&host=static-df60bd79.duylinh.org&path=%2Fapi%2Fv2%2Fstream&mode=packet-up&alpn=h2%2Chttp%2F1.1&sni=static-df60bd79.duylinh.org#alice%40or-001-XHTTP"
    );
  });

  it("TestGoldenXHTTPDirectURIMatchesWorker", () => {
    expect(buildVLESSXHTTPDirectURI("JPY-01", UUID, "cdn-82169439.duylinh.net", "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10", "stream-one")).toBe(
      "vless://2f8a1c3e-1111-4222-8333-abcdefabcdef@cdn-82169439.duylinh.net:443?encryption=none&security=tls&type=xhttp&host=cdn-82169439.duylinh.net&path=%2F3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10&mode=stream-one&alpn=h2%2Chttp%2F1.1&sni=cdn-82169439.duylinh.net#JPY-01-XHTTP-Direct"
    );
  });
});
