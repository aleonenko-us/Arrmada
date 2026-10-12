package subtitles

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// LangStatus is one kept-language's coverage for a file: whether an external SRT exists, and if
// not, the first source the ladder will try and what it falls back to.
type LangStatus struct {
	Lang string `json:"lang"`
	// Variant is "" for the full subtitle (an SDH one counts) and "forced" for a profile's
	// forced want, which only a forced sidecar meets.
	Variant string `json:"variant,omitempty"`
	Have    bool   `json:"have"`             // an external .srt for this language sits next to the file
	Source  string `json:"source,omitempty"` // when !Have: extract | ocr | download | ai
	// Fallback is "ai" when the first source is extract or download and the local AI
	// could still make this language if that source comes up empty.
	Fallback string `json:"fallback,omitempty"`
	// Orphan is set when the language is missing but a subtitle in the folder that pairs
	// with no video (an old name, most likely) covers it. Plex won't show it for this file.
	Orphan bool `json:"orphan,omitempty"`
	// Reason is set when the language is missing and the sweep is leaving it alone for
	// now (backoff.go): the most actionable outcome of the last run. NextRetryAt is when
	// it comes up again (unix seconds; 0 = only RetryOn's events or a button press), and
	// RetryOn the events that bring it back early (CSV of creds | model | langs).
	Reason      string `json:"reason,omitempty"`
	NextRetryAt int64  `json:"next_retry_at,omitempty"`
	RetryOn     string `json:"retry_on,omitempty"`
	// Health is a present language's sync Health (health.go); nil while missing.
	Health *LangHealth `json:"health,omitempty"`
	// File is a present language's sidecar name, so a redo can say which file it replaces.
	File string `json:"file,omitempty"`
}

// sidecarNameFor is the name of the paired sidecar that covers lang in a scan: tagged in
// that language (full before SDH), or the untagged one when untagged (the first kept
// language).
func sidecarNameFor(sc sidecarScan, lang string, untagged bool) string {
	best := ""
	for i, lv := range sc.Variants {
		if i >= len(sc.Names) || !coversFull(lv.Variant) {
			continue
		}
		if (lv.Lang != "" && langMatches(lv.Lang, lang)) || (lv.Lang == "" && untagged) {
			if best == "" || lv.Variant == VariantFull {
				best = sc.Names[i]
			}
		}
	}
	return best
}

// SubHealth is a file's Health: its worst present language's (health.go).
type SubHealth struct {
	State string   `json:"state"`           // HealthOK | HealthUncertain | HealthBad | HealthUnknown | HealthUnchecked
	Score int      `json:"score,omitempty"` // that language's sync confidence, when checked
	Notes []string `json:"notes,omitempty"`
}

// FileSubs is one media file's subtitle picture for the Library tab.
type FileSubs struct {
	Kind        string       `json:"kind"` // "movie" | "episode"
	MovieID     int64        `json:"movie_id,omitempty"`
	SeriesID    int64        `json:"series_id,omitempty"`
	Season      int          `json:"season,omitempty"`
	Episode     int          `json:"episode,omitempty"`
	Title       string       `json:"title"`
	Year        int          `json:"year,omitempty"`
	PosterURL   string       `json:"poster_url,omitempty"`
	Path        string       `json:"path"`
	DurationSec float64      `json:"duration_sec,omitempty"`
	AudioLangs  []string     `json:"audio_langs,omitempty"`
	Embedded    []SubTrack   `json:"embedded"`          // embedded subtitle tracks (for badges/filters)
	External    []string     `json:"external"`          // kept languages that already have a sidecar
	Languages   []LangStatus `json:"languages"`         // per-kept-language coverage + best source
	Health      *SubHealth   `json:"health,omitempty"`  // the worst present language's Health; nil = none present
	Missing     int          `json:"missing"`           // count of wanted subtitles still without an SRT
	Orphans     []Orphan     `json:"orphans,omitempty"` // movies: subtitles in the folder paired with no video
	// ProfileID is the language profile the title is pinned to (0 = the default profile).
	ProfileID int64 `json:"profile_id,omitempty"`
}

// ErrFlatTV: TV is never listed flat, one probed row per episode (tens of thousands of
// them); it's per show (SeriesGroups) or one show's episodes (SeriesEpisodes).
var ErrFlatTV = errors.New("TV isn't listed flat: use group=series or series=<id>")

