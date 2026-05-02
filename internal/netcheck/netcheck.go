package netcheck

import (
	"net"
	"strconv"
	"time"
)

// IsTCPPortBound returns true if host:port already has a listener locally.
func IsTCPPortBound(host string, port int) (bool, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return true, nil
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
func SuggestMode() string {
	bound, _ := IsTCPPortBound("0.0.0.0", 443)
	if bound {
		return "cloudflare"
	}
	if !CanReachOutbound443(3 * time.Second) {
		return "cloudflare"
	}
	return "direct"
}
