-- Persist the cloudflared tunnel UUID on the node row so deleteNode can still
-- clean up the tunnel when the agent is unreachable at delete time (otherwise
-- the row is removed and the tunnel leaks with no record to find it by).
-- Additive + nullable: backwards compatible. Populated on /admin/v1/status sync.
ALTER TABLE nodes ADD COLUMN tunnel_uuid TEXT;
