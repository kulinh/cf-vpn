package cert

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeRunner struct {
	calls    [][]string
	envCalls [][]string
	err      error
}

func (f *fakeRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.envCalls = append(f.envCalls, append([]string(nil), env...))
	return nil, f.err
}

func TestLegoManagerIssueRunsLegoAndCopiesCerts(t *testing.T) {
	dir := t.TempDir()
	legoPath := filepath.Join(dir, "lego")
	host := "quic-1.example.com"
	certSrc := filepath.Join(legoPath, "certificates", host+".crt")
	keySrc := filepath.Join(legoPath, "certificates", host+".key")
	if err := os.MkdirAll(filepath.Dir(certSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certSrc, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keySrc, []byte("key"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRunner{}
	m := NewLegoManager("/usr/local/bin/lego", legoPath, "ops@example.com", r)
	certDest := filepath.Join(dir, "out", "fullchain.pem")
	keyDest := filepath.Join(dir, "out", "privkey.pem")
	if err := m.Issue(context.Background(), host, certDest, keyDest, "tok"); err != nil {
		t.Fatal(err)
	}

	want := []string{"/usr/local/bin/lego", "--accept-tos", "--email=ops@example.com", "--dns=cloudflare", "--path=" + legoPath, "--domains=" + host, "run"}
	if !reflect.DeepEqual(r.calls, [][]string{want}) {
		t.Fatalf("lego issue call = %#v, want %#v", r.calls, [][]string{want})
	}
	if !reflect.DeepEqual(r.envCalls, [][]string{{"CF_DNS_API_TOKEN=tok"}}) {
		t.Fatalf("env calls = %#v", r.envCalls)
	}
	assertFile(t, certDest, "cert", 0o600)
	assertFile(t, keyDest, "key", 0o600)
}

func TestLegoManagerRenewRunsLegoRenewDaysAndCopiesCerts(t *testing.T) {
	dir := t.TempDir()
	legoPath := filepath.Join(dir, "lego")
	host := "quic-1.example.com"
	certSrc := filepath.Join(legoPath, "certificates", host+".crt")
	keySrc := filepath.Join(legoPath, "certificates", host+".key")
	if err := os.MkdirAll(filepath.Dir(certSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certSrc, []byte("renewed cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keySrc, []byte("renewed key"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRunner{}
	m := NewLegoManager("/usr/local/bin/lego", legoPath, "ops@example.com", r)
	certDest := filepath.Join(dir, "nested", "fullchain.pem")
	keyDest := filepath.Join(dir, "nested", "privkey.pem")
	if err := m.Renew(context.Background(), host, certDest, keyDest, "tok", 30); err != nil {
		t.Fatal(err)
	}

	want := []string{"/usr/local/bin/lego", "--accept-tos", "--email=ops@example.com", "--dns=cloudflare", "--path=" + legoPath, "--domains=" + host, "renew", "--days=30"}
	if !reflect.DeepEqual(r.calls, [][]string{want}) {
		t.Fatalf("lego renew call = %#v, want %#v", r.calls, [][]string{want})
	}
	if !reflect.DeepEqual(r.envCalls, [][]string{{"CF_DNS_API_TOKEN=tok"}}) {
		t.Fatalf("env calls = %#v", r.envCalls)
	}
	assertFile(t, certDest, "renewed cert", 0o600)
	assertFile(t, keyDest, "renewed key", 0o600)
}

func assertFile(t *testing.T, path, want string, wantPerm os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != wantPerm {
		t.Fatalf("%s perm = %o, want %o", path, info.Mode().Perm(), wantPerm)
	}
}
