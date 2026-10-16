-- 0322_library_unmatched_root_key: the needs-review list is keyed by the library folder too.
--
-- With more than one Movies or TV folder (0321), two folders can each hold a folder named
-- "Frozen (2013)": keyed by (media, folder) alone, the second scan row replaced the first and
-- a manual pick in one folder dropped the other's row. The key is now (media, root, folder).
-- Every existing row keeps its root — the folder its scan walked, which before 0321 was the
-- one (default) folder — so nothing already listed changes.
CREATE TABLE library_unmatched_new (
    media           TEXT NOT NULL,
    root            TEXT NOT NULL,
    folder          TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    year            INTEGER NOT NULL DEFAULT 0,
    candidates_json TEXT NOT NULL DEFAULT '[]',
    scanned_at      TEXT NOT NULL,
    PRIMARY KEY (media, root, folder)
);

INSERT INTO library_unmatched_new (media, root, folder, title, year, candidates_json, scanned_at)
    SELECT media, root, folder, title, year, candidates_json, scanned_at FROM library_unmatched;

DROP TABLE library_unmatched;
ALTER TABLE library_unmatched_new RENAME TO library_unmatched;
