-- Last outcome observed by the cron fleet sweep: 'ok' | 'transport' | 'config'.
-- The sweep logs an audit event when this value CHANGES, not on a counter
-- reaching a magic number: keying the "config error" line on
-- consecutive_failures = 1 logged nothing at all when a transport miss came
-- first (the counter was already 1), which is the failure mode 0017 was meant
-- to remove. NULL means "no sweep since this migration" and is read as 'ok', so
-- the first sweep after deploy cannot manufacture an event.
-- One ALTER per file: D1 has no ADD COLUMN IF NOT EXISTS.
ALTER TABLE nodes ADD COLUMN last_sweep_outcome TEXT;
