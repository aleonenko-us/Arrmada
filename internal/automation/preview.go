package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Release previews: the release a search would take for a title, worked out without taking
// it. A request waiting for approval is previewed so whoever approves it can see what
// they'd get — or that nothing is out yet.
//
// A preview is a dry run in everything that matters to the title:
//   - nothing is grabbed: no download, no grab or acquisition row, no history line;
//   - no search_attempts row is stored, so "last searched", the empty-search count, the
//     Wanted view and search.finished never hear of it (most previewed titles aren't in
//     the library yet, and the request keeps the result instead);
//   - the missing-sweep's backoff (misses, last search) is never touched;
//   - it is background work: an indexer backing off after failures is left alone, as the
//     sweeps leave it, and a title being searched for real right now isn't previewed.
//
// Indexer health still sees the queries: an indexer that fails a preview is failing.

// PreviewInput says what to preview.
type PreviewInput struct {
	MediaType string // movie | series | book
	TMDBID    int    // movies and shows
	// Seasons are the seasons a series request asks for; none is the whole show.
	Seasons []int
	// Books: the library row when there is one, else the title and author asked for.
	BookID        int64
	Title, Author string
	// Profile is the quality profile to rank under ("" = the media type's default).
	Profile string
	// Key names what the preview is for ("request:12"). Its release tokens are good only
	// for that key, and only through GrabPreviewed.
	Key string
}

// Preview outcomes.
const (
	PreviewFound       = "found"       // a search would grab these releases
	PreviewNone        = "none"        // the indexers answered, and nothing would be grabbed
	PreviewUnavailable = "unavailable" // no indexer could answer: all paused, failing, or none set up
	PreviewBusy        = "busy"        // being searched or downloaded for real; look again later
	PreviewNotWanted   = "not_wanted"  // nothing to look for: it's on disk, or nothing has aired
)

// Preview is what a search would do for a title, said for staff.
type Preview struct {
	Outcome string `json:"outcome"`
	// Reason is the SearchOutcome reason code behind the outcome (ReasonGrabbed when
	// something would be taken).
	Reason  string `json:"reason"`
	Profile string `json:"profile"` // the profile it was ranked under, as resolved
	// Releases are what would be grabbed, best first (for a show, those for Season).
	Releases []PreviewRelease `json:"releases"`
	// Why the first release won, and what it beat — the interactive search's explanation.
	Why        []string `json:"why,omitempty"`
	ChosenOver string   `json:"chosen_over,omitempty"`
	// What the search saw: releases returned, for this title, and fit to take.
	Returned int `json:"returned"`
	Matching int `json:"matching"`
	Eligible int `json:"eligible"`
	// TopReasons are the commonest reasons releases were turned down, most first.
	TopReasons    []PreviewReason `json:"top_reasons,omitempty"`
	IndexerIssues []IndexerIssue  `json:"indexer_issues,omitempty"`
	Searched      int             `json:"searched"` // indexers asked by the widest query
	Queries       int             `json:"queries"`  // searches sent, each to every indexer serving it
	// Series: the season the releases are for, and a line about the rest ("2 more seasons
	// not checked").
	Season      int    `json:"season,omitempty"`
	SeasonsNote string `json:"seasons_note,omitempty"`
	// Tokens stand for Releases' download links (releasetokens.go), for GrabPreviewed.
	// Never serialised.
	Tokens []string `json:"-"`
}

// PreviewRelease is one release a preview would grab: what it is, never where it is.
type PreviewRelease struct {
	Title      string   `json:"title"`
	Indexer    string   `json:"indexer"`
	Summary    string   `json:"summary"` // "1080p · WEB-DL"
	Resolution string   `json:"resolution,omitempty"`
	Source     string   `json:"source,omitempty"`
	Codec      string   `json:"codec,omitempty"`
	HDR        []string `json:"hdr,omitempty"`
	SizeGB     float64  `json:"size_gb"`
	Seeders    int      `json:"seeders"`
	Transport  string   `json:"transport,omitempty"` // torrent | usenet
	// Kind is what the release is: a series' "complete series", "pack" or "episode"; a
	// book's "ebook" or "audiobook".
	Kind    string   `json:"kind,omitempty"`
	Covers  string   `json:"covers,omitempty"` // series: "S01", "S01E03"
	Format  string   `json:"format,omitempty"` // books: EPUB, M4B…
	Matched []string `json:"matched,omitempty"`
	Avoided []string `json:"avoided,omitempty"`
}

