package zones

import (
	"regexp"
	"testing"
)

func TestDefaultPool_HasNineZonesWithUniqueNames(t *testing.T) {
	if len(DefaultPool) != 9 {
		t.Fatalf("DefaultPool size = %d, want 9", len(DefaultPool))
	}
	seen := map[string]bool{}
	for _, z := range DefaultPool {
		if seen[z.Name] {
			t.Errorf("duplicate zone name: %s", z.Name)
		}
		seen[z.Name] = true
	}
}

func TestDefaultPool_CFZoneIDsAre32Hex(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	for _, z := range DefaultPool {
		if !re.MatchString(z.CFZoneID) {
			t.Errorf("zone %s: cf_zone_id %q does not match ^[0-9a-f]{32}$", z.Name, z.CFZoneID)
		}
	}
}

func TestDefaultPool_ContainsExpectedZones(t *testing.T) {
	want := []string{
		"888vn.net", "dongnat247.com", "abony.xyz", "duylinh.org",
		"duylinh.net", "rwl247.dev", "rwl265.com", "rwl265.org", "rwl.one",
	}
	got := map[string]bool{}
	for _, z := range DefaultPool {
		got[z.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("DefaultPool missing %s", name)
		}
	}
}
