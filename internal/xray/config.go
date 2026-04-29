package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

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

type vlessClient struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type vlessSettings struct {
	Clients    []vlessClient `json:"clients"`
	Decryption string        `json:"decryption"`
}

var userNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func ValidateUserName(name string) error {
	if !userNameRE.MatchString(name) {
		return fmt.Errorf("invalid user name %q (allowed: %s)", name, userNameRE.String())
	}
	return nil
}

// NewBaseConfig returns a minimal Config suitable for tests. Production
// installs use templates.RenderXray so the full runtime config (ports,
// streamSettings, outbounds, routing) is written verbatim.
func NewBaseConfig(user, uuid, password string) Config {
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
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
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
		out = append(out, c.Email)
	}
	return out
}

func CountUsers(cfg Config) int { return len(ListUserNames(cfg)) }

// GetVLESSClient returns the UUID of the VLESS client whose email matches name.
func GetVLESSClient(cfg Config, name string) (string, bool) {
	in := findInbound(&cfg, "vless")
	if in == nil {
		return "", false
	}
	s, err := loadVLESS(in)
	if err != nil {
		return "", false
	}
	for _, c := range s.Clients {
		if c.Email == name {
			return c.ID, true
		}
	}
	return "", false
}

func AddUser(cfg *Config, name, uuid, password string) error {
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
	vs.Clients = append(vs.Clients, vlessClient{ID: uuid, Email: name})
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
	found := false
	filteredV := vs.Clients[:0]
	for _, c := range vs.Clients {
		if c.Email == name {
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
	if err := f.Chmod(mode); err != nil {
		f.Close()
		os.Remove(tmp)
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
