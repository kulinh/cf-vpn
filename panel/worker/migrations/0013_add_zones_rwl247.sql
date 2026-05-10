-- Add rwl247.cc and rwl247.cn to the zone pool. Keep in sync with
-- internal/zones/pool.go::DefaultPool.
INSERT OR IGNORE INTO zones (name, cf_zone_id, enabled, created_at) VALUES
  ('rwl247.cc', '96a3929c790e792217de111b0020490d', 1, strftime('%s','now')),
  ('rwl247.cn', 'e920d9605e5b7d8c642d4302d5e421f5', 1, strftime('%s','now'));
