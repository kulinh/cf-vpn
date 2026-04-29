-- Seed the canonical 9-zone pool. INSERT OR IGNORE preserves any zone an
-- admin added manually with the same name. Keep this list in sync with
-- internal/zones/pool.go::DefaultPool.
INSERT OR IGNORE INTO zones (name, cf_zone_id, enabled, created_at) VALUES
  ('888vn.net',      'd283b103c5a5175a0296440b8809c4c4', 1, strftime('%s','now')),
  ('dongnat247.com', 'a3930e1fb144d97eacc339ba5fb74cac', 1, strftime('%s','now')),
  ('abony.xyz',      '4c3edba4567090b9a78760b7510335fc', 1, strftime('%s','now')),
  ('duylinh.org',    'da5fc161a906d173f0bb92670b9f5557', 1, strftime('%s','now')),
  ('duylinh.net',    '3851409c42f485f6fc9c87c4570ad9fd', 1, strftime('%s','now')),
  ('rwl247.dev',     '2158ccce56880a4f3be1f4a0be66109a', 1, strftime('%s','now')),
  ('rwl265.com',     '78c5bc6cef91f5749cb4c1e489fcd1f1', 1, strftime('%s','now')),
  ('rwl265.org',     '95ac57c37138eaa8bfa862b88fcdd784', 1, strftime('%s','now')),
  ('rwl.one',        '73de3bba83ad186e0d287553f5ae3e21', 1, strftime('%s','now'));