// Library probes every downloaded movie (media="movies") and returns its subtitle coverage
// against the kept languages. Cached probes keep it fast after the first scan.
func (s *Service) Library(ctx context.Context, media string) ([]FileSubs, error) {
	if media != "movies" {
		return nil, ErrFlatTV
	}
	cfg := s.profileCfg(ctx) // read once for the whole pass
	canDownload := s.provider != nil && s.provider.CanDownload()
	backoffs, now := s.loadBackoffs(ctx), time.Now() // one query for the whole pass
	health := s.liveFilesByKey(ctx)                  // and one for Health
	var out []FileSubs

	list, err := s.movies.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		if !m.HasFile || m.MovieFilePath == "" {
			continue
		}
		fs := FileSubs{
			Kind: "movie", MovieID: m.ID, Title: m.Title, Year: m.Year,
			PosterURL: m.PosterURL, Path: m.MovieFilePath,
		}
		p, pinned := cfg.forTitle("movie", m.ID, 0)
		if pinned {
			fs.ProfileID = p.ID
		}
		s.fillCoverageW(ctx, &fs, p.wants(), canDownload)
		applyBackoff(&fs, backoffs, now)
		applyHealth(&fs, health[mediaKeyOf(&fs)])
		out = append(out, fs)
	}
	return out, nil
}

// fillCoverage is fillCoverageW for a flat language list (the full subtitle of each).
func (s *Service) fillCoverage(ctx context.Context, fs *FileSubs, langs []string, canDownload bool) {
	s.fillCoverageW(ctx, fs, plainWants(langs), canDownload)
}

// fillCoverageW probes the file and computes its coverage against a title's wants. Best-effort:
// a file that can't be probed still reports its sidecar coverage (embedded tracks just come
// back empty). A full want the file's audio makes unnecessary (OnlyIfAudioDiffers) isn't
// listed and isn't missing.
func (s *Service) fillCoverageW(ctx context.Context, fs *FileSubs, wants []Want, canDownload bool) {
	unknownAudio := false // an untagged track whisper hasn't listened to yet
	mi, err := s.probeFile(ctx, fs.Path)
	if err == nil && mi != nil {
		fs.DurationSec = mi.DurationSec
		fs.AudioLangs = mi.withDetected().AudioLangs // tags, plus what detection heard
		fs.Embedded = mi.Subs
		for _, t := range mi.Audio {
			if _, heard := mi.Detected[t.Index]; t.untagged() && !heard {
				unknownAudio = true
			}
		}
	} else {
		mi = nil
	}
	if fs.Embedded == nil {
		fs.Embedded = []SubTrack{} // never nil → JSON emits [] not null (frontend iterates it)
	}
	cov := coverageOf(fs.Path, kindOf(fs.Kind), wants, mi)
	fs.Orphans = cov.sc.Orphans
	fs.External = nonNil(cov.sc.Present) // kept languages with a paired full sidecar already
	first := ""                          // the language an untagged "<base>.srt" counts for
	if fulls := wantLangs(wants, VariantFull); len(fulls) > 0 {
		first = fulls[0]
	}
	fs.Languages = make([]LangStatus, 0, len(cov.active))
	for _, w := range cov.active {
		l := w.Lang
		if cov.have[w.key()] {
			ls := LangStatus{Lang: l, Variant: w.Variant, Have: true, File: sidecarNameFor(cov.sc, l, l == first)}
			if w.Variant == VariantForced {
				ls.File = forcedSidecarName(cov.sc, l)
			}
			fs.Languages = append(fs.Languages, ls)
			continue
		}
		fs.Missing++
		if w.Variant == VariantForced {
			// Only a forced track or a foreign-parts upload makes one; whisper can't.
			ls := LangStatus{Lang: l, Variant: VariantForced}
			if _, ok := pickForcedTrack(fs.Embedded, l); ok {
				ls.Source = "extract"
			} else if canDownload {
				ls.Source = "download"
			}
			fs.Languages = append(fs.Languages, ls)
			continue
		}
		ls := LangStatus{Lang: l, Have: false, Source: bestSourcePref(fs.Embedded, l, w.SDH, canDownload), Orphan: cov.orphaned[strings.ToLower(l)]}
		if (ls.Source == "extract" || ls.Source == "download") && s.aiCanMake(fs.AudioLangs, unknownAudio, l) {
			ls.Fallback = "ai"
		}
		fs.Languages = append(fs.Languages, ls)
	}
}

// forcedSidecarName is the paired forced sidecar for a language, "" when there is none.
func forcedSidecarName(sc sidecarScan, lang string) string {
	for i, lv := range sc.Variants {
		if i < len(sc.Names) && lv.Variant == VariantForced && lv.Lang != "" && langMatches(lv.Lang, lang) {
			return sc.Names[i]
		}
	}
	return ""
}

