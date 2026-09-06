package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tuneCall struct {
	name string
	args []string
}

// tuneStub replaces the exec seam: it records calls and answers sysctl reads
// with the configured congestion control.
type tuneStub struct {
	calls      []tuneCall
	active     string            // value reported for tcp_congestion_control
	fail       map[string]bool   // command name+args prefix that should fail
	failOutput map[string]string //  ... and what it prints
}

func (s *tuneStub) run(_ context.Context, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, tuneCall{name: name, args: args})
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if s.fail[key] {
		return []byte(s.failOutput[key]), fmt.Errorf("stub failure: %s", key)
	}
	if name == "sysctl" && len(args) >= 2 && args[0] == "-n" {
		return []byte(s.active + "\n"), nil
	}
	return nil, nil
}

func (s *tuneStub) calledWith(name string, args ...string) bool {
	want := strings.TrimSpace(name + " " + strings.Join(args, " "))
	for _, c := range s.calls {
		if strings.TrimSpace(c.name+" "+strings.Join(c.args, " ")) == want {
			return true
		}
	}
	return false
}

func withTuneSeams(t *testing.T, stub *tuneStub) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "90-cfvpn.conf")
	oldPath, oldRun := sysctlConfPath, runTuneCommand
	sysctlConfPath = path
	runTuneCommand = stub.run
	t.Cleanup(func() {
		sysctlConfPath = oldPath
		runTuneCommand = oldRun
	})
	return path
}

func TestSysctlTuningContent(t *testing.T) {
	want := "net.core.default_qdisc = fq\n" +
		"net.ipv4.tcp_congestion_control = bbr\n" +
		"net.ipv4.tcp_mtu_probing = 1\n"
	if SysctlTuning != want {
		t.Fatalf("drop-in content drifted:\n got: %q\nwant: %q", SysctlTuning, want)
	}
}

func TestRunTuneNetWritesDropInAndVerifies(t *testing.T) {
	stub := &tuneStub{active: "bbr"}
	path := withTuneSeams(t, stub)

	var out, errBuf bytes.Buffer
	if err := RunTuneNet(context.Background(), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != SysctlTuning {
		t.Fatalf("drop-in content = %q", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644 (sysctl must be able to read it)", info.Mode().Perm())
	}
	if !stub.calledWith("modprobe", "tcp_bbr") {
		t.Errorf("modprobe tcp_bbr not attempted: %#v", stub.calls)
	}
	if !stub.calledWith("sysctl", "--system") {
		t.Errorf("sysctl --system not run: %#v", stub.calls)
	}
	if !strings.Contains(out.String(), "bbr active") {
		t.Errorf("stdout = %q", out.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("unexpected warning: %s", errBuf.String())
	}
}

func TestRunTuneNetIsIdempotent(t *testing.T) {
	stub := &tuneStub{active: "bbr"}
	path := withTuneSeams(t, stub)
	if err := os.WriteFile(path, []byte(SysctlTuning), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	if err := RunTuneNet(context.Background(), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Errorf("stdout = %q", out.String())
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != SysctlTuning {
		t.Fatalf("content changed: %q", raw)
	}
}

// A kernel without BBR must warn, not fail: the node still works.
func TestRunTuneNetWarnsWhenBBRNotActive(t *testing.T) {
	stub := &tuneStub{active: "cubic"}
	withTuneSeams(t, stub)
	var out, errBuf bytes.Buffer
	if err := RunTuneNet(context.Background(), &out, &errBuf); err != nil {
		t.Fatalf("tuning must not fail the caller: %v", err)
	}
	if !strings.Contains(errBuf.String(), `is "cubic"`) {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

// modprobe failing is expected when tcp_bbr is built into the kernel.
func TestRunTuneNetToleratesModprobeFailure(t *testing.T) {
	stub := &tuneStub{
		active:     "bbr",
		fail:       map[string]bool{"modprobe tcp_bbr": true},
		failOutput: map[string]string{"modprobe tcp_bbr": "modprobe: FATAL: Module tcp_bbr not found"},
	}
	withTuneSeams(t, stub)
	var out, errBuf bytes.Buffer
	if err := RunTuneNet(context.Background(), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "bbr active") {
		t.Errorf("stdout = %q", out.String())
	}
}

// `sysctl --system` is missing on some minimal images; fall back to -p.
func TestRunTuneNetFallsBackToSysctlDashP(t *testing.T) {
	stub := &tuneStub{active: "bbr", fail: map[string]bool{"sysctl --system": true}}
	path := withTuneSeams(t, stub)
	var out, errBuf bytes.Buffer
	if err := RunTuneNet(context.Background(), &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !stub.calledWith("sysctl", "-p", path) {
		t.Fatalf("no fallback to sysctl -p: %#v", stub.calls)
	}
	if !strings.Contains(out.String(), "bbr active") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestRunTuneNetWarnsWhenBothApplyPathsFail(t *testing.T) {
	stub := &tuneStub{active: "bbr"}
	path := withTuneSeams(t, stub)
	stub.fail = map[string]bool{"sysctl --system": true, "sysctl -p " + path: true}
	var out, errBuf bytes.Buffer
	if err := RunTuneNet(context.Background(), &out, &errBuf); err != nil {
		t.Fatalf("tuning must not fail the caller: %v", err)
	}
	if !strings.Contains(errBuf.String(), "next reboot") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestCongestionControlParsing(t *testing.T) {
	cases := map[string]string{
		"bbr\n": "bbr",
		"net.ipv4.tcp_congestion_control = bbr\n": "bbr",
		"net.ipv4.tcp_congestion_control=cubic":   "cubic",
		"  cubic  ":                               "cubic",
		"":                                        "",
		"sysctl: warning\nbbr":                    "bbr",
	}
	for in, want := range cases {
		if got := CongestionControlFromSysctl(in); got != want {
			t.Errorf("CongestionControlFromSysctl(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsBBRActive("net.ipv4.tcp_congestion_control = bbr\n") {
		t.Error("IsBBRActive false for bbr")
	}
	if IsBBRActive("cubic\n") {
		t.Error("IsBBRActive true for cubic")
	}
}
