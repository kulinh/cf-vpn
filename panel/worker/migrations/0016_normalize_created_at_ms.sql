-- Normalize legacy created_at values to milliseconds.
-- R2 normalized events.ts (0012) and zones.created_at (0015) but missed
-- nodes.created_at and user_nodes.created_at. Prod currently holds all three
-- shapes in nodes.created_at: ms (SIN-01 = 1776769571071), seconds
-- (VNM-02 = 1777657969) and ISO text (VNM-01 = "2026-04-27T08:09:23.247Z").
-- The column is declared INTEGER but SQLite's type affinity accepts TEXT, and
-- SQLite orders TEXT above INTEGER, so MAX(created_at) / ORDER BY created_at
-- are wrong today.
--
-- Every write site already emits ms (lib/db.ts nowTs, NOW_MS=$(date +%s%3N)),
-- so this is legacy sediment. Both guards are self-excluding, so re-running the
-- file is a no-op.
-- The strftime() guard matters: on a value SQLite cannot parse as a date it
-- returns NULL, and writing that into a NOT NULL column (declared, though
-- SQLite only enforces it on INSERT) would be strictly worse than the mixed
-- units we are fixing. Such a row is left alone for a human to look at.
UPDATE nodes      SET created_at = CAST(strftime('%s', created_at) AS INTEGER) * 1000
  WHERE typeof(created_at) = 'text' AND strftime('%s', created_at) IS NOT NULL;
UPDATE user_nodes SET created_at = CAST(strftime('%s', created_at) AS INTEGER) * 1000
  WHERE typeof(created_at) = 'text' AND strftime('%s', created_at) IS NOT NULL;
UPDATE nodes      SET created_at = created_at * 1000 WHERE created_at < 10000000000;
UPDATE user_nodes SET created_at = created_at * 1000 WHERE created_at < 10000000000;
UPDATE users      SET created_at = created_at * 1000 WHERE created_at < 10000000000;