// applyBackoff marks the missing languages the sweep is leaving alone for now, from the
// backoff rows a pass loaded once (a nil set marks nothing).
func applyBackoff(fs *FileSubs, bs Backoffs, now time.Time) {
	if len(bs) == 0 {
		return
	}
	key := fmt.Sprintf("m:%d", fs.MovieID)
	if fs.Kind == "episode" {
		key = fmt.Sprintf("e:%d:%d:%d", fs.SeriesID, fs.Season, fs.Episode)
	}
	for i := range fs.Languages {
		ls := &fs.Languages[i]
		if ls.Have {
			continue
		}
		b, ok := bs[BackoffKey(key, ls.Lang, ls.Variant)]
		if !ok || !b.Blocked(fs.Path, now) {
			continue
		}
		ls.Reason, ls.NextRetryAt, ls.RetryOn = string(b.PrimaryReason), b.NextRetryAt, strings.Join(b.ClearOn, ",")
	}
}

// loadBackoffs reads every backoff row for a library pass; an error just means the
// chips don't show next-try dates this time.
func (s *Service) loadBackoffs(ctx context.Context) Backoffs {
	bs, err := s.ledger.LoadBackoffs(ctx)
	if err != nil {
		s.log.Warn("subtitles: could not read backoff for the library", "err", err)
	}
	return bs
}

// aiCanMake reports whether the local AI could produce lang from audio in audioLangs:
// a model is installed, whisper can go in that direction, and the model for it is there.
// While an untagged track hasn't been listened to (unknownAudio), it may well be in that
// language — the AI rung detects first — so any model will do.
func (s *Service) aiCanMake(audioLangs []string, unknownAudio bool, lang string) bool {
	ai := s.aiGen()
	if !ai.available() {
		return false
	}
	if unknownAudio && ai.canRun(false) {
		return true
	}
	plan := aiPlan(audioLangs, lang)
	return plan != "" && ai.canRun(plan == "translate")
}

// bestSource names the FIRST rung the ladder in process() will try for a missing language: an
// embedded text track (extract), else a provider download when one is configured, else AI.
// It is only the starting point — a rung that comes up empty falls through to the next one, so
// "download" means "download, then AI" (LangStatus.Fallback says whether AI can actually act).
func bestSource(embedded []SubTrack, lang string, canDownload bool) string {
	return bestSourcePref(embedded, lang, SDHAny, canDownload)
}

// bestSourcePref is bestSource for a want with an SDH preference: one that avoids SDH
// doesn't count an SDH track as something to extract.
func bestSourcePref(embedded []SubTrack, lang, sdh string, canDownload bool) string {
	if _, ok := pickFullTrackPref(embedded, lang, sdh); ok {
		return "extract"
	}
	// An embedded IMAGE track (PGS/VobSub) would be the next-best source — but OCR isn't
	// implemented, and routing to it sent every PGS-only file to "pending" without ever
	// trying a download. That is precisely the file that most needs a text subtitle: an
	// image track is what makes Plex burn subtitles in and transcode the video. So fall
	// through to the sources that exist. When OCR ships it slots in here.
	if canDownload {
		return "download"
	}
	return "ai"
}

// pickFullTrack chooses the embedded text track to extract as a language's full subtitle:
// a plain track first, then an SDH one (full dialogue plus sound cues), with the track
// flagged default winning a tie and stream order after that. A forced track is never
// picked — it only carries the foreign-language lines — so a language whose only text
// track is forced falls through to download or AI.
func pickFullTrack(subs []SubTrack, lang string) (SubTrack, bool) {
	return pickFullTrackPref(subs, lang, SDHAny)
}

// pickFullTrackPref is pickFullTrack under a profile's SDH preference: SDHPrefer puts an
// SDH track ahead of a plain one, and SDHAvoid never picks one.
func pickFullTrackPref(subs []SubTrack, lang, sdh string) (SubTrack, bool) {
	rank := func(t SubTrack) int {
		r := 0
		if t.SDH != (sdh == SDHPrefer) {
			r += 2
		}
		if !t.Default {
			r++
		}
		return r
	}
	ok := func(t SubTrack) bool { return t.Text && !t.Forced && !(sdh == SDHAvoid && t.SDH) }
	return pickTrack(subs, lang, ok, rank)
}

// pickForcedTrack is the first forced text track in a language, if any.
func pickForcedTrack(subs []SubTrack, lang string) (SubTrack, bool) {
	return pickTrack(subs, lang, func(t SubTrack) bool { return t.Text && t.Forced }, func(SubTrack) int { return 0 })
}

