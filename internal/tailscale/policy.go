// Package tailscale edits the tailnet policy file (ACL) through the Tailscale
// API with an OAuth client, and patches its derpMap without disturbing the
// rest of the HuJSON document (comments and formatting are preserved by
// applying RFC 6902 patches through github.com/tailscale/hujson).
package tailscale

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/tailscale/hujson"
)

// Region describes one custom DERP region with a single node, the shape
// cf-vpn deploys (derper on one VPS).
type Region struct {
	ID       int
	Code     string
	Name     string
	HostName string
	DERPPort int
	STUNPort int
}

// Summary is the derpMap as the policy currently declares it.
type Summary struct {
	OmitDefaultRegions bool
	Regions            []Region
}

func parse(policy []byte) (hujson.Value, error) {
	v, err := hujson.Parse(policy)
	if err != nil {
		return hujson.Value{}, fmt.Errorf("parse policy: %w", err)
	}
	return v, nil
}

// standard returns the policy as strict JSON, for inspection only.
// hujson.Standardize rewrites its argument in place (the parsed literals
// alias the input), so it always gets a copy — the caller's bytes must stay
// the original HuJSON with its comments.
func standard(policy []byte) (map[string]json.RawMessage, error) {
	std, err := hujson.Standardize(append([]byte(nil), policy...))
	if err != nil {
		return nil, fmt.Errorf("standardize policy: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(std, &top); err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	return top, nil
}

type derpMapJSON struct {
	OmitDefaultRegions bool `json:"OmitDefaultRegions"`
	Regions            map[string]struct {
		RegionID   int    `json:"RegionID"`
		RegionCode string `json:"RegionCode"`
		RegionName string `json:"RegionName"`
		Nodes      []struct {
			HostName string `json:"HostName"`
			DERPPort int    `json:"DERPPort"`
			STUNPort int    `json:"STUNPort"`
		} `json:"Nodes"`
	} `json:"Regions"`
}

// Summarize reads derpMap out of the policy. A policy without derpMap yields
// an empty Summary and no error.
func Summarize(policy []byte) (Summary, error) {
	top, err := standard(policy)
	if err != nil {
		return Summary{}, err
	}
	raw, ok := top["derpMap"]
	if !ok {
		return Summary{}, nil
	}
	var dm derpMapJSON
	if err := json.Unmarshal(raw, &dm); err != nil {
		return Summary{}, fmt.Errorf("decode derpMap: %w", err)
	}
	s := Summary{OmitDefaultRegions: dm.OmitDefaultRegions}
	for _, r := range dm.Regions {
		reg := Region{ID: r.RegionID, Code: r.RegionCode, Name: r.RegionName}
		if len(r.Nodes) > 0 {
			reg.HostName, reg.DERPPort, reg.STUNPort = r.Nodes[0].HostName, r.Nodes[0].DERPPort, r.Nodes[0].STUNPort
		}
		s.Regions = append(s.Regions, reg)
	}
	sort.Slice(s.Regions, func(i, j int) bool { return s.Regions[i].ID < s.Regions[j].ID })
	return s, nil
}

func hasDerpMap(policy []byte) (bool, error) {
	top, err := standard(policy)
	if err != nil {
		return false, err
	}
	_, ok := top["derpMap"]
	return ok, nil
}

func apply(policy []byte, patch string) ([]byte, error) {
	v, err := parse(policy)
	if err != nil {
		return nil, err
	}
	if err := v.Patch([]byte(patch)); err != nil {
		return nil, fmt.Errorf("patch policy: %w", err)
	}
	v.Format()
	return v.Pack(), nil
}

// SetOmitDefaultRegions sets derpMap.OmitDefaultRegions and touches nothing
// else. The policy must already declare derpMap (custom regions); omitting the
// public relays without any region of your own would strand every device.
func SetOmitDefaultRegions(policy []byte, omit bool) ([]byte, error) {
	s, err := Summarize(policy)
	if err != nil {
		return nil, err
	}
	ok, err := hasDerpMap(policy)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("policy has no derpMap; add a region first (cfvpnctl derp region add)")
	}
	if omit && len(s.Regions) == 0 {
		return nil, fmt.Errorf("refusing to omit the default DERP regions: derpMap has no custom region")
	}
	// JSON Patch "add" on an object member replaces an existing member, so it
	// covers both the first write and later flips.
	return apply(policy, fmt.Sprintf(`[{"op":"add","path":"/derpMap/OmitDefaultRegions","value":%t}]`, omit))
}

// AddRegion inserts (or replaces) one single-node region under
// derpMap.Regions. If the policy has no derpMap yet, one is created with
// OmitDefaultRegions false.
func AddRegion(policy []byte, r Region) ([]byte, error) {
	if r.ID <= 0 || r.Code == "" || r.Name == "" || r.HostName == "" || r.DERPPort <= 0 || r.STUNPort <= 0 {
		return nil, fmt.Errorf("region needs id, code, name, host, derp-port and stun-port")
	}
	if r.ID < 900 {
		// Tailscale reserves the low IDs for its own regions; custom regions
		// conventionally start at 900.
		return nil, fmt.Errorf("region id %d: custom regions must use ids >= 900", r.ID)
	}
	ok, err := hasDerpMap(policy)
	if err != nil {
		return nil, err
	}
	if !ok {
		policy, err = apply(policy, `[{"op":"add","path":"/derpMap","value":{"OmitDefaultRegions":false,"Regions":{}}}]`)
		if err != nil {
			return nil, err
		}
	}
	top, err := standard(policy)
	if err != nil {
		return nil, err
	}
	var dm map[string]json.RawMessage
	if err := json.Unmarshal(top["derpMap"], &dm); err != nil {
		return nil, fmt.Errorf("decode derpMap: %w", err)
	}
	if _, ok := dm["Regions"]; !ok {
		policy, err = apply(policy, `[{"op":"add","path":"/derpMap/Regions","value":{}}]`)
		if err != nil {
			return nil, err
		}
	}
	node := map[string]any{"Name": strconv.Itoa(r.ID) + "a", "RegionID": r.ID, "HostName": r.HostName, "DERPPort": r.DERPPort, "STUNPort": r.STUNPort}
	region := map[string]any{"RegionID": r.ID, "RegionCode": r.Code, "RegionName": r.Name, "Nodes": []any{node}}
	val, _ := json.Marshal(region)
	return apply(policy, fmt.Sprintf(`[{"op":"add","path":"/derpMap/Regions/%d","value":%s}]`, r.ID, val))
}

// RemoveRegion deletes derpMap.Regions[id]. Removing the last region while
// OmitDefaultRegions is true is refused for the same reason as above.
func RemoveRegion(policy []byte, id int) ([]byte, error) {
	s, err := Summarize(policy)
	if err != nil {
		return nil, err
	}
	found := false
	for _, r := range s.Regions {
		if r.ID == id {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("region %d is not in derpMap", id)
	}
	if s.OmitDefaultRegions && len(s.Regions) == 1 {
		return nil, fmt.Errorf("refusing to remove the last custom region while OmitDefaultRegions is true; run `cfvpnctl derp china-mode off` first")
	}
	return apply(policy, fmt.Sprintf(`[{"op":"remove","path":"/derpMap/Regions/%d"}]`, id))
}

// Canonical returns the policy as compact standard JSON with sorted keys, so
// two policies can be compared for semantic equality regardless of comments.
func Canonical(policy []byte) ([]byte, error) {
	std, err := hujson.Standardize(append([]byte(nil), policy...))
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(std, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v) // encoding/json sorts map keys
}
