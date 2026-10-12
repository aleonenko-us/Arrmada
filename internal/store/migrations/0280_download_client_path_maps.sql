-- 0280_download_client_path_maps: remote path mappings per download client.
--
-- An external or seedbox qBittorrent can see its downloads under a different path than
-- Arrmada does (it saves to /downloads, Arrmada has that folder mounted at
-- /media/downloads). Nothing translated between the two, so such a client downloaded fine
-- and nothing ever imported. path_maps is a JSON list of {remote, local} prefixes; empty
-- means no mapping, which is every existing client and the bundled one.
ALTER TABLE download_clients ADD COLUMN path_maps TEXT NOT NULL DEFAULT '';
