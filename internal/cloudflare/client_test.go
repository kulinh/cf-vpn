package cloudflare

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetZoneIDBySuffix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "example.com" {
			w.Write([]byte(`{"success":true,"result":[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
			return
		}
		w.Write([]byte(`{"success":true,"result":[]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	zone, err := c.GetZoneID(context.Background(), "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if zone != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("expected aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, got %q", zone)
	}
}

func TestUpsertARecordCreatesWhenAbsent(t *testing.T) {
	mux := http.NewServeMux()
	var posted bool
	mux.HandleFunc("/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("type") != "A" {
				t.Fatalf("expected type=A, got %s", r.URL.Query().Get("type"))
			}
			if r.URL.Query().Get("name.exact") != "vpn+test.example.com" {
				t.Fatalf("expected name.exact=vpn+test.example.com, got %s", r.URL.Query().Get("name.exact"))
			}
			if r.URL.Query().Get("match") != "all" {
				t.Fatalf("expected match=all, got %s", r.URL.Query().Get("match"))
			}
			w.Write([]byte(`{"success":true,"result":[]}`))
		case http.MethodPost:
			posted = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"type":"A"`) {
				t.Fatalf("expected type A, got %s", body)
			}
			if !strings.Contains(string(body), `"content":"203.0.113.42"`) {
				t.Fatalf("expected content IP, got %s", body)
			}
			if !strings.Contains(string(body), `"proxied":false`) {
				t.Fatalf("expected proxied:false, got %s", body)
			}
			w.Write([]byte(`{"success":true,"result":{"id":"dddddddddddddddddddddddddddddddd"}}`))
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.UpsertARecord(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "vpn+test.example.com", "203.0.113.42"); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("expected POST")
	}
}

func TestUpsertARecordUpdatesWhenPresent(t *testing.T) {
	mux := http.NewServeMux()
	var put bool
	mux.HandleFunc("/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			t.Fatalf("expected type=A, got %s", r.URL.Query().Get("type"))
		}
		if r.URL.Query().Get("name.exact") != "vpn+test.example.com" {
			t.Fatalf("expected name.exact=vpn+test.example.com, got %s", r.URL.Query().Get("name.exact"))
		}
		if r.URL.Query().Get("match") != "all" {
			t.Fatalf("expected match=all, got %s", r.URL.Query().Get("match"))
		}
		w.Write([]byte(`{"success":true,"result":[{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`))
	})
	mux.HandleFunc("/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/dns_records/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		put = true
		w.Write([]byte(`{"success":true,"result":{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.UpsertARecord(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "vpn+test.example.com", "203.0.113.42"); err != nil {
		t.Fatal(err)
	}
	if !put {
		t.Fatal("expected PUT to existing record")
	}
}

func TestDeleteARecordByNameRemovesMatching(t *testing.T) {
	mux := http.NewServeMux()
	var deleted bool
	mux.HandleFunc("/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			t.Fatalf("expected type=A, got %s", r.URL.Query().Get("type"))
		}
		if r.URL.Query().Get("name.exact") != "old.example.com" {
			t.Fatalf("expected name.exact=old.example.com, got %s", r.URL.Query().Get("name.exact"))
		}
		if r.URL.Query().Get("match") != "all" {
			t.Fatalf("expected match=all, got %s", r.URL.Query().Get("match"))
		}
		w.Write([]byte(`{"success":true,"result":[{"id":"cccccccccccccccccccccccccccccccc"}]}`))
	})
	mux.HandleFunc("/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/dns_records/cccccccccccccccccccccccccccccccc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		deleted = true
		w.Write([]byte(`{"success":true,"result":{"id":"cccccccccccccccccccccccccccccccc"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.DeleteARecordByName(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "old.example.com"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected DELETE")
	}
}

func TestDeleteARecordByNameNoopWhenAbsent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/zones/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			t.Fatalf("expected type=A, got %s", r.URL.Query().Get("type"))
		}
		if r.URL.Query().Get("name.exact") != "missing.example.com" {
			t.Fatalf("expected name.exact=missing.example.com, got %s", r.URL.Query().Get("name.exact"))
		}
		if r.URL.Query().Get("match") != "all" {
			t.Fatalf("expected match=all, got %s", r.URL.Query().Get("match"))
		}
		w.Write([]byte(`{"success":true,"result":[]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: "a", HTTP: ts.Client()}
	if err := c.DeleteARecordByName(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "missing.example.com"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// D1Query is how a node writes fleet settings from Go (the shell twin is
// scripts/d1-set-node.sh). Its failure modes were untested: D1 reports a bad statement
// inside a success:true envelope, and a gateway page is not JSON at all.
func TestD1Query(t *testing.T) {
	const acct = "8706ce6c15ce482de516ffc045414678"
	const db = "0649f07f-e2c0-47f3-b84a-273f7f67332e"
	newClient := func(body string, status int) (*Client, *string) {
		var gotBody string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			gotBody = string(raw)
			if want := "/client/v4/accounts/" + acct + "/d1/database/" + db + "/query"; r.URL.Path != want {
				t.Errorf("path = %s, want %s", r.URL.Path, want)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(ts.Close)
		return &Client{BaseURL: ts.URL + "/client/v4", Token: "t", AccountID: acct, HTTP: ts.Client()}, &gotBody
	}

	c, body := newClient(`{"success":true,"result":[{"success":true,"results":[{"value":"uae"}]}]}`, 200)
	rows, err := c.D1Query(context.Background(), db, "SELECT value FROM settings WHERE key = ?", []any{"rules_mode"})
	if err != nil {
		t.Fatal(err)
	}
	if string(rows) != `[{"value":"uae"}]` {
		t.Fatalf("rows = %s", rows)
	}
	if !strings.Contains(*body, `"params":["rules_mode"]`) {
		t.Fatalf("parameters must be bound, not interpolated: %s", *body)
	}

	// A rejected statement: the reason lives in the statement, not the envelope.
	c, _ = newClient(`{"success":true,"result":[{"success":false,"error":"no such column: rules_mode"}]}`, 200)
	_, err = c.D1Query(context.Background(), db, "SELECT rules_mode FROM settings", nil)
	if err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Fatalf("expected the statement error to surface, got %v", err)
	}

	// A write returns no rows.
	c, _ = newClient(`{"success":true,"result":[{"success":true}]}`, 200)
	rows, err = c.D1Query(context.Background(), db, "INSERT INTO settings VALUES (?,?,?)", []any{"k", "v", 1})
	if err != nil || string(rows) != "[]" {
		t.Fatalf("write: rows=%s err=%v", rows, err)
	}

	// A Cloudflare gateway page is not JSON; the status has to be in the error.
	c, _ = newClient(`<html>502 Bad Gateway</html>`, 502)
	if _, err := c.D1Query(context.Background(), db, "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("expected the HTTP status in the error, got %v", err)
	}

	// A database id that is not a UUID never reaches the network.
	c, _ = newClient(`{"success":true,"result":[]}`, 200)
	if _, err := c.D1Query(context.Background(), "../../accounts", "SELECT 1", nil); err == nil || !strings.Contains(err.Error(), "d1 database id") {
		t.Fatalf("expected the id to be rejected, got %v", err)
	}
}
