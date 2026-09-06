package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kulinh/cf-vpn/internal/cert"
	"github.com/kulinh/cf-vpn/internal/cloudflare"
	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/hysteria"
	"github.com/kulinh/cf-vpn/internal/netinfo"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/systemd"
	"github.com/kulinh/cf-vpn/internal/templates"
	"github.com/kulinh/cf-vpn/internal/validate"
	"github.com/kulinh/cf-vpn/internal/xray"
	"github.com/kulinh/cf-vpn/internal/zones"
)

const hysteriaConfigPath = "/etc/cfvpn/hysteria/config.yaml"

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type statusResponse struct {
	Xray           string `json:"xray"`
	Cloudflared    string `json:"cloudflared"`
	Hysteria       string `json:"hysteria"`
	VpnHost        string `json:"vpn_host"`
	Zone           string `json:"zone,omitempty"`
	PublicIP       string `json:"public_ip,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Hy2Host        string `json:"hy2_host,omitempty"`
	Hy2Port        int    `json:"hy2_port,omitempty"`
	Hy2ObfsPW      string `json:"hy2_obfs_pw,omitempty"`
	TunnelUUID     string `json:"tunnel_uuid"`
	LastRotateAt   int64  `json:"last_rotate_at"`
	RealityPubKey  string `json:"reality_pubkey,omitempty"`
	RealityShortID string `json:"reality_sid,omitempty"`
	RealitySNI     string `json:"reality_sni,omitempty"`
	RealityDest    string `json:"reality_dest,omitempty"`
	XHTTPPath      string `json:"xhttp_path,omitempty"`
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

type addUserRequest struct {
	Name string `json:"name"`
}

type addUserResponse struct {
	Name      string `json:"name"`
	VlessUUID string `json:"vless_uuid"`
	Hy2PW     string `json:"hy2_pw"`
}

func main() {
	if strings.TrimSpace(os.Getenv("AGENT_SHARED_SECRET")) == "" {
		log.Fatal("AGENT_SHARED_SECRET is required (set in /etc/cfvpn/cfvpn.env); refusing to start with no auth on a publicly-resolvable admin tunnel")
	}
	addr := strings.TrimSpace(os.Getenv("CFVPN_AGENT_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:6788"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/v1/status", handleStatus)
	mux.HandleFunc("/admin/v1/healthcheck", handleHealthcheck)
	mux.HandleFunc("/admin/v1/rotate-domain", handleRotateDomain)
	mux.HandleFunc("/admin/v1/sync", handleSync)
	mux.HandleFunc("/admin/v1/users", handleUsers)
	mux.HandleFunc("/admin/v1/users/", handleUser)
	mux.HandleFunc("/admin/v1/shutdown-tunnel", handleShutdownTunnel)

	// Wrap the mux in an auth middleware. If AGENT_SHARED_SECRET is set,
	// all admin endpoints require an Authorization: Bearer <secret> header.
	handler := withAuth(mux)

	// M-G7: explicit timeouts (the defaults are "no limit", so one stuck peer
	// pins a connection and its goroutine forever) and a graceful shutdown.
	// WriteTimeout has to outlast the slowest handler: rotate-domain issues a
	// certificate, which waits on ACME DNS propagation.
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() {
		log.Printf("cfvpn-agent listening on %s", addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case sig := <-stop:
		// `systemctl restart cfvpn-agent` in the middle of applyUsers is exactly
		// how xray and hysteria end up with different user sets: the flock is
		// released by the kernel on exit, but a half-applied config is not
		// rolled back by anyone. Let in-flight handlers finish first.
		log.Printf("cfvpn-agent received %s; draining in-flight requests", sig)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown failed (%v); closing connections", err)
			_ = server.Close()
		}
		log.Print("cfvpn-agent stopped")
	}
}

// shutdownGrace bounds the drain on SIGTERM. It must stay below the unit's
// TimeoutStopSec (systemd's default 90s) so systemd never has to SIGKILL us
// mid-write.
const shutdownGrace = 60 * time.Second

// acquireLockOrFail takes the node config lock for a request, writing the
// response itself when it cannot.
//
// M-G3: a lock held by another writer (a `cfvpnctl install` can now hold it for
// minutes, see H11) is a RETRYABLE condition, so it answers 503 lock_busy
// rather than 500. The panel can back off on that; a 500 looks like the node is
// broken and, worse, an unbounded blocking flock used to hold the connection
// open until the panel's own timeout fired while leaking an OS thread per
// waiter on the agent.
func acquireLockOrFail(w http.ResponseWriter, r *http.Request) (func(), error) {
	unlock, err := commands.AcquireConfigLock(r.Context())
	if err == nil {
		return unlock, nil
	}
	if errors.Is(err, commands.ErrLockBusy) {
		writeError(w, http.StatusServiceUnavailable, "lock_busy",
			"another cfvpn config operation is in progress on this node; retry shortly")
		return nil, err
	}
	writeError(w, http.StatusInternalServerError, "lock_failed", err.Error())
	return nil, err
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
		Xray:           serviceState("cfvpn-xray.service"),
		Cloudflared:    serviceState("cfvpn-cloudflared.service"),
		Hysteria:       serviceState("cfvpn-hysteria.service"),
		VpnHost:        env["DOMAIN"],
		Zone:           zoneForHost(env["DOMAIN"]),
		PublicIP:       env["PUBLIC_IP"],
		Mode:           env["MODE"],
		Hy2Host:        env["HY2_HOST"],
		Hy2Port:        parseInt(env["HY2_PORT"]),
		Hy2ObfsPW:      env["HY2_OBFS_PW"],
		TunnelUUID:     firstNonEmpty(env["ADMIN_TUNNEL_UUID"], env["TUNNEL_UUID"]),
		LastRotateAt:   parseInt64(env["LAST_ROTATE_AT"]),
		RealityPubKey:  env[state.KeyRealityPub],
		RealityShortID: env[state.KeyRealityShortID],
		RealitySNI:     env[state.KeyRealitySNI],
		RealityDest:    env[state.KeyRealityDest],
		XHTTPPath:      env[state.KeyXHTTPPath],
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
	// HY2 state is intentionally not gated here — if xray is healthy the node
	// should stay active even when HY2 is temporarily down (e.g. cert renewal).
	start := time.Now()
	code, err := probeHealth(r.Context(), env)
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
	// H7/H8: the hostnames in this request are written into cfvpn.env, into
	// cloudflared's YAML ingress and into a cert request. Validate them at the
	// boundary rather than trusting the caller, whatever the panel does today.
	if err := validateRotateRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_host", err.Error())
		return
	}

	// H11: rotate rewrites xray/config.json, cloudflared/config.yml,
	// hysteria/config.yaml and cfvpn.env — every file the config lock exists to
	// protect — and used to hold nothing at all, so a rotate interleaved with a
	// sync silently lost one side's user set.
	unlock, err := acquireLockOrFail(w, r)
	if err != nil {
		return
	}
	defer unlock()

	env, err := state.Load(paths.EnvFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "env_load_failed", err.Error())
		return
	}
	mode := strings.TrimSpace(env["MODE"])
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
	cfClient := cloudflare.DefaultClient(env["CF_API_TOKEN"], env["CF_ACCOUNT_ID"])
	var result commands.RotateDirectResult
	// Capture rotate diagnostics (incl. DNS rollback warnings) instead of
	// discarding them — these are the only trace when a rotate fails partway.
	var rotateLog bytes.Buffer
	switch mode {
	case "direct":
		result, err = commands.RunRotateDirect(
			r.Context(),
			commands.RotateDirectInputs{
				NewHost:       req.NewHost,
				NewZone:       zoneForHost(req.NewHost),
				NewZoneID:     req.NewZoneID,
				OldHost:       req.OldHost,
				OldZoneID:     req.OldZoneID,
				NewHy2Host:    req.NewHy2Host,
				NewHy2Zone:    req.NewHy2Zone,
				NewHy2ZoneID:  req.NewHy2ZoneID,
				OldHy2Host:    req.OldHy2Host,
				OldHy2ZoneID:  req.OldHy2ZoneID,
				CFAPIToken:    env["CF_API_TOKEN"],
				ExistingUsers: users,
			},
			commands.RotateDirectDeps{
				CF:     cfClient,
				IP:     netinfo.NewDefault(),
				Cert:   cert.NewDefault(),
				Runner: systemd.ExecRunner{},
			},
			io.Discard,
			&rotateLog,
		)
	case "cloudflare":
		result, err = commands.RunRotateCloudflare(
			r.Context(),
			commands.RotateCloudflareInputs{
				NewHost:       req.NewHost,
				NewZoneID:     req.NewZoneID,
				OldHost:       req.OldHost,
				OldZoneID:     req.OldZoneID,
				NewHy2Host:    req.NewHy2Host,
				NewHy2ZoneID:  req.NewHy2ZoneID,
				OldHy2Host:    req.OldHy2Host,
				OldHy2ZoneID:  req.OldHy2ZoneID,
				CFAPIToken:    env["CF_API_TOKEN"],
				ExistingUsers: users,
			},
			commands.RotateCloudflareDeps{
				CF:     cfClient,
				Cert:   cert.NewDefault(),
				Runner: systemd.ExecRunner{},
			},
			io.Discard,
			&rotateLog,
		)
	default:
		writeError(w, http.StatusBadRequest, "rotate_unsupported", fmt.Sprintf("rotate-domain is not supported for mode=%q", mode))
		return
	}
	if diag := strings.TrimSpace(rotateLog.String()); diag != "" {
		log.Printf("rotate-domain diagnostics (mode=%s): %s", mode, diag)
	}
	if err != nil {
		detail := err.Error()
		if diag := strings.TrimSpace(rotateLog.String()); diag != "" {
			detail = fmt.Sprintf("%s; diagnostics: %s", detail, diag)
		}
		writeError(w, http.StatusInternalServerError, "rotate_failed", detail)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vpn_host":    result.VpnHost,
		"public_ip":   result.PublicIP,
		"hy2_host":    result.Hy2Host,
		"hy2_port":    result.Hy2Port,
		"hy2_obfs_pw": result.Hy2ObfsPW,
		"mode":        mode,
	})
}

// validateRotateRequest trims and checks every hostname the rotate request
// carries. new_host is required; the others are optional and only checked when
// present. Zone ids, when supplied, must look like Cloudflare zone ids because
// they are interpolated into API paths.
func validateRotateRequest(req *rotateRequest) error {
	req.NewHost = strings.TrimSpace(req.NewHost)
	req.OldHost = strings.TrimSpace(req.OldHost)
	req.NewHy2Host = strings.TrimSpace(req.NewHy2Host)
	req.OldHy2Host = strings.TrimSpace(req.OldHy2Host)

	if err := validate.Hostname(req.NewHost); err != nil {
		return fmt.Errorf("new_host: %w", err)
	}
	for name, host := range map[string]string{
		"old_host":     req.OldHost,
		"new_hy2_host": req.NewHy2Host,
		"old_hy2_host": req.OldHy2Host,
	} {
		if host == "" {
			continue
		}
		if err := validate.Hostname(host); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	for name, id := range map[string]string{
		"new_zone_id":     strings.TrimSpace(req.NewZoneID),
		"old_zone_id":     strings.TrimSpace(req.OldZoneID),
		"new_hy2_zone_id": strings.TrimSpace(req.NewHy2ZoneID),
		"old_hy2_zone_id": strings.TrimSpace(req.OldHy2ZoneID),
	} {
		if id == "" {
			continue
		}
		if err := validate.HexID32(id); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
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
	unlock, err := acquireLockOrFail(w, r)
	if err != nil {
		return
	}
	defer unlock()
	env, result, err := applyUsers(r.Context(), req.Users)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sync_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"vpn_host":       result.VpnHost,
		"public_ip":      result.PublicIP,
		"hy2_host":       env["HY2_HOST"],
		"hy2_port":       result.Hy2Port,
		"users":          len(req.Users),
		"mode":           env[state.KeyMode],
		"reality_pubkey": env[state.KeyRealityPub],
		"reality_sid":    env[state.KeyRealityShortID],
		"reality_sni":    env[state.KeyRealitySNI],
		"reality_dest":   env[state.KeyRealityDest],
		"xhttp_path":     env[state.KeyXHTTPPath],
	})
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	var req addUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := xray.ValidateUserName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_user", err.Error())
		return
	}
	unlock, err := acquireLockOrFail(w, r)
	if err != nil {
		return
	}
	defer unlock()
	records, err := currentSyncUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_users_failed", err.Error())
		return
	}
	for _, u := range records {
		if u.Name == name {
			if _, _, err := applyUsers(r.Context(), records); err != nil {
				writeError(w, http.StatusInternalServerError, "apply_users_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, addUserResponse{Name: u.Name, VlessUUID: u.VlessUUID, Hy2PW: u.Hy2PW})
			return
		}
	}
	uuid, err := commands.GenerateUUIDv4()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate_uuid_failed", err.Error())
		return
	}
	hy2PW, err := commands.GeneratePassword(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate_password_failed", err.Error())
		return
	}
	records = append(records, syncUser{Name: name, VlessUUID: uuid, Hy2PW: hy2PW})
	if _, _, err := applyUsers(r.Context(), records); err != nil {
		writeError(w, http.StatusInternalServerError, "apply_users_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, addUserResponse{Name: name, VlessUUID: uuid, Hy2PW: hy2PW})
}

func handleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin/v1/users/")
	name = strings.TrimSpace(name)
	if err := xray.ValidateUserName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_user", err.Error())
		return
	}
	unlock, err := acquireLockOrFail(w, r)
	if err != nil {
		return
	}
	defer unlock()
	records, err := currentSyncUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_users_failed", err.Error())
		return
	}
	filtered := records[:0]
	found := false
	for _, u := range records {
		if u.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, u)
	}
	if !found {
		writeError(w, http.StatusNotFound, "user_not_found", name)
		return
	}
	if _, _, err := applyUsers(r.Context(), filtered); err != nil {
		writeError(w, http.StatusInternalServerError, "apply_users_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func currentSyncUsers() ([]syncUser, error) {
	cfg, err := xray.Load(paths.XrayConfigFile)
	if err != nil {
		return nil, fmt.Errorf("load xray config: %w", err)
	}
	hy2Users, err := hysteria.ListUsers(hysteriaConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load hysteria config: %w", err)
	}
	hy2ByName := make(map[string]string, len(hy2Users))
	for _, u := range hy2Users {
		hy2ByName[u.Name] = u.Password
	}
	names := xray.ListUserNames(cfg)
	byName := make(map[string]syncUser, len(names))
	for _, name := range names {
		uuid, ok := xray.GetVLESSClient(cfg, name)
		if !ok {
			return nil, fmt.Errorf("%s missing vless uuid", name)
		}
		if _, exists := byName[name]; exists {
			continue
		}
		hy2PW := hy2ByName[name]
		if hy2PW == "" {
			// User exists in xray but is missing from the hysteria config — the
			// two configs have drifted (e.g. a prior applyUsers wrote xray then
			// failed before SetUsers). We can't recover the real password
			// locally, so we mint a new one to keep the node functional; this
			// DIVERGES from the password the panel/D1 holds for this user, so
			// their Hy2 client will break until a panel re-sync
			// (POST /admin/v1/sync) pushes the authoritative password. Log it
			// loudly so the drift isn't silent.
			var err error
			hy2PW, err = commands.GeneratePassword(24)
			if err != nil {
				return nil, fmt.Errorf("generate hy2 password for %s: %w", name, err)
			}
			log.Printf("warning: user %q present in xray but missing from hysteria config; minted a new Hy2 password — it will not match the panel until a re-sync (POST /admin/v1/sync)", name)
		}
		byName[name] = syncUser{Name: name, VlessUUID: uuid, Hy2PW: hy2PW}
	}
	records := make([]syncUser, 0, len(byName))
	for _, record := range byName {
		records = append(records, record)
	}
	return records, nil
}

func toTemplateUsers(users []commands.ExistingUser) []templates.XrayUser {
	out := make([]templates.XrayUser, 0, len(users))
	for _, u := range users {
		out = append(out, templates.XrayUser{Name: u.Name, UUID: u.UUID})
	}
	return out
}

func applyUsers(ctx context.Context, reqUsers []syncUser) (map[string]string, commands.RotateDirectResult, error) {
	users := make([]commands.ExistingUser, 0, len(reqUsers))
	for _, u := range reqUsers {
		if err := xray.ValidateUserName(u.Name); err != nil {
			return nil, commands.RotateDirectResult{}, err
		}
		if strings.TrimSpace(u.VlessUUID) == "" || strings.TrimSpace(u.Hy2PW) == "" {
			return nil, commands.RotateDirectResult{}, fmt.Errorf("user %s missing credentials", u.Name)
		}
		users = append(users, commands.ExistingUser{Name: u.Name, UUID: u.VlessUUID})
	}
	env, err := state.Load(paths.EnvFile)
	if err != nil {
		return nil, commands.RotateDirectResult{}, fmt.Errorf("load env: %w", err)
	}
	result := commands.RotateDirectResult{VpnHost: env["DOMAIN"], PublicIP: env["PUBLIC_IP"], Hy2Host: env["HY2_HOST"], Hy2Port: parseInt(env["HY2_PORT"]), Hy2ObfsPW: env["HY2_OBFS_PW"]}
	rendered, err := renderXrayForMode(env, users)
	if err != nil {
		return nil, commands.RotateDirectResult{}, err
	}
	// H12: parse-check the candidate config, then keep the previous one so a
	// failed restart can be undone. applyUsers had no rollback at all: a config
	// xray refuses was published over the live one and the node was left with a
	// broken file on disk and xray down.
	oldXray, oldXrayErr := os.ReadFile(paths.XrayConfigFile)
	if err := commands.WriteXrayConfigChecked(ctx, paths.XrayConfigFile, []byte(rendered), 0o600); err != nil {
		return nil, commands.RotateDirectResult{}, fmt.Errorf("write xray config: %w", err)
	}
	if err := systemd.Restart(ctx, systemd.ExecRunner{}, "cfvpn-xray.service"); err != nil {
		if oldXrayErr == nil {
			if rerr := commands.WriteAtomicFile(paths.XrayConfigFile, oldXray, 0o600); rerr != nil {
				log.Printf("restore previous xray config failed: %v", rerr)
			} else if rerr := systemd.Restart(ctx, systemd.ExecRunner{}, "cfvpn-xray.service"); rerr != nil {
				log.Printf("restart xray on the restored config failed: %v", rerr)
			}
		}
		return nil, commands.RotateDirectResult{}, fmt.Errorf("restart xray: %w", err)
	}
	hy2Users := make([]hysteria.User, len(reqUsers))
	for i, u := range reqUsers {
		hy2Users[i] = hysteria.User{Name: u.Name, Password: u.Hy2PW}
	}
	if err := hysteria.SetUsers(hysteriaConfigPath, hy2Users); err != nil {
		return nil, commands.RotateDirectResult{}, fmt.Errorf("set hysteria users: %w", err)
	}
	if err := hysteria.ReloadService(ctx, systemd.ExecRunner{}); err != nil {
		return nil, commands.RotateDirectResult{}, fmt.Errorf("reload hysteria: %w", err)
	}
	if err := commands.RegenerateSubscriptions(env["DOMAIN"]); err != nil {
		return nil, commands.RotateDirectResult{}, fmt.Errorf("regenerate subscriptions: %w", err)
	}
	return env, result, nil
}

// renderXrayForMode produces the xray config for a user-mutation event.
// User mutations (add/remove/sync) re-render against the EXISTING host — no
// DNS, no cert reissue, no host rotation. Branches on Mode + Reality params.
func renderXrayForMode(env map[string]string, users []commands.ExistingUser) (string, error) {
	tplUsers := toTemplateUsers(users)
	mode := strings.TrimSpace(env["MODE"])
	if mode == "cloudflare" {
		out, err := templates.RenderXrayCloudflareHTTPUpgrade(tplUsers, env["DOMAIN"], commands.XrayDNSServersFromEnv(env))
		if err != nil {
			return "", fmt.Errorf("render cloudflare xray: %w", err)
		}
		return out, nil
	}
	if commands.IsRealityMode(env) {
		out, err := templates.RenderXrayDirectReality(templates.XrayDirectRealityInputs{
			Users:       tplUsers,
			PrivateKey:  env[state.KeyRealityPriv],
			ShortIDs:    []string{env[state.KeyRealityShortID]},
			Dest:        env[state.KeyRealityDest],
			ServerNames: []string{env[state.KeyRealitySNI]},
			DNSServers:  commands.XrayDNSServersFromEnv(env),
		})
		if err != nil {
			return "", fmt.Errorf("render reality xray: %w", err)
		}
		return out, nil
	}
	return "", fmt.Errorf("legacy WS+TLS direct mode is no longer supported; run `cfvpnctl upgrade --mode direct` to migrate this node to Reality")
}

func probeHealth(ctx context.Context, env map[string]string) (int, error) {
	mode := strings.TrimSpace(env["MODE"])
	domain := strings.TrimSpace(env["DOMAIN"])
	if mode == "cloudflare" {
		host := domain
		if host == "" {
			host = "127.0.0.1"
		}
		return probeHTTPUpgrade(ctx, "127.0.0.1:10001", templates.VLESSPath, host)
	}
	if domain == "" {
		return 0, fmt.Errorf("DOMAIN is empty")
	}
	// Reality nodes don't expose a real TLS endpoint on :443 — use TCP connect.
	// Return synthetic 426 so the existing IsHealthyCode check in the caller
	// treats a successful TCP open as healthy.
	if commands.IsRealityMode(env) {
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(domain, "443"))
		if err != nil {
			return 0, err
		}
		conn.Close()
		return 426, nil
	}
	return probeURL(ctx, "https://"+domain+templates.VLESSPath)
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

// probeHTTPUpgrade sends a minimal WebSocket-style upgrade request to addr+path
// and returns the parsed HTTP status line code. xray's HTTPUpgrade transport
// (>= 26.x) closes plain GETs without writing a status line, so a real upgrade
// handshake is the only reliable health signal. Caller treats 101 as healthy.
func probeHTTPUpgrade(ctx context.Context, addr, path, host string) (int, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return 0, err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return 0, err
	}
	parts := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid status line: %q", line)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid status code %q: %w", parts[1], err)
	}
	return code, nil
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

func handleShutdownTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	const unit = "cfvpn-cloudflared.service"

	stopCmd := exec.CommandContext(ctx, "systemctl", "stop", unit)
	stopOut, stopErr := stopCmd.CombinedOutput()
	if stopErr != nil {
		writeError(w, http.StatusInternalServerError, "stop_failed",
			fmt.Sprintf("%s: %s", stopErr.Error(), strings.TrimSpace(string(stopOut))))
		return
	}

	disableCmd := exec.CommandContext(ctx, "systemctl", "disable", unit)
	_, _ = disableCmd.CombinedOutput()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": unit,
		"state":   serviceState(unit),
	})
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

// withAuth wraps the admin mux in Bearer-token auth. The agent refuses to
// start if AGENT_SHARED_SECRET is empty (see main()): the admin tunnel is
// publicly resolvable, so default-allow would leave /admin/v1/* exposed.
func withAuth(next http.Handler) http.Handler {
	secret := strings.TrimSpace(os.Getenv("AGENT_SHARED_SECRET"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(
			[]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(secret)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing agent shared secret")
			return
		}
		next.ServeHTTP(w, r)
	})
}
