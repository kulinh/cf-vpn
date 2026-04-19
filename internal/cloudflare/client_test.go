package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetZoneIDBySuffix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "example.com" {
			w.Write([]byte(`{"success":true,"result":[{"id":"zone-1"}]}`))
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
	if zone != "zone-1" {
		t.Fatalf("expected zone-1, got %q", zone)
	}
}
