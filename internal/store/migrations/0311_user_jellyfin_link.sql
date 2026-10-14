-- 0311_user_jellyfin_link: link an account to a Jellyfin user, for "Sign in with Jellyfin"
-- and for linking Jellyfin from the account menu — the same shape as the Plex link (0050,
-- 0181).
--
-- jellyfin_id is the Jellyfin user's id (a GUID from the server Arrmada is connected to).
-- One Jellyfin user is one Arrmada account, so it's unique wherever it's set. Nullable:
-- most accounts have none.
--
-- jellyfin_username is the Jellyfin name the link was made with, for "Linked as <name>".
--
-- password_login (0181) keeps its meaning: whether anyone knows the account's password.
-- Accounts made by Jellyfin sign-in get a random one nobody knows, so a Jellyfin (or Plex)
-- link is their only way in, and unlinking the last of them is refused.
ALTER TABLE users ADD COLUMN jellyfin_id TEXT;
ALTER TABLE users ADD COLUMN jellyfin_username TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_jellyfin_id ON users(jellyfin_id)
	WHERE jellyfin_id IS NOT NULL AND jellyfin_id <> '';
