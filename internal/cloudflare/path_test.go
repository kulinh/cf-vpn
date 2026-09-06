package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const goodZone = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// LOW/C2: ids used to be concatenated into the API path unchecked. The tunnel
// id in particular arrives from an agent's status response, so a traversal
// payload reached a different Cloudflare endpoint entirely.
func TestClientRejectsTraversalInIDs(t *testing.T) {
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		w.Write([]byte(`{"success":true,"result":[]}`))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL + "/client/v4", Token: "t", AccountID: goodZone, HTTP: srv.Client()}

	bad := "../accounts/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/tokens"
	if err := c.UpsertARecord(context.Background(), bad, "x.example.com", "203.0.113.1"); err == nil {
		t.Error("UpsertARecord accepted a traversal zone id")
	}
	if err := c.UpsertCNAME(context.Background(), bad, "x.example.com", "t.cfargotunnel.com"); err == nil {
		t.Error("UpsertCNAME accepted a traversal zone id")
	}
	if err := c.DeleteARecordByName(context.Background(), bad, "x.example.com"); err == nil {
		t.Error("DeleteARecordByName accepted a traversal zone id")
	}
	if err := c.DeleteCNAMEByName(context.Background(), bad, "x.example.com"); err == nil {
		t.Error("DeleteCNAMEByName accepted a traversal zone id")
	}
	if err := c.DeleteTunnel(context.Background(), "../../zones"); err == nil {
		t.Error("DeleteTunnel accepted a traversal tunnel id")
	}
	if len(requested) != 0 {
		t.Fatalf("requests were sent despite invalid ids: %v", requested)
	}
}

func TestDeleteTunnelRequiresUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/cfd_tunnel/2f8a1c3e-1111-4222-8333-abcdefabcdef") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"success":true,"result":{}}`))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL + "/client/v4", Token: "t", AccountID: goodZone, HTTP: srv.Client()}

	if err := c.DeleteTunnel(context.Background(), "2f8a1c3e-1111-4222-8333-abcdefabcdef"); err != nil {
		t.Fatalf("valid tunnel id rejected: %v", err)
	}
	if err := c.DeleteTunnel(context.Background(), "not-a-uuid"); err == nil {
		t.Error("non-UUID tunnel id accepted")
	}
}

// A malformed record id in the API's own response must not be pasted into the
// next request path either.
func TestRecordIDFromResponseIsValidated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"success":true,"result":[{"id":"../../../accounts"}]}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.Write([]byte(`{"success":true,"result":{}}`))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL + "/client/v4", Token: "t", AccountID: goodZone, HTTP: srv.Client()}

	if err := c.UpsertARecord(context.Background(), goodZone, "x.example.com", "203.0.113.1"); err == nil {
		t.Error("accepted a malformed record id from the API response")
	}
}

func TestCreateTunnelRequiresAccountID(t *testing.T) {
	c := Client{BaseURL: "http://127.0.0.1:1/client/v4", Token: "t", AccountID: "bad account", HTTP: http.DefaultClient}
	if _, _, err := c.CreateTunnel(context.Background(), "cfvpn-HKG-01"); err == nil {
		t.Error("accepted a malformed account id")
	}
}
