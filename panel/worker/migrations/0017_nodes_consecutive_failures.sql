-- Cron fleet-health sweep debounce: a node is only marked 'unreachable' after
-- two consecutive failed sweeps. One transient tunnel hiccup produced 79
-- error/recover event pairs in 29 days of prod data (~99% of the events table).
-- One ALTER per file: D1 has no ADD COLUMN IF NOT EXISTS, so a re-run fails
-- with "duplicate column name" and must not take other statements down with it.
ALTER TABLE nodes ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
