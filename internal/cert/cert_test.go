package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
	certPEM, keyPEM := testPEMPair(t)
	if err := os.WriteFile(certSrc, []byte(certPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keySrc, []byte(keyPEM), 0o644); err != nil {
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
	assertFile(t, certDest, certPEM, 0o600)
	assertFile(t, keyDest, keyPEM, 0o600)
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
	certPEM, keyPEM := testPEMPair(t)
	if err := os.WriteFile(certSrc, []byte(certPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keySrc, []byte(keyPEM), 0o644); err != nil {
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
	assertFile(t, certDest, certPEM, 0o600)
	assertFile(t, keyDest, keyPEM, 0o600)
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

// testPEMPair returns a self-signed certificate and its key in PEM form.
// copyResult verifies the published pair with tls.LoadX509KeyPair, so the
// fixtures have to be real PEM — "cert"/"key" placeholders would only prove
// that the verification is absent.
func testPEMPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func TestCopyResultRestoresThePreviousPairWhenTheNewOneIsInvalid(t *testing.T) {
	dir := t.TempDir()
	legoPath := filepath.Join(dir, "lego")
	host := "quic-1.example.com"
	if err := os.MkdirAll(filepath.Join(legoPath, "certificates"), 0o755); err != nil {
		t.Fatal(err)
	}
	// lego left a cert that is not valid PEM (truncated download, disk full).
	if err := os.WriteFile(filepath.Join(legoPath, "certificates", host+".crt"), []byte("-----BEGIN CERTIFICATE-----\ntruncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legoPath, "certificates", host+".key"), []byte("nonsense"), 0o600); err != nil {
		t.Fatal(err)
	}
	certDest := filepath.Join(dir, "out", "fullchain.pem")
	keyDest := filepath.Join(dir, "out", "privkey.pem")
	oldCert, oldKey := testPEMPair(t)
	if err := os.MkdirAll(filepath.Dir(certDest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certDest, []byte(oldCert), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyDest, []byte(oldKey), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewLegoManager("/usr/local/bin/lego", legoPath, "ops@example.com", &fakeRunner{})
	if err := m.Issue(context.Background(), host, certDest, keyDest, "tok"); err == nil {
		t.Fatal("expected the invalid pair to be refused")
	}
	// The service keeps serving the certificate it had.
	assertFile(t, certDest, oldCert, 0o600)
	assertFile(t, keyDest, oldKey, 0o600)
}
