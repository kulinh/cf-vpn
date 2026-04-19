package subscription

import "testing"

func TestBuildVLESSURI(t *testing.T) {
	uri := BuildVLESSURI("alice", "uuid-a", "vpn.example.com")
	want := "vless://uuid-a@vpn.example.com:443?encryption=none&security=tls&type=ws&host=vpn.example.com&path=%2Fvless&sni=vpn.example.com#alice-VLESS"
	if uri != want {
		t.Fatalf("unexpected URI: %s", uri)
	}
}
