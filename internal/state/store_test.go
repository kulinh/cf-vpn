package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfvpn.env")

	s := map[string]string{"DOMAIN": "vpn.example.com", "TUNNEL_UUID": "abc"}
	if err := SaveAtomic(path, s, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded["DOMAIN"] != "vpn.example.com" || loaded["TUNNEL_UUID"] != "abc" {
		t.Fatalf("unexpected loaded map: %#v", loaded)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected mode: %o", info.Mode().Perm())
	}
}

func TestSaveAtomicOverwriteKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfvpn.env")

	if err := SaveAtomic(path, map[string]string{"A": "1"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAtomic(path, map[string]string{"A": "2", "B": "3"}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "2" || got["B"] != "3" {
		t.Fatalf("unexpected values: %#v", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode drifted: %o", info.Mode().Perm())
	}
	// Temp file should not linger.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file leaked: %v", err)
	}
}

// H7: a value with an embedded newline used to be written verbatim; Load is
// line-based and last-wins, so DOMAIN could append its own
// AGENT_SHARED_SECRET= line and take over the agent's auth at next restart.
func TestSaveAtomicRejectsNewlineInValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfvpn.env")
	if err := SaveAtomic(path, map[string]string{"AGENT_SHARED_SECRET": "realsecret"}, 0o600); err != nil {
		t.Fatal(err)
	}

	inject := map[string]string{
		"AGENT_SHARED_SECRET": "realsecret",
		"DOMAIN":              "cdn-aaaa.rwl.one\nAGENT_SHARED_SECRET=attackerchosen",
	}
	if err := SaveAtomic(path, inject, 0o600); err == nil {
		t.Fatal("expected SaveAtomic to reject a newline in a value")
	}

	// The previous file must be untouched.
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["AGENT_SHARED_SECRET"] != "realsecret" {
		t.Fatalf("secret was overwritten: %#v", got)
	}
	if _, ok := got["DOMAIN"]; ok {
		t.Fatalf("partial write leaked: %#v", got)
	}
}

func TestSaveAtomicRejectsCarriageReturnAndNUL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfvpn.env")
	for _, bad := range []string{"a\rB=c", "a\x00b"} {
		if err := SaveAtomic(path, map[string]string{"DOMAIN": bad}, 0o600); err == nil {
			t.Errorf("expected rejection of value %q", bad)
		}
	}
}

func TestSaveAtomicRejectsInvalidKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfvpn.env")
	for _, bad := range []string{"", "1LEADING_DIGIT", "HAS SPACE", "HAS=EQUALS", "HAS\nNEWLINE", "lower-dash"} {
		if err := SaveAtomic(path, map[string]string{bad: "x"}, 0o600); err == nil {
			t.Errorf("expected rejection of key %q", bad)
		}
	}
}

func TestSaveAtomicAcceptsRealWorldValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfvpn.env")
	values := map[string]string{
		"DOMAIN":              "cdn-a1b2.rwl.one",
		"CF_API_TOKEN":        "v1.0-abc_DEF-123",
		"XRAY_DNS_SERVERS":    "https://1.1.1.1/dns-query,https://9.9.9.9/dns-query",
		"REALITY_PRIVATE_KEY": "XkP_9mQ2r-tuvWxyz0123456789AbCdEfGhIjKl",
		"HY2_OBFS_PW":         "p@ss w/rd:1+2",
		"_UNDERSCORE_LEAD":    "ok",
	}
	if err := SaveAtomic(path, values, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range values {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
