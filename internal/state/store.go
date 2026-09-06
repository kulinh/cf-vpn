package state

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kulinh/cf-vpn/internal/fsutil"
)

// envKeyRE is the shell-compatible env key shape. The file is consumed both by
// Load below and by systemd's EnvironmentFile= (cfvpn-agent.service) and the
// shell installer's `source`, so anything outside this shape is not a key.
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func Load(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
	}
	return out, s.Err()
}

// ValidateEnv rejects any key or value that could not survive a Load/Save round
// trip as a single KEY=VALUE line.
//
// This is the line-injection guard. The file is parsed line by line with
// last-wins semantics, so a value containing a newline lets its writer append
// arbitrary further keys — e.g. a rotate that puts
// "cdn.example.com\nAGENT_SHARED_SECRET=attacker" into DOMAIN silently replaces
// the agent's auth secret at the next restart (systemd reads this same file via
// EnvironmentFile=). Values are hex, UUIDs, hostnames, ports and URLs today, so
// refusing CR/LF and NUL outright costs nothing and needs no escaping scheme
// that `source` would have to understand.
func ValidateEnv(values map[string]string) error {
	for k, v := range values {
		if !envKeyRE.MatchString(k) {
			return fmt.Errorf("state: invalid env key %q", k)
		}
		if i := strings.IndexAny(v, "\n\r\x00"); i >= 0 {
			return fmt.Errorf("state: value for %q contains a newline or NUL at byte %d", k, i)
		}
	}
	return nil
}

// SaveAtomic writes the env map as sorted KEY=VALUE lines. It refuses to write
// anything that would not round-trip through Load (see ValidateEnv) and leaves
// the previous file untouched on any error.
func SaveAtomic(path string, values map[string]string, mode os.FileMode) error {
	if err := ValidateEnv(values); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values[k])
		b.WriteByte('\n')
	}
	return fsutil.WriteFile(path, []byte(b.String()), mode)
}
