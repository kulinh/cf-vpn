-- 0021: direct XHTTP route (TLS front on the node's own hostname, one secret
-- path proxied to xray). Both set = the subscription gets <node>-XHTTP-Direct.
-- Written from VNM-01 via scripts/d1-set-node.sh <NODE> xhttp-direct.
ALTER TABLE nodes ADD COLUMN xhttp_direct_host TEXT;
ALTER TABLE nodes ADD COLUMN xhttp_direct_path TEXT;
