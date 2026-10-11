-- 0207_library_unmatched: the folders a library scan couldn't confidently identify, with
-- the candidates offered for a manual pick (Settings → Library, "needs review").
--
-- They used to live only in memory, so a restart (an update, a crash) emptied the list
-- and the only way back was another full scan. Each scan replaces its media's rows; a
-- manual pick deletes the one it settled. media is "movie" today and the table is kept
-- media-agnostic so series can move onto it. candidates_json is the provider's search
-- results as the scan saw them (an empty array when there were none).
CREATE TABLE IF NOT EXISTS library_unmatched (
    media           TEXT NOT NULL,
    root            TEXT NOT NULL,
    folder          TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    year            INTEGER NOT NULL DEFAULT 0,
    candidates_json TEXT NOT NULL DEFAULT '[]',
    scanned_at      TEXT NOT NULL,
    PRIMARY KEY (media, folder)
);
