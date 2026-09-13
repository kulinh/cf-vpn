package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kulinh/cf-vpn/internal/commands"
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

func TestEmptySyncRefusal(t *testing.T) {
	withUsers := syncRequest{Users: []syncUser{{Name: "kulinh", VlessUUID: "u", Hy2PW: "p"}}}
	// A list with users, or an explicit confirmation, or a node that has no
	// users anyway: nothing to protect.
	if r := emptySyncRefusal(withUsers, 2); r != "" {
		t.Errorf("a non-empty list must pass: %q", r)
	}
	if r := emptySyncRefusal(syncRequest{ConfirmEmpty: true}, 2); r != "" {
		t.Errorf("confirm_empty must pass: %q", r)
	}
	if r := emptySyncRefusal(syncRequest{}, 0); r != "" {
		t.Errorf("an already empty node must pass: %q", r)
	}
	// The dangerous case: empty list, no confirmation, node still has users.
	r := emptySyncRefusal(syncRequest{}, 3)
	if r == "" || !strings.Contains(r, "3 user(s)") || !strings.Contains(r, "confirm_empty=true") {
		t.Fatalf("expected a refusal naming the count and the flag, got %q", r)
	}
}

func TestParsePortOrWarn(t *testing.T) {
	for in, want := range map[string]int{"": 0, "45321": 45321, " 443 ": 443, "abc": 0, "0": 0, "70000": 0, "-1": 0} {
		if got := parsePortOrWarn("HY2_PORT", in); got != want {
			t.Errorf("parsePortOrWarn(%q) = %d, want %d", in, got, want)
		}
	}
}

// The panel learns about a node's H3 route from /status and nowhere else
// (mergeH3Runtime in panel/worker/src/routes/nodes.ts). If the agent stops
// reporting the pair, the panel keeps a stale row instead of noticing the
// route was turned off — and `omitempty` here is what lets "disabled" arrive
// as an absent key, so the encoding matters as much as the value.
func TestStatusResponseCarriesTheH3Route(t *testing.T) {
	resp := statusResponse{
		XHTTPH3Host: "quic-b55170f3.dongnat247.com",
		XHTTPH3Path: "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10",
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["xhttp_h3_host"] != "quic-b55170f3.dongnat247.com" {
		t.Errorf("xhttp_h3_host = %v", got["xhttp_h3_host"])
	}
	if got["xhttp_h3_path"] != "/3e6f9770dcd50c915247c33fd08196de51072c667f2b2b10" {
		t.Errorf("xhttp_h3_path = %v", got["xhttp_h3_path"])
	}
}

// A node without the route must omit both keys, which the panel reads as
// "not reported, keep the row" rather than "clear it".
func TestStatusResponseOmitsTheH3RouteWhenUnset(t *testing.T) {
	raw, err := json.Marshal(statusResponse{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["xhttp_h3_host"]; ok {
		t.Error("xhttp_h3_host must be omitted when unset")
	}
	if _, ok := got["xhttp_h3_path"]; ok {
		t.Error("xhttp_h3_path must be omitted when unset")
	}
}

// The panel hands out the NaiveProxy route from these three keys; an unset
// route must omit all of them (keep-on-absent, like the H3 pair).
func TestStatusResponseNaiveRoute(t *testing.T) {
	raw, err := json.Marshal(statusResponse{NaiveHost: "cdn.example.net", NaiveUser: "u1", NaivePass: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["naive_host"] != "cdn.example.net" || got["naive_user"] != "u1" || got["naive_pass"] != "p1" {
		t.Errorf("naive fields = %v %v %v", got["naive_host"], got["naive_user"], got["naive_pass"])
	}
	raw, _ = json.Marshal(statusResponse{})
	got = map[string]any{}
	_ = json.Unmarshal(raw, &got)
	for _, k := range []string{"naive_host", "naive_user", "naive_pass"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must be omitted when unset", k)
		}
	}
}

// M4: every body-reading handler caps the body at maxRequestBody and answers
// 413 before doing any work (no lock, no config read).
func TestBodyReadingHandlersRejectOversizedBodies(t *testing.T) {
	// Syntactically valid JSON so that only the size can be the reason.
	huge := `{"new_host":"` + strings.Repeat("a", maxRequestBody+1) + `"}`
	for path, h := range map[string]http.HandlerFunc{
		"/admin/v1/rotate-domain": handleRotateDomain,
		"/admin/v1/sync":          handleSync,
		"/admin/v1/users":         handleUsers,
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(huge)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: status = %d, want 413; body: %.200s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "body_too_large") {
			t.Errorf("%s: body = %.200s", path, rec.Body.String())
		}
	}
}

func TestDecodeJSONBody(t *testing.T) {
	var v map[string]string
	rec := httptest.NewRecorder()
	if !decodeJSONBody(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"kulinh"}`)), &v) || v["name"] != "kulinh" {
		t.Fatalf("a small valid body must decode: %v %s", v, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	if decodeJSONBody(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":`)), &v) {
		t.Fatal("malformed JSON decoded")
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_json") {
		t.Fatalf("malformed JSON: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Just under the cap still decodes.
	body := `{"name":"` + strings.Repeat("a", maxRequestBody-20) + `"}`
	rec = httptest.NewRecorder()
	if !decodeJSONBody(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &v) {
		t.Fatalf("a body under the cap was refused: %d %.200s", rec.Code, rec.Body.String())
	}
}

// L4: the agent enforces commands.MaxUsers like the CLI does.
func TestUserLimitRefusal(t *testing.T) {
	for n := 0; n <= commands.MaxUsers; n++ {
		if r := userLimitRefusal(n); r != "" {
			t.Errorf("userLimitRefusal(%d) = %q, want pass", n, r)
		}
	}
	r := userLimitRefusal(commands.MaxUsers + 1)
	if r == "" || !strings.Contains(r, fmt.Sprintf("max %d", commands.MaxUsers)) {
		t.Fatalf("expected a refusal naming the limit, got %q", r)
	}
}

// A sync over the limit is refused with 400 before the lock or any config is
// touched.
func TestHandleSyncRejectsTooManyUsers(t *testing.T) {
	users := make([]syncUser, commands.MaxUsers+1)
	for i := range users {
		users[i] = syncUser{Name: fmt.Sprintf("user%d", i), VlessUUID: "u", Hy2PW: "p"}
	}
	raw, err := json.Marshal(syncRequest{Users: users})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handleSync(rec, httptest.NewRequest(http.MethodPost, "/admin/v1/sync", strings.NewReader(string(raw))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_limit_exceeded") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
