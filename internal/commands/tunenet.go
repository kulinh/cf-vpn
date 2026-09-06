package commands

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// sysctlConfPath is the drop-in cfvpn owns. Anything else under
// /etc/sysctl.d is left alone.
var sysctlConfPath = "/etc/sysctl.d/90-cfvpn.conf"

// SysctlTuning is the exact content of that drop-in.
//
// Why: on the VN <-> APAC path this fleet serves, 25-30% packet loss is normal
// at peak. Linux's default cubic reads that as congestion and collapses the
// window to under 1 KB/s, which is what "the VPN is dead but the node is up"
// looks like from a client. BBR models bandwidth and RTT instead of counting
// losses, so it keeps the pipe full through the same loss; fq is the qdisc BBR
// needs for its pacing; tcp_mtu_probing recovers from the black-holed ICMP
// "fragmentation needed" that tunnels on this path routinely hit.
const SysctlTuning = `net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.ipv4.tcp_mtu_probing = 1
`

// runTuneCommand executes one tuning command. It is a var so tests do not need
// root (or a real sysctl).
var runTuneCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// CongestionControlFromSysctl extracts the active algorithm from the output of
// `sysctl net.ipv4.tcp_congestion_control`, accepting both the "key = value"
// form and the bare value printed by `sysctl -n`.
func CongestionControlFromSysctl(out string) string {
	line := strings.TrimSpace(out)
	if line == "" {
		return ""
	}
	// Take the last non-empty line: sysctl may print warnings first.
	lines := strings.Split(line, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(lines[i])
		if candidate == "" {
			continue
		}
		if _, value, found := strings.Cut(candidate, "="); found {
			return strings.TrimSpace(value)
		}
		return strings.Fields(candidate)[len(strings.Fields(candidate))-1]
	}
	return ""
}

// IsBBRActive reports whether sysctl output says BBR is the active congestion
// control.
func IsBBRActive(out string) bool {
	return CongestionControlFromSysctl(out) == "bbr"
}

// RunTuneNet applies the node network tuning. It is idempotent: the drop-in is
// only rewritten when its content differs, and every step is safe to repeat.
//
// It never fails the caller for a tuning problem — a node that cannot enable
// BBR still works, just slower on a lossy path — so problems are reported on
// stderr and the function returns nil. Only a failure to write the file itself
// is an error, since that is a real filesystem problem.
func RunTuneNet(ctx context.Context, stdout, stderr io.Writer) error {
	changed, err := writeIfChanged(sysctlConfPath, []byte(SysctlTuning), 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", sysctlConfPath, err)
	}
	if changed {
		fmt.Fprintf(stdout, "tune-net: wrote %s\n", sysctlConfPath)
	} else {
		fmt.Fprintf(stdout, "tune-net: %s already up to date\n", sysctlConfPath)
	}

	// tcp_bbr is built into most modern kernels; modprobe then fails with
	// "module not found" even though BBR is available, so its failure is not
	// interesting on its own — the verification below is what counts.
	if out, err := runTuneCommand(ctx, "modprobe", "tcp_bbr"); err != nil {
		fmt.Fprintf(stdout, "tune-net: modprobe tcp_bbr: %v (%s) — continuing; BBR may be built in\n",
			err, strings.TrimSpace(string(out)))
	}

	if out, err := runTuneCommand(ctx, "sysctl", "--system"); err != nil {
		// Older/busybox sysctl has no --system.
		if out2, err2 := runTuneCommand(ctx, "sysctl", "-p", sysctlConfPath); err2 != nil {
			warnf(stderr, "warning: tune-net: sysctl --system failed (%v: %s) and sysctl -p failed (%v: %s); "+
				"settings will apply at the next reboot", err, strings.TrimSpace(string(out)), err2, strings.TrimSpace(string(out2)))
			return nil
		}
	}

	out, err := runTuneCommand(ctx, "sysctl", "-n", "net.ipv4.tcp_congestion_control")
	if err != nil {
		warnf(stderr, "warning: tune-net: cannot read net.ipv4.tcp_congestion_control (%v); "+
			"assuming the setting did not take effect", err)
		return nil
	}
	active := CongestionControlFromSysctl(string(out))
	if active != "bbr" {
		warnf(stderr, "warning: tune-net: net.ipv4.tcp_congestion_control is %q, not \"bbr\" — "+
			"this kernel may lack the tcp_bbr module; the node still works but will collapse "+
			"to <1KB/s on a lossy path", active)
		return nil
	}
	fmt.Fprintln(stdout, "tune-net: bbr active (qdisc fq, tcp_mtu_probing 1)")
	return nil
}

// tuneNetBestEffort runs the tuning as part of install/upgrade. Network tuning
// must never abort a provisioning run, so even a write failure is only a
// warning here.
func tuneNetBestEffort(ctx context.Context, stdout, stderr io.Writer) {
	if stdout == nil {
		stdout = io.Discard
	}
	if err := RunTuneNet(ctx, stdout, stderr); err != nil {
		warnf(stderr, "warning: network tuning skipped: %v", err)
	}
}
