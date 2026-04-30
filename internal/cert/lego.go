package cert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const adminHostZone = "rwl247.dev"

type Runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.CombinedOutput()
}

type LegoManager struct {
	Binary string
	Path   string
	Email  string
	Runner Runner
}

func NewDefault() *LegoManager {
	email := os.Getenv("LEGO_EMAIL")
	if email == "" {
		email = "admin@" + adminHostZone
	}
	return NewLegoManager("/usr/local/bin/lego", "/etc/cfvpn/lego", email, ExecRunner{})
}

func NewLegoManager(bin, path, email string, r Runner) *LegoManager {
	return &LegoManager{Binary: bin, Path: path, Email: email, Runner: r}
}

func (m *LegoManager) Issue(ctx context.Context, host, certPath, keyPath, token string) error {
	if err := m.run(ctx, host, token, "run"); err != nil {
		return err
	}
	return m.copyResult(host, certPath, keyPath)
}

func (m *LegoManager) Renew(ctx context.Context, host, certPath, keyPath, token string, days int) error {
	args := []string{"renew"}
	if days > 0 {
		args = append(args, "--days="+strconv.Itoa(days))
	}
	if err := m.run(ctx, host, token, args...); err != nil {
		return err
	}
	return m.copyResult(host, certPath, keyPath)
}

func (m *LegoManager) run(ctx context.Context, host, token string, command ...string) error {
	r := m.Runner
	if r == nil {
		r = ExecRunner{}
	}
	bin := m.Binary
	if bin == "" {
		bin = "/usr/local/bin/lego"
	}
	path := m.Path
	if path == "" {
		path = "/etc/cfvpn/lego"
	}
	email := m.Email
	if email == "" {
		email = "admin@" + adminHostZone
	}
	args := []string{
		"--accept-tos",
		"--email=" + email,
		"--dns=cloudflare",
		"--path=" + path,
		"--domains=" + host,
	}
	if os.Getenv("LEGO_DISABLE_CP") == "1" {
		args = append(args, "--dns.propagation-rns")
	}
	if resolvers := os.Getenv("LEGO_DNS_RESOLVERS"); resolvers != "" {
		args = append(args, "--dns.resolvers="+resolvers)
	}
	if wait := os.Getenv("LEGO_PROPAGATION_WAIT"); wait != "" {
		args = append(args, "--dns.propagation-wait="+wait)
	}
	args = append(args, command...)
	out, err := r.Run(ctx, []string{"CF_DNS_API_TOKEN=" + token}, bin, args...)
	if err != nil {
		return fmt.Errorf("lego %s %s: %w: %s", command[0], host, err, out)
	}
	return nil
}

func (m *LegoManager) copyResult(host, certPath, keyPath string) error {
	path := m.Path
	if path == "" {
		path = "/etc/cfvpn/lego"
	}
	certSrc := filepath.Join(path, "certificates", host+".crt")
	keySrc := filepath.Join(path, "certificates", host+".key")
	if err := copyCertFile(certSrc, certPath); err != nil {
		return fmt.Errorf("copy cert: %w", err)
	}
	if err := copyCertFile(keySrc, keyPath); err != nil {
		return fmt.Errorf("copy key: %w", err)
	}
	return nil
}

func copyCertFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
