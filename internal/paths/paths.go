package paths

const (
	EnvFile            = "/etc/cfvpn/cfvpn.env"
	XrayConfigFile     = "/etc/cfvpn/xray/config.json"
	CloudflaredConfig  = "/etc/cfvpn/cloudflared/config.yml"
	CloudflaredCredDir = "/etc/cfvpn/cloudflared"
	SubscriptionDir    = "/var/lib/cfvpn/subscriptions"
	StateDir           = "/var/lib/cfvpn/state"
	HealthStateFile    = "/var/lib/cfvpn/state/healthcheck.state"
)