// PreviewReason is one reason releases were turned down, with its label and count.
type PreviewReason struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// maxPreviewReleases bounds the releases one preview lists and holds tokens for: a season
// found only as single episodes can be twenty of them.
const maxPreviewReleases = 30

// errPreviewUnsupported is a preview of a kind of title this build can't search for.
var errPreviewUnsupported = errors.New("nothing to preview this with")

// PreviewTitle works out what a search would grab for in, without grabbing it (see the
// top of this file). A title in the library is previewed as it is there (what it already
// has isn't looked for again); one that isn't is previewed as approving it would add it.
//
// What it costs the indexers, per preview — each search goes to every enabled indexer
// that serves the kind, through the per-host throttle:
//   - a movie: one search (by id where the indexer takes ids), plus one by its original
//     title only when nothing in the first was for the film — what a real search sends;
//   - a show: two — the broad title search (multi-season and complete packs) and one for
//     the first season wanted (none for anime, whose numbering the broad search covers);
//   - a book: one per edition wanted (ebook, audiobook), plus a title-only retry for an
//     edition the author + title search found nothing for.
func (c *Coordinator) PreviewTitle(ctx context.Context, in PreviewInput) (Preview, error) {
	// Background work, whatever the caller's context says: a preview must never wake an
	// indexer the sweeps are leaving alone.
	ctx = indexer.WithoutInteractive(ctx)
	switch in.MediaType {
	case ReleaseKindMovie:
		return c.previewMovie(ctx, in)
	case ReleaseKindSeries:
		return c.previewSeries(ctx, in)
	case ReleaseKindBook:
		return c.previewBook(ctx, in)
	}
	return Preview{}, fmt.Errorf("%w: %q", errPreviewUnsupported, in.MediaType)
}

// previewSearchErr is the preview of a search that couldn't run: every indexer paused,
// failing or none serving it is "unavailable"; anything else is an error.
func previewSearchErr(p Preview, notes *searchNotes, err error) (Preview, error) {
	var out SearchOutcome
	out.noteSearchErr(err)
	if out.Reason == "" {
		return p, err
	}
	p.Outcome, p.Reason = PreviewUnavailable, out.Reason
	p.IndexerIssues, p.Searched, p.Queries = notes.issues(), notes.searched(), notes.queryCount()
	return p, nil
}

// finishPreview fills in what the search saw from its collector, and the outcome from
// the releases found.
func finishPreview(p *Preview, notes *searchNotes) {
	var out SearchOutcome
	out.absorb(notes)
	p.Returned, p.Eligible = out.Returned, out.Eligible
	if p.Matching == 0 {
		p.Matching = max(0, out.Returned-out.WrongTitle)
	}
	p.TopReasons = topReasons(out.Reasons, 3)
	p.IndexerIssues, p.Searched, p.Queries = notes.issues(), notes.searched(), notes.queryCount()
	switch {
	case len(p.Releases) > 0:
		p.Outcome, p.Reason = PreviewFound, ReasonGrabbed
	default:
		p.Outcome = PreviewNone
		out.Searched = true
		out.settle()
		p.Reason = out.Reason
	}
}

