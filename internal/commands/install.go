package commands

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/kulinh/cf-vpn/internal/subscription"
)

type InstallInputs struct {
	CFAPIToken  string
	CFAccountID string
	Domain      string
	User1Name   string
}

func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func RunInstall(ctx context.Context, in InstallInputs, stdout, stderr io.Writer) error {
	if in.CFAPIToken == "" || in.CFAccountID == "" || in.Domain == "" {
		return fmt.Errorf("CF_API_TOKEN, CF_ACCOUNT_ID, and DOMAIN are required")
	}
	if in.User1Name == "" {
		in.User1Name = "user1"
	}

	// Call packages in this order during implementation:
	// 1) Ensure binaries (xray/cloudflared)
	// 2) Create tunnel + write credentials
	// 3) Upsert DNS CNAME
	// 4) Render and write xray/cloudflared config + env atomically
	// 5) Install/reload/enable systemd units
	// 6) Probe https://DOMAIN/vless expecting 400/426
	// 7) Print user1 subscription

	uuid, _ := randomB64(16)
	pass, _ := randomB64(24)
	v := subscription.BuildVLESSURI(in.User1Name, uuid, in.Domain)
	t := subscription.BuildTrojanURI(in.User1Name, pass, in.Domain)
	fmt.Fprintln(stdout, subscription.BuildSubscriptionB64(v, t))
	return nil
}
