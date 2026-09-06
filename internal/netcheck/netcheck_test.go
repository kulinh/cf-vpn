package netcheck

import (
	"net"
	"os"
	"strconv"
	"testing"
)

// freePort returns a port with no listener. There is an inherent TOCTOU window,
// but the OS does not hand out the same ephemeral port again immediately.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestIsTCPPortBoundFalseForFreePort(t *testing.T) {
	bound, err := IsTCPPortBound("127.0.0.1", freePort(t))
	if err != nil {
		t.Fatal(err)
	}
	if bound {
		t.Error("free port reported as bound")
	}
}

func TestIsTCPPortBoundTrueForListeningPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	bound, err := IsTCPPortBound(host, port)
	if err != nil {
		t.Fatal(err)
	}
	if !bound {
		t.Error("expected port to be reported as bound")
	}
}

// LOW: only EADDRINUSE means "bound". A permission error (unprivileged bind of
// :443) used to be reported as bound, which made SuggestMode pick cloudflare on
// a node where direct was correct.
func TestIsTCPPortBoundReportsNonEADDRINUSEAsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: binding a privileged port succeeds")
	}
	bound, err := IsTCPPortBound("127.0.0.1", 443)
	if err == nil {
		t.Skip("this environment allows unprivileged binds of :443")
	}
	if bound {
		t.Errorf("permission error reported as bound (err=%v)", err)
	}
}

// An address this host does not own gives EADDRNOTAVAIL, which is also not
// "bound".
func TestIsTCPPortBoundUnavailableAddressIsAnError(t *testing.T) {
	bound, err := IsTCPPortBound("192.0.2.1", freePort(t))
	if err == nil {
		t.Skip("this environment can bind 192.0.2.1")
	}
	if bound {
		t.Errorf("unavailable address reported as bound (err=%v)", err)
	}
}
