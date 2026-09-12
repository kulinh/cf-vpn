package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/state"
)

// The fleet-wide "travel mode" for the Shadowrocket .conf: which blocked-site
// module the panel inlines when a subscription link carries no ?rules=.
// Stored in D1 (settings.rules_mode, migration 0022) so a config link that is
// already installed on the phone follows the trip without being edited.
//
// It lives in D1 rather than on a node because the reader is the Worker; it is
// written from VNM-01 with the account CF token in /etc/cfvpn/cfvpn.env (the
// same path scripts/d1-set-node.sh uses), so nothing new has to be exposed
// through Cloudflare Access.

// DefaultD1DatabaseID is the panel's production database (cfvpn_panel_prod).
const DefaultD1DatabaseID = "0649f07f-e2c0-47f3-b84a-273f7f67332e"

const rulesModeKey = "rules_mode"

// RulesModes are the values the Worker understands (see lib/cnrules.ts and
// routes/sub.ts): cn/uae pick a module, none leaves the [Rule] tail bare.
var RulesModes = []string{"cn", "uae", "none"}

var rulesModeEnvPath = "/etc/cfvpn/cfvpn.env"

// RulesModeStore is the one D1 call this needs; cloudflare.Client satisfies it.
type RulesModeStore interface {
	D1Query(ctx context.Context, databaseID, sql string, params []any) (json.RawMessage, error)
}

// RulesModeDeps lets tests inject a fake store and clock.
type RulesModeDeps struct {
	Store      RulesModeStore   // nil = cloudflare.DefaultClient from rulesModeEnvPath
	DatabaseID string           // "" = CFVPN_D1_DATABASE_ID in the env file, else DefaultD1DatabaseID
	Now        func() time.Time // nil = time.Now
}

func (d *RulesModeDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *RulesModeDeps) store() (RulesModeStore, string, error) {
	if d.Store != nil {
		db := d.DatabaseID
		if db == "" {
			db = DefaultD1DatabaseID
		}
		return d.Store, db, nil
	}
	env, err := state.Load(rulesModeEnvPath)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", rulesModeEnvPath, err)
	}
	token, account := strings.TrimSpace(env["CF_API_TOKEN"]), strings.TrimSpace(env["CF_ACCOUNT_ID"])
	if token == "" || account == "" {
		return nil, "", fmt.Errorf("%s: CF_API_TOKEN and CF_ACCOUNT_ID must both be set", rulesModeEnvPath)
	}
	db := d.DatabaseID
	if db == "" {
		db = strings.TrimSpace(env["CFVPN_D1_DATABASE_ID"])
	}
	if db == "" {
		db = DefaultD1DatabaseID
	}
	return cloudflare.DefaultClient(token, account), db, nil
}

// NormalizeRulesMode accepts the stored values plus the words people type:
// china → cn, home → cn (blacklist mode is harmless at home), off → none.
func NormalizeRulesMode(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cn", "china":
		return "cn", nil
	case "uae":
		return "uae", nil
	case "none", "off":
		return "none", nil
	case "home":
		return "cn", nil
	}
	return "", fmt.Errorf("unknown rules mode %q (want one of %s)", s, strings.Join(RulesModes, ", "))
}

func describeRulesMode(mode string) string {
	switch mode {
	case "uae":
		return "Shadowrocket .conf inlines sr_proxy_list_UAE (OTT calls + sites TDRA blocks)"
	case "none":
		return "Shadowrocket .conf ends with a bare FINAL,DIRECT; load a module by hand"
	default:
		return "Shadowrocket .conf inlines sr_proxy_list_CN (GFW list)"
	}
}

// RunRulesModeSet writes settings.rules_mode and reports what the .conf will
// now contain. The phone still has to refresh its config to see the change.
func RunRulesModeSet(ctx context.Context, mode string, deps RulesModeDeps, stdout io.Writer) error {
	normalized, err := NormalizeRulesMode(mode)
	if err != nil {
		return err
	}
	store, db, err := deps.store()
	if err != nil {
		return err
	}
	ts := deps.now().UnixMilli()
	if _, err := store.D1Query(ctx, db,
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		[]any{rulesModeKey, normalized, ts}); err != nil {
		return fmt.Errorf("write %s: %w", rulesModeKey, err)
	}
	fmt.Fprintf(stdout, "rules_mode = %s — %s\n", normalized, describeRulesMode(normalized))
	fmt.Fprintln(stdout, "Subscription links without ?rules= follow it from the next refresh (pull the RWL8899 config in Shadowrocket).")
	return nil
}

// RunRulesModeShow prints the stored mode, or the default when never set.
func RunRulesModeShow(ctx context.Context, deps RulesModeDeps, stdout io.Writer) error {
	store, db, err := deps.store()
	if err != nil {
		return err
	}
	raw, err := store.D1Query(ctx, db, "SELECT value, updated_at FROM settings WHERE key = ?", []any{rulesModeKey})
	if err != nil {
		return fmt.Errorf("read %s: %w", rulesModeKey, err)
	}
	var rows []struct {
		Value     string `json:"value"`
		UpdatedAt int64  `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("read %s: unexpected rows: %w", rulesModeKey, err)
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "rules_mode = cn (default, never set) — %s\n", describeRulesMode("cn"))
		return nil
	}
	mode, err := NormalizeRulesMode(rows[0].Value)
	if err != nil {
		fmt.Fprintf(stdout, "rules_mode = %q (unknown value, the Worker falls back to cn)\n", rows[0].Value)
		return nil
	}
	when := time.UnixMilli(rows[0].UpdatedAt).UTC().Format(time.RFC3339)
	fmt.Fprintf(stdout, "rules_mode = %s (set %s) — %s\n", mode, when, describeRulesMode(mode))
	return nil
}
