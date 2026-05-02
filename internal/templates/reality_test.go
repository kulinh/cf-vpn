package templates

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func normJSON(t *testing.T, s string) string {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unmarshal: %v\ninput: %s", err, s)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestRenderXrayDirectReality(t *testing.T) {
	in := XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "alice", UUID: "uuid-alice"}},
		PrivateKey:  "priv-x25519",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	}
	out, err := RenderXrayDirectReality(in)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`"flow": "xtls-rprx-vision"`,
		`"security": "reality"`,
		`"dest": "www.microsoft.com:443"`,
		`"privateKey": "priv-x25519"`,
		`"shortIds"`,
		"d3cbbc0b4c5bc5f9",
		"alice@vpn",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRenderXrayDirectRealityValidation(t *testing.T) {
	base := XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "alice", UUID: "uuid-alice"}},
		PrivateKey:  "priv-x25519",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	}

	cases := []struct {
		name string
		mut  func(*XrayDirectRealityInputs)
	}{
		{"empty PrivateKey", func(in *XrayDirectRealityInputs) { in.PrivateKey = "" }},
		{"empty Dest", func(in *XrayDirectRealityInputs) { in.Dest = "" }},
		{"empty ServerNames", func(in *XrayDirectRealityInputs) { in.ServerNames = nil }},
		{"empty ShortIDs", func(in *XrayDirectRealityInputs) { in.ShortIDs = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mut(&in)
			if _, err := RenderXrayDirectReality(in); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestRenderXrayDirectRealityMatchesJPY02(t *testing.T) {
	golden, err := os.ReadFile("testdata/jpy02_reality_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	in := XrayDirectRealityInputs{
		Users:       []XrayUser{{Name: "kulinh", UUID: "b3252cd7-d1c5-4f7f-b257-ef750ac838c9"}},
		PrivateKey:  "QPTgOMQeFazVzKeLaHafW89CpJN6mQoPZl9Lsi66I34",
		ShortIDs:    []string{"d3cbbc0b4c5bc5f9"},
		Dest:        "www.microsoft.com:443",
		ServerNames: []string{"www.microsoft.com"},
	}

	out, err := RenderXrayDirectReality(in)
	if err != nil {
		t.Fatal(err)
	}

	got := normJSON(t, out)
	want := normJSON(t, string(golden))

	if got != want {
		t.Fatalf("renderer output does not match golden file.\n\nGOT:\n%s\n\nWANT:\n%s", got, want)
	}
}
