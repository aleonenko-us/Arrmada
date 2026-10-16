# Import a library from Radarr and Sonarr

Status: built on `feat/arr-import` (2026-10-16), from `main` @ 586c05a0 (highest migration 0322; this work uses `0324_arrimport_paced_search` — 0323 went to the webhook lane).

People moving from the *arr stack want their library over in one go. Until now Settings → Import & migrate only read Radarr's/Sonarr's root folders ([FOLDERS](FOLDERS.md)); the library scan then adopted whatever files it found, unmonitored, with no profile, and nothing Radarr or Sonarr only *wanted*.

## Design

- **Connect, preview, import.** `POST /api/v1/import/arr/preview` (admin) reads the app — `/api/v3/system/status`, `/qualityprofile`, `/rootfolder`, `/movie` or `/series` — and answers what an import would do; nothing changes anywhere. `POST /api/v1/import/arr/run` (admin) checks the choices and the connection, then imports as a job (`import.arr`, target `arr`: one import at a time, so two can't race to add the same titles). The API key comes with each request and is used for it alone: never stored, logged or returned (tests check the answers and the job result; the live run grepped the log and the data folder).
- **Matching.** Films by TMDB id (Radarr has it). Shows by Sonarr 4's TMDB id, else Arrmada's own TVDB ids, else TMDB's `/find` by TVDB id (`metadata.TMDB.SeriesByTVDB`, for Sonarr 3). No match: skipped with the reason.
- **Folders.** The existing root-folder matcher (`arrimport.Map`: same path, else same name) plus "add it as a folder". A title whose file is found goes to the folder the file is in; a file-less title to its root folder's Arrmada folder (or the folder rules).
- **Paths.** `PathMap{From, To}` prefix remaps (longest prefix wins, Windows paths case-insensitive); the preview suggests one for every root folder matched to an Arrmada folder at another path. A file is used only when it exists, lies inside one of Arrmada's library folders for that media, and isn't already held by another title.
- **Profiles.** Per *arr profile: an Arrmada profile of the same name (loosely), else one reaching the same highest resolution (the default first), else the default.
- **Films.** `movies.AddImported`: created **unmonitored**, then its file recorded where it is with the library scan's quiet adoption (no outbox row, no per-film alert — Convert's and Subtitles' own sweeps index adopted files) or, when wanted, its paced-search mark set — and only then monitored, so a sweep running meanwhile never sees a film wanting a file it has. Everything after the insert runs on `context.WithoutCancel`, so a cancel or the timeout leaves the film complete or unmonitored; a failed step leaves it unmonitored and says so. A film already in Arrmada is left alone, except that one still without a file gets Radarr's (`movies.AttachImported`, the scan's attach). Radarr saying a film has a file without saying where counts as a file not found.
- **Shows.** `series.AddWith` **paused** (monitored gate off) with Sonarr's monitored seasons (`Seasons`) and "monitor new seasons" (Sonarr 4's `monitorNewItems`, else the latest season's flag); then anime type when Sonarr says anime, each episode's monitored flag (never specials), each episode file recorded on the episodes its name resolves to (`ResolveEpisodes`, so multi-episode and absolute-numbered files work) through a conditional update that never replaces a file, the paced mark — and only when all of that went through is the show monitored as Sonarr had it (on `context.WithoutCancel`; a failed step leaves it paused, named in the summary). Sonarr's per-episode flags and its own file-to-episode mapping are used only in seasons numbered the same way (Arrmada's listing is TVDB's, or the season has as many episodes); elsewhere the season choice stands, files go by their names, and the summary names the seasons. An existing show only gets files for episodes that have none.
- **Missing files.** A title whose files can't be found is skipped by default (so a wrong mapping never becomes re-downloading a library); "also bring over titles whose files can't be found" adds them without.
- **No download flood.** Nothing is searched on import. Wanted titles (monitored, no file) carry `search_paced = 1` (migration 0324); each missing sweep searches at most `automation.PacedSearchesPerSweep` (10) marked titles, clearing the mark on each one it searched — at most 120 imported films an hour (movie sweep every 5 minutes) and 40 shows (every 15). Unmarked titles are searched as before. Upgrades already have their per-sweep budget; RSS only sees new uploads.
- **Errors.** Each title runs on its own (`safego.Call`): an error or a panic is that title's failure, noted, and the rest carry on. A cancel or the 6-hour timeout stops between titles and the result says how far it got.
- **UI.** The "Folders from Radarr or Sonarr" card became "Import from Radarr / Sonarr" (`#arr-import`, old `#arr-folders` redirects), first on Import & migrate: connect → summary → 1 Folders → 2 Where the files are (maps, Check again, not-found list, import-missing switch) → 3 Quality profiles → Import (follows the job) → summary with skipped/failed titles named → Import another. Calls in the lazy `importApi` module; requester first load unchanged.

## Tasks

- [x] Migration `0324_arrimport_paced_search` (movies/series `search_paced`) and paced missing sweeps (tests: 25 imported films → 10 per sweep; shows likewise).
- [x] `arrimport` client: status, profiles (highest allowed resolution through groups), movies with files (`/moviefile` fallback), series, episodes, episode files; redacted address in errors.
- [x] Path maps, profile suggestions, preview counts.
- [x] Import engine: films and shows, existing-title file attach, idempotency, per-title error isolation, TVDB → TMDB.
- [x] `movies.AddImported` / `AttachImported` / `InLibrary`, `series.LibraryFolderOf`, `metadata.TMDB.SeriesByTVDB`.
- [x] Routes `POST /api/v1/import/arr/preview` and `/run` (admin), golden table, httpapi tests (validation, no key in answers or result, one at a time, no TMDB key).
- [x] Settings card, search index, redirect from the old anchor, vitest + e2e.
- [x] Docs: configuration.md, features.md, README.
- [x] Verified against a real Radarr 6.4 and Sonarr 4.0 (containers, synthetic titles and lavfi files) end to end with this branch's binary; their answers are the test fixtures (`internal/arrimport/testdata/live`).

## Not done here

- Tags, blocklists, history, download-client and indexer settings, custom formats.
- A second Radarr's copies (Radarr-4K) of films already imported aren't added as extra Arrmada versions: the film is "already here". Adding them as versions is a follow-up.
- Sonarr's episode numbering is TVDB's; where Arrmada's listing numbers a show differently, episode-level monitoring follows the season choice and files go by their names.
