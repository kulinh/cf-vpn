-- 0011_nodes_agent_secret.sql
-- Adds the per-node bearer secret used to authenticate Worker → agent calls.
-- The Worker reads nodes.agent_secret and sets `Authorization: Bearer <secret>`
-- on every /admin/v1/* request. install-node.sh prints the SQL needed to
-- mirror /etc/cfvpn/cfvpn.env's AGENT_SHARED_SECRET into this column.
--
-- Backwards compatible: nullable. Worker falls back to env.AGENT_SHARED_SECRET
-- only when this column is null/empty; new nodes should always populate it.
ALTER TABLE nodes ADD COLUMN agent_secret TEXT;
