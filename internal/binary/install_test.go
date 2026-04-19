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
