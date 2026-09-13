-- 0025: public IPv6 of a node. Set = the subscription gets <node>-Reality-v6
-- (direct nodes with Reality) and <node>-HY2-v6 (nodes with HY2) next to the
-- IPv4 routes, dialling the address in brackets. They go in PROXY only, never
-- in AUTO or HY2-BACKUP: a client without IPv6 must not have its automatic
-- pick land on a route it cannot reach.
--
-- Not reported by the agent; scripts/d1-set-node.sh <NODE> ipv6 copies
-- PUBLIC_IPV6 from the node's cfvpn.env (empty = NULL).
ALTER TABLE nodes ADD COLUMN public_ipv6 TEXT;
