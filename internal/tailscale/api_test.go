package tailscale

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newFakeAPI(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	policy := samplePolicy
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "id1" || r.Form.Get("client_secret") != "sec1" {
			http.Error(w, `{"message":"bad client"}`, http.StatusUnauthorized)
			return
		}
		seen = append(seen, "token")
		_, _ = w.Write([]byte(`{"access_token":"tok123","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v2/tailnet/-/acl", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok123" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			seen = append(seen, "get")
			w.Header().Set("ETag", `"etag-1"`)
			_, _ = w.Write([]byte(policy))
		case http.MethodPost:
			seen = append(seen, "post if-match="+r.Header.Get("If-Match"))
			if r.Header.Get("If-Match") != `"etag-1"` {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			b, _ := io.ReadAll(r.Body)
			policy = string(b)
			_, _ = w.Write(b)
		}
	})
	mux.HandleFunc("/api/v2/tailnet/-/acl/validate", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "validate")
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "BROKEN") {
			_, _ = w.Write([]byte(`{"message":"policy is broken"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestClientRoundTrip(t *testing.T) {
	srv, seen := newFakeAPI(t)
	c := &Client{ClientID: "id1", ClientSecret: "sec1", BaseURL: srv.URL}
	ctx := context.Background()
	pol, etag, err := c.GetPolicy(ctx)
	if err != nil || etag != `"etag-1"` || !strings.Contains(string(pol), "derpMap") {
		t.Fatalf("get: %v etag=%q", err, etag)
	}
	on, _ := SetOmitDefaultRegions(pol, true)
	if err := c.ValidatePolicy(ctx, on); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidatePolicy(ctx, []byte(`{"BROKEN": 1}`)); err == nil || !strings.Contains(err.Error(), "policy is broken") {
		t.Fatalf("validate must surface the API message, got %v", err)
	}
	stored, err := c.SetPolicy(ctx, on, etag)
	if err != nil || !strings.Contains(string(stored), `"OmitDefaultRegions": true`) {
		t.Fatalf("set: %v\n%s", err, stored)
	}
	if _, err := c.SetPolicy(ctx, on, `"stale"`); err == nil || !strings.Contains(err.Error(), "412") {
		t.Fatalf("stale etag must be refused, got %v", err)
	}
	// One token fetch for the whole session.
	tokens := 0
	for _, s := range *seen {
		if s == "token" {
			tokens++
		}
	}
	if tokens != 1 {
		t.Fatalf("expected one token exchange, got %d (%v)", tokens, *seen)
	}
}

func TestClientBadCredentials(t *testing.T) {
	srv, _ := newFakeAPI(t)
	c := &Client{ClientID: "id1", ClientSecret: "wrong", BaseURL: srv.URL}
	if _, _, err := c.GetPolicy(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}
