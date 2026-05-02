package subscription

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func BuildVLESSRealityURI(name, uuid, host, sni, pbk, sid string) string {
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=%s&pbk=%s&sid=%s&fp=chrome#%s-Reality",
		uuid, host, sni, pbk, sid, name,
	)
}

func BuildVLESSHTTPUpgradeURI(name, uuid, domain, path string) string {
	encPath := strings.ReplaceAll(path, "/", "%2F")
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=httpupgrade&host=%s&path=%s&sni=%s#%s-HTTPUpgrade",
		uuid, domain, domain, encPath, domain, name,
	)
}

func BuildSubscriptionB64(uris ...string) string {
	payload := strings.Join(uris, "\n")
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
