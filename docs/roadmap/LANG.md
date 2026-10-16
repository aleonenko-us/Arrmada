# Audio languages: badges on files and language rules in profiles

Status: built on `feat/audio-languages` (2026-10-16), from `main` @ dcf987f1. No migration.

Inspired by GitHub issue #12, which asked for two things: show the audio languages of the files on disk as badges on the movie and series pages, and colour them by whether the file matches the profile's languages. Profiles had no language settings, so the second half is new: an **Audio language** row in the quality profile's target, with the same choices as the codec, HDR and audio rows.

## Part 1 — what the files carry

- **Data source: Convert's library index.** `convert_library` already holds an analysis of every movie and episode file (`IndexAll` at startup and nightly, `IndexMovie`/`IndexSeries` after every import, rename and conversion), including every audio stream's language tag. The badges read it; a page load never probes a file. So coverage is library-wide already, through an existing throttled background pass (the tool runner's idle priority, one file at a time), and no new scanner was needed. A file not analysed yet — or analysed by an older analysis version, or replaced since (its size no longer matches) — shows no badges until the index reaches it.
- **Untagged tracks.** A track tagged `und` or nothing takes the language the subtitle AI heard on it when it has listened (the subtitles probe cache's `detected`, with the same confidence threshold the AI rung uses), read by `subtitles.DetectedAudio` from the cache alone and wired into Convert in `main` (Convert must not import subtitles). It is marked as detected (a dashed chip, "detected by the AI" in its tooltip). Otherwise it is one `?` chip.
- **Commentary tracks** are left out: an English commentary on a German dub isn't English audio.
- **Codes and order:** the one language table (`internal/langs`) folds every spelling (ger/deu, fre/fra, pt-BR…) to the base ISO 639-1 code, shown upper-case, with the English name in the tooltip and accessible name. One chip per language (a tag beats a detection), the title's original language first, then by code, an unidentified track last (`langs.OrderAudio`).
- **Where:** each movie version's file panel, each episode card, and a per-season line ("DE 10/10 · EN 9/10", plus how many files miss the language rules).
- **API:** `GET /api/v1/movies/{id}` adds `audio_langs` and `audio_lang_fit` to each version; `GET /api/v1/series/{id}` to each episode. Both are omitted when there's nothing to show. List endpoints are unchanged.

## Part 2 — language rules in profiles

The target (`IdealFile.Language`) maps a base code, or `original` (the title's TMDB `original_language`), to Must / Prefer / Avoid. No entries = no rules = exactly the old behaviour.

**What a release carries, by its name** (`quality/lang.go releaseLangs`, from the parser's `Languages`/`Multi`):

- no language named → the original language (assumed, and the release row says so);
- MULTi / DUAL / German DL or ML → the original plus the languages it names; MULTi alone → the original plus others it doesn't name;
- a tagged name (GERMAN, TRUEFRENCH/VFF, iTALiAN, ITA.ENG…) → exactly those.

A title whose original language isn't known, with a release that names none, can't be judged: no rule applies to it, and an `original` entry is dropped for that title.

**A file on disk** is judged by its probed tracks (Convert's facts, AI-heard languages included), which win over its name; an untagged track is read the way the name reads; with nothing to read it by, the set is "open" (never all-avoided, never proof of a Must).

**The choices:**

| | Release on offer | File on disk |
|---|---|---|
| **Must** | refused unless its name carries the language (reject code `missing_language`, "Missing German audio — your profile requires it"). Every Must is needed: they're languages a file has together, not alternatives like codecs. | doesn't fit — like any other Must, the upgrade sweep replaces it (v2: under every trigger; classic: when the trigger counts a preferences gain) |
| **Prefer** | each preferred (or required) language it has counts on the **preferences** key (v2) / scores +50 like a preferred format (classic). Not waived for a thin bitrate: the language is the point. | missing one, the target isn't met, so upgrades keep looking (preferences gain) |
| **Avoid** | a release whose *every* language is avoided goes to the avoided tier. An extra audio track costs nothing, so dual audio with one avoided language isn't avoided. | leaving it is a preferences gain, like an avoided target format; never upgraded *into* one |

**Badge colour on the detail pages** (`StoredProfile.LanguageFit`, the same reading as the engine): green — every Must, and a Prefer when any are set; orange — every Must but none of the preferred; red — a Must is missing, or every language is avoided; no colour without rules. A ✓ / ! / ✕ mark and a sentence in the tooltip and accessible name go with the colour, so colour is never the only signal.

## Decisions

1. **One row in the target, not a separate setting.** It sits beside codec, HDR and audio, uses the same buttons, is saved in the same `ideal` JSON column (no migration), and appears in the profile summary line ("German audio required · original audio preferred").
2. **Both rankings.** Must and Avoid live in `Evaluate`, which both share; Prefer is one more count on v2's preferences key and the format score classic already sums. Implementing classic was cheap, and leaving the rollback without language rules would have made "roll back to v1" quietly change what's grabbed.
3. **Foreign-only audio defers to explicit rules.** When the row has a Must or Prefer language for the title, the Unwanted pack's foreign-only item is not applied: the profile has said which languages it wants (a German-preferring profile would otherwise see every GERMAN release avoided as foreign). With only Avoid entries it still applies, but a release both would avoid is named once, by the language rule — no double counting (the tier is one tier anyway). The editor says so under the item.
4. **No foreign-only judgement on files, still.** The title's original language reaches files on disk and the import gate for the language rules only (`Candidate.ruleLang`), never `OrigLanguage`, so the foreign-only check keeps never judging a library file — otherwise profiles without language rules would have changed.
5. **Fit bars stay picture-and-sound.** `CheckFit` (the library Fit bars, Cutoff unmet) doesn't take languages; their verdict is the badges' colour. A profile with only language rules gets no Fit bars, as before it had any target (`IdealFile.FitEmpty`).
6. **Budgets.** Adding a rule to a big profile makes many files upgradeable at once; the existing per-sweep upgrade budget applies to them like any other upgrade (tested), and the save dialog's dry run counts them first.
7. **Parser extensions, conservative:** "German.DL" where an episode title sits (only when DL/ML follows), PLDUB/DUBPL, and a few more capitalised three-letter codes; everyday words (FIN, NOR, DAN) stay words.

## Checklist

- [x] Parser: more language tags, with tests (German.DL in TV names, PLDUB, POL/POR/DUT/CZE/HUN/TUR/UKR/CHI/HIN/SWE/HEB).
- [x] `langs.AudioLang` / `OrderAudio` / `AudioFit` (normalisation tests).
- [x] `IdealFile.Language`: read/normalised/compiled, summary line, book/music profiles never apply it.
- [x] Engine: Must reject (`missing_language`), Prefer on the preferences key and classic's score, Avoid tier; v1 and v2; `describe` lifts the language Must for not-fitting files; escape from an avoided language.
- [x] Foreign-only defers to Must/Prefer rules; not named twice beside a language Avoid.
- [x] Files on disk: Convert facts carry track languages (AI-heard for untagged), probed wins over the name; `CurrentFile.OrigLanguage` from the movie/show; import gate; save dry run (`ImpactFile.OrigLanguage`, `LibraryEpisodeFile.OrigLanguage`).
- [x] Goldens: ranking v1/v2 and upgrade goldens unchanged; new `ranking_lang_golden.json` for both rankings.
- [x] Per-sweep budget respected with a language rule (automation test).
- [x] Detail API: `audio_langs` / `audio_lang_fit` on movie versions and episodes (API shape tests).
- [x] Interactive search rows show the languages a name carries; refused rows say which language is missing.
- [x] UI: badges on movie files, episode cards and the season line; the Audio language row in the profile editor (searchable list + Original language); the foreign-only note.
- [x] e2e: series and movie detail badges, the editor row, release rows; contrast for the badges and the row.
- [x] User docs (docs/features.md).

## Not done here / follow-ups

- Badges don't appear on list pages (by design: list endpoints stay light).
- The library Fit bars and Cutoff unmet don't list language misses; the badges' colour is that verdict. Adding language issues to `CheckFit` would need the title's original language in every Fit read.
- The AI only listens to an untagged track when a subtitle needs it, so many untagged tracks stay `?`. A library-wide "identify untagged audio" pass would cost whisper time on every such file; it's left to the owner to ask for.
- An "Original language" entry does nothing for titles without a TMDB original language (rare).
- Regional variants (pt-BR vs pt-PT audio) are treated as their base language: release names and track tags rarely say which.
