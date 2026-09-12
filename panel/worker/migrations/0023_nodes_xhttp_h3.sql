-- 0023: XHTTP-over-H3 route on direct-mode nodes. xray serves it on UDP 443
-- under a real certificate (Hysteria2's, same hostname) next to REALITY on TCP
-- 443. Both set = the subscription gets <node>-XHTTP-H3.
--
-- Reported by the agent on /status and applied only when the node is
-- MODE=direct (see mergeH3Runtime); scripts/d1-set-node.sh <NODE> xhttp-h3 is
-- the manual fallback.
ALTER TABLE nodes ADD COLUMN xhttp_h3_host TEXT;
ALTER TABLE nodes ADD COLUMN xhttp_h3_path TEXT;
