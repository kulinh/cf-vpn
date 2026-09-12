package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeRulesStore struct {
	calls []struct {
		db, sql string
		params  []any
	}
	rows json.RawMessage
	err  error
}

func (f *fakeRulesStore) D1Query(_ context.Context, db, sql string, params []any) (json.RawMessage, error) {
	f.calls = append(f.calls, struct {
		db, sql string
		params  []any
	}{db, sql, params})
	if f.err != nil {
		return nil, f.err
	}
	if f.rows == nil {
		return json.RawMessage("[]"), nil
	}
	return f.rows, nil
}

func TestNormalizeRulesMode(t *testing.T) {
	for in, want := range map[string]string{"cn": "cn", "China": "cn", "home": "cn", "UAE": "uae", "none": "none", "off": "none"} {
		got, err := NormalizeRulesMode(in)
		if err != nil || got != want {
			t.Errorf("%q → (%q, %v), want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeRulesMode("mars"); err == nil || !strings.Contains(err.Error(), "cn, uae, none") {
		t.Fatalf("unknown mode must list the valid ones, got %v", err)
	}
}

func TestRunRulesModeSetUpserts(t *testing.T) {
	f := &fakeRulesStore{}
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	err := RunRulesModeSet(context.Background(), "UAE", RulesModeDeps{Store: f, Now: func() time.Time { return now }}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0].db != DefaultD1DatabaseID {
		t.Fatalf("calls = %+v", f.calls)
	}
	c := f.calls[0]
	if !strings.HasPrefix(c.sql, "INSERT INTO settings") || !strings.Contains(c.sql, "ON CONFLICT(key) DO UPDATE") {
		t.Fatalf("sql = %q", c.sql)
	}
	if fmt.Sprint(c.params) != fmt.Sprint([]any{"rules_mode", "uae", now.UnixMilli()}) {
		t.Fatalf("params = %v", c.params)
	}
	if !strings.Contains(out.String(), "rules_mode = uae") || !strings.Contains(out.String(), "sr_proxy_list_UAE") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunRulesModeSetRejectsGarbageBeforeWriting(t *testing.T) {
	f := &fakeRulesStore{}
	if err := RunRulesModeSet(context.Background(), "mars", RulesModeDeps{Store: f}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
	if len(f.calls) != 0 {
		t.Fatalf("must not touch D1 on a bad mode, calls=%d", len(f.calls))
	}
}

func TestRunRulesModeShow(t *testing.T) {
	var out bytes.Buffer
	if err := RunRulesModeShow(context.Background(), RulesModeDeps{Store: &fakeRulesStore{}, DatabaseID: "11111111-2222-3333-4444-555555555555"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rules_mode = cn (default, never set)") {
		t.Fatalf("unset → %q", out.String())
	}

	out.Reset()
	f := &fakeRulesStore{rows: json.RawMessage(`[{"value":"uae","updated_at":1789300800000}]`)}
	if err := RunRulesModeShow(context.Background(), RulesModeDeps{Store: f}, &out); err != nil {
		t.Fatal(err)
	}
	if f.calls[0].db != DefaultD1DatabaseID || !strings.Contains(f.calls[0].sql, "FROM settings WHERE key = ?") {
		t.Fatalf("call = %+v", f.calls[0])
	}
	if !strings.Contains(out.String(), "rules_mode = uae (set 2026-09-") {
		t.Fatalf("set → %q", out.String())
	}

	out.Reset()
	if err := RunRulesModeShow(context.Background(), RulesModeDeps{Store: &fakeRulesStore{err: fmt.Errorf("boom")}}, &out); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("store error must surface, got %v", err)
	}
}
