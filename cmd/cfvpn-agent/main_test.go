package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// H7/H8 at the agent boundary: a rotate request is the one place where a
// caller-supplied string becomes a line in cfvpn.env, a hostname in
// cloudflared's YAML ingress, and a certificate subject.
func TestValidateRotateRequestRejectsInjection(t *testing.T) {
	bad := []rotateRequest{
		{NewHost: ""},
		{NewHost: "cdn-a1b2.rwl.one\nAGENT_SHARED_SECRET=attacker"},
		{NewHost: "cdn-a1b2.rwl.one\n    service: http://127.0.0.1:22"},
		{NewHost: "cdn a1b2.rwl.one"},
		{NewHost: "cdn-a1b2.rwl.one", OldHost: "old\nDOMAIN=x"},
		{NewHost: "cdn-a1b2.rwl.one", NewHy2Host: "hy2\rx"},
		{NewHost: "cdn-a1b2.rwl.one", OldHy2Host: "-bad.example.com"},
		{NewHost: "cdn-a1b2.rwl.one", NewZoneID: "../../accounts"},
		{NewHost: "cdn-a1b2.rwl.one", OldZoneID: "not-a-zone-id"},
	}
	for _, req := range bad {
		r := req
		if err := validateRotateRequest(&r); err == nil {
			t.Errorf("accepted %#v", req)
		}
	}
}

func TestValidateRotateRequestAcceptsRealRequest(t *testing.T) {
	req := rotateRequest{
		NewHost:      " cdn-a1b2.rwl.one ",
		NewZoneID:    "0123456789abcdef0123456789abcdef",
		OldHost:      "cdn-9z8y.rwl.one",
		OldZoneID:    "0123456789abcdef0123456789abcdef",
		NewHy2Host:   "hy2-c3d4.rwl.one",
		NewHy2ZoneID: "fedcba98765432100123456789abcdef",
	}
	if err := validateRotateRequest(&req); err != nil {
		t.Fatalf("rejected a valid request: %v", err)
	}
	if req.NewHost != "cdn-a1b2.rwl.one" {
		t.Fatalf("new_host was not trimmed: %q", req.NewHost)
	}
}

// A rotate carrying an injected hostname must be refused before any config is
// touched — 400, not a 500 from somewhere deeper.
func TestHandleRotateDomainRejectsInjectedHostBeforeDoingWork(t *testing.T) {
	body := `{"new_host":"cdn-a1b2.rwl.one\nAGENT_SHARED_SECRET=attacker"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/rotate-domain", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handleRotateDomain(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_host") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandleRotateDomainRejectsNonPOST(t *testing.T) {
	rec := httptest.NewRecorder()
	handleRotateDomain(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/rotate-domain", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestZoneForHost(t *testing.T) {
	cases := map[string]string{
		"cdn-a1b2.rwl.one":  "rwl.one",
		"hkg-01.rwl247.dev": "rwl247.dev",
		"deep.sub.rwl.one":  "rwl.one",
		"example.com":       "example.com",
		"":                  "",
	}
	for host, want := range cases {
		if got := zoneForHost(host); got != want {
			t.Errorf("zoneForHost(%q) = %q, want %q", host, got, want)
		}
	}
}
