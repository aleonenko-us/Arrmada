# More than one library folder for Movies and TV

Status: built on `feat/library-folders` (2026-10-16), from `main` @ cb5a5998 (highest migration 0320; this work uses 0321 and 0322).

Asked for on Reddit and in GitHub issue #3: people keep separate folders — Movies and Kids/Animation movies, TV and Cartoons/Anime — often as separate Plex or Jellyfin libraries. Until now each library had exactly one folder (`internal/libroots`: `lib_movies_dir` / `lib_tv_dir`, falling back to `ARRMADA_MOVIES_DIR` / `ARRMADA_TV_DIR`).

## Owner decisions

1. **Scope:** Movies and TV only. Ebooks, audiobooks and music keep one folder.
2. **Picking a folder:** rules plus an admin override.
   - Each extra folder can carry one optional rule: **Animation** (TMDB genre Animation), **Anime** (shows: the existing anime numbering flag; films: Animation + Japanese original language — the same test series use when they're added), **Kids** (US ratings G, TV-Y, TV-Y7, TV-Y7-FV, TV-G; shows also TMDB's Kids genre). Shows' US TV rating is now read from TMDB's `content_ratings`.
   - Folders are ordered: a new title goes to the first extra folder whose rule matches, otherwise to the default folder.
   - Staff who add titles today (admins and managers) choose the folder when adding a title and when approving a request. Requesters never pick.
3. **Moving a title to another folder moves its files**, as a background job: a rename on one filesystem; across filesystems copy, verify every file, put the copy in place, repoint the database, then retire the originals through `library.RemoveToBin` (never `os.Remove`). Plex and Jellyfin get partial scans of the old and the new folder.
4. **Pickers only with several folders:** "Move to folder", the folder name on a title and every folder picker appear only when that library has more than one folder. With one folder the UI is exactly as before.

## Design

- **Default stays where it is.** The Settings → Library folder (with its env fallback and the first-run Folders step) is the default folder, id 0, resolved by libroots as before. `library_folders` (migration 0321) holds only the *extra* folders; `movies.library_folder_id` / `series.library_folder_id` (default 0) say which folder a title belongs to. An install that never adds a folder has no rows and every answer is the default.
- **`internal/libfolders`:** the folder list (cached in memory, reloaded after every write through the service), rules and `Pick`, `Owner` (the longest folder holding a path), add/update/reorder/remove with overlap checks (no folder inside another, or inside/around the data, downloads or any other library folder; the folder must already exist), and the remove guard (refused while any title is assigned to it or any library file lies in it, in one transaction).
- **`libroots.Roots.WithExtras`:** `Libraries()`/`All()` list the extras (each a `Root` named after its library, with its own label), so the recycle bins (`library.RootBins`), Convert's holds, the SABnzbd leftover guard, the download-client path checks, manual import's allowed roots and file info all cover them. `MovieFolders`/`TVFolders` list a library's folders, default first.
- **Placing files:** the importer resolves a film's root from `MovieTrack.Root` (set by `movies.TrackNaming`) and a show's through `Importer.InTV(root)` (`automation.tvImp`). The root is the folder the title's files are in when that's one of the library's, else its assignment — so an import, upgrade or rename never splits a title across folders.
- **Move job** (`libfolders.Mover`, job kind `library.move`, its own class `library-move`, one at a time, so a long copy never holds up imports): refused while a download for the title is in flight (fail closed) or a search of it runs (searches are held for the whole move), while Convert has a swap open for it, when a file sits loose in a library folder itself, when another title shares the folder, when the destination already has that folder, or when two of the title's folders would land on the same place. A copy onto another drive first checks the destination has the room (size + 1 %, at least 256 MB), is built in a hidden staging folder directly in the destination library folder (`.arrmada-moving-…`, skipped by every scan, Plex and Jellyfin), compared with the original before it's put in place, and the originals that were copied — looked at again just before — go to the recycle bin; a file that turned up meanwhile stays and the job says so. A failed, cancelled or timed-out copy deletes its staging folder outright (the one approved exception to "deletes go through the bin": it's Arrmada's own duplicate, the originals untouched, and only a path shaped like a staging folder in the destination library folder can be deleted that way); a leftover from a crash is removed by the next move into that folder. `RewritePrefix` repoints every stored path in one transaction per folder, which re-checks that the destination folder still exists; a folder can't be removed while a move is putting files into it. A schema test fails if a new path column is neither repointed nor listed as ignored.
- **Scans with several folders** skip and name a folder they can't read (a share that's down) and scan the rest; its needs-review rows stay. With one folder an unreadable folder still fails the scan. The needs-review list is keyed by (media, library folder, folder) since migration 0322.

## Tasks

- [x] Migration 0321: `library_folders`, `movies.library_folder_id`, `series.library_folder_id` (additive; migration test).
- [x] `internal/libfolders`: folders, rules and order, default fallback, overlap checks, remove guard (unit tests).
- [x] libroots extras; recycle bins named per folder (`RootBins` uses the folder's label).
- [x] Importer destination per folder (films: `MovieTrack.Root`; shows: `Importer.InTV`); upgrades/replacements follow the title's folder.
- [x] Movies: add picks a folder (rules or chosen), scans walk every folder and assign by where a title was found, needs-review remembers each folder's root (manual pick validated), missing-file check judges each folder on its own, delete/rename tidy-up never removes any library folder.
- [x] Series: the same for shows (scan, add, `RootFor`, prune guard); shows' US content rating stored for the Kids rule.
- [x] Requests: approve carries the chosen folder.
- [x] Move job: same-filesystem rename, cross-filesystem copy → verify → bin, path rewrite of every table, Plex/Jellyfin scans of both folders (tests with an injected EXDEV).
- [x] Health folder checks, System → Status disks, Dashboard storage (now the folders in use, not the environment's), downloads-share check, startup folder log: every extra folder too.
- [x] Plex and Jellyfin: a change in an extra folder resolves against that folder; the library-updates cards list and test each folder; Jellyfin Watch-link folder lookup.
- [x] Settings → Library "More Movies and TV folders" card: add (folder picker), name, rule, order, remove (refused with what to do while titles remain), Docker mount hint; in the settings search.
- [x] Add movie / Add series / approve: folder picker only with several folders, preselected by the rules (the series dialog, which adds straight from results, defaults to "By the folder rules").
- [x] Movie and series pages: folder name and "Move to folder" (confirm dialog following the job) only with several folders.
- [x] Import & migrate: read a Radarr's/Sonarr's root folders, match them to Arrmada's (same path, else same name), add the missing ones and scan.
- [x] Routes in the golden table; e2e fixture for the new read; e2e spec for one and several folders.
- [x] Review fixes: hidden staging folder deleted outright on failure (narrow guard) and cleared by the next move; free-space check; duplicate destinations refused; remove vs move can't cross; originals re-checked before retiring; needs-review keyed by root (0322); unreadable folder skipped in multi-folder scans; Radarr/Sonarr redirects not followed; blank default checked against extra folders; moves in their own job class.

## Not done here / follow-ups

- ~~**Radarr/Sonarr import is folders only.**~~ Done in [ARRIMPORT](ARRIMPORT.md): the folders card grew into a full title importer (profiles, monitoring, wanted titles, existing files).
- **No "make this the default" swap.** The default is the Settings → Library folder; change it there. Swapping a default with an extra folder would have to reassign every title of both in step with a settings write.
- **Changing an extra folder's path** isn't offered (the titles in it would point at a folder that doesn't hold them): move the titles, remove the folder, add the new one.
- Existing titles are never re-sorted by a rule added later: rules decide only for new titles; use Move to folder.
