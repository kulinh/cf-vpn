package cert

import "context"

// Manager issues and renews per-host certificates.
type Manager interface {
	Issue(ctx context.Context, host, certPath, keyPath, token string) error
	Renew(ctx context.Context, host, certPath, keyPath, token string, days int) error
}
