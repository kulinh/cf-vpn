// internal/systemd/units.go
package systemd

import "fmt"

func XrayService(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=cfvpn xray
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/xray run -c %s
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=3
User=root
AmbientCapabilities=CAP_NET_BIND_SERVICE

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

func AgentService() string {
	return `[Unit]
Description=cfvpn agent
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cfvpn-agent
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`
}

func HysteriaService(configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=cf-vpn Hysteria 2 server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/hysteria server -c %s
Restart=on-failure
RestartSec=2s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, configPath)
}

func CertRenewService() string {
	return `[Unit]
Description=cf-vpn cert renew
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/cfvpn cert-renew
`
}

func CertRenewTimer() string {
	return `[Unit]
Description=cf-vpn daily cert renew

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
`
}
