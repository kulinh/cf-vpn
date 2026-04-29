package zones

import (
	"bytes"
	"crypto/rand"
	"errors"
	"regexp"
	"testing"
)

func TestPrefixes_FiveEntries(t *testing.T) {
	want := []string{"cdn", "static", "assets", "edge", "media"}
	if len(Prefixes) != len(want) {
		t.Fatalf("Prefixes size = %d, want %d", len(Prefixes), len(want))
	}
	for i, p := range want {
		if Prefixes[i] != p {
			t.Errorf("Prefixes[%d] = %q, want %q", i, Prefixes[i], p)
		}
	}
}

func TestGenerateHost_Deterministic(t *testing.T) {
	// First byte selects prefix index (5 → 0..4 via modulo).
	// Next 4 bytes become 8 hex chars.
	rng := bytes.NewReader([]byte{0x00, 0xa7, 0xf3, 0xc9, 0x1b})
	got, err := GenerateHost(rng, "duylinh.org")
	if err != nil {
		t.Fatal(err)
	}
	if got != "cdn-a7f3c91b.duylinh.org" {
		t.Fatalf("got %q want %q", got, "cdn-a7f3c91b.duylinh.org")
	}
}

func TestGenerateHostWith_CustomPrefixes(t *testing.T) {
	rng := bytes.NewReader([]byte{0, 0xab, 0xcd, 0xef, 0x12})
	got, err := GenerateHostWith(rng, "example.com", []string{"quic", "udp", "hy"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "quic-abcdef12.example.com" {
		t.Fatalf("got %q want %q", got, "quic-abcdef12.example.com")
	}
}

func TestGenerateHost_PrefixSelection(t *testing.T) {
	cases := []struct {
		first byte
		want  string
	}{
		{0, "cdn"}, {1, "static"}, {2, "assets"}, {3, "edge"}, {4, "media"},
		{5, "cdn"}, {9, "media"}, // wraps via modulo
	}
	for _, c := range cases {
		rng := bytes.NewReader([]byte{c.first, 0, 0, 0, 0})
		got, err := GenerateHost(rng, "z.com")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want+"-00000000.z.com" {
			t.Errorf("first byte %d: got %q, want prefix %q", c.first, got, c.want)
		}
	}
}

func TestGenerateHost_FormatRegex(t *testing.T) {
	re := regexp.MustCompile(`^(cdn|static|assets|edge|media)-[0-9a-f]{8}\.example\.com$`)
	for i := 0; i < 100; i++ {
		got, err := GenerateHost(rand.Reader, "example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(got) {
			t.Errorf("iteration %d: %q does not match format", i, got)
		}
	}
}

func TestGenerateHost_RNGError(t *testing.T) {
	// Empty reader → io.EOF on first read.
	if _, err := GenerateHost(bytes.NewReader(nil), "z.com"); err == nil {
		t.Fatal("expected error from empty rng")
	}
}

func TestPickZone_NoExclude(t *testing.T) {
	candidates := []Zone{
		{Name: "a.com", CFZoneID: "11111111111111111111111111111111"},
		{Name: "b.com", CFZoneID: "22222222222222222222222222222222"},
	}
	rng := bytes.NewReader([]byte{0x01}) // index 1 % 2 = 1
	got, err := PickZone(rng, candidates, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "b.com" {
		t.Errorf("got %s want b.com", got.Name)
	}
}

func TestPickZone_ExcludesCurrent(t *testing.T) {
	candidates := []Zone{
		{Name: "a.com"}, {Name: "b.com"}, {Name: "c.com"},
	}
	for i := 0; i < 100; i++ {
		got, err := PickZone(rand.Reader, candidates, "b.com")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name == "b.com" {
			t.Fatalf("iteration %d: PickZone returned excluded zone", i)
		}
	}
}

func TestPickZone_OnlyExcludedReturnsErr(t *testing.T) {
	candidates := []Zone{{Name: "only.com"}}
	if _, err := PickZone(rand.Reader, candidates, "only.com"); !errors.Is(err, ErrNoEligibleZones) {
		t.Fatalf("got %v, want ErrNoEligibleZones", err)
	}
}

func TestPickZone_EmptyReturnsErr(t *testing.T) {
	if _, err := PickZone(rand.Reader, nil, ""); !errors.Is(err, ErrNoEligibleZones) {
		t.Fatalf("got %v, want ErrNoEligibleZones", err)
	}
}
