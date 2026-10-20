-- 0349_setup_sessions: the first-run wizard's short-lived setup session.
--
-- On a fresh install the wizard now runs before any account exists (the account is made
-- last), so its calls need something to authorise them. A setup session is that: a random
-- token (only its hash is kept) that the server hands to a LAN browser while no admin
-- exists. It is checked on every request against both its expiry and "no admin exists", so
-- it dies the moment the first admin is made, and it is accepted by an explicit list of the
-- wizard's routes only (httpapi/setup_session.go). Rows are swept when the admin is made.
CREATE TABLE setup_sessions (
    token_hash TEXT PRIMARY KEY,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_setup_sessions_expiry ON setup_sessions(expires_at);
