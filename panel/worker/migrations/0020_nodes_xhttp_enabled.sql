-- 0020: XHTTP second inbound per node (cfvpnctl xhttp enable). 1 = the node
-- serves /api/v2/stream (packet-up) next to HTTPUpgrade and the subscription
-- gets a <node>-XHTTP line. Written from VNM-01 via scripts/d1-set-node.sh.
ALTER TABLE nodes ADD COLUMN xhttp_enabled INTEGER NOT NULL DEFAULT 0;
