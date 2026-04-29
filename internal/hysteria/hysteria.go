package hysteria

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kulinh/cf-vpn/internal/systemd"
)

type User struct{ Name, Password string }

type Config struct {
	Listen   string
	TLSCert  string
	TLSKey   string
	ObfsPW   string
	UpMbps   int
	DownMbps int
	Users    []User
}

func Render(cfg Config) ([]byte, error) {
	if cfg.Listen == "" || cfg.TLSCert == "" || cfg.TLSKey == "" || cfg.ObfsPW == "" {
		return nil, fmt.Errorf("hysteria: missing required field")
	}

	users := append([]User(nil), cfg.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	var b strings.Builder
	fmt.Fprintf(&b, "listen: %s\n", yamlQuote(cfg.Listen))
	fmt.Fprintf(&b, "tls:\n  cert: %s\n  key: %s\n", yamlQuote(cfg.TLSCert), yamlQuote(cfg.TLSKey))
	fmt.Fprintf(&b, "obfs:\n  type: salamander\n  salamander:\n    password: %s\n", yamlQuote(cfg.ObfsPW))
	fmt.Fprintf(&b, "bandwidth:\n  up: %dmbps\n  down: %dmbps\n", cfg.UpMbps, cfg.DownMbps)
	b.WriteString("auth:\n  type: userpass\n  userpass:\n")
	for _, u := range users {
		fmt.Fprintf(&b, "    %s: %s\n", yamlQuote(u.Name), yamlQuote(u.Password))
	}
	return []byte(b.String()), nil
}

func WriteConfig(path string, cfg Config) error {
	body, err := Render(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func Load(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Users: parseUsers(body)}
	return cfg, nil
}

func ListUsers(path string) ([]User, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	users := append([]User(nil), cfg.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	return users, nil
}

func SetUsers(path string, users []User) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := replaceUserpass(body, users)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func ReloadService(ctx context.Context, r systemd.Runner) error {
	return r.Run(ctx, "systemctl", "restart", "cfvpn-hysteria.service")
}

func parseUsers(body []byte) []User {
	lines := splitLines(body)
	start, end := userpassRange(lines)
	if start < 0 {
		return nil
	}
	var users []User
	for _, line := range lines[start:end] {
		if indentWidth(line) != 4 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, pw, ok := cutUserpassEntry(trimmed)
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		pw = strings.TrimSpace(pw)
		if unquoted, ok := yamlUnquote(name); ok {
			name = unquoted
		}
		if unquoted, ok := yamlUnquote(pw); ok {
			pw = unquoted
		}
		if name != "" {
			users = append(users, User{Name: name, Password: pw})
		}
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	return users
}

func replaceUserpass(body []byte, users []User) []byte {
	lines := splitLines(body)
	start, end := userpassRange(lines)
	if start < 0 {
		return body
	}

	users = append([]User(nil), users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	var repl []string
	for _, u := range users {
		repl = append(repl, fmt.Sprintf("    %s: %s", yamlQuote(u.Name), yamlQuote(u.Password)))
	}

	out := make([]string, 0, len(lines)-end+start+len(repl))
	out = append(out, lines[:start]...)
	out = append(out, repl...)
	out = append(out, lines[end:]...)
	return []byte(strings.Join(out, "\n") + trailingNewline(body))
}

func userpassRange(lines []string) (int, int) {
	inAuth := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := indentWidth(line)
		if indent == 0 && strings.HasSuffix(trimmed, ":") {
			inAuth = trimmed == "auth:"
			continue
		}
		if inAuth && indent == 2 && trimmed == "userpass:" {
			start := i + 1
			end := start
			for end < len(lines) {
				if strings.TrimSpace(lines[end]) == "" {
					end++
					continue
				}
				if indentWidth(lines[end]) <= 2 {
					break
				}
				end++
			}
			return start, end
		}
	}
	return -1, -1
}

func cutUserpassEntry(s string) (string, string, bool) {
	inQuote := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inQuote && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && c == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func splitLines(body []byte) []string {
	trimmed := bytes.TrimSuffix(body, []byte("\n"))
	if len(trimmed) == 0 {
		return nil
	}
	var lines []string
	s := bufio.NewScanner(bytes.NewReader(trimmed))
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	return lines
}

func indentWidth(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

func trailingNewline(body []byte) string {
	if bytes.HasSuffix(body, []byte("\n")) {
		return "\n"
	}
	return ""
}

func yamlQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func yamlUnquote(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(s)-1; i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s)-1 {
			return "", false
		}
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			return "", false
		}
	}
	return b.String(), true
}
