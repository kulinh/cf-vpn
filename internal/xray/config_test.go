package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kulinh/cf-vpn/internal/templates"
)

func TestAddAndRemoveUser(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := AddUser(&cfg, "alice", "uuid-a", "pass-a"); err != nil {
		t.Fatal(err)
	}
	names := ListUserNames(cfg)
	if len(names) != 2 {
		t.Fatalf("expected 2 users, got %d", len(names))
	}
	if err := RemoveUser(&cfg, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(ListUserNames(cfg)) != 1 {
		t.Fatalf("expected 1 user after remove")
	}
}

func TestAddUserSucceedsWithoutTrojanInbound(t *testing.T) {
	cfg := Config{Inbounds: []Inbound{vlessInbound("user1", "uuid-1")}}

	if err := AddUser(&cfg, "alice", "uuid-a", "pass-a"); err != nil {
		t.Fatalf("AddUser without trojan inbound: %v", err)
	}

	if _, ok := GetVLESSClient(cfg, "alice"); !ok {
		t.Fatalf("expected alice in vless clients")
	}
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("expected no trojan inbound to be added, got %d inbounds", len(cfg.Inbounds))
	}
}

func TestRemoveUserSucceedsWithoutTrojanInbound(t *testing.T) {
	cfg := Config{Inbounds: []Inbound{vlessInbound("user1", "uuid-1")}}

	if err := RemoveUser(&cfg, "user1"); err != nil {
		t.Fatalf("RemoveUser without trojan inbound: %v", err)
	}

	if _, ok := GetVLESSClient(cfg, "user1"); ok {
		t.Fatalf("expected user1 removed from vless clients")
	}
}

func vlessInbound(name, uuid string) Inbound {
	settings, _ := json.Marshal(vlessSettings{
		Clients:    []vlessClient{{ID: uuid, Email: name}},
		Decryption: "none",
	})
	return Inbound{Protocol: "vless", Settings: settings}
}

func TestValidateUserName(t *testing.T) {
	good := []string{"alice", "user_1", "user-1", "A", "a123456789012345678901234567890AB"[:32]}
	for _, n := range good {
		if err := ValidateUserName(n); err != nil {
			t.Errorf("expected %q valid: %v", n, err)
		}
	}
	bad := []string{"", "has space", "name!", "a123456789012345678901234567890ABC"}
	for _, n := range bad {
		if err := ValidateUserName(n); err == nil {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestAddUserRejectsDuplicate(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := AddUser(&cfg, "user1", "uuid-2", "pass-2"); err == nil {
		t.Fatalf("expected duplicate to error")
	}
}

func TestRemoveUserMissing(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := RemoveUser(&cfg, "nobody"); err == nil {
		t.Fatalf("expected missing to error")
	}
}

// TestTemplateRoundTripPreservesFields verifies that loading the rendered
// xray template, mutating users, and saving preserves every field required
// by xray at runtime — including the private-IP egress block per spec §10.
func TestTemplateRoundTripPreservesFields(t *testing.T) {
	rendered, err := templates.RenderXrayCloudflareHTTPUpgrade(
		[]templates.XrayUser{{Name: "user1", UUID: "uuid-1"}},
		"vpn.example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := AddUser(&cfg, "alice", "uuid-a", "pass-a"); err != nil {
		t.Fatal(err)
	}
	if err := SaveAtomic(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Top-level fields preserved.
	for _, key := range []string{"log", "inbounds", "outbounds", "routing"} {
		if _, ok := obj[key]; !ok {
			t.Errorf("top-level %q missing after round-trip", key)
		}
	}

	// Private-IP egress block preserved (spec §10).
	routingRaw := obj["routing"]
	if !contains(routingRaw, "geoip:private") {
		t.Errorf("routing rule 'geoip:private' missing after round-trip: %s", routingRaw)
	}
	if !contains(routingRaw, "block") {
		t.Errorf("routing outboundTag 'block' missing after round-trip: %s", routingRaw)
	}

	// Inbound-level fields preserved.
	var inbounds []map[string]json.RawMessage
	if err := json.Unmarshal(obj["inbounds"], &inbounds); err != nil {
		t.Fatalf("unmarshal inbounds: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(inbounds))
	}
	for i, in := range inbounds {
		for _, key := range []string{"tag", "listen", "port", "protocol", "settings", "streamSettings"} {
			if _, ok := in[key]; !ok {
				t.Errorf("inbound[%d] missing %q: %v", i, key, in)
			}
		}
	}

	// Reload and verify both users present.
	cfg2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := CountUsers(cfg2); n != 2 {
		t.Fatalf("expected 2 users after round-trip, got %d", n)
	}
}

func contains(b []byte, substr string) bool {
	return len(b) > 0 && len(substr) > 0 && indexOf(string(b), substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