// topReasons is the n commonest reject codes, most first, with their words.
func topReasons(reasons map[string]int, n int) []PreviewReason {
	out := make([]PreviewReason, 0, len(reasons))
	for code, count := range reasons {
		out = append(out, PreviewReason{Code: code, Label: ReasonLabel(code), Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// previewRelease is one release's display facts.
func previewRelease(rel indexer.Release, p parser.Release, summary string) PreviewRelease {
	pr := PreviewRelease{
		Title: rel.Title, Indexer: rel.Indexer, Summary: summary,
		Resolution: string(p.Resolution), Source: string(p.Source), Codec: string(p.Codec), HDR: p.HDR,
		SizeGB: rel.SizeGB(), Seeders: rel.Seeders, Transport: string(rel.Transport),
	}
	if pr.Transport == "" {
		pr.Transport = string(indexer.TransportTorrent)
	}
	return pr
}

// issuePreview gives each release a preview token for key, in order. editions, for books,
// says which edition each release is.
func (c *Coordinator) issuePreview(p *Preview, rels []indexer.Release, kind, key string, editions []string) {
	for i, rel := range rels {
		ref := ReleaseRef{
			Indexer: rel.Indexer, DownloadURL: rel.DownloadURL, Title: rel.Title, InfoHash: rel.InfoHash,
			Transport: string(rel.Transport), MediaKind: kind, PreviewKey: key, SizeGB: rel.SizeGB(),
		}
		if kind == ReleaseKindSeries {
			ref.Season = -1 // a whole-show grab, as the sweep makes it
		}
		if i < len(editions) {
			ref.Edition = editions[i]
		}
		p.Tokens = append(p.Tokens, c.previewTokens().issue(ref))
	}
}

// ---- movies ----------------------------------------------------------------

func (c *Coordinator) previewMovie(ctx context.Context, in PreviewInput) (Preview, error) {
	if c.movies == nil {
		return Preview{}, errPreviewUnsupported
	}
	m, err := c.movies.GetByTMDB(ctx, in.TMDBID)
	switch {
	case err == nil:
		if p, done := c.movieHeld(ctx, m); done {
			return p, nil
		}
	case errors.Is(err, movies.ErrNotFound):
		if m, err = c.movies.Draft(ctx, in.TMDBID, in.Profile); err != nil {
			return Preview{}, err
		}
	default:
		return Preview{}, err
	}
	profile := c.effectiveProfile(ctx, in.Profile, quality.MediaMovie)
	p := Preview{Profile: profile, Releases: []PreviewRelease{}}
	ctx, notes := newSearchNotes(ctx)
	keys := c.movieKeys(ctx, m)
	result, err := c.searchMovie(ctx, m, keys)
	if err != nil {
		return previewSearchErr(p, notes, err)
	}
	notes.consider(result.Releases)
	matching := matchingMovieReleases(m, keys, result.Releases)
	notes.dropped(result.Releases, matching, DropWrongTitle)
	p.Matching = len(matching)
	// The same pool a search decides over: grabbable, deduped and not blocklisted (the
	// global blocklist applies to a film not in the library yet too).
	byName, cands, err := c.candidatesFrom(ctx, m.ID, matching)
	if err != nil {
		return Preview{}, err
	}
	if m.ID > 0 {
		pending, err := c.pendingGrabTitles(ctx, m.ID)
		if err != nil {
			return Preview{}, err
		}
		cands = dropPendingCands(notes, cands, pending)
	}
	decision := c.quality.Decide(ctx, profile, tagMovie(cands, m))
	notes.decided(decision)
	if w := decision.Winner; w != nil {
		rel := byName[w.Candidate.Name]
		pr := previewRelease(rel, w.Candidate.Release, summarize(w.Candidate.Release))
		pr.Matched, pr.Avoided = w.Matched, w.AvoidedFormats
		p.Releases = append(p.Releases, pr)
		p.Why, p.ChosenOver = decision.Why, decision.ChosenOver
		c.issuePreview(&p, []indexer.Release{rel}, ReleaseKindMovie, in.Key, nil)
	}
	finishPreview(&p, notes)
	return p, nil
}

// movieHeld is the preview of a library film there's nothing to search for: one being
// searched right now, one with something already downloading, one already on disk.
// done is false when it should be searched like any other.
func (c *Coordinator) movieHeld(ctx context.Context, m movies.Movie) (Preview, bool) {
	switch {
	case c.claims.claimed(movieKey(m.ID)):
		return Preview{Outcome: PreviewBusy, Reason: ReasonAlreadySearching, Releases: []PreviewRelease{}}, true
	case m.HasFile:
		return Preview{Outcome: PreviewNotWanted, Reason: ReasonNothingWanted, Releases: []PreviewRelease{}}, true
	}
	if acqs, err := c.Active(ctx, "movie", m.ID); err == nil && len(acqs) > 0 {
		return Preview{Outcome: PreviewBusy, Reason: ReasonAlreadyDownloading, Releases: []PreviewRelease{}}, true
	}
	return Preview{}, false
}

// dropPendingCands leaves out the candidates already grabbed and still in flight.
func dropPendingCands(notes *searchNotes, cands []quality.Candidate, pending map[string]bool) []quality.Candidate {
	if len(pending) == 0 {
		return cands
	}
	out := cands[:0]
	for _, cand := range cands {
		if pending[normTitle(cand.Name)] {
			notes.mark(cand.Name, DropPending)
			continue
		}
		out = append(out, cand)
	}
	return out
}

// ---- series ----------------------------------------------------------------

// previewPick is one release the dry-run plan took.
type previewPick struct{ name, label string }

func (c *Coordinator) previewSeries(ctx context.Context, in PreviewInput) (Preview, error) {
	if c.series == nil {
		return Preview{}, errPreviewUnsupported
	}
	var only map[int]bool
	if len(in.Seasons) > 0 {
		only = map[int]bool{}
		for _, n := range in.Seasons {
			only[n] = true
		}
	}
	s, err := c.previewShow(ctx, in.TMDBID, in.Profile, only)
	if err != nil {
		return Preview{}, err
	}
	held := Preview{Releases: []PreviewRelease{}}
	if s.ID > 0 && c.claims.claimed(seriesKey(s.ID)) {
		held.Outcome, held.Reason = PreviewBusy, ReasonAlreadySearching
		return held, nil
	}
	wanted, seriesSeasons := wantedEpisodes(s)
	if s.ID > 0 && len(wanted) > 0 {
		// What's already on its way is held back, as the Search button holds it back.
		inSeasons, whole, _, err := c.seriesInFlightFor(ctx, s, nil)
		if err != nil {
			return Preview{}, err
		}
		if whole {
			held.Outcome, held.Reason = PreviewBusy, ReasonAlreadyDownloading
			return held, nil
		}
		kept := wanted[:0]
		for _, k := range wanted {
			if !inSeasons[k.season] {
				kept = append(kept, k)
			}
		}
		if len(kept) == 0 {
			held.Outcome, held.Reason = PreviewBusy, ReasonAlreadyDownloading
			return held, nil
		}
		wanted = kept
	}
	if len(wanted) == 0 {
		// On disk already, or nothing it asks for has aired.
		held.Outcome, held.Reason = PreviewNotWanted, ReasonNothingWanted
		return held, nil
	}
	wantSeasons := sortedSeasons(seasonsOf(setOf(wanted)))
	first := wantSeasons[0]

	profile := c.effectiveProfile(ctx, in.Profile, quality.MediaSeries)
	p := Preview{Profile: profile, Season: first, Releases: []PreviewRelease{}}
	ctx, notes := newSearchNotes(ctx)
	title := indexerQuery(s.Title)
	res, err := c.search(ctx, seriesQuery(s, title, 100))
	if err != nil {
		return previewSearchErr(p, notes, err)
	}
	releases := res.Releases
	if !s.IsAnime() {
		releases = c.searchSeasons(ctx, s, title, []int{first}, releases)
	}
	notes.consider(releases)

	blocked, err := c.blockedSetSeries(ctx, s.ID)
	if err != nil {
		return Preview{}, err
	}
	pending := map[string]bool{}
	if s.ID > 0 {
		if pending, err = c.pendingSeriesGrabTitles(ctx, s.ID); err != nil {
			return Preview{}, err
		}
	}
	// The candidates a real search ranks (grabSeriesScoped): grabbable, not blocklisted,
	// and this show.
	byName := map[string]indexer.Release{}
	var cands []quality.Candidate
	rts := newRuntimeIndex(s)
	for _, rel := range c.networks(ctx).usable(ctx, releases) {
		if blocked[normTitle(rel.Title)] {
			notes.mark(rel.Title, DropBlocklisted)
			continue
		}
		if !seriesTitleMatches(rel.Title, s) {
			notes.mark(rel.Title, DropWrongTitle)
			continue
		}
		byName[rel.Title] = rel
		cands = append(cands, c.newSeriesCandidate(ctx, s, rts, rel))
	}
	p.Matching = len(cands)
	decision := c.quality.Decide(ctx, profile, cands)
	notes.decided(decision)

	// The plan a search would make over everything wanted, with every grab a dry run: the
	// season pack against single episodes is decided exactly as the sweep decides it.
	var picks []previewPick
	taken := map[string]bool{}
	planSeriesGrabs(planInput{
		eligible:      decision.Eligible,
		wanted:        wanted,
		seriesSeasons: seriesSeasons,
		counts:        seasonEpisodeCounts(s),
		ended:         showEnded(s.Status),
		cover: func(r parser.Release, needed map[epKey]bool) []epKey {
			return c.coveredByFor(ctx, s, r, needed)
		},
		try: func(name, label string) bool {
			rel, ok := byName[name]
			if !ok || rel.DownloadURL == "" {
				return false
			}
			if pending[normTitle(rel.Title)] {
				notes.mark(rel.Title, DropPending) // already on its way: covered, not taken again
				return true
			}
			if !taken[name] {
				taken[name] = true
				picks = append(picks, previewPick{name, label})
			}
			return true
		},
	})

	// The releases for the first season, and what they say about the rest.
	firstEps := map[epKey]bool{}
	for _, k := range wanted {
		if k.season == first {
			firstEps[k] = true
		}
	}
	coveredSeasons := map[int]bool{}
	var rels []indexer.Release
	for _, pk := range picks {
		cand := parser.Parse(pk.name)
		if len(c.coveredByFor(ctx, s, cand, firstEps)) == 0 {
			continue
		}
		if len(rels) >= maxPreviewReleases {
			break
		}
		rel := byName[pk.name]
		rels = append(rels, rel)
		pr := previewRelease(rel, cand, summarizeSeries(cand))
		pr.Kind, pr.Covers = previewSeriesKind(cand), previewCovers(cand)
		if ev := evaluationOf(decision, pk.name); ev != nil {
			pr.Matched, pr.Avoided = ev.Matched, ev.AvoidedFormats
		}
		p.Releases = append(p.Releases, pr)
		for _, sn := range wantSeasons {
			if cand.Kind() != parser.KindEpisode && cand.CoversSeason(sn) {
				coveredSeasons[sn] = true
			}
		}
	}
	if len(p.Releases) == 1 && decision.Winner != nil && decision.Winner.Candidate.Name == p.Releases[0].Title {
		p.Why, p.ChosenOver = decision.Why, decision.ChosenOver
	} else if len(p.Releases) > 0 {
		p.Why = []string{seriesPlanWhy(p.Releases)}
	}
	p.SeasonsNote = seasonsNote(wantSeasons, first, coveredSeasons)
	c.issuePreview(&p, rels, ReleaseKindSeries, in.Key, nil)
	finishPreview(&p, notes)
	return p, nil
}

// previewShow is the show as a search would see it once the request is approved: the
// library's row with the requested seasons wanted, or a draft from the metadata provider.
func (c *Coordinator) previewShow(ctx context.Context, tmdbID int, profile string, only map[int]bool) (series.Series, error) {
	lib, err := c.series.GetByTMDB(ctx, tmdbID)
	if errors.Is(err, series.ErrNotFound) {
		return c.series.Draft(ctx, tmdbID, profile, only)
	}
	if err != nil {
		return series.Series{}, err
	}
	s, err := c.series.Get(ctx, lib.ID)
	if err != nil {
		return series.Series{}, err
	}
	// Approving monitors what was asked for; what the library already has stays had.
	for i := range s.Seasons {
		sn := &s.Seasons[i]
		want := sn.SeasonNumber > 0 && (only == nil || only[sn.SeasonNumber])
		sn.Monitored = want
		for j := range sn.Episodes {
			sn.Episodes[j].Monitored = want
		}
	}
	return s, nil
}

// evaluationOf is the decision's evaluation of the release called name.
func evaluationOf(d quality.Decision, name string) *quality.Evaluation {
	for i := range d.Eligible {
		if d.Eligible[i].Candidate.Name == name {
			return &d.Eligible[i]
		}
	}
	return nil
}

func previewSeriesKind(r parser.Release) string {
	switch r.Kind() {
	case parser.KindCompleteShow:
		return "complete series"
	case parser.KindMultiSeason, parser.KindSeasonPack:
		return "pack"
	}
	return "episode"
}

func previewCovers(r parser.Release) string {
	switch r.Kind() {
	case parser.KindCompleteShow:
		return "all seasons"
	case parser.KindMultiSeason:
		return "S" + joinSeasons(r.Seasons)
	case parser.KindSeasonPack:
		return fmt.Sprintf("S%02d", r.Season)
	case parser.KindEpisode:
		if len(r.Episodes) > 0 {
			return fmt.Sprintf("S%02dE%02d", r.Season, r.Episodes[0])
		}
	}
	return ""
}

// seriesPlanWhy says why a show's season is taken the way it is.
func seriesPlanWhy(rels []PreviewRelease) string {
	packs := 0
	for _, r := range rels {
		if r.Kind != "episode" {
			packs++
		}
	}
	switch {
	case packs == len(rels) && packs == 1:
		return "One pack covers the season"
	case packs == 0:
		return "No pack fit the profile, so it's taken episode by episode"
	}
	return "A pack where one fits, single episodes for the rest"
}

// seasonsNote is the line about the seasons wanted beyond the one previewed: covered by a
// pack it found too, or not checked.
func seasonsNote(want []int, first int, covered map[int]bool) string {
	also, unchecked := 0, 0
	for _, sn := range want {
		switch {
		case sn == first:
		case covered[sn]:
			also++
		default:
			unchecked++
		}
	}
	switch {
	case also > 0 && unchecked > 0:
		return fmt.Sprintf("Also covers %d more %s; %d more not checked", also, pluralize(also, "season"), unchecked)
	case also > 0:
		return fmt.Sprintf("Also covers the other %d %s asked for", also, pluralize(also, "season"))
	case unchecked > 0:
		return fmt.Sprintf("%d more %s not checked", unchecked, pluralize(unchecked, "season"))
	}
	return ""
}

// ---- books -----------------------------------------------------------------

// previewBookID stands for a book that isn't in the library yet while matching releases
// against the library's titles: no real row has a negative id.
const previewBookID = -1

func (c *Coordinator) previewBook(ctx context.Context, in PreviewInput) (Preview, error) {
	if c.books == nil {
		return Preview{}, errPreviewUnsupported
	}
	held := Preview{Releases: []PreviewRelease{}}
	all, err := c.books.List(ctx)
	if err != nil {
		return Preview{}, err
	}
	var b books.Book
	if in.BookID > 0 {
		if b, err = c.books.Get(ctx, in.BookID); err != nil {
			return Preview{}, err
		}
		if c.claims.claimed(bookKey(b.ID)) {
			held.Outcome, held.Reason = PreviewBusy, ReasonAlreadySearching
			return held, nil
		}
		if acqs, err := c.Active(ctx, "book", b.ID); err == nil && len(acqs) > 0 {
			held.Outcome, held.Reason = PreviewBusy, ReasonAlreadyDownloading
			return held, nil
		}
	} else {
		b = books.Book{ID: previewBookID, Title: in.Title, Author: in.Author}
		all = append(all, b)
	}
	match := books.MatcherOver(all)
	sp := c.bookProfile(ctx, in.Profile)
	wantEbook, wantAudio := books.WantedEditions(sp.FormatScores)
	var kinds []string
	if wantEbook && b.Ebook == nil {
		kinds = append(kinds, books.KindEbook)
	}
	if wantAudio && b.Audiobook == nil {
		kinds = append(kinds, books.KindAudiobook)
	}
	if len(kinds) == 0 {
		held.Outcome, held.Reason = PreviewNotWanted, ReasonNothingWanted
		return held, nil
	}
	p := Preview{Profile: c.effectiveProfile(ctx, in.Profile, quality.MediaBook), Releases: []PreviewRelease{}}
	ctx, notes := newSearchNotes(ctx)
	nets := c.networks(ctx)
	allowed := c.bookLanguages(ctx)
	var rels []indexer.Release
	var editions []string
	answered := false
	for _, kind := range kinds {
		res, err := c.searchBook(ctx, b, kind)
		if err != nil {
			if answered {
				continue // the other edition's search answered; this failure isn't the story
			}
			return previewSearchErr(p, notes, err)
		}
		answered = true
		notes.consider(res.Releases)
		mine := releasesForBookWith(match, b, res.Releases)
		notes.dropped(res.Releases, mine, DropWrongTitle)
		if kind == books.KindAudiobook {
			kept := dropVersionReleases(b, mine)
			notes.dropped(mine, kept, DropOutOfScope)
			mine = kept
		}
		p.Matching += len(mine)
		id := b.ID
		if id < 0 {
			id = 0 // the global blocklist still applies
		}
		usable, err := c.dropUngrabbableNoted(ctx, id, mine, nil)
		if err != nil {
			return Preview{}, err
		}
		noteBookVerdicts(notes, sp, allowed, usable, kind)
		best := pickBestBookForKind(nets, sp, allowed, usable, kind, b.Title)
		if best == nil {
			continue
		}
		pr := previewRelease(*best, parser.Release{}, summarizeBook(releaseBookFormat(*best)))
		pr.Kind, pr.Format = kind, releaseBookFormat(*best)
		p.Releases = append(p.Releases, pr)
		rels = append(rels, *best)
		editions = append(editions, kind)
	}
	c.issuePreview(&p, rels, ReleaseKindBook, in.Key, editions)
	finishPreview(&p, notes)
	return p, nil
}
