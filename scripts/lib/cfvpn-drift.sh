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
#   drift_compare <d1_file> <node_file>             — credential mismatches
#   drift_transport_compare <d1_file> <node_file>   — transport-flag mismatches
#   drift_rank_rc <current> <new>                   — fold exit statuses (1 > 2 > 0)
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

# drift_transport_compare <d1_file> <node_file>
#
# Credentials are not the only thing that drifts: which TRANSPORTS a node
# actually runs is stored twice as well. The node decides from its cfvpn.env
# (HY2_ENABLED / XHTTP_ENABLED / XHTTP_DIRECT_HOST), while the panel decides
# what to put in a subscription from D1 (nodes.hy2_host NULL-or-set /
# xhttp_enabled / xhttp_direct_host). `cfvpnctl hy2 disable` run over SSH
# changes only the first, so the panel keeps handing out a hysteria2:// link to
# a node with no hysteria listening — and a dead HY2 endpoint looks exactly like
# a timeout to the client, same as a wrong password.
#
# Both files are TSV, one row per node, holding the RAW values so the
# default-on/default-off rules live in one place:
#   d1:   <node_id>\t<hy2_host>\t<xhttp_enabled>\t<xhttp_direct_host>\t<xhttp_h3_host>
#   node: <node_id>\t<HY2_ENABLED>\t<XHTTP_ENABLED>\t<XHTTP_DIRECT_HOST>\t<XHTTP_H3_HOST>
# (NULL/absent -> ""). Rows written before the H3 column existed have only four
# fields; an absent fifth reads as "no route" on both sides, so it is not drift.
#
# Prints "<node>\t-\t<field>\td1=<x>\tnode=<y>" per mismatch (the "-" holds the
# user column of drift_compare's format, since these flags are per node) and
# returns 1 when anything drifted, 2 when a file cannot be read.
drift_transport_compare() {
  local d1_file="$1" node_file="$2"
  [ -r "$d1_file" ]   || { echo "drift_transport_compare: cannot read $d1_file" >&2; return 2; }
  [ -r "$node_file" ] || { echo "drift_transport_compare: cannot read $node_file" >&2; return 2; }

  awk -F'\t' '
    function norm(v) {
      gsub(/^[ \t]+|[ \t]+$/, "", v)
      return tolower(v)
    }
    # Mirrors commands.Hy2Enabled: a MISSING HY2_ENABLED means ON, so every node
    # that predates the key keeps its hysteria.
    function hy2_node(v) { v = norm(v); return (v == "0" || v == "false" || v == "no" || v == "off") ? "off" : "on" }
    # D1 records "no hysteria" as hy2_host NULL (scripts/d1-set-node.sh hy2-off).
    function hy2_d1(v)   { return (norm(v) == "") ? "off" : "on" }
    # Mirrors commands.XHTTPEnabled: a missing XHTTP_ENABLED means OFF.
    function xh_node(v)  { v = norm(v); return (v == "1" || v == "true" || v == "yes" || v == "on") ? "on" : "off" }
    function xh_d1(v)    { return (norm(v) == "1") ? "on" : "off" }
    function host(v)     { gsub(/^[ \t]+|[ \t]+$/, "", v); return (v == "") ? "-" : v }
    function report(node, field, d1v, nodev) {
      if (d1v != nodev) { print node "\t-\t" field "\td1=" d1v "\tnode=" nodev; bad = 1 }
    }
    NR == FNR { d1[$1] = $2 "\t" $3 "\t" $4 "\t" $5; next }
    {
      node = $1
      seen[node] = 1
      if (!(node in d1)) { print node "\t-\tnode_row\td1=<absent>\tnode=present"; bad = 1; next }
      split(d1[node], want, "\t")
      report(node, "hy2_enabled",       hy2_d1(want[1]), hy2_node($2))
      report(node, "xhttp_enabled",     xh_d1(want[2]),  xh_node($3))
      report(node, "xhttp_direct_host", host(want[3]),   host($4))
      report(node, "xhttp_h3_host",     host(want[4]),   host($5))
    }
    END {
      for (node in d1) if (!(node in seen)) { print node "\t-\tnode_row\td1=present\tnode=<absent>"; bad = 1 }
      exit bad ? 1 : 0
    }
  ' "$d1_file" "$node_file" | sort
  return "${PIPESTATUS[0]}"
}

# drift_rank_rc CURRENT NEW — fold one exit status into the running one and print
# the result. The status is a RANKING, not a maximum: drift (1) outranks "could
# not check" (2), which outranks in-sync (0). Plain arithmetic would let a single
# unreachable host downgrade confirmed drift to "transient problem, retry later",
# which is exactly how a real mismatch survives a week of green-looking runs.
drift_rank_rc() {
  local cur="$1" new="$2"
  case "$new" in
    1) printf '1\n' ;;
    2) if [ "$cur" -eq 1 ]; then printf '1\n'; else printf '2\n'; fi ;;
    *) printf '%s\n' "$cur" ;;
  esac
}
