package tailscale

import (
	"bytes"
	"strings"
	"testing"
)

const samplePolicy = `// Example policy with comments that must survive edits.
{
	"grants": [
		// Allow all connections.
		{"src": ["*"], "dst": ["*"], "ip": ["*"]},
	],
	// Custom DERP regions (cf-vpn).
	"derpMap": {
		"OmitDefaultRegions": false,
		"Regions": {
			"900": {
				"RegionID":   900,
				"RegionCode": "hkg",
				"RegionName": "HKG-01",
				"Nodes": [
					{"Name": "900a", "RegionID": 900, "HostName": "derp-a.example.net", "DERPPort": 8443, "STUNPort": 3478},
				],
			},
		},
	},
	"ssh": [
		{"action": "check", "src": ["autogroup:member"], "dst": ["autogroup:self"], "users": ["autogroup:nonroot", "root"]},
	],
}
`

func mustSummary(t *testing.T, p []byte) Summary {
	t.Helper()
	s, err := Summarize(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSetOmitDefaultRegionsFlipsOnlyThatKey(t *testing.T) {
	on, err := SetOmitDefaultRegions([]byte(samplePolicy), true)
	if err != nil {
		t.Fatal(err)
	}
	if !mustSummary(t, on).OmitDefaultRegions {
		t.Fatal("flag not set")
	}
	off, err := SetOmitDefaultRegions(on, false)
	if err != nil {
		t.Fatal(err)
	}
	if mustSummary(t, off).OmitDefaultRegions {
		t.Fatal("flag not cleared")
	}
	// Comments survive and nothing else changed semantically.
	for _, want := range []string{"// Example policy with comments", "// Allow all connections.", "// Custom DERP regions (cf-vpn)."} {
		if !strings.Contains(string(on), want) || !strings.Contains(string(off), want) {
			t.Fatalf("comment lost: %q", want)
		}
	}
	a, _ := Canonical([]byte(samplePolicy))
	b, _ := Canonical(off)
	if !bytes.Equal(a, b) {
		t.Fatalf("on+off must round-trip to the same policy:\n%s\n%s", a, b)
	}
	c, _ := Canonical(on)
	if bytes.Equal(a, c) || !bytes.Contains(c, []byte(`"OmitDefaultRegions":true`)) {
		t.Fatalf("on must differ only by the flag: %s", c)
	}
	if strings.Count(string(c), "OmitDefaultRegions") != 1 {
		t.Fatal("the key must appear exactly once")
	}
}

func TestSetOmitDefaultRegionsRefusesWithoutRegions(t *testing.T) {
	noRegions := strings.Replace(samplePolicy, `"Regions": {
			"900": {
				"RegionID":   900,
				"RegionCode": "hkg",
				"RegionName": "HKG-01",
				"Nodes": [
					{"Name": "900a", "RegionID": 900, "HostName": "derp-a.example.net", "DERPPort": 8443, "STUNPort": 3478},
				],
			},
		},`, `"Regions": {},`, 1)
	if _, err := SetOmitDefaultRegions([]byte(noRegions), true); err == nil {
		t.Fatal("must refuse to strand the tailnet")
	}
	if _, err := SetOmitDefaultRegions([]byte(`{"grants": []}`), true); err == nil {
		t.Fatal("must refuse when derpMap is absent")
	}
}

func TestAddAndRemoveRegion(t *testing.T) {
	r := Region{ID: 901, Code: "jpy", Name: "JPY-01", HostName: "derp-b.example.net", DERPPort: 8443, STUNPort: 3478}
	added, err := AddRegion([]byte(samplePolicy), r)
	if err != nil {
		t.Fatal(err)
	}
	s := mustSummary(t, added)
	if len(s.Regions) != 2 || s.Regions[1] != r {
		t.Fatalf("regions after add: %+v", s.Regions)
	}
	if !strings.Contains(string(added), "// Custom DERP regions (cf-vpn).") {
		t.Fatal("comment lost on add")
	}
	removed, err := RemoveRegion(added, 901)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Canonical([]byte(samplePolicy))
	b, _ := Canonical(removed)
	if !bytes.Equal(a, b) {
		t.Fatalf("add+remove must round-trip:\n%s\n%s", a, b)
	}
	if _, err := RemoveRegion(added, 902); err == nil {
		t.Fatal("removing an unknown region must fail")
	}
	if _, err := AddRegion([]byte(samplePolicy), Region{ID: 5, Code: "x", Name: "x", HostName: "h", DERPPort: 1, STUNPort: 1}); err == nil {
		t.Fatal("ids below 900 must be rejected")
	}
}

func TestRemoveLastRegionWhileOmittingIsRefused(t *testing.T) {
	on, _ := SetOmitDefaultRegions([]byte(samplePolicy), true)
	if _, err := RemoveRegion(on, 900); err == nil {
		t.Fatal("must refuse")
	}
}

func TestAddRegionCreatesDerpMapWhenAbsent(t *testing.T) {
	out, err := AddRegion([]byte("{\n\t// nothing yet\n\t\"grants\": [],\n}\n"), Region{ID: 900, Code: "hkg", Name: "HKG-01", HostName: "h.example", DERPPort: 8443, STUNPort: 3478})
	if err != nil {
		t.Fatal(err)
	}
	s := mustSummary(t, out)
	if s.OmitDefaultRegions || len(s.Regions) != 1 || s.Regions[0].HostName != "h.example" {
		t.Fatalf("unexpected summary %+v", s)
	}
}
