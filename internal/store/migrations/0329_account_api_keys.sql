-- 0329_account_api_keys: personal API keys (Me → Personal API keys) for scripts and
-- integrations such as n8n, Home Assistant or curl. The api_keys table has existed since
-- 0001, but nothing ever issued a key, so it only needs the columns the feature uses:
--   scope              'read' (GET routes only) or 'full' (whatever the role allows on
--                      the routes keys may reach)
--   role               the creator's role when the key was made. A key acts as the lower
--                      of this and the account's current role, so a demotion takes effect
--                      at once and a later promotion never widens an old key.
--   last4              the key's last four characters, so its owner can tell keys apart
--                      ("arrk_…7f3q"). The rest is only ever stored as its SHA-256 hash.
--   expires_at         NULL = never
--   last_used_network  coarse network of the last use ("local", "203.0.113.x"), as
--                      sessions keep it. Only the key's owner sees it.
--
-- Any row from the old scheme ("arr_" keys) can't be one anybody holds — no release ever
-- created one — and wouldn't validate under the new prefix, so it goes.
DELETE FROM api_keys;
ALTER TABLE api_keys ADD COLUMN scope TEXT NOT NULL DEFAULT 'read';
ALTER TABLE api_keys ADD COLUMN role TEXT NOT NULL DEFAULT 'readonly';
ALTER TABLE api_keys ADD COLUMN last4 TEXT NOT NULL DEFAULT '';
ALTER TABLE api_keys ADD COLUMN expires_at TIMESTAMP;
ALTER TABLE api_keys ADD COLUMN last_used_network TEXT NOT NULL DEFAULT '';
