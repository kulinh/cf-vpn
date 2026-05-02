package binary

import (
	"context"
	"testing"
)

type FakeRunner struct{ Calls [][]string }

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	return nil
}

func TestEnsureXraySkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureXray(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls when already installed")
	}
}

func TestEnsureXrayInvokesInstallerWhenMissing(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureXray(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 || fake.Calls[0][0] != "bash" {
		t.Fatalf("expected bash install call, got %#v", fake.Calls)
	}
}

func TestEnsureCloudflaredSkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureCloudflared(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls")
	}
}

func TestEnsureCloudflaredInvokesInstallerWhenMissing(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureCloudflared(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 install call, got %d", len(fake.Calls))
	}
}

func TestEnsureHysteriaSkipsInstallerButDisablesDefaultUnit(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureHysteria(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 disable call when binary already installed, got %d: %#v", len(fake.Calls), fake.Calls)
	}
	if !contains(fake.Calls[0][2], "disable --now hysteria-server.service") {
		t.Fatalf("expected disable command, got %#v", fake.Calls[0])
	}
}

func TestEnsureHysteriaInvokesInstallerThenDisablesDefaultUnit(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureHysteria(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls (install + disable), got %d: %#v", len(fake.Calls), fake.Calls)
	}
	if fake.Calls[0][0] != "bash" || fake.Calls[0][1] != "-c" {
		t.Fatalf("expected bash -c install call, got %#v", fake.Calls[0])
	}
	if !contains(fake.Calls[0][2], "https://get.hy2.sh") {
		t.Fatalf("expected install command to fetch hy2 installer, got %q", fake.Calls[0][2])
	}
	if !contains(fake.Calls[1][2], "disable --now hysteria-server.service") {
		t.Fatalf("expected disable command, got %#v", fake.Calls[1])
	}
}

func TestEnsureLegoSkipsWhenAlreadyPresent(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureLego(context.Background(), fake, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("expected no install calls")
	}
}

func TestEnsureLegoInstallsLatestLinuxAMD64Asset(t *testing.T) {
	fake := &FakeRunner{}
	if err := EnsureLego(context.Background(), fake, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 install call, got %d", len(fake.Calls))
	}
	got := fake.Calls[0]
	if got[0] != "bash" || got[1] != "-lc" {
		t.Fatalf("expected bash -lc install call, got %#v", got)
	}
	cmd := got[2]
	for _, want := range []string{
		"https://api.github.com/repos/go-acme/lego/releases/latest",
		"linux_amd64.tar.gz",
		"/usr/local/bin/lego",
		"install -m 755",
	} {
		if !contains(cmd, want) {
			t.Fatalf("expected install command to contain %q, got %q", want, cmd)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return substr == ""
}
