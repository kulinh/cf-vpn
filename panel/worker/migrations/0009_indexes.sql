-- Add missing indexes for common query patterns.
-- All additive, fully reversible (DROP INDEX).

-- user_nodes PK leads with user_id, so "find users by node_id" full-scans.
CREATE INDEX IF NOT EXISTS idx_user_nodes_node_id ON user_nodes(node_id);

-- Audit-log filters by node_id / user_id (partial: skip NULL rows).
CREATE INDEX IF NOT EXISTS idx_events_node_id ON events(node_id) WHERE node_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_user_id ON events(user_id) WHERE user_id IS NOT NULL;

-- Prevent two nodes from sharing the same vpn_host (no current duplicates verified).
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_vpn_host ON nodes(vpn_host);
