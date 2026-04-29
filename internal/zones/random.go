package zones

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Prefixes is the CDN-style prefix pool. GenerateHost picks one entry per call.
var Prefixes = []string{"cdn", "static", "assets", "edge", "media"}

// ErrNoEligibleZones means PickZone was given no usable candidates (empty
// list, or only the excluded zone). Callers map this to a 400 response.
var ErrNoEligibleZones = errors.New("no eligible zones")

// GenerateHostWith reads 5 bytes from rng: byte 0 selects a prefix from the
// supplied pool (modulo len(prefixes)), bytes 1..4 hex-encode to the 8-char
// body. Returns "<prefix>-<8hex>.<zone>".
func GenerateHostWith(rng io.Reader, zone string, prefixes []string) (string, error) {
	if len(prefixes) == 0 {
		return "", errors.New("zones: empty prefix pool")
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(rng, buf); err != nil {
		return "", fmt.Errorf("read rng: %w", err)
	}
	return prefixes[int(buf[0])%len(prefixes)] + "-" + hex.EncodeToString(buf[1:5]) + "." + zone, nil
}

// GenerateHost reads 5 bytes from rng using Prefixes as the prefix pool.
func GenerateHost(rng io.Reader, zone string) (string, error) {
	return GenerateHostWith(rng, zone, Prefixes)
}

// Hy2Prefixes is the Hysteria2 prefix pool.
var Hy2Prefixes = []string{"quic", "udp", "hy"}

// GenerateHy2Host reads 5 bytes from rng using Hy2Prefixes as the prefix pool.
func GenerateHy2Host(rng io.Reader, zone string) (string, error) {
	return GenerateHostWith(rng, zone, Hy2Prefixes)
}

// PickZone returns one Zone from candidates, excluding any whose Name equals
// excludeName (pass "" to disable). Returns ErrNoEligibleZones when the
// filtered list is empty.
func PickZone(rng io.Reader, candidates []Zone, excludeName string) (Zone, error) {
	eligible := make([]Zone, 0, len(candidates))
	for _, z := range candidates {
		if z.Name != excludeName {
			eligible = append(eligible, z)
		}
	}
	if len(eligible) == 0 {
		return Zone{}, ErrNoEligibleZones
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(rng, buf); err != nil {
		return Zone{}, fmt.Errorf("read rng: %w", err)
	}
	return eligible[int(buf[0])%len(eligible)], nil
}
