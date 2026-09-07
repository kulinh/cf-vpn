#!/usr/bin/env bash
# cfvpn-drift.sh — compare the credentials a node actually serves against the
# ones D1 hands to clients. Source it, do not execute it.
#
# The panel builds every subscription link from D1 `user_nodes`; the node
# serves whatever is in /etc/cfvpn/xray/config.json and
# /etc/cfvpn/hysteria/config.yaml. Those two are written at different times by
# different paths (install, add-user, agent sync, a hand edit), so they drift —
# and drift is invisible until a client fails. A wrong Hysteria2 password does
# not produce an auth error: the QUIC server simply never answers, so the user
# reports a *timeout* and the node looks perfectly healthy from the outside.
#
# Provides:
#   drift_compare <d1_file> <node_file>   — prints one line per mismatch
#
# Both files are TSV with the same shape, one row per (node, user):
#   <node_id>\t<user>\t<vless_uuid>\t<hy2_pw>
# A field may be "-" when that side has no value (user absent, HY2 not set up).

# drift_compare — prints "<node>\t<user>\t<field>\td1=<x>\tnode=<y>" per
# mismatch, sorted, and returns 1 when anything drifted. Rows present on only
# one side are reported too: a user D1 promises but the node does not serve is
# exactly as broken as a wrong password.
drift_compare() {
  local d1_file="$1" node_file="$2"
  [ -r "$d1_file" ]   || { echo "drift_compare: cannot read $d1_file" >&2; return 2; }
  [ -r "$node_file" ] || { echo "drift_compare: cannot read $node_file" >&2; return 2; }

  awk -F'\t' '
    function mask(v) {
      # Never print a credential in full: enough to tell two values apart,
      # not enough to use. Short values are already unusable, so keep them.
      if (v == "" || v == "-") return "-"
      if (length(v) <= 8) return v
      return substr(v, 1, 8) "…"
    }
    NR == FNR { d1[$1 "\t" $2] = $3 "\t" $4; next }
    {
      key = $1 "\t" $2
      seen[key] = 1
      if (!(key in d1)) { print key "\tuser\td1=<absent>\tnode=present"; bad = 1; next }
      split(d1[key], want, "\t")
      if (want[1] != $3) { print key "\tvless_uuid\td1=" mask(want[1]) "\tnode=" mask($3); bad = 1 }
      if (want[2] != $4) { print key "\thy2_pw\td1=" mask(want[2]) "\tnode=" mask($4); bad = 1 }
    }
    END {
      for (key in d1) if (!(key in seen)) { print key "\tuser\td1=present\tnode=<absent>"; bad = 1 }
      exit bad ? 1 : 0
    }
  ' "$d1_file" "$node_file" | sort
  return "${PIPESTATUS[0]}"
}
