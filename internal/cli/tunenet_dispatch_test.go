package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `tune-net` must be reachable from the CLI: it is how an already-installed
// node picks up the BBR/fq tuning without a full re-deploy.
func TestTuneNetRejectsExtraArgs(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"tune-net", "extra"}, &out, &errBuf); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "usage: cfvpnctl tune-net") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}

func TestUnknownCommandStillRejected(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"tune-network"}, &out, &errBuf); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "unknown command") {
		t.Fatalf("stderr = %q", errBuf.String())
	}
}
