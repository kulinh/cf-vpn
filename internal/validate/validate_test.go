package validate

import (
	"strings"
	"testing"
)

func TestHostnameAccepts(t *testing.T) {
	for _, ok := range []string{
		"cdn-a1b2.rwl.one",
		"hy2-c3d4.rwl.one",
		"hkg-01.rwl247.dev",
		"example.com",
		"a",
		"www.apple.com",
	} {
		if err := Hostname(ok); err != nil {
			t.Errorf("Hostname(%q) = %v, want nil", ok, err)
		}
	}
}

func TestHostnameRejects(t *testing.T) {
	cases := []string{
		"",
		"evil.example.com\n    service: http://127.0.0.1:22",
		"host\rname.com",
		"host name.com",
		" host.com",
		"host.com ",
		"host.com.",     // trailing dot leaves an empty label
		"-lead.com",     // label may not start with '-'
		"trail-.com",    // ... nor end with one
		"host.com:443",  // port is not part of a hostname
		"http://h.com",  // scheme
		"h.com/path",    // path
		"a..b.com",      // empty label
		strings.Repeat("a", 64) + ".com", // label > 63
		strings.Repeat("a.", 200) + "com", // > 253 bytes
	}
	for _, bad := range cases {
		if err := Hostname(bad); err == nil {
			t.Errorf("Hostname(%q) = nil, want error", bad)
		}
	}
}

func TestUUID(t *testing.T) {
	if err := UUID("2f8a1c3e-1111-4222-8333-abcdefabcdef"); err != nil {
		t.Errorf("valid uuid rejected: %v", err)
	}
	for _, bad := range []string{
		"",
		"../../etc/passwd",
		"2f8a1c3e-1111-4222-8333-abcdefabcde",
		"2f8a1c3e111142228333abcdefabcdef",
		"2f8a1c3e-1111-4222-8333-abcdefabcdef\nservice: x",
		"2f8a1c3e-1111-4222-8333-abcdefabcdeg",
	} {
		if err := UUID(bad); err == nil {
			t.Errorf("UUID(%q) = nil, want error", bad)
		}
	}
}

func TestHexID32(t *testing.T) {
	if err := HexID32("0123456789abcdef0123456789abcdef"); err != nil {
		t.Errorf("valid zone id rejected: %v", err)
	}
	for _, bad := range []string{
		"",
		"0123456789ABCDEF0123456789ABCDEF", // uppercase is not what CF returns
		"0123456789abcdef0123456789abcde",
		"../../accounts/x/tokens",
		"0123456789abcdef0123456789abcdef/../..",
	} {
		if err := HexID32(bad); err == nil {
			t.Errorf("HexID32(%q) = nil, want error", bad)
		}
	}
}
