package templates

import "testing"

func TestVLESSPathIsNeutral(t *testing.T) {
	if VLESSPath != "/api/v1/sync" {
		t.Fatalf("VLESSPath got %q want /api/v1/sync", VLESSPath)
	}
}
