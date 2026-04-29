package subscription

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func BuildVLESSURI(name, uuid, domain string) string {
	return fmt.Sprintf("vless://%s@%s:443?encryption=none&security=tls&type=ws&host=%s&path=%%2Fvless&sni=%s#%s-VLESS", uuid, domain, domain, domain, name)
}

func BuildSubscriptionB64(uris ...string) string {
	payload := strings.Join(uris, "\n")
	return base64.StdEncoding.EncodeToString([]byte(payload))
}
