-- Normalize legacy zones.created_at rows from seconds to milliseconds.
-- Code path (routes/zones.ts createZone -> lib/db.ts nowTs) writes Date.now() ms;
-- the seed migrations (0005, 0013) inserted seconds via strftime('%s','now').
-- Threshold 10^10 (~Sat Nov 20 2286 if seconds, ~1970 if ms) distinguishes them.
UPDATE zones SET created_at = created_at * 1000 WHERE created_at < 10000000000;
