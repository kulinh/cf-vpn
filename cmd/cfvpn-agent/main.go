package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/netinfo"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/xray"
	"github.com/kulinh/cf-vpn/internal/zones"
)

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type statusResponse struct {
	Xray         string `json:"xray"`
	Cloudflared  string `json:"cloudflared"`
	Hysteria     string `json:"hysteria"`
	VpnHost      string `json:"vpn_host"`
	Zone         string `json:"zone,omitempty"`
	PublicIP     string `json:"public_ip,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Hy2Host      string `json:"hy2_host,omitempty"`
	Hy2Port      int    `json:"hy2_port,omitempty"`
	Hy2ObfsPW    string `json:"hy2_obfs_pw,omitempty"`
	TunnelUUID   string `json:"tunnel_uuid"`
	LastRotateAt int64  `json:"last_rotate_at"`
}

type healthcheckResponse struct {
	OK        bool  `json:"ok"`
	Code      int   `json:"code"`
	LatencyMS int64 `json:"latency_ms"`
}

type rotateRequest struct {
	NewHost      string `json:"new_host"`
	NewZoneID    string `json:"new_zone_id"`
	OldHost      string `json:"old_host"`
	OldZoneID    string `json:"old_zone_id"`
	NewHy2Host   string `json:"new_hy2_host"`
	NewHy2Zone   string `json:"new_hy2_zone"`
	NewHy2ZoneID string `json:"new_hy2_zone_id"`
	OldHy2Host   string `json:"old_hy2_host"`
	OldHy2ZoneID string `json:"old_hy2_zone_id"`
}

type syncUser struct {
	Name      string `json:"name"`
	VlessUUID string `json:"vless_uuid"`
	Hy2PW     string `json:"hy2_pw"`
}

type syncRequest struct {
	Users []syncUser `json:"users"`
}

func main() {
	addr := strings.TrimSpace(os.Getenv("CFVPN_AGENT_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8787"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/v1/status", handleStatus)
	mux.HandleFunc("/admin/v1/healthcheck", handleHealthcheck)
	mux.HandleFunc("/admin/v1/rotate-domain", handleRotateDomain)
	mux.HandleFunc("/admin/v1/sync", handleSync)

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("cfvpn-agent listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	env, err := state.Load(paths.EnvFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_load_failed", err.Error())
		return
	}
	resp := statusResponse{
		Xray:         serviceState("cfvpn-xray.service"),
		Cloudflared:  serviceState("cfvpn-cloudflared.service"),
		Hysteria:     serviceState("cfvpn-hysteria.service"),
		VpnHost:      env["DOMAIN"],
		Zone:         zoneForHost(env["DOMAIN"]),
		PublicIP:     env["PUBLIC_IP"],
		Mode:         env["MODE"],
		Hy2Host:      env["HY2_HOST"],
		Hy2Port:      parseInt(env["HY2_PORT"]),
		Hy2ObfsPW:   env["HY2_OBFS_PW"],
		TunnelUUID:   firstNonEmpty(env["ADMIN_TUNNEL_UUID"], env["TUNNEL_UUID"]),
		LastRotateAt: parseInt64(env["LAST_ROTATE_AT"]),
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleHealthcheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	env, err := state.Load(paths.EnvFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_load_failed", err.Error())
		return
	}
	if serviceState("cfvpn-hysteria.service") != "active" {
		writeJSON(w, http.StatusOK, healthcheckResponse{OK: false, Code: 0, LatencyMS: 0})
		return
	}
	start := time.Now()
	code, err := probeHealth(r.Context(), env["DOMAIN"], env["MODE"])
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeError(w, http.StatusBadGateway, "healthcheck_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, healthcheckResponse{OK: commands.IsHealthyCode(code), Code: code, LatencyMS: latency})
}

func handleRotateDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	var req rotateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	env, err := state.Load(paths.EnvFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_load_failed", err.Error())
		return
	}
	cfg, err := xray.Load(paths.XrayConfigFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "xray_load_failed", err.Error())
		return
	}
	users := make([]commands.ExistingUser, 0, len(xray.ListUserNames(cfg)))
	for _, name := range xray.ListUserNames(cfg) {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			writeError(w, http.StatusInternalServerError, "xray_user_invalid", fmt.Sprintf("%s missing vless uuid", name))
			return
		}
		users = append(users, commands.ExistingUser{Name: name, UUID: uuid})
	}
	result, err := commands.RunRotateDirect(
		r.Context(),
		commands.RotateDirectInputs{
			NewHost:      req.NewHost,
			NewZone:      zoneForHost(req.NewHost),
			NewZoneID:    req.NewZoneID,
			OldHost:      req.OldHost,
			OldZoneID:    req.OldZoneID,
			NewHy2Host:   req.NewHy2Host,
			NewHy2Zone:   req.NewHy2Zone,
			NewHy2ZoneID: req.NewHy2ZoneID,
			OldHy2Host:   req.OldHy2Host,
			OldHy2ZoneID: req.OldHy2ZoneID,
			CFAPIToken:   env["CF_API_TOKEN"],
			ExistingUsers: users,
		},
		commands.RotateDirectDeps{
			CF:     &cloudflare.Client{BaseURL: "https://api.cloudflare.com/client/v4", Token: env["CF_API_TOKEN"], AccountID: env["CF_ACCOUNT_ID"], HTTP: http.DefaultClient},
			IP:     netinfo.NewDefault(),
			Cert:   cert.NewDefault(),
			Runner: systemd.ExecRunner{},
		},
		io.Discard,
		io.Discard,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rotate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vpn_host": result.VpnHost,
		"public_ip": result.PublicIP,
		"hy2_host": result.Hy2Host,
		"hy2_port": result.Hy2Port,
		"hy2_obfs_pw": result.Hy2ObfsPW,
	})
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	users := make([]commands.ExistingUser, 0, len(req.Users))
	for _, u := range req.Users {
		users = append(users, commands.ExistingUser{Name: u.Name, UUID: u.VlessUUID})
	}
	env, err := state.Load(paths.EnvFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_load_failed", err.Error())
		return
	}
	result, err := commands.RunRotateDirect(
		r.Context(),
		commands.RotateDirectInputs{
			NewHost:       env["DOMAIN"],
			NewZone:       zoneForHost(env["DOMAIN"]),
			NewZoneID:     zoneIDForHost(env["DOMAIN"]),
			CFAPIToken:    env["CF_API_TOKEN"],
			ExistingUsers: users,
		},
		commands.RotateDirectDeps{
			CF:     &cloudflare.Client{BaseURL: "https://api.cloudflare.com/client/v4", Token: env["CF_API_TOKEN"], AccountID: env["CF_ACCOUNT_ID"], HTTP: http.DefaultClient},
			IP:     netinfo.NewDefault(),
			Cert:   cert.NewDefault(),
			Runner: systemd.ExecRunner{},
		},
		io.Discard,
		io.Discard,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sync_failed", err.Error())
		return
	}
	hy2Users := make([]hysteria.User, len(req.Users))
	for i, u := range req.Users {
		hy2Users[i] = hysteria.User{Name: u.Name, Password: u.Hy2PW}
	}
	if err := hysteria.SetUsers("/etc/cfvpn/hysteria/config.yaml", hy2Users); err != nil {
		writeError(w, http.StatusInternalServerError, "hysteria_set_users_failed", err.Error())
		return
	}
	if err := hysteria.ReloadService(r.Context(), systemd.ExecRunner{}); err != nil {
		writeError(w, http.StatusInternalServerError, "hysteria_reload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vpn_host": result.VpnHost, "public_ip": result.PublicIP, "hy2_host": env["HY2_HOST"], "users": len(users)})
}

func probeHealth(ctx context.Context, domain string, mode string) (int, error) {
	if strings.TrimSpace(mode) == "cloudflare" {
		return probeURL(ctx, "http://127.0.0.1:10001/vless")
	}
	if strings.TrimSpace(domain) == "" {
		return 0, fmt.Errorf("DOMAIN is empty")
	}
	return probeURL(ctx, "https://"+domain+"/vless")
}

func probeURL(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func serviceState(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", name)
	out, err := cmd.Output()
	if err != nil {
		return "inactive"
	}
	return strings.TrimSpace(string(out))
}

func zoneForHost(host string) string {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	for _, z := range zones.DefaultPool {
		if host == z.Name || strings.HasSuffix(host, "."+z.Name) {
			return z.Name
		}
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return ""
}

func zoneIDForHost(host string) string {
	zone := zoneForHost(host)
	for _, z := range zones.DefaultPool {
		if z.Name == zone {
			return z.CFZoneID
		}
	}
	return ""
}

func parseInt(s string) int {
	var out int
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%d", &out)
	return out
}

func parseInt64(s string) int64 {
	var out int64
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%d", &out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]string{"error": code, "detail": detail})
}

