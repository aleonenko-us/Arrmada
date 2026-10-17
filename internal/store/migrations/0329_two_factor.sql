-- 0329_two_factor: optional two-factor sign-in (TOTP, RFC 6238) for Arrmada's own
-- password sign-in. Plex and Jellyfin sign-in, API keys and the audiobook apps are not
-- covered: they never use the Arrmada password.
--
-- user_totp: one row per account that has started or finished setting it up.
--   secret      the base32 shared secret. Stored as the app stores its other credentials
--               (there is no app-wide encryption key to wrap it with): never sent back after
--               setup, never logged, and blanked from the support bundle.
--   enabled     0 while setting up (the code shown hasn't been confirmed yet), 1 once on
--   last_step   the newest 30-second step a code was accepted for; a code for that step or
--               an older one is refused, so a code works once
--   enabled_at  when it was turned on (NULL while setting up)
--
-- user_recovery_codes: the ten one-time codes shown once at setup, bcrypt-hashed. used_at
-- stamps a spent code.
--
-- user_trusted_devices: "trust this device for 30 days". A random token in an HttpOnly
-- cookie, stored hashed like a session token; it only skips the code, never the password.
-- Turning two-factor off, a password change and signing out everywhere forget them.
--
-- Every table goes with the account (ON DELETE CASCADE); auth.MergeDuplicate removes a
-- merged duplicate's rows explicitly too.
CREATE TABLE IF NOT EXISTS user_totp (
    user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret     TEXT      NOT NULL,
    enabled    INTEGER   NOT NULL DEFAULT 0,
    last_step  INTEGER   NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    enabled_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_recovery_codes (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT    NOT NULL,
    used_at   TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_user_recovery_codes_user ON user_recovery_codes(user_id);

CREATE TABLE IF NOT EXISTS user_trusted_devices (
    token_hash TEXT PRIMARY KEY,
    user_id    INTEGER   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_user_trusted_devices_user ON user_trusted_devices(user_id);
