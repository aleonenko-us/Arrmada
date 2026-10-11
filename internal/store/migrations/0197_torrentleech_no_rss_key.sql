-- 0197_torrentleech_no_rss_key: TorrentLeech indexers stop keeping an "RSS key".
--
-- The field was collected but never used: downloads and the new-uploads feed go through
-- the logged-in session (with FlareSolverr for Cloudflare). The cookie-free RSS download
-- link it was meant for couldn't be confirmed against TorrentLeech, and such a link
-- carries the key in its path, so the field is gone from the forms and a saved key —
-- a secret nothing reads — is cleared rather than left lying in the database.
UPDATE indexers SET api_key = '' WHERE kind = 'torrentleech' AND api_key != '';
