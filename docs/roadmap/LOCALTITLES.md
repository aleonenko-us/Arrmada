# Titles in other languages

Status: matching built on `fix/localized-titles` (merged 498bccc3, GitHub #13); TV searches built on `feat/series-search-localized` (2026-10-17), from `main` @ 14e9d3c1. No migration.

A German household's folders and trackers name titles in German: Wild is "Der große Trip – Wild", Money Heist is "Haus des Geldes". Settings → Library → **Also match titles in** (`match_title_languages`, empty by default) names those languages. Each title's translated names in them are read from TMDB on add, refresh and a throttled backfill, and stored with it (`extra_json`: `local_titles`, `local_langs`).

## What uses them

- **Library scans** — a folder named in one of the languages is identified by a TMDB search in that language, only when exactly one title is named so.
- **Imports** — a download named by a translated title imports to its title (`MatchRelease`'s last tier for shows, `Match` for movies): never a name another title already goes by, never one two titles share. A download grabbed for a title is also checked against every name TMDB has for that one title (`KnownAs`).
- **TV searches** (new) — the sweeps, Search now, the interactive list and quick Grab buttons, upgrades and RSS also take a release named by a show's stored translated title (`series.SearchFit`, `series/searchtitles.go`), and one extra query asks indexers by it.

Movie searches don't use translated titles: they already ask by the original title when the English one finds nothing, and a film's releases are far less often named only in another language.

## TV searches: the rule

A search grabs what it accepts with nobody looking, so this tier is stricter than the import's:

1. Only the languages set, and only the titles stored for the show. A search never calls TMDB.
2. Only for a release the show's own title and aliases don't name. When they do, `FitRelease` decides as before, a year or country that disagrees included.
3. Never a name some library show goes by, in any year or country (`NamesAShow`).
4. Never a name another library show's translated title shares, in any of the languages set. Ambiguous means no match, whatever the year (the import's tier lets a year choose; a search doesn't).
5. Year and country, as `FitRelease` checks them, but they must be known: a year in the release must be the show's, and a country tag must be its origin. The import's tier lets an unknown origin pass; this one doesn't.
6. A name shorter than three letters never counts, whether a translated title or a release's title (with or without its country tag) (`minLocalKeyLen`). Letters, not bytes: "Он" is two letters.
7. **Evidence beyond the name, always.** A translated title is also some other show's name: Ghosts (US) is "Fantasmas" in Spanish, and so is HBO's English "Fantasmas", which the library has never heard of. So the release must also say the title's language (German, GER, SPANISH…), or be MULTi/DUAL (German DL included), or carry the show's year or country tag. With none of these it is refused — in every search, RSS and the extra query's results alike. (The first build took a release with neither year nor country on rules 3–5 alone; the review found the Fantasmas wrong grab.)
8. **One short word.** A translated title that is a single word of four letters or fewer ("Haus", "Тьма") needs the release's year or country tag to agree — its language alone isn't enough for a word that names that many shows.
9. **Only once every show is read.** Until every library show with a TMDB id has had its translated titles read for the languages set (after the setting changes, the backfill reads one show every two seconds; a failed read is retried at the next refresh), a show not yet read could share the name unseen, so the tier stays off. It says so once in the log (Info, naming up to five of the shows and the count).

With the setting empty `SearchFit` is `FitRelease`, exactly; the existing identity and golden tests are unchanged.

**RSS.** A feed release matched by a translated title is never also named by another show (rule 3) and never shared with one (rule 4), so it goes to one show or none. The grab step judges by the same rule over the same library snapshot.

**Imports of what a search took.** The import's tier accepts everything this one does (it is looser), and a grab is also confirmed by `KnownAs` from the stored titles.

## TV searches: the extra query

One text query by the translated title (`SearchFit.QueryTitle`: the first title, in the setting's order, that rules 3, 4 and 8 allow and that is in Latin script — queries are folded to ASCII, so "Бумажный дом" would be asked as nothing and "911 служба спасения" as "911"), only for a show that has one:

- **Sweeps and Search now:** after the title, alias and season queries, and only when episodes are still uncovered, the same way the anime absolute-number follow-up works. Scoped to the one season still missing something when it's one (a standard show), broad otherwise.
- **Interactive list and quick Grab buttons:** one more query, scoped like the main one (season and episode as tvsearch parameters for a standard show).
- **Text only**, never the show's TVDB/IMDb ids: an indexer that takes ids would answer the id search again and ignore the name.
- Through the per-host indexer throttle like every query; at most one per search.
- **Not** for upgrades (every 6 hours for every show: the cost isn't worth it; a release named so that the title query returns is still taken), RSS (no queries), or the request preview (no extra query; its identity check is the search's).
- Only releases that are the show count as matching in the search's outcome.

**Cost of the rule.** The sweep, the upgrade sweep and RSS build one `SearchFit` over the library they already listed. Anywhere else (Search now, the interactive list, the series page's download indicator, Wanted) it reads the library once per search, and only when a show's translated title names a release: a light read (`Repo.searchLibrary`: id, title, year, TMDB id, type, extra, aliases — none of List's per-show statistics) that fails, and so falls back to `FitRelease`, when the aliases can't be read.

## Not changed

- The import gate, `MatchRelease`, `KnownAs` and the library-wide movie matching.
- No new UI: the setting's help text on Settings → Library says TV searches use it. A "N shows not read yet" line was considered: read-only facts can't go in GET /settings (its body is PUT back whole), and a new endpoint for it wasn't worth it next to the log line.

A torrent added to the client by hand is told apart by the same rule in the sweeps' in-flight check and on the Wanted page, as the episode download indicator shows it.

## Display titles (GitHub #27)

A separate, later feature that reads the same TMDB names (`TMDB.Titles`, one cached request
per title) for a different purpose: which name the app SHOWS. Settings → Library → Display
titles (`display_title_mode` english/original/language, `display_title_language`,
`display_title_original_in`) plus a per-title override (`title_override` column, migration
0333). The names it needs — the original title in every script, the original language and
the translations in the chosen language — are stored as `extra_json.display_names` by add,
refresh and the same throttled titles backfill; the shown title is worked out from the row
and stored in `display_title` ('' = the canonical title), so lists, the calendar and alerts
read it without TMDB. It never feeds matching or naming: `TitleKeys`, `FitRelease`,
`MatchRelease`, the importer and renames keep `title`. Code: `internal/displaytitle`,
`movies/display.go`, `series/display.go`, `httpapi/displaytitles.go`.

## Checklist

- [x] `series.SearchFit` (`Fit`, `QueryTitle`; lazy library read, fails closed) with tests: setting empty is `FitRelease` for every show and release; German release taken; another show's title, a shared title, a library show's own name refused; year and country; short words; languages not set; library not fully read.
- [x] Wired into the sweep, Search now, upgrades, interactive search, quick Grab buttons, request preview and RSS; the episode download indicator.
- [x] Extra query: once, only with translated titles and something left to find (automation tests count the queries).
- [x] RSS can't give a release to the wrong show (unit and end-to-end).
- [x] Settings help text, user docs.
- [x] Review fixes, each tested: evidence beyond the name (Fantasmas), letters not bytes, Latin-only query titles, the light library read and one rule per sweep, the once-per-process log, matching counts, the in-flight check by the same rule.
