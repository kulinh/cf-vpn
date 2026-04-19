// internal/templates/render.go
package templates

import (
	"bytes"
	"text/template"
)

const cloudflaredTemplate = `tunnel: {{.TunnelUUID}}
credentials-file: /etc/cfvpn/cloudflared/{{.TunnelUUID}}.json
ingress:
  - hostname: {{.Domain}}
    path: ^/vless$
    service: http://127.0.0.1:10001
  - hostname: {{.Domain}}
    path: ^/trojan$
    service: http://127.0.0.1:10002
  - service: http_status:404
`

const xrayTemplate = `{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "tag": "vless-ws",
      "listen": "127.0.0.1",
      "port": 10001,
      "protocol": "vless",
      "settings": {"clients": [{"id": "{{.UUID}}", "email": "{{.User}}@vpn"}], "decryption": "none"},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/vless"}}
    },
    {
      "tag": "trojan-ws",
      "listen": "127.0.0.1",
      "port": 10002,
      "protocol": "trojan",
      "settings": {"clients": [{"password": "{{.Password}}", "email": "{{.User}}@vpn"}]},
      "streamSettings": {"network": "ws", "wsSettings": {"path": "/trojan"}}
    }
  ],
  "outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "block", "protocol": "blackhole"}],
  "routing": {"rules": [{"type": "field", "ip": ["geoip:private"], "outboundTag": "block"}]}
}
`

func RenderCloudflared(tunnelUUID, domain string) (string, error) {
	t, err := template.New("cloudflared").Parse(cloudflaredTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"TunnelUUID": tunnelUUID, "Domain": domain})
	return b.String(), err
}

func RenderXray(user, uuid, password string) (string, error) {
	t, err := template.New("xray").Parse(xrayTemplate)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, map[string]string{"User": user, "UUID": uuid, "Password": password})
	return b.String(), err
}
