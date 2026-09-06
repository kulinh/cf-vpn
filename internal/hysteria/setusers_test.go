package hysteria_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kulinh/cf-vpn/internal/hysteria"
)

// H9: a config whose auth block does not match the expected indentation (hand
// edited, flow style, different indent) used to be written back untouched with
// err == nil, so the agent answered {"ok":true} while applying nothing and
// every Hy2 user on the node became invisible to ListUsers.
func TestSetUsersFailsWhenNoUserpassBlock(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"flow-style":   "listen: \":24430\"\nauth: {type: userpass, userpass: {alice: a}}\n",
		"no-auth":      "listen: \":24430\"\nobfs:\n  type: salamander\n",
		"wrong-indent": "listen: \":24430\"\nauth:\n    type: userpass\n    userpass:\n        alice: a\n",
		"empty":        "",
	}
	for name, body := range cases {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		err := hysteria.SetUsers(path, []hysteria.User{{Name: "bob", Password: "b"}})
		if err == nil {
			t.Errorf("%s: SetUsers returned nil while applying nothing", name)
		}
		after, _ := os.ReadFile(path)
		if string(after) != body {
			t.Errorf("%s: config was modified despite the error:\n%s", name, after)
		}
	}
}

// M-G1/M-G8: the writer must enforce 0600 on an already-existing file, not
// only on create.
func TestSetUsersTightensPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: \":24430\"\nauth:\n  type: userpass\n  userpass:\n    alice: \"a\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hysteria.SetUsers(path, []hysteria.User{{Name: "bob", Password: "b"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	users, err := hysteria.ListUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "bob" {
		t.Fatalf("users = %#v", users)
	}
}