// pickTrack returns the lowest-ranked track that passes ok and is in lang (stream order
// breaks ties). A regional want ("pt-br") takes a track tagged with just its base
// language ("por") only when no track carries the region: muxers rarely write regions,
// but a track explicitly tagged for another region ("pt-pt") is never taken.
func pickTrack(subs []SubTrack, lang string, ok func(SubTrack) bool, rank func(SubTrack) int) (SubTrack, bool) {
	pick := func(match func(string) bool) (SubTrack, bool) {
		best, found := SubTrack{}, false
		for _, t := range subs {
			if !ok(t) || !match(t.Lang) {
				continue
			}
			if !found || rank(t) < rank(best) {
				best, found = t, true
			}
		}
		return best, found
	}
	if t, found := pick(func(tag string) bool { return langMatches(tag, lang) }); found {
		return t, true
	}
	if base := langs.Base(lang); base != langs.Canonical(lang) {
		return pick(func(tag string) bool { return langs.Canonical(tag) == base })
	}
	return SubTrack{}, false
}

// langMatches reports whether a track's language tag matches a wanted code in any spelling
// (en ≡ eng ≡ english; a base want matches a regional track). An untagged track never
// matches a specific wanted language. See langs.Match.
func langMatches(track, wanted string) bool { return langs.Match(track, wanted) }

// SeriesGroup is one show's subtitle coverage, rolled up.
//
// A flat list of every episode is unusable at library scale — 23,000 rows, each of
// which has to be probed before the page can render. Showing shows first means one row
// per series and probing only the show you open.
type SeriesGroup struct {
	SeriesID  int64  `json:"series_id"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	Episodes  int    `json:"episodes"` // episodes with a file on disk
	Missing   int    `json:"missing"`  // episodes still missing at least one kept language
	Covered   int    `json:"covered"`  // episodes with every wanted subtitle present
	Seasons   int    `json:"seasons"`
	ProfileID int64  `json:"profile_id,omitempty"` // the language profile the show is pinned to (0 = default)
}

// SeriesGroups returns one row per show with a file on disk, for the Library list.
//
// Coverage here is counted from the SIDECARS on disk, deliberately without probing the
// video files. Probing is what makes the flat list slow, and the roll-up only needs to
// answer "does this show need attention?" — the per-episode view probes properly once
// you open one show.
func (s *Service) SeriesGroups(ctx context.Context) ([]SeriesGroup, error) {
	if s.series == nil {
		return []SeriesGroup{}, nil
	}
	cfg := s.profileCfg(ctx) // read once for the whole pass
	list, err := s.series.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SeriesGroup, 0, len(list))
	for _, sm := range list {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		g, ok := s.groupFor(ctx, sm.ID, cfg)
		if !ok {
			continue
		}
		out = append(out, g)
	}
	// Shows needing the most work first: that's what the page is for.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Missing != out[j].Missing {
			return out[i].Missing > out[j].Missing
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

// SeriesEpisodes returns the full per-episode coverage for ONE show — the expanded view.
// Probing is bounded to that show, which is what makes it affordable.
func (s *Service) SeriesEpisodes(ctx context.Context, seriesID int64) ([]FileSubs, error) {
	if s.series == nil {
		return []FileSubs{}, nil
	}
	p, pinned := s.profileCfg(ctx).forTitle("episode", 0, seriesID)
	wants := p.wants()
	canDownload := s.provider != nil && s.provider.CanDownload()
	backoffs, now := s.loadBackoffs(ctx), time.Now() // one query for the whole pass
	full, err := s.series.Get(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	out := []FileSubs{}
	for _, sn := range full.Seasons {
		for _, e := range sn.Episodes {
			if !e.HasFile || e.FilePath == "" {
				continue
			}
			fs := FileSubs{
				Kind: "episode", SeriesID: full.ID, Season: e.SeasonNumber, Episode: e.EpisodeNumber,
				Title: fmt.Sprintf("%s - S%02dE%02d", full.Title, e.SeasonNumber, e.EpisodeNumber),
				Year:  full.Year, PosterURL: full.PosterURL, Path: e.FilePath,
			}
			if pinned {
				fs.ProfileID = p.ID
			}
			s.fillCoverageW(ctx, &fs, wants, canDownload)
			applyBackoff(&fs, backoffs, now)
			s.fillHealth(ctx, &fs)
			out = append(out, fs)
		}
	}
	return out, nil
}
