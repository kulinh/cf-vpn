ALTER TABLE users ADD COLUMN sub_token TEXT;

UPDATE users
SET sub_token = lower(hex(randomblob(16)))
WHERE sub_token IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_sub_token ON users(sub_token);
