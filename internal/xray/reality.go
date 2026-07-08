package xray

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

type Keypair struct{ Private, Public string }

type RealityParams struct {
	PrivateKey, PublicKey, ShortID, Dest, SNI string
}

type GenerateRealityOptions struct {
	XrayBin     string   // path to xray binary; default "xray"
	Dest        string   // default "www.apple.com:443"
	SNI         string   // default "www.apple.com"
	StubKeypair *Keypair // tests only; if non-nil bypass `xray x25519`
}

func GenerateRealityParams(opts GenerateRealityOptions) (RealityParams, error) {
	if opts.XrayBin == "" {
		opts.XrayBin = "xray"
	}
	// www.apple.com: TLS 1.3, globally reachable, and a clean Reality "steal"
	// target. (www.microsoft.com was the old default but stopped working as a
	// REALITY dest on recent xray — its handshake no longer completes, which
	// silently timed out every Reality client. CN nodes override this to a
	// China-local dest like connect.rom.miui.com via REALITY_DEST/REALITY_SNI.)
	if opts.Dest == "" {
		opts.Dest = "www.apple.com:443"
	}
	if opts.SNI == "" {
		opts.SNI = "www.apple.com"
	}

	var kp Keypair
	if opts.StubKeypair != nil {
		kp = *opts.StubKeypair
	} else {
		out, err := exec.Command(opts.XrayBin, "x25519").Output()
		if err != nil {
			return RealityParams{}, fmt.Errorf("xray x25519: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Private key:"):
				kp.Private = strings.TrimSpace(strings.TrimPrefix(line, "Private key:"))
			case strings.HasPrefix(line, "PrivateKey:"):
				kp.Private = strings.TrimSpace(strings.TrimPrefix(line, "PrivateKey:"))
			case strings.HasPrefix(line, "Public key:"):
				kp.Public = strings.TrimSpace(strings.TrimPrefix(line, "Public key:"))
			case strings.Contains(line, "PublicKey"):
				// Handle "Password (PublicKey): <value>" format. Split on the
				// first ':' after "PublicKey" so a value byte that happens to
				// match `): ` is not eaten by TrimLeft.
				idx := strings.Index(line, "PublicKey")
				rest := line[idx+len("PublicKey"):]
				if i := strings.Index(rest, ":"); i >= 0 {
					rest = rest[i+1:]
				}
				rest = strings.TrimRight(strings.TrimSpace(rest), ")")
				kp.Public = strings.TrimSpace(rest)
			}
		}
		if kp.Private == "" || kp.Public == "" {
			return RealityParams{}, fmt.Errorf("xray x25519: could not parse output:\n%s", out)
		}
	}

	var sid [8]byte
	if _, err := rand.Read(sid[:]); err != nil {
		return RealityParams{}, err
	}
	return RealityParams{
		PrivateKey: kp.Private,
		PublicKey:  kp.Public,
		ShortID:    hex.EncodeToString(sid[:]),
		Dest:       opts.Dest,
		SNI:        opts.SNI,
	}, nil
}
