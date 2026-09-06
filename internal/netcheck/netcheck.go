package netcheck

import (
	"errors"
	"net"
	"strconv"
	"syscall"
	"time"
)

// IsTCPPortBound reports whether host:port already has a listener locally.
//
// Only EADDRINUSE means "bound". Every other failure — EACCES on :443 when not
// running as root, EADDRNOTAVAIL for an address this host does not own — says
// nothing about a listener and is returned as an error. Reporting those as
// "bound" made SuggestMode() pick cloudflare on a node where direct was
// correct, purely because the probe ran unprivileged.
func IsTCPPortBound(host string, port int) (bool, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return true, nil
		}
		return false, err
	}
	_ = ln.Close()
	return false, nil
}

// CanReachOutbound443 returns true if a TCP connect to 1.1.1.1:443 succeeds.
func CanReachOutbound443(timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// SuggestMode returns "direct" if 443 is free locally and we can reach the
// internet on 443; otherwise "cloudflare".
//
// A probe error (typically EACCES because the caller is not root) is treated as
// "unknown, not bound": xray itself runs as root and can bind :443, so the
// permission of whoever ran the probe must not decide the node's mode.
func SuggestMode() string {
	bound, err := IsTCPPortBound("0.0.0.0", 443)
	if err == nil && bound {
		return "cloudflare"
	}
	if !CanReachOutbound443(3 * time.Second) {
		return "cloudflare"
	}
	return "direct"
}
