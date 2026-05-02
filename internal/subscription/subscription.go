package subscription

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Deprecated: WS path. Will be removed once all nodes are migrated to Reality or XHTTP.
func BuildVLESSURI(name, uuid, domain string) string {
	return fmt.Sprintf("vless://%s@%s:443?encryption=none&security=tls&type=ws&host=%s&path=%%2Fvless&sni=%s#%s-VLESS", uuid, domain, domain, domain, name)
}

func BuildVLESSRealityURI(name, uuid, host, sni, pbk, sid string) string {
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=reality&flow=xtls-rprx-vision&type=tcp&sni=%s&pbk=%s&sid=%s&fp=chrome#%s-Reality",
		uuid, host, sni, pbk, sid, name,
	)
}

func BuildVLESSXHTTPURI(name, uuid, domain, path string) string {
	encPath := strings.ReplaceAll(path, "/", "%2F")
	return fmt.Sprintf(
		"vless://%s@%s:443?encryption=none&security=tls&type=xhttp&host=%s&path=%s&mode=stream-up&sni=%s#%s-XHTTP",
		uuid, domain, domain, encPath, domain, name,
	)
}

func BuildSubscriptionB64(uris ...string) string {
	payload := strings.Join(uris, "\n")
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
