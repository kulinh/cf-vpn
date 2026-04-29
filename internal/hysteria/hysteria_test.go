package hysteria_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/hysteria"
)

func TestRenderConfigGolden(t *testing.T) {
	cfg := hysteria.Config{
		Listen:   ":34567",
		TLSCert:  "/etc/cfvpn/hysteria/cert.pem",
		TLSKey:   "/etc/cfvpn/hysteria/key.pem",
		ObfsPW:   "obfs-XXXX",
		UpMbps:   100,
		DownMbps: 1000,
		Users:    []hysteria.User{{Name: "user1", Password: "pw1"}},
	}
	got, err := hysteria.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("testdata/golden.yaml")
	if string(got) != string(want) {
		t.Fatalf("render mismatch:\n--got--\n%s\n--want--\n%s", got, want)
	}
}

func TestRenderMissingRequiredField(t *testing.T) {
	_, err := hysteria.Render(hysteria.Config{
		Listen: ":34567",
		TLSKey: "/etc/cfvpn/hysteria/key.pem",
		ObfsPW: "obfs-XXXX",
	})
	if err == nil || err.Error() != "hysteria: missing required field" {
		t.Fatalf("missing field error = %v", err)
	}
}

func TestRenderSortsUsers(t *testing.T) {
	got, err := hysteria.Render(hysteria.Config{
		Listen:   ":34567",
		TLSCert:  "/cert.pem",
		TLSKey:   "/key.pem",
		ObfsPW:   "obfs",
		UpMbps:   10,
		DownMbps: 20,
		Users: []hysteria.User{
			{Name: "zara", Password: "z"},
			{Name: "alice", Password: "a"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(got), "    \"alice\": \"a\"\n") > strings.Index(string(got), "    \"zara\": \"z\"\n") {
		t.Fatalf("users not sorted:\n%s", got)
	}
}

func TestRenderQuotesSpecialCharacterScalars(t *testing.T) {
	got, err := hysteria.Render(hysteria.Config{
		Listen:   ":34567",
		TLSCert:  "/cert: #one.pem",
		TLSKey:   "/key{two}.pem",
		ObfsPW:   "abc # comment\nwith\ttab\rand \"quote\" \\ slash",
		UpMbps:   10,
		DownMbps: 20,
		Users: []hysteria.User{
			{Name: "foo: bar", Password: "*alias"},
			{Name: "{quoted}", Password: "abc # comment"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"listen: \":34567\"",
		"cert: \"/cert: #one.pem\"",
		"key: \"/key{two}.pem\"",
		"password: \"abc # comment\\nwith\\ttab\\rand \\\"quote\\\" \\\\ slash\"",
		"    \"foo: bar\": \"*alias\"",
		"    \"{quoted}\": \"abc # comment\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestWriteConfigCreatesParentDirAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	err := hysteria.WriteConfig(path, hysteria.Config{
		Listen: ":34567", TLSCert: "/cert.pem", TLSKey: "/key.pem", ObfsPW: "obfs",
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
}

func TestWriteConfigRewritesExistingFileAsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old: config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := hysteria.WriteConfig(path, hysteria.Config{
		Listen: ":34567", TLSCert: "/cert.pem", TLSKey: "/key.pem", ObfsPW: "obfs",
	}); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
}

func TestListUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`listen: :34567
x-extra: true
auth:
  type: userpass
  userpass:
    zara: z
    alice: a
bandwidth:
  up: 1mbps
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := hysteria.ListUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []hysteria.User{{Name: "alice", Password: "a"}, {Name: "zara", Password: "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users = %#v, want %#v", got, want)
	}
}

func TestSetUsersPreservesUnrelatedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `listen: :34567
tls:
  cert: /cert.pem
  key: /key.pem
custom:
  keep: yes
auth:
  type: userpass
  userpass:
    old: pw
bandwidth:
  up: 100mbps
  down: 1000mbps
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hysteria.SetUsers(path, []hysteria.User{{Name: "zara", Password: "z"}, {Name: "alice", Password: "a"}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"custom:\n  keep: yes", "auth:\n  type: userpass\n  userpass:\n    \"alice\": \"a\"\n    \"zara\": \"z\"\n", "bandwidth:\n  up: 100mbps"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "old: pw") {
		t.Fatalf("old user still present:\n%s", text)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
}

func TestListUsersParsesQuotedColonInUserpassKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `listen: ":34567"
auth:
  type: userpass
  userpass:
    "foo: bar": "*alias # x"
bandwidth:
  up: 100mbps
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := hysteria.ListUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []hysteria.User{{Name: "foo: bar", Password: "*alias # x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users = %#v, want %#v", got, want)
	}
}

func TestSetUsersAndListUsersRoundTripSpecialCharacterUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `listen: ":34567"
auth:
  type: userpass
  userpass:
    "old": "pw"
bandwidth:
  up: 100mbps
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []hysteria.User{
		{Name: "abc # comment", Password: "foo: bar"},
		{Name: "name\nline", Password: "pass\tword\rquoted \"value\" and \\ slash"},
		{Name: "*alias", Password: "{braces}"},
	}

	if err := hysteria.SetUsers(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := hysteria.ListUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	sortUsers(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users = %#v, want %#v", got, want)
	}
}

func TestSetUsersMissingFile(t *testing.T) {
	err := hysteria.SetUsers(filepath.Join(t.TempDir(), "missing.yaml"), nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
}

func TestReloadServiceRestartsHysteria(t *testing.T) {
	r := &fakeRunner{}
	if err := hysteria.ReloadService(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.name != "systemctl" {
		t.Fatalf("name = %q, want systemctl", r.name)
	}
	wantArgs := []string{"restart", "cfvpn-hysteria.service"}
	if !reflect.DeepEqual(r.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", r.args, wantArgs)
	}
}

type fakeRunner struct {
	name string
	args []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	r.name = name
	r.args = append([]string(nil), args...)
	return nil
}

func sortUsers(users []hysteria.User) {
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
}
