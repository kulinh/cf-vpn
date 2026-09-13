import { describe, expect, it } from "vitest";
import { isDnsHostname, isIPv4, isIPv6Literal } from "./hosts";

describe("isDnsHostname", () => {
  it("accepts plain multi-label names", () => {
    for (const h of ["edge-64b43148.dongnat247.com", "a.b", "JPY-03.rwl247.dev", "x1.example.co.uk"]) expect(isDnsHostname(h)).toBe(true);
  });
  it("rejects anything that would break a URI authority or a DNS call", () => {
    for (const h of ["", "localhost", "a b.example.com", "x#y.example.com", "u@h.example.com", "h.example.com:443", "h.example.com/p",
      "h.example.com.", "-a.example.com", "a-.example.com", "a..example.com", `${"a".repeat(64)}.com`, "1.2.3.4:5", undefined, 5]) {
      expect(isDnsHostname(h)).toBe(false);
    }
  });
});

describe("isIPv4", () => {
  it("accepts dotted quads in range only", () => {
    expect(isIPv4("129.225.185.197")).toBe(true);
    for (const v of ["1.2.3.4#x", "256.1.1.1", "1.2.3", "::1", "", null]) expect(isIPv4(v)).toBe(false);
  });
});

describe("isIPv6Literal", () => {
  it("accepts bare IPv6 literals", () => {
    for (const v of ["2603:c023:19:9800:0:f882:7490:be7a", "2a12:a304:4:8f3::a", "::1", "fe80::", "2001:db8:0:0:0:0:0:1"]) expect(isIPv6Literal(v)).toBe(true);
  });
  it("rejects brackets, zones, mapped IPv4 and malformed groups", () => {
    for (const v of ["[2001:db8::1]", "fe80::1%eth0", "::ffff:1.2.3.4", "a:b", "1:2:3:4:5:6:7", ":1:2:3:4:5:6:7", "1::2::3", "12345::1", "g::1", "1:::2", ""]) {
      expect(isIPv6Literal(v)).toBe(false);
    }
  });
});
