package commands

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInstallRequiresDomain(t *testing.T) {
	var out, errBuf bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: ""}
	if err := RunInstall(context.Background(), cfg, &out, &errBuf); err == nil {
		t.Fatalf("expected error when domain empty")
	}
}

func TestInstallRequiresToken(t *testing.T) {
	var out, errBuf bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "", CFAccountID: "a", Domain: "vpn.example.com"}
	if err := RunInstall(context.Background(), cfg, &out, &errBuf); err == nil {
		t.Fatalf("expected error when token empty")
	}
}

func TestInstallPrintsSubscription(t *testing.T) {
	var out, errBuf bytes.Buffer
	cfg := InstallInputs{CFAPIToken: "t", CFAccountID: "a", Domain: "vpn.example.com"}
	if err := RunInstall(context.Background(), cfg, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatalf("expected subscription output on stdout")
	}
}
