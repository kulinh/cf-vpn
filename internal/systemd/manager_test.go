package systemd

import (
	"context"
	"testing"
)

type fakeRunner struct{ calls [][]string }

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return nil
}

func TestEnableNowInvokesSystemctl(t *testing.T) {
	r := &fakeRunner{}
	if err := EnableNow(context.Background(), r, "cfvpn-xray.service"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || r.calls[0][0] != "systemctl" || r.calls[0][1] != "enable" || r.calls[0][2] != "--now" || r.calls[0][3] != "cfvpn-xray.service" {
		t.Fatalf("unexpected calls: %#v", r.calls)
	}
}

func TestDaemonReloadInvokesSystemctl(t *testing.T) {
	r := &fakeRunner{}
	if err := DaemonReload(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || r.calls[0][1] != "daemon-reload" {
		t.Fatalf("unexpected calls: %#v", r.calls)
	}
}
