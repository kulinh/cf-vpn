package state

import "testing"

func TestKeysAreUnique(t *testing.T) {
	all := []string{KeyMode, KeyDomain, KeyPublicIP, KeyAdminHost, KeyAdminTunnelUUID, KeyNodeID,
		KeyRealityPriv, KeyRealityPub, KeyRealityShortID, KeyRealityDest, KeyRealitySNI,
		KeyXHTTPPath, KeyHy2Host, KeyHy2Port, KeyHy2ObfsPW}
	seen := map[string]bool{}
	for _, k := range all {
		if seen[k] { t.Errorf("dup key %q", k) }
		seen[k] = true
	}
}
