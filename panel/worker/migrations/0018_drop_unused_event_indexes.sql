-- Drop two indexes nothing reads.
-- 0009 created idx_events_node_id and idx_events_user_id for "audit-log filters
-- by node_id / user_id", but no such filter was ever written: routes/events.ts
-- listEvents is the only reader of the table and does
-- "SELECT ... FROM events ORDER BY ts DESC LIMIT ?" (the prune in index.ts
-- filters on ts). They only cost write amplification on the hottest insert path
-- in the schema.
--
-- idx_user_nodes_node_id and idx_nodes_vpn_host from 0009 stay: the first backs
-- a real lookup, the second enforces a UNIQUE constraint.
DROP INDEX IF EXISTS idx_events_node_id;
DROP INDEX IF EXISTS idx_events_user_id;
