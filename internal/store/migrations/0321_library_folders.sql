-- 0321_library_folders: more than one folder for the Movies and TV libraries.
--
-- The folder chosen in first-run setup / Settings → Library (lib_movies_dir, lib_tv_dir,
-- with the ARRMADA_*_DIR environment fallback) stays exactly where it is and stays the
-- default. This table holds only the EXTRA folders, so an install that never adds one has
-- no rows here and behaves as before.
--
--   media     'movie' or 'series'
--   path      the folder as Arrmada sees it (inside the container)
--   label     what the UI calls it ("Kids movies"); '' = its last path segment
--   rule      '' (picked by hand only), 'animation', 'anime' or 'kids': a new title goes
--             to the first folder, by position, whose rule matches it — else the default
--   position  the order rules are tried in
--
-- movies.library_folder_id and series.library_folder_id say which folder a title belongs
-- to: 0 is the default folder, anything else a row here. Every existing title is 0, which
-- is where its files already are.
CREATE TABLE IF NOT EXISTS library_folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    media      TEXT    NOT NULL CHECK (media IN ('movie', 'series')),
    path       TEXT    NOT NULL,
    label      TEXT    NOT NULL DEFAULT '',
    rule       TEXT    NOT NULL DEFAULT '' CHECK (rule IN ('', 'animation', 'anime', 'kids')),
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE (media, path)
);

ALTER TABLE movies ADD COLUMN library_folder_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN library_folder_id INTEGER NOT NULL DEFAULT 0;
