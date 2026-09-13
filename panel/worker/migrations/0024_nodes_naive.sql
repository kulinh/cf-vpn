-- 0024: NaiveProxy route. A Caddy forward_proxy on the node's TCP 443 under the
-- real certificate for naive_host, with one shared basic-auth pair per node.
-- All three set = the subscription gets <node>-Naive (Hiddify and sing-box
-- clients only; Shadowrocket, Clash and v2rayN cannot parse naive).
--
-- Reported by the agent on /status in any mode (see mergeNaiveRuntime);
-- scripts/d1-set-node.sh <NODE> naive is the manual fallback.
ALTER TABLE nodes ADD COLUMN naive_host TEXT;
ALTER TABLE nodes ADD COLUMN naive_user TEXT;
ALTER TABLE nodes ADD COLUMN naive_pass TEXT;
