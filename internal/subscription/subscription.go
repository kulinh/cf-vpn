package subscription

import (
	"encoding/base64"
	"fmt"
)

func BuildVLESSURI(name, uuid, domain string) string {
	return fmt.Sprintf("vless://%s@%s:443?encryption=none&security=tls&type=ws&host=%s&path=%%2Fvless&sni=%s#%s-VLESS", uuid, domain, domain, domain, name)
}

func BuildTrojanURI(name, password, domain string) string {
	return fmt.Sprintf("trojan://%s@%s:443?security=tls&type=ws&host=%s&path=%%2Ftrojan&sni=%s#%s-Trojan", password, domain, domain, domain, name)
}

func BuildSubscriptionB64(vless, trojan string) string {
	payload := vless + "\n" + trojan
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
