package xray

import (
	"testing"
)

func TestGenerateRealityParamsShape(t *testing.T) {
	p, err := GenerateRealityParams(GenerateRealityOptions{
		StubKeypair: &Keypair{Private: "priv", Public: "pub"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.PrivateKey != "priv" {
		t.Errorf("priv: got %q want priv", p.PrivateKey)
	}
	if p.PublicKey != "pub" {
		t.Errorf("pub: got %q want pub", p.PublicKey)
	}
	if len(p.ShortID) != 16 {
		t.Errorf("shortid length: got %d want 16", len(p.ShortID))
	}
	if !isHex(p.ShortID) {
		t.Errorf("shortid not hex: %q", p.ShortID)
	}
	if p.Dest != "www.apple.com:443" {
		t.Errorf("dest: got %q", p.Dest)
	}
	if p.SNI != "www.apple.com" {
		t.Errorf("sni: got %q", p.SNI)
	}
}

func TestGenerateRealityParamsDefaults(t *testing.T) {
	p, err := GenerateRealityParams(GenerateRealityOptions{
		StubKeypair: &Keypair{Private: "x", Public: "y"},
		Dest:        "www.google.com:443",
		SNI:         "www.google.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Dest != "www.google.com:443" {
		t.Errorf("custom dest: %q", p.Dest)
	}
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
