-- Normalize legacy events.ts rows from seconds to milliseconds.
-- Code path (lib/db.ts nowTs) writes Date.now() ms; a few legacy rows were
-- inserted in seconds. Threshold 10^10 (~Sat Nov 20 2286 if seconds, ~1970 if ms)
-- distinguishes them safely.
UPDATE events SET ts = ts * 1000 WHERE ts < 10000000000;
