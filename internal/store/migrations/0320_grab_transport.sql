-- 0320_grab_transport: how a grab's download travels — 'torrent' or 'usenet'.
--
-- Usenet support keeps every hash column as it is: info_hash (and imports.download_hash,
-- import_reviews.hash) holds a download key, a torrent's info hash or "sab-<job id>" for a
-- usenet job (internal/download/key.go). This column says which, so seed rules, stall
-- checks and the queue can treat a usenet job as one without parsing the key — and so a
-- grab recorded before its client said the job's id still knows what it is.
--
-- Every grab before this was a torrent.

ALTER TABLE grabs ADD COLUMN transport TEXT NOT NULL DEFAULT 'torrent';
