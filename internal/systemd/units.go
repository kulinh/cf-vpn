// internal/systemd/units.go
package systemd

import "fmt"

func XrayService(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=cfvpn xray
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/xray -config %s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`, configPath)
}

func CloudflaredService(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=cfvpn cloudflared
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cloudflared tunnel --config %s run
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`, configPath)
}

func HealthcheckService() string {
	return `[Unit]
Description=cfvpn periodic healthcheck

[Service]
Type=oneshot
ExecStart=/usr/local/bin/cfvpnctl healthcheck run
`
}

func HealthcheckTimer() string {
	return `[Unit]
Description=cfvpn healthcheck timer

[Timer]
OnBootSec=2m
OnUnitActiveSec=5m
Unit=cfvpn-healthcheck.service

[Install]
WantedBy=timers.target
`
}
