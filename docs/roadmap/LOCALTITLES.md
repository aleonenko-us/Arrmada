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
6. A name shorter than three letters never counts, whether a translated title or a release's title (with or without its country tag) (`minLocalKeyLen`).
7. **One short word.** A translated title that is a single word of four letters or fewer ("Haus", "Wolf") needs the release's year or country tag to agree. Such a word names too many shows the library has never heard of, and the extra query below would bring their releases in by the page. Longer one-word titles ("Geister") and anything of two words or more don't need it: rules 3–5 already cover what the library knows, and a release with neither year nor country is still taken by them.
8. **Only once every show is read.** Until every library show with a TMDB id has had its translated titles read for the languages set (after the setting changes, the backfill reads one show every two seconds; a failed read is retried at the next refresh), a show not yet read could share the name unseen, so the tier stays off.

With the setting empty `SearchFit` is `FitRelease`, exactly; the existing identity and golden tests are unchanged.

**RSS.** A feed release matched by a translated title is never also named by another show (rule 3) and never shared with one (rule 4), so it goes to one show or none. The grab step judges by the same rule over the same library snapshot.

**Imports of what a search took.** The import's tier accepts everything this one does (it is looser), and a grab is also confirmed by `KnownAs` from the stored titles.

## TV searches: the extra query

One text query by the translated title (`SearchFit.QueryTitle`: the first title, in the setting's order, that rules 3, 4 and 7 allow), only for a show that has one:

- **Sweeps and Search now:** after the title, alias and season queries, and only when episodes are still uncovered, the same way the anime absolute-number follow-up works. Scoped to the one season still missing something when it's one (a standard show), broad otherwise.
- **Interactive list and quick Grab buttons:** one more query, scoped like the main one (season and episode as tvsearch parameters for a standard show).
- **Text only**, never the show's TVDB/IMDb ids: an indexer that takes ids would answer the id search again and ignore the name.
- Through the per-host indexer throttle like every query; at most one per search.
- **Not** for upgrades (every 6 hours for every show: the cost isn't worth it; a release named so that the title query returns is still taken), RSS (no queries), or the request preview (no extra query; its identity check is the search's).

## Not changed

- The import gate, `MatchRelease`, `KnownAs` and the library-wide movie matching.
- A torrent added to the client by hand is still recognised as a show's only by its own titles and aliases (the in-flight check). Arrmada's own grabs are recognised by their record.
- No new UI: the setting's help text on Settings → Library says TV searches use it.

## Checklist

- [x] `series.SearchFit` (`Fit`, `QueryTitle`; lazy library read, fails closed) with tests: setting empty is `FitRelease` for every show and release; German release taken; another show's title, a shared title, a library show's own name refused; year and country; short words; languages not set; library not fully read.
- [x] Wired into the sweep, Search now, upgrades, interactive search, quick Grab buttons, request preview and RSS; the episode download indicator.
- [x] Extra query: once, only with translated titles and something left to find (automation tests count the queries).
- [x] RSS can't give a release to the wrong show (unit and end-to-end).
- [x] Settings help text, user docs.
