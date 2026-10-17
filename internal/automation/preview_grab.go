package automation

import (
	"context"
	"errors"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// GrabPreviewed grabs the releases a preview chose (preview.go) for a title that has since
// joined the library as item id: "Approve & grab this" on a request. It is the approval's
// search, so it runs where that search would — as the title's search job — and holds the
// title's search claim while it grabs, so a sweep can't grab something else beside it.
//
// The previewed release is grabbed only while its token stands (key and kind as issued,
// not expired, not lost to a restart), it isn't blocklisted or already on its way, and
// there's room for it. Otherwise — or when the grab itself fails — it searches the
// ordinary way and grabs what that picks, so an approval always ends in a grab or a search.
// A show then searches for the rest of what was asked for, holding back what's in flight.
func (c *Coordinator) GrabPreviewed(ctx context.Context, kind string, id int64, tokens []string, key string) (SearchOutcome, error) {
	refs, err := c.previewTokens().resolve(tokens, key, kind)
	if err != nil {
		c.log.Info("request: the previewed release has expired — searching instead", "media", kind, "id", id, "err", err)
		return c.searchInstead(ctx, kind, id)
	}
	switch kind {
	case ReleaseKindMovie:
		return c.grabPreviewedMovie(ctx, id, refs[0])
	case ReleaseKindSeries:
		return c.grabPreviewedSeries(ctx, id, refs)
	case ReleaseKindBook:
		return c.grabPreviewedBook(ctx, id, refs)
	}
	return SearchOutcome{}, errPreviewUnsupported
}

// searchInstead is the approval's ordinary search, for a preview that can't be grabbed.
func (c *Coordinator) searchInstead(ctx context.Context, kind string, id int64) (SearchOutcome, error) {
	switch kind {
	case ReleaseKindMovie:
		return c.SearchMovie(ctx, id)
	case ReleaseKindSeries:
		return c.SearchSeriesManual(ctx, id)
	case ReleaseKindBook:
		return c.SearchBookNow(ctx, id)
	}
	return SearchOutcome{}, errPreviewUnsupported
}

// previewGrabbed is the outcome of grabbing previewed releases, stored as the title's
// search attempt the way an RSS grab is: the approval did find and take something.
func (c *Coordinator) previewGrabbed(ctx context.Context, mediaType string, id int64, titles []string) SearchOutcome {
	n := len(titles)
	out := SearchOutcome{Searched: true, Returned: n, Matching: n, Usable: n, Eligible: n, Grabbed: n, GrabbedTitles: titles, Reason: ReasonGrabbed}
	c.recordAttempt(WithDefaultSearchTrigger(ctx, TriggerRequest), nil, mediaType, id, "", &out, nil)
	return out
}

func (c *Coordinator) grabPreviewedMovie(ctx context.Context, id int64, ref ReleaseRef) (SearchOutcome, error) {
	title, ok, err := c.tryPreviewedMovie(ctx, id, ref)
	if err != nil {
		return SearchOutcome{Reason: ReasonAlreadySearching}, err
	}
	if !ok {
		return c.SearchMovie(ctx, id)
	}
	if title == "" {
		return SearchOutcome{Reason: ReasonAlreadyDownloading}, nil
	}
	return c.previewGrabbed(ctx, AttemptMovie, id, []string{title}), nil
}

// tryPreviewedMovie grabs ref for movie id under its search claim. ok is false when it
// should be searched for instead; title is "" with ok when nothing was needed (something
// is already downloading for it, or it has its file).
func (c *Coordinator) tryPreviewedMovie(ctx context.Context, id int64, ref ReleaseRef) (title string, ok bool, err error) {
	release, claimed := c.claims.claim(movieKey(id))
	if !claimed {
		return "", false, ErrAlreadySearching // that search covers it
	}
	defer release()
	m, err := c.movies.Get(ctx, id)
	if err != nil {
		return "", false, nil
	}
	ctx = c.forMovie(ctx, m)
	want, inFlight, err := c.wantedVersions(ctx, id)
	if err != nil {
		return "", false, nil
	}
	if len(want) == 0 {
		return "", inFlight, nil // in flight: done; nothing wanted at all: the search says so
	}
	if len(want) > 1 {
		return "", false, nil // a search per track: one previewed release can't stand for them all
	}
	v := want[0]
	for _, w := range want {
		if w.IsDefault {
			v = w
		}
	}
	if !c.previewGrabbable(ctx, "movie", id, ref) {
		return "", false, nil
	}
	if !c.diskOKFor(ctx, ref.Indexer, ref.SizeGB) {
		return "", false, nil // the search says why
	}
	hash, err := c.Grab(ctx, ref.Indexer, ref.DownloadURL, ref.Title)
	if err != nil {
		c.log.Warn("request: couldn't grab the previewed release — searching instead", "movie", m.Title, "release", ref.Title, "err", err)
		return "", false, nil
	}
	profile := c.effectiveProfile(ctx, v.QualityProfile, quality.MediaMovie)
	c.recordGrab(ctx, m.ID, v.ID, ref.Title, ref.Indexer, profile, c.quality.StallMinutes(ctx, profile), hash)
	c.movies.AddEvent(ctx, m.ID, "grabbed", ref.Title+" · "+ref.Indexer+" · the release previewed on approval")
	c.log.Info("request: grabbed the previewed release", "movie", m.Title, "release", ref.Title, "indexer", ref.Indexer)
	return ref.Title, true, nil
}

// previewGrabbable reports whether a previewed release may still be grabbed for the item:
// not blocklisted since (for it, or everywhere) and not already grabbed and in flight. An
// unreadable list says no, as it does for every grab.
func (c *Coordinator) previewGrabbable(ctx context.Context, mediaType string, id int64, ref ReleaseRef) bool {
	blocked, err := c.blockedSetOf(ctx, id, mediaType)
	if err != nil || blocked[normTitle(ref.Title)] {
		return false
	}
	pending, err := c.pendingTitlesOf(ctx, id, mediaType)
	return err == nil && !pending[normTitle(ref.Title)]
}

func (c *Coordinator) grabPreviewedSeries(ctx context.Context, id int64, refs []ReleaseRef) (SearchOutcome, error) {
	titles, err := c.tryPreviewedSeries(ctx, id, refs)
	if err != nil {
		return SearchOutcome{Reason: ReasonAlreadySearching}, err
	}
	var out SearchOutcome
	if len(titles) > 0 {
		out = c.previewGrabbed(ctx, AttemptSeries, id, titles)
	}
	// The rest of what was asked for: the other seasons, and anything the preview's
	// releases didn't cover. What was just grabbed is in flight, so it's held back.
	rest, err := c.SearchSeriesManual(ctx, id)
	if errors.Is(err, ErrAlreadySearching) {
		err = nil
	}
	if len(titles) == 0 {
		return rest, err
	}
	out.Grabbed += rest.Grabbed
	out.GrabbedTitles = append(out.GrabbedTitles, rest.GrabbedTitles...)
	return out, err
}

// tryPreviewedSeries grabs the previewed releases for show id under its search claim and
// returns those it grabbed. What the show wants is read again first, as approved: a release
// is taken only while it still covers a wanted episode no download in flight holds, and a
// pack only while it's proportionate to what's wanted — so an approval trimmed to one
// season never takes the complete-series pack previewed for four. Whatever is skipped is
// left to the search that follows.
func (c *Coordinator) tryPreviewedSeries(ctx context.Context, id int64, refs []ReleaseRef) ([]string, error) {
	release, claimed := c.claims.claim(seriesKey(id))
	if !claimed {
		return nil, ErrAlreadySearching
	}
	defer release()
	s, err := c.series.Get(ctx, id)
	if err != nil {
		return nil, nil
	}
	ctx = c.forSeries(ctx, s)
	wanted, seriesSeasons := wantedEpisodes(s)
	inSeasons, whole, _, err := c.seriesInFlightFor(ctx, s, nil)
	if err != nil || whole {
		return nil, nil // unreadable, or a pack for the whole show is on its way: the search says so
	}
	needed := map[epKey]bool{}
	for _, k := range wanted {
		if !inSeasons[k.season] {
			needed[k] = true
		}
	}
	var titles []string
	gb := 0.0
	for _, ref := range refs {
		if len(needed) == 0 {
			break
		}
		r := parser.Parse(ref.Title)
		covers := c.coveredByFor(ctx, s, r, needed)
		if len(covers) == 0 {
			continue // nothing it holds is still wanted
		}
		if r.Kind() != parser.KindEpisode && !packIsProportionate(seasonsOf(needed), packSeasonsOf(r, seriesSeasons)) {
			continue // more seasons than the approval wants
		}
		if !c.previewGrabbable(ctx, "series", id, ref) || !c.diskOKFor(ctx, ref.Indexer, gb+ref.SizeGB) {
			continue
		}
		// An automatic pick, not a hand-picked one: the import gate still applies.
		if err := c.grabForSeries(ctx, id, ref.Indexer, ref.DownloadURL, ref.Title, false, WholeShow); err != nil {
			c.log.Warn("request: couldn't grab a previewed release", "series", s.Title, "release", ref.Title, "err", err)
			continue
		}
		for _, k := range covers {
			delete(needed, k)
		}
		gb += ref.SizeGB
		titles = append(titles, ref.Title)
	}
	if len(titles) > 0 {
		c.log.Info("request: grabbed the previewed releases", "series", s.Title, "releases", len(titles))
	}
	return titles, nil
}

func (c *Coordinator) grabPreviewedBook(ctx context.Context, id int64, refs []ReleaseRef) (SearchOutcome, error) {
	titles, err := c.tryPreviewedBook(ctx, id, refs)
	if err != nil {
		return SearchOutcome{Reason: ReasonAlreadySearching}, err
	}
	if len(titles) == 0 {
		return c.SearchBookNow(ctx, id)
	}
	return c.previewGrabbed(ctx, AttemptBook, id, titles), nil
}

func (c *Coordinator) tryPreviewedBook(ctx context.Context, id int64, refs []ReleaseRef) ([]string, error) {
	release, claimed := c.claims.claim(bookKey(id))
	if !claimed {
		return nil, ErrAlreadySearching
	}
	defer release()
	b, err := c.books.Get(ctx, id)
	if err != nil {
		return nil, nil
	}
	var titles []string
	taken := map[string]bool{}
	for _, ref := range refs {
		kind := ref.Edition
		if (kind == books.KindEbook && b.Ebook != nil) || (kind == books.KindAudiobook && b.Audiobook != nil) {
			continue // that edition is here already
		}
		if !c.previewGrabbable(ctx, "book", id, ref) {
			continue
		}
		hash, err := c.grabTo(ctx, ref.Indexer, ref.DownloadURL, ref.Title, bookCategory)
		if err != nil {
			c.log.Warn("request: couldn't grab a previewed release", "book", b.Title, "release", ref.Title, "err", err)
			continue
		}
		c.recordBookGrab(ctx, id, 0, ref.Title, ref.Indexer, b.QualityProfile, hash)
		c.books.AddEvent(ctx, id, "grabbed", "Grabbed the release previewed on approval from "+ref.Indexer+": "+ref.Title)
		titles = append(titles, ref.Title)
		taken[kind] = true
	}
	if len(titles) == 0 {
		return nil, nil
	}
	// An edition the book wants that the preview didn't cover (it found none, or the grab
	// failed) is searched now, still under the claim, as the approval's search would have
	// — never the edition just grabbed, which is on its way.
	sp := c.bookProfile(ctx, b.QualityProfile)
	wantEbook, wantAudio := books.WantedEditions(sp.FormatScores)
	var out SearchOutcome
	for _, kind := range []string{books.KindEbook, books.KindAudiobook} {
		want := (kind == books.KindEbook && wantEbook && b.Ebook == nil) || (kind == books.KindAudiobook && wantAudio && b.Audiobook == nil)
		if !want || taken[kind] {
			continue
		}
		if err := c.searchEdition(ctx, b, kind, sp, &out); err != nil {
			break
		}
	}
	return append(titles, out.GrabbedTitles...), nil
}
