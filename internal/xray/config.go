package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

type Config struct {
	Inbounds []Inbound `json:"inbounds"`
}

type Inbound struct {
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings"`
}

type vlessClient struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type vlessSettings struct {
	Clients    []vlessClient `json:"clients"`
	Decryption string        `json:"decryption"`
}

type trojanClient struct {
	Password string `json:"password"`
	Email    string `json:"email"`
}

type trojanSettings struct {
	Clients []trojanClient `json:"clients"`
}

var userNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func ValidateUserName(name string) error {
	if !userNameRE.MatchString(name) {
		return fmt.Errorf("invalid user name %q (allowed: %s)", name, userNameRE.String())
	}
	return nil
}

func NewBaseConfig(user, uuid, password string) Config {
	vless, _ := json.Marshal(vlessSettings{
		Clients:    []vlessClient{{ID: uuid, Email: user}},
		Decryption: "none",
	})
	trojan, _ := json.Marshal(trojanSettings{
		Clients: []trojanClient{{Password: password, Email: user}},
	})
	return Config{Inbounds: []Inbound{
		{Protocol: "vless", Settings: vless},
		{Protocol: "trojan", Settings: trojan},
	}}
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

func loadTrojan(in *Inbound) (*trojanSettings, error) {
	var s trojanSettings
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

func saveTrojan(in *Inbound, s *trojanSettings) error {
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

// GetTrojanClient returns the password of the Trojan client whose email matches name.
func GetTrojanClient(cfg Config, name string) (string, bool) {
	in := findInbound(&cfg, "trojan")
	if in == nil {
		return "", false
	}
	s, err := loadTrojan(in)
	if err != nil {
		return "", false
	}
	for _, c := range s.Clients {
		if c.Email == name {
			return c.Password, true
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
	tin := findInbound(cfg, "trojan")
	if vin == nil || tin == nil {
		return fmt.Errorf("config missing vless or trojan inbound")
	}
	vs, err := loadVLESS(vin)
	if err != nil {
		return err
	}
	ts, err := loadTrojan(tin)
	if err != nil {
		return err
	}
	vs.Clients = append(vs.Clients, vlessClient{ID: uuid, Email: name})
	ts.Clients = append(ts.Clients, trojanClient{Password: password, Email: name})
	if err := saveVLESS(vin, vs); err != nil {
		return err
	}
	return saveTrojan(tin, ts)
}

func RemoveUser(cfg *Config, name string) error {
	vin := findInbound(cfg, "vless")
	tin := findInbound(cfg, "trojan")
	if vin == nil || tin == nil {
		return fmt.Errorf("config missing vless or trojan inbound")
	}
	vs, err := loadVLESS(vin)
	if err != nil {
		return err
	}
	ts, err := loadTrojan(tin)
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
	filteredT := ts.Clients[:0]
	for _, c := range ts.Clients {
		if c.Email == name {
			continue
		}
		filteredT = append(filteredT, c)
	}
	if !found {
		return fmt.Errorf("user %q not found", name)
	}
	vs.Clients = filteredV
	ts.Clients = filteredT
	if err := saveVLESS(vin, vs); err != nil {
		return err
	}
	return saveTrojan(tin, ts)
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
