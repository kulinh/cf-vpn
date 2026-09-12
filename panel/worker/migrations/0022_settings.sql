-- 0022: fleet-wide key/value settings. First key: rules_mode (cn | uae | none),
-- the blocked-site module the Shadowrocket .conf inlines when the link carries
-- no ?rules=. Written from VNM-01 by `cfvpnctl rules-mode set` / the Telegram
-- bot's /mode, read by routes/sub.ts.
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
