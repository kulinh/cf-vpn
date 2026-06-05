package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// emailSuffix is the canonical xray email suffix written by AddUser and the
// templates. ListUserNames/GetVLESSClient/RemoveUser strip it on read so the
// rest of the codebase can refer to users by their bare name.
const emailSuffix = "@vpn"

// normalizeEmail returns the canonical bare user name. It strips any number
// of trailing "@vpn" tokens so legacy clients written with mismatched
// suffixes still resolve to the right user.
func normalizeEmail(email string) string {
	email = strings.TrimSpace(email)
	for strings.HasSuffix(email, emailSuffix) {
		email = strings.TrimSuffix(email, emailSuffix)
	}
	return email
}

// Config represents an xray config, preserving any top-level fields the
// control plane does not know about (log, outbounds, routing, ...).
type Config struct {
	Inbounds []Inbound
	Extras   map[string]json.RawMessage
}

// Inbound represents a single xray inbound, preserving any fields the
// control plane does not know about (tag, listen, port, streamSettings, ...).
type Inbound struct {
	Protocol string
	Settings json.RawMessage
	Extras   map[string]json.RawMessage
}

// vlessClient preserves any fields the control plane does not know about
// (notably `flow` for XTLS-Reality clients) via the Extras catch-all so
// JSON round-trips through Load/SaveAtomic don't strip them.
type vlessClient struct {
	ID     string
	Email  string
	Flow   string
	Extras map[string]json.RawMessage
}

type vlessSettings struct {
	Clients    []vlessClient
	Decryption string
	Extras     map[string]json.RawMessage
}

var (
	vlessClientOrder   = []string{"id", "email", "flow"}
	vlessSettingsOrder = []string{"clients", "decryption"}
)

func (c vlessClient) MarshalJSON() ([]byte, error) {
	merged := make(map[string]json.RawMessage, len(c.Extras)+3)
	for k, v := range c.Extras {
		merged[k] = v
	}
	idRaw, err := json.Marshal(c.ID)
	if err != nil {
		return nil, err
	}
	merged["id"] = idRaw
	emailRaw, err := json.Marshal(c.Email)
	if err != nil {
		return nil, err
	}
	merged["email"] = emailRaw
	if c.Flow != "" {
		flowRaw, err := json.Marshal(c.Flow)
		if err != nil {
			return nil, err
		}
		merged["flow"] = flowRaw
	}
	return marshalStableObject(merged, vlessClientOrder)
}

func (c *vlessClient) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["id"]; ok {
		if err := json.Unmarshal(v, &c.ID); err != nil {
			return err
		}
		delete(raw, "id")
	}
	if v, ok := raw["email"]; ok {
		if err := json.Unmarshal(v, &c.Email); err != nil {
			return err
		}
		delete(raw, "email")
	}
	if v, ok := raw["flow"]; ok {
		if err := json.Unmarshal(v, &c.Flow); err != nil {
			return err
		}
		delete(raw, "flow")
	}
	c.Extras = raw
	return nil
}

func (s vlessSettings) MarshalJSON() ([]byte, error) {
	merged := make(map[string]json.RawMessage, len(s.Extras)+2)
	for k, v := range s.Extras {
		merged[k] = v
	}
	clientsRaw, err := json.Marshal(s.Clients)
	if err != nil {
		return nil, err
	}
	merged["clients"] = clientsRaw
	decRaw, err := json.Marshal(s.Decryption)
	if err != nil {
		return nil, err
	}
	merged["decryption"] = decRaw
	return marshalStableObject(merged, vlessSettingsOrder)
}

func (s *vlessSettings) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["clients"]; ok {
		if err := json.Unmarshal(v, &s.Clients); err != nil {
			return err
		}
		delete(raw, "clients")
	}
	if v, ok := raw["decryption"]; ok {
		if err := json.Unmarshal(v, &s.Decryption); err != nil {
			return err
		}
		delete(raw, "decryption")
	}
	s.Extras = raw
	return nil
}

var userNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func ValidateUserName(name string) error {
	if !userNameRE.MatchString(name) {
		return fmt.Errorf("invalid user name %q (allowed: %s)", name, userNameRE.String())
	}
	return nil
}

// NewBaseConfig returns a minimal Config suitable for tests. Production
// installs render the full runtime config (ports, streamSettings, outbounds,
// routing) via templates.RenderXrayDirectReality or
// RenderXrayCloudflareHTTPUpgrade.
func NewBaseConfig(user, uuid, password string) Config {
	_ = password
	vless, _ := json.Marshal(vlessSettings{
		Clients:    []vlessClient{{ID: uuid, Email: user}},
		Decryption: "none",
	})
	return Config{Inbounds: []Inbound{
		{Protocol: "vless", Settings: vless},
	}}
}

// MarshalJSON emits the Config by merging the known `inbounds` field with
// any preserved Extras, producing a canonical object.
func (c Config) MarshalJSON() ([]byte, error) {
	merged := make(map[string]json.RawMessage, len(c.Extras)+1)
	for k, v := range c.Extras {
		merged[k] = v
	}
	rawInbounds, err := json.Marshal(c.Inbounds)
	if err != nil {
		return nil, err
	}
	merged["inbounds"] = rawInbounds
	return marshalStableObject(merged, topLevelOrder)
}

