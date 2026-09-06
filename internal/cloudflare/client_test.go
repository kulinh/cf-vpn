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
