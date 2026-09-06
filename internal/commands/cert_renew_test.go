package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/state"
)

// certRenewFake writes the bytes it is told to, so the test can control
// whether the certificate "changed".
type certRenewFake struct {
	newCert  string
	renews   int
	renewErr error
}

func (f *certRenewFake) Issue(_ context.Context, _, certPath, keyPath, _ string) error {
	return f.write(certPath, keyPath)
}

func (f *certRenewFake) Renew(_ context.Context, _, certPath, keyPath, _ string, _ int) error {
	f.renews++
	if f.renewErr != nil {
		return f.renewErr
	}
	return f.write(certPath, keyPath)
}

func (f *certRenewFake) write(certPath, keyPath string) error {
	if err := os.WriteFile(certPath, []byte(f.newCert), 0o600); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte("key-"+f.newCert), 0o600)
}

// withCertPaths points HysteriaCertPaths at a temp dir.
func withCertPaths(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	old := hysteriaCertDir
	hysteriaCertDir = dir
	t.Cleanup(func() { hysteriaCertDir = old })
	return HysteriaCertPaths()
}

func TestRunCertRenewNoHostIsNoOp(t *testing.T) {
	withTempPaths(t)
	mgr := &certRenewFake{newCert: "x"}
	var out, errBuf bytes.Buffer
	if err := RunCertRenew(context.Background(), map[string]string{"CF_API_TOKEN": "t"}, CertRenewDeps{Cert: mgr}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if mgr.renews != 0 {
		t.Fatalf("renewed with no HY2_HOST configured")
	}
}

func TestRunCertRenewRequiresToken(t *testing.T) {
	withTempPaths(t)
	var out, errBuf bytes.Buffer
	err := RunCertRenew(context.Background(), map[string]string{state.KeyHy2Host: "hy2.example.com"}, CertRenewDeps{Cert: &certRenewFake{}}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected an error without CF_API_TOKEN")
	}
}

// M-G2: hysteria loads its leaf at startup and nothing else restarts it after a
// renewal, so a changed certificate must trigger a restart here.
func TestRunCertRenewRestartsHysteriaWhenCertChanged(t *testing.T) {
	withTempPaths(t)
	certPath, keyPath := withCertPaths(t)
	if err := os.WriteFile(certPath, []byte("old-cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key-old-cert"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{}
	mgr := &certRenewFake{newCert: "new-cert"}
	env := map[string]string{"CF_API_TOKEN": "t", state.KeyHy2Host: "hy2.example.com"}

	var out, errBuf bytes.Buffer
	if err := RunCertRenew(context.Background(), env, CertRenewDeps{Cert: mgr, Runner: runner}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if mgr.renews != 1 {
		t.Fatalf("renews = %d, want 1", mgr.renews)
	}
	if !strings.Contains(recorderCallsOf(runner), "systemctl restart cfvpn-hysteria.service") {
		t.Fatalf("hysteria was not restarted after the cert changed; calls: %s", recorderCallsOf(runner))
	}
	if !strings.Contains(out.String(), "certificate changed") {
		t.Errorf("stdout = %q", out.String())
	}
}

// An unchanged certificate (lego skips renewal until the threshold) must NOT
// bounce the service — the timer runs daily on every node.
func TestRunCertRenewSkipsRestartWhenUnchanged(t *testing.T) {
	withTempPaths(t)
	certPath, keyPath := withCertPaths(t)
	if err := os.WriteFile(certPath, []byte("same-cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key-same-cert"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{}
	mgr := &certRenewFake{newCert: "same-cert"}
	env := map[string]string{"CF_API_TOKEN": "t", state.KeyHy2Host: "hy2.example.com"}

	var out, errBuf bytes.Buffer
	if err := RunCertRenew(context.Background(), env, CertRenewDeps{Cert: mgr, Runner: runner}, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if calls := recorderCallsOf(runner); calls != "" {
		t.Fatalf("expected no systemctl calls for an unchanged cert, got: %s", calls)
	}
}

func TestRunCertRenewPropagatesRenewError(t *testing.T) {
	withTempPaths(t)
	withCertPaths(t)
	mgr := &certRenewFake{renewErr: fmt.Errorf("acme rate limited")}
	env := map[string]string{"CF_API_TOKEN": "t", state.KeyHy2Host: "hy2.example.com"}
	var out, errBuf bytes.Buffer
	err := RunCertRenew(context.Background(), env, CertRenewDeps{Cert: mgr, Runner: &recordingRunner{}}, &out, &errBuf)
	if err == nil || !strings.Contains(err.Error(), "acme rate limited") {
		t.Fatalf("err = %v, want the renew error", err)
	}
}

func recorderCallsOf(r *recordingRunner) string {
	var b strings.Builder
	for _, c := range r.calls {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