// UnmarshalJSON splits the object into typed inbounds and an Extras bag.
func (c *Config) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if inboundsRaw, ok := raw["inbounds"]; ok {
		if err := json.Unmarshal(inboundsRaw, &c.Inbounds); err != nil {
			return err
		}
		delete(raw, "inbounds")
	} else {
		c.Inbounds = nil
	}
	c.Extras = raw
	return nil
}

// MarshalJSON emits the Inbound by merging `protocol` + `settings` with
// any preserved Extras.
func (in Inbound) MarshalJSON() ([]byte, error) {
	merged := make(map[string]json.RawMessage, len(in.Extras)+2)
	for k, v := range in.Extras {
		merged[k] = v
	}
	protoRaw, err := json.Marshal(in.Protocol)
	if err != nil {
		return nil, err
	}
	merged["protocol"] = protoRaw
	if len(in.Settings) > 0 {
		merged["settings"] = in.Settings
	}
	return marshalStableObject(merged, inboundOrder)
}

// UnmarshalJSON splits the inbound into typed protocol/settings plus Extras.
func (in *Inbound) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if protoRaw, ok := raw["protocol"]; ok {
		if err := json.Unmarshal(protoRaw, &in.Protocol); err != nil {
			return err
		}
		delete(raw, "protocol")
	}
	if settingsRaw, ok := raw["settings"]; ok {
		in.Settings = settingsRaw
		delete(raw, "settings")
	}
	in.Extras = raw
	return nil
}

// topLevelOrder and inboundOrder preserve a stable field order for
// human-readable output; unknown keys are appended in sorted order.
var (
	topLevelOrder = []string{"log", "inbounds", "outbounds", "routing"}
	inboundOrder  = []string{"tag", "listen", "port", "protocol", "settings", "streamSettings"}
)

func marshalStableObject(m map[string]json.RawMessage, preferred []string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	emit := func(k string, v json.RawMessage) error {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyRaw, err := json.Marshal(k)
		if err != nil {
			return err
		}
		buf.Write(keyRaw)
		buf.WriteByte(':')
		buf.Write(v)
		return nil
	}
	seen := make(map[string]bool, len(m))
	for _, k := range preferred {
		if v, ok := m[k]; ok {
			if err := emit(k, v); err != nil {
				return nil, err
			}
			seen[k] = true
		}
	}
	leftovers := make([]string, 0, len(m))
	for k := range m {
		if !seen[k] {
			leftovers = append(leftovers, k)
		}
	}
	sortStrings(leftovers)
	for _, k := range leftovers {
		if err := emit(k, m[k]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func sortStrings(s []string) {
	sort.Strings(s)
}

func findInbound(cfg *Config, protocol string) *Inbound {
	for i := range cfg.Inbounds {
		if cfg.Inbounds[i].Protocol == protocol {
			return &cfg.Inbounds[i]
		}
	}
	return nil
}

func loadVLESS(in *Inbound) (*vlessSettings, error) {
	var s vlessSettings
	if err := json.Unmarshal(in.Settings, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveVLESS(in *Inbound, s *vlessSettings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	in.Settings = raw
	return nil
}

func ListUserNames(cfg Config) []string {
	in := findInbound(&cfg, "vless")
	if in == nil {
		return nil
	}
	s, err := loadVLESS(in)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(s.Clients))
	for _, c := range s.Clients {
		out = append(out, normalizeEmail(c.Email))
	}
	return out
}

func CountUsers(cfg Config) int { return len(ListUserNames(cfg)) }

// GetVLESSClient returns the UUID of the VLESS client whose normalized email
// matches the bare name (with or without the legacy "@vpn" suffix).
func GetVLESSClient(cfg Config, name string) (string, bool) {
	in := findInbound(&cfg, "vless")
	if in == nil {
		return "", false
	}
	s, err := loadVLESS(in)
	if err != nil {
		return "", false
	}
	target := normalizeEmail(name)
	for _, c := range s.Clients {
		if normalizeEmail(c.Email) == target {
			return c.ID, true
		}
	}
	return "", false
}

// AddUser appends a new VLESS client. If flow is non-empty (e.g.
// "xtls-rprx-vision" for Reality nodes) it is stored on the client so
// xray will accept connections from this user.
func AddUser(cfg *Config, name, uuid, flow string) error {
	if err := ValidateUserName(name); err != nil {
		return err
	}
	for _, existing := range ListUserNames(*cfg) {
		if existing == name {
			return fmt.Errorf("user %q already exists", name)
		}
	}
	vin := findInbound(cfg, "vless")
	if vin == nil {
		return fmt.Errorf("config missing vless inbound")
	}
	vs, err := loadVLESS(vin)
	if err != nil {
		return err
	}
	vs.Clients = append(vs.Clients, vlessClient{ID: uuid, Email: name + emailSuffix, Flow: flow})
	return saveVLESS(vin, vs)
}

func RemoveUser(cfg *Config, name string) error {
	vin := findInbound(cfg, "vless")
	if vin == nil {
		return fmt.Errorf("config missing vless inbound")
	}
	vs, err := loadVLESS(vin)
	if err != nil {
		return err
	}
	target := normalizeEmail(name)
	found := false
	filteredV := vs.Clients[:0]
	for _, c := range vs.Clients {
		if normalizeEmail(c.Email) == target {
			found = true
			continue
		}
		filteredV = append(filteredV, c)
	}
	if !found {
		return fmt.Errorf("user %q not found", name)
	}
	vs.Clients = filteredV
	return saveVLESS(vin, vs)
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	return cfg, json.Unmarshal(raw, &cfg)
}

func SaveAtomic(path string, cfg Config, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
