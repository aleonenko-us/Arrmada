package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/audiobook"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/extract"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// bookCategory keeps ebook/audiobook downloads in their own download-client category
// so the book importer processes them (not the movie/series video importers).
const bookCategory = download.CategoryBooks

var reBookFormat = regexp.MustCompile(`(?i)\b(epub|azw3|azw|mobi|pdf|cbz|cbr|fb2|djvu|lit|m4b|m4a|mp3|aac|flac|ogg|opus)\b`)

// detectBookFormat extracts the ebook/audiobook format tag from a release name.
func detectBookFormat(title string) string {
	return strings.ToUpper(reBookFormat.FindString(title))
}

// SearchBooksMissing sweeps every monitored book and grabs any wanted edition it lacks.
func (c *Coordinator) SearchBooksMissing(ctx context.Context) {
	c.searchBooksMissing(WithDefaultSearchTrigger(ctx, TriggerSweep), bookSweepCap)
}

// searchBooksMissing is the sweep with its per-run cap as a parameter, so a test can
// exercise the cap without forty throttled searches.
func (c *Coordinator) searchBooksMissing(ctx context.Context, maxSearches int) {
	if c.books == nil {
		return
	}
	all, err := c.books.List(ctx)
	if err != nil {
		return
	}
	// A book stays "missing" for the whole download (b.Ebook is only set after import),
	// so without an in-flight guard every sweep re-grabbed the same release.
	queue, qerr := c.downloads.Queue(ctx)
	if qerr != nil {
		// An unreadable queue looks exactly like an empty one — every in-flight book
		// would read as "not downloading" and the sweep would stack a duplicate grab
		// on each. Skip the cycle; the next sweep is 15 minutes away.
		c.log.Warn("book: couldn't read the download queue — skipping the missing-books sweep this cycle", "err", qerr)
		return
	}
	states, err := c.books.SearchStates(ctx)
	if err != nil {
		c.log.Warn("book: couldn't read search states — skipping the missing-books sweep this cycle", "err", err)
		return
	}
	// Every monitored book that still lacks a wanted edition and whose wait on the ladder
	// is over. Nothing is ever dropped for good; a book that keeps coming up empty is just
	// asked about less often.
	now := c.clock()
	type dueBook struct {
		b     books.Book
		state books.SearchState
	}
	downloading := booksDownloading(queue, books.MatcherOver(all))
	var due []dueBook
	for _, b := range all {
		if !b.Monitored || !c.bookLacksWanted(ctx, b) {
			continue
		}
		st := states[b.ID]
		// An upcoming book isn't searched before its release day; from that day it is
		// searched at least daily for two weeks (books.NextSearchAtFor).
		if next := books.NextSearchAtFor(st.LastAt, st.Misses, b.ReleaseDate, now); !next.IsZero() && now.Before(next) {
			continue
		}
		if downloading[b.ID] {
			continue // already downloading for this book — let it finish
		}
		if st.Misses > 0 && books.ReleasedSinceLastSearch(st.LastAt, b.ReleaseDate, now) {
			// Out since the last look: the empty searches from before it existed don't
			// count against it.
			c.books.ResetForRelease(ctx, b.ID)
			st.Misses = 0
		}
		due = append(due, dueBook{b, st})
	}
	// Longest-waiting first, never-searched before all. With the cap, a backlog (every
	// book that used to be given up on comes due at once after an upgrade) is spread over
	// the next sweeps instead of hitting the trackers in one burst.
	sort.SliceStable(due, func(i, j int) bool {
		a, b := parseTime(due[i].state.LastAt), parseTime(due[j].state.LastAt)
		if !a.Equal(b) {
			return a.Before(b) // the zero time (never searched) sorts first
		}
		return due[i].b.ID < due[j].b.ID
	})
	var outage outageTally
	defer outage.report(c.log, "book search sweep")
	searched := 0
	for _, d := range due {
		if searched >= maxSearches {
			break
		}
		searched++
		b := d.b
		out, err := c.searchBookOnce(ctx, b.ID)
		n := out.Grabbed
		if errors.Is(err, ErrAlreadySearching) {
			c.log.Debug("book: skipping a book that is already being searched", "title", b.Title)
			continue
		}
		// An error — above all an indexer outage — is not a miss: a search nobody could
		// answer says nothing about whether the book is out there.
		if outage.note(err) {
			if outage.stop() {
				break // the indexers are down: the rest would only fail the same way
			}
			continue
		}
		if err != nil {
			c.log.Warn("book: search failed", "title", b.Title, "err", err)
		}
		if _, miss := sweepOutcome(err, true, n); miss {
			c.books.RecordSearchMiss(ctx, b.ID)
			// Say so once, at the step down to monthly checks, so a book searched rarely
			// doesn't look like one nobody has got to yet.
			if d.state.Misses+1 == books.MonthlyAfter+1 {
				c.log.Info("book: still nothing after many searches — checking monthly from now on",
					"title", b.Title)
			}
		}
	}
	if len(due) > searched {
		c.log.Debug(fmt.Sprintf("books: %d due, searched %d, rest next sweep", len(due), searched))
	}
}

// bookSweepCap is the most books one automatic sweep searches. Book searches are the
// expensive kind (one per wanted edition, across every indexer) and private trackers
// rate-limit; the rest wait for the next sweep, oldest first.
const bookSweepCap = 40

// bookLacksWanted reports whether a book is missing an edition, or a monitored audio
// version, that its profile wants — the same conditions searchBookOnce searches for. A
// complete book has nothing to search, and counting it would only use up the cap.
func (c *Coordinator) bookLacksWanted(ctx context.Context, b books.Book) bool {
	sp := c.bookProfile(ctx, b.QualityProfile)
	wantEbook, wantAudio := books.WantedEditions(sp.FormatScores)
	if (wantEbook && b.Ebook == nil) || (wantAudio && b.Audiobook == nil) {
		return true
	}
	for _, v := range b.AudioVersions {
		if versionWanted(v) {
			return true
		}
	}
	return false
}

// clock is the sweep's "now"; tests set c.now.
func (c *Coordinator) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// booksDownloading returns the IDs of books the queue already holds a book torrent for,
// so search/RSS sweeps don't stack a second grab on an in-flight one.
//
// It resolves each queue item once against one library snapshot. The per-book form ran
// MatchByRelease — a full books-table read — for every queue item and every book, each
// cycle.
func booksDownloading(queue []download.Item, match func(string) (books.Book, bool)) map[int64]bool {
	out := map[int64]bool{}
	for _, it := range queue {
		if it.Category != bookCategory {
			continue
		}
		if b, ok := match(it.Name); ok {
			out[b.ID] = true
		}
	}
	return out
}

// SearchBookNow searches for each wanted edition (ebook/audiobook, per the profile)
// that the book doesn't yet have, grabs the best-format release for it, and says what it
// found.
func (c *Coordinator) SearchBookNow(ctx context.Context, bookID int64) (SearchOutcome, error) {
	return c.searchBookOnce(ctx, bookID)
}

// searchBookOnce is SearchBookNow; the missing-books sweep reads the outcome's grab count
// to leave alone a book that keeps coming up empty.
func (c *Coordinator) searchBookOnce(ctx context.Context, bookID int64) (SearchOutcome, error) {
	if c.books == nil {
		return SearchOutcome{Reason: ReasonNothingWanted}, nil
	}
	release, ok := c.claims.claim(bookKey(bookID))
	if !ok {
		return SearchOutcome{Reason: ReasonAlreadySearching}, ErrAlreadySearching
	}
	defer release()
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return SearchOutcome{}, err
	}
	sp := c.bookProfile(ctx, b.QualityProfile)
	wantEbook, wantAudio := books.WantedEditions(sp.FormatScores)
	// A search that couldn't run stops the pass: the next edition would only hit the same
	// dead indexers, and returning the error is what keeps the sweep from counting a miss.
	var out SearchOutcome
	var searchErr error
	if wantEbook && b.Ebook == nil {
		searchErr = c.searchEdition(ctx, b, books.KindEbook, sp, &out)
	}
	if searchErr == nil && wantAudio && b.Audiobook == nil {
		searchErr = c.searchEdition(ctx, b, books.KindAudiobook, sp, &out)
	}
	for _, v := range b.AudioVersions {
		if searchErr != nil {
			break
		}
		if !versionWanted(v) {
			continue
		}
		searchErr = c.searchVersion(ctx, b, v, sp, &out)
	}
	if out.Grabbed > 0 {
		// Something was findable after all. Clearing here rather than in the sweep covers
		// the manual Search button too: a book you un-stuck by hand goes back to being
		// swept normally, which matters when only one of its two editions landed.
		c.books.ResetSearchMisses(ctx, bookID)
	}
	if !out.Searched {
		out.Reason = ReasonNothingWanted
	} else {
		out.settle()
	}
	if searchErr != nil && out.Grabbed == 0 {
		out.noteSearchErr(searchErr)
	}
	return out, searchErr
}

// searchEdition searches one edition and adds what it found to out. Each edition is its
// own search attempt (scope "ebook" or "audiobook").
func (c *Coordinator) searchEdition(ctx context.Context, b books.Book, kind string, sp quality.StoredProfile, out *SearchOutcome) error {
	ctx, notes := newSearchNotes(ctx)
	title, st, err := c.grabBookEditionCounted(ctx, b, kind, sp, nil)
	c.recordEditionAttempt(ctx, notes, b.ID, kind, title, st, err, out)
	return err
}

// recordEditionAttempt stores one edition's (or audio version's) search as an attempt under
// scope, and adds its counts to the book's whole outcome.
func (c *Coordinator) recordEditionAttempt(ctx context.Context, notes *searchNotes, bookID int64, scope, title string, st editionStats, err error, out *SearchOutcome) {
	eo := SearchOutcome{Searched: true, Returned: st.returned, Matching: st.matching, Usable: st.usable}
	if title != "" {
		eo.Grabbed, eo.GrabbedTitles = 1, []string{title}
	}
	if err != nil {
		eo.noteSearchErr(err)
	} else {
		eo.settle()
	}
	c.recordAttempt(ctx, notes, AttemptBook, bookID, scope, &eo, err)
	out.Searched = true
	out.Returned += st.returned
	out.Matching += st.matching
	out.Usable += st.usable
	if title != "" {
		out.Grabbed++
		out.GrabbedTitles = append(out.GrabbedTitles, title)
	}
	out.WrongTitle += eo.WrongTitle
	out.Blocklisted += eo.Blocklisted
	out.Pending += eo.Pending
	out.OutOfScope += eo.OutOfScope
	out.Rejected += eo.Rejected
	out.Eligible += eo.Eligible
	for code, n := range eo.Reasons {
		if out.Reasons == nil {
			out.Reasons = map[string]int{}
		}
		out.Reasons[code] += n
	}
	for name, msg := range eo.IndexerErrors {
		if out.IndexerErrors == nil {
			out.IndexerErrors = map[string]string{}
		}
		out.IndexerErrors[name] = msg
	}
	if out.Example == "" {
		out.Example = eo.Example
	}
}

// grabBookEditionExcluding searches for one edition and grabs the best release, skipping
// the normalized titles in exclude, and returns the title it grabbed ("" for none). A
// stall fail-over excludes the stalled release, which stays un-blocklisted until
// something replaces it. The error is the search's when it couldn't run at all (every
// indexer failed, or none serves books) — swallowing that made an outage look like
// "nothing found".
func (c *Coordinator) grabBookEditionExcluding(ctx context.Context, b books.Book, kind string, sp quality.StoredProfile, exclude map[string]bool) (string, error) {
	title, _, err := c.grabBookEditionCounted(ctx, b, kind, sp, exclude)
	return title, err
}

// editionStats counts what one edition search saw, for the Search button's outcome.
type editionStats struct{ returned, matching, usable int }

// grabBookEditionCounted is grabBookEditionExcluding that also counts what it saw.
func (c *Coordinator) grabBookEditionCounted(ctx context.Context, b books.Book, kind string, sp quality.StoredProfile, exclude map[string]bool) (string, editionStats, error) {
	var st editionStats
	res, err := c.searchBook(ctx, b, kind)
	if err != nil {
		return "", st, err
	}
	notes := notesFrom(ctx) // the edition's search attempt, when it is recorded
	notes.consider(res.Releases)
	st.returned = len(res.Releases)
	if len(res.Releases) == 0 {
		return "", st, nil
	}
	before := append([]indexer.Release(nil), res.Releases...)
	// Only releases that actually name THIS book. Book indexers fuzzy-match, so a query of
	// "Frank Herbert Dune" routinely returns Dune Messiah and Children of Dune; nothing
	// downstream checked the title, so a sequel could out-score the book we asked for, be
	// grabbed, import into this book's folder, and satisfy the edition forever (there is no
	// upgrade pass to correct it later). Movies and series both gate their search results
	// this way, and the books RSS path runs the same gate (releasesForBookWith).
	res.Releases = releasesForBookWith(c.books.Matcher(ctx), b, res.Releases)
	notes.dropped(before, res.Releases, DropWrongTitle)
	if kind == books.KindAudiobook {
		mine := append([]indexer.Release(nil), res.Releases...)
		res.Releases = dropVersionReleases(b, res.Releases) // those belong to a version
		notes.dropped(mine, res.Releases, DropOutOfScope)
	}
	st.matching = len(res.Releases)
	if len(res.Releases) == 0 {
		c.log.Info("book: no release matched this title", "title", b.Title, "edition", kind)
		return "", st, nil
	}
	// Don't re-grab a blocklisted (e.g. stalled) release. The DB pending-grab guard,
	// mirroring the movie path's pendingGrabTitles, drops a release already grabbed for
	// this book (and not yet imported/failed), even when the queue-based bookDownloading
	// check couldn't see it.
	res.Releases, err = c.dropUngrabbableNoted(ctx, b.ID, res.Releases, exclude)
	if err != nil {
		c.skipUnreadable(b.Title, err)
		return "", st, err
	}
	st.usable = len(res.Releases)
	allowed := c.bookLanguages(ctx)
	noteBookVerdicts(notes, sp, allowed, res.Releases, kind)
	best := pickBestBookForKind(c.networks(ctx), sp, allowed, res.Releases, kind, b.Title)
	if best == nil {
		c.log.Info("book: no matching-format release", "title", b.Title, "edition", kind)
		return "", st, nil
	}
	hash, err := c.grabTo(ctx, best.Indexer, best.DownloadURL, best.Title, bookCategory)
	if err != nil {
		c.log.Warn("book: grab failed", "title", b.Title, "err", err)
		return "", st, nil
	}
	c.recordBookGrab(ctx, b.ID, 0, best.Title, best.Indexer, b.QualityProfile, hash)
	c.learnBookSeries(ctx, b, *best)
	c.books.AddEvent(ctx, b.ID, "grabbed", fmt.Sprintf("Grabbed the %s edition from %s: %s", kind, best.Indexer, best.Title))
	c.log.Info("book: grabbing", "title", b.Title, "edition", kind, "release", best.Title, "format", detectBookFormat(best.Title))
	return best.Title, st, nil
}

// releasesForBookWith keeps only the releases whose name resolves to b when matched against
// the whole library (match is one library snapshot: books.Matcher or books.MatcherOver).
// The search path and the RSS path both filter through it.
//
// It reuses the import-side matcher deliberately, so the searcher and the importer agree on
// what a release is: whole-word title containment, author-confirmed candidates preferred,
// and the LONGEST matching title winning. That last rule is what keeps a "Dune Messiah"
// release off "Dune" — Messiah is the more specific claim, so it only passes this gate for
// the book it really is.
//
// Limit worth knowing: if the sequel is NOT in the library there is no longer title to beat
// "Dune", so such a release still passes. Catching that needs a book database we don't have;
// the import-side identity gate is what stops it landing on the wrong book unnoticed.
func releasesForBookWith(match func(string) (books.Book, bool), b books.Book, releases []indexer.Release) []indexer.Release {
	out := make([]indexer.Release, 0, len(releases))
	for _, rel := range releases {
		if mb, ok := match(rel.Title); ok && mb.ID == b.ID {
			out = append(out, rel)
		}
	}
	return out
}

// reSeriesPosition splits a book tracker's series credit into its name and number.
// MyAnonaMouse renders one as "Coven of Bones #1"; a book in several series comes back
// comma-joined, and the first is the one that names it.
var reSeriesPosition = regexp.MustCompile(`^(.*?)\s*#\s*([0-9]+(?:\.[0-9]+)?)\s*$`)

// parseBookSeries pulls the series name and position out of an indexer's series credit.
// A position of 0 means the series is known but its number isn't — worth recording even
// so, because grouping a library by series doesn't need the numbers.
func parseBookSeries(credit string) (name string, position float64) {
	// Only the first credit. An omnibus can list several series, and the one it leads
	// with is the one it belongs to; guessing between them would file it arbitrarily.
	if i := strings.Index(credit, ","); i >= 0 {
		credit = credit[:i]
	}
	credit = strings.TrimSpace(credit)
	if credit == "" {
		return "", 0
	}
	m := reSeriesPosition.FindStringSubmatch(credit)
	if m == nil {
		return credit, 0
	}
	name = strings.TrimSpace(m[1])
	if name == "" {
		return "", 0 // "#3" on its own names no series
	}
	position, _ = strconv.ParseFloat(m[2], 64)
	return name, position
}

// learnBookSeries records the series a grabbed release says this book belongs to.
//
// Book trackers state it and Arrmada used to discard it: the searcher folded the credit
// into the text it scored against and kept nothing, so a library had no idea which of its
// books were one series or which entry was missing between two it owned.
//
// Only ever fills in. A release that names no series can't erase what an earlier one said,
// and a book that already has a series isn't relabelled by whatever it's grabbed next.
func (c *Coordinator) learnBookSeries(ctx context.Context, b books.Book, rel indexer.Release) {
	if c.books == nil || b.SeriesName != "" || rel.Series == "" {
		return
	}
	name, pos := parseBookSeries(rel.Series)
	if name == "" {
		return
	}
	if err := c.books.SetSeries(ctx, b.ID, name, pos); err != nil {
		c.log.Warn("book: could not record series", "title", b.Title, "series", name, "err", err)
		return
	}
	c.log.Info("book: learned its series from the release", "title", b.Title, "series", name, "position", pos)
}

// BookSeriesBackfill reports what a backfill run did.
type BookSeriesBackfill struct {
	Scanned int `json:"scanned"` // books that had no series recorded
	Learned int `json:"learned"` // books a release could name a series for
}

// BackfillBookSeries fills in the series for books already in the library.
//
// The series is normally learned from the release that matched a book, which means a
// library assembled before this existed knows nothing — and would only fill in as each
// book happened to be grabbed again, which for a book you already own is never. This runs
// one search per unlabelled book purely to READ the series off the results.
//
// It grabs nothing and changes nothing else. Releases are put through the same identity
// gate the searcher uses, so a series is only taken from a release that really is this
// book — a "Red Rising" result would otherwise label Golden Son with its own series and
// position.
func (c *Coordinator) BackfillBookSeries(ctx context.Context) (BookSeriesBackfill, error) {
	var res BookSeriesBackfill
	if c.books == nil {
		return res, nil
	}
	all, err := c.books.List(ctx)
	if err != nil {
		return res, err
	}
	match := books.MatcherOver(all) // recording a series doesn't change what matches
	for _, b := range all {
		if ctx.Err() != nil {
			c.log.Info("book: series backfill cancelled", "scanned", res.Scanned, "learned", res.Learned)
			return res, ctx.Err()
		}
		if b.SeriesName != "" {
			continue
		}
		res.Scanned++
		out, err := c.searchBook(ctx, b, "")
		if err != nil || len(out.Releases) == 0 {
			continue
		}
		for _, rel := range releasesForBookWith(match, b, out.Releases) {
			if rel.Series == "" {
				continue
			}
			name, pos := parseBookSeries(rel.Series)
			if name == "" {
				continue
			}
			if err := c.books.SetSeries(ctx, b.ID, name, pos); err != nil {
				c.log.Warn("book: series backfill could not save", "title", b.Title, "err", err)
				break
			}
			c.log.Info("book: series backfilled", "title", b.Title, "series", name, "position", pos)
			res.Learned++
			break // the first release that names this book is enough
		}
	}
	c.log.Info("book: series backfill done", "scanned", res.Scanned, "learned", res.Learned)
	return res, nil
}

// BookSeriesEntry is one position in a series as seen from the library: a book you own,
// or a hole where one should be.
type BookSeriesEntry struct {
	BookID   int64   `json:"book_id,omitempty"` // 0 for a gap — nothing in the library
	Title    string  `json:"title"`
	Position float64 `json:"position,omitempty"`
	HasFile  bool    `json:"has_file"`
	Missing  bool    `json:"missing"` // a numbered entry with no book in the library
	// From the catalogue's series listing (Hardcover): a missing entry can be added.
	Key      string `json:"key,omitempty"`
	Author   string `json:"author,omitempty"`
	Year     int    `json:"year,omitempty"`
	CoverURL string `json:"cover_url,omitempty"`
}

// BookSeries is a book's series as the library can see it.
type BookSeries struct {
	Name    string            `json:"name"`
	Entries []BookSeriesEntry `json:"entries"`
	Gaps    int               `json:"gaps"`
	// Source is "catalogue" when the whole series came from Hardcover (every entry,
	// Total known) and "library" when it's inferred from owned books alone.
	Source string `json:"source"`
	Total  int    `json:"total,omitempty"`
	Key    string `json:"key,omitempty"`
}

// BookSeriesFor returns the series a book belongs to, its siblings in reading order, and
// the numbered positions missing between them.
//
// The gaps are inferred, not looked up: nothing here knows how long a series really is,
// only what the library holds. A hole BETWEEN two owned entries is a fact — you have #1
// and #3, so #2 exists and you don't have it. Anything past the highest owned position is
// a guess, so it isn't reported. That keeps every gap shown a real one.
func (c *Coordinator) BookSeriesFor(ctx context.Context, bookID int64) (BookSeries, error) {
	if c.books == nil {
		return BookSeries{}, nil
	}
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return BookSeries{}, err
	}
	// The catalogue's own listing, when the book carries its series key: every entry,
	// missing ones included by title, not just the holes between owned books.
	if v, ok := c.books.CatalogueSeries(ctx, bookID); ok {
		out := BookSeries{Name: v.Name, Gaps: v.Gaps, Source: "catalogue", Total: v.Total, Key: v.Key}
		for _, e := range v.Entries {
			out.Entries = append(out.Entries, BookSeriesEntry{
				BookID: e.BookID, Title: e.Title, Position: e.Position, HasFile: e.HasFile, Missing: e.Missing,
				Key: e.Key, Author: e.Author, Year: e.Year, CoverURL: e.CoverURL,
			})
		}
		return out, nil
	}
	if b.SeriesName == "" {
		return BookSeries{}, nil
	}
	siblings, err := c.books.SeriesSiblings(ctx, b.SeriesName)
	if err != nil {
		return BookSeries{}, err
	}
	out := BookSeries{Name: b.SeriesName, Source: "library"}
	owned := map[int]books.Book{}
	maxPos := 0
	for _, sb := range siblings {
		// Only whole numbers can imply a gap. A novella at 1.5 is optional by nature —
		// its absence says nothing about the series being incomplete.
		if p := sb.SeriesPosition; p > 0 && p == float64(int(p)) {
			owned[int(p)] = sb
			if int(p) > maxPos {
				maxPos = int(p)
			}
		}
	}
	for i := 1; i <= maxPos; i++ {
		if sb, ok := owned[i]; ok {
			out.Entries = append(out.Entries, BookSeriesEntry{
				BookID: sb.ID, Title: sb.Title, Position: sb.SeriesPosition, HasFile: sb.HasFile})
			continue
		}
		out.Entries = append(out.Entries, BookSeriesEntry{Position: float64(i), Missing: true})
		out.Gaps++
	}
	// Everything the whole-number walk didn't cover: novellas, and entries whose number
	// no release ever stated. They belong to the series and shouldn't vanish from it.
	for _, sb := range siblings {
		p := sb.SeriesPosition
		if p > 0 && p == float64(int(p)) {
			continue // already placed above
		}
		out.Entries = append(out.Entries, BookSeriesEntry{
			BookID: sb.ID, Title: sb.Title, Position: p, HasFile: sb.HasFile})
	}
	return out, nil
}

// bookQuery builds the indexer query: author + title, with no edition word.
//
// It used to append "audiobook" for audio searches, on the reasoning that ebook releases
// dominate a bare title search. That works on a general tracker, whose release NAMES carry
// the word. On a dedicated book tracker it is fatal: MyAnonaMouse matches all query words
// against title/author/narrator/series, none of which contain "audiobook", so every audio
// search returned zero. The edition now travels as SearchQuery.BookEdition and each
// searcher applies it the way its own API expects — a category on MAM, a query word on
// the general trackers.
func bookQuery(b books.Book) string {
	if b.Author != "" {
		return b.Author + " " + b.Title
	}
	return b.Title
}

// searchBook runs the indexer query for a book: author + title, and when that finds
// nothing, the title alone. The author on file can be wrong — a catalogue that
// credited the illustrator first — or spelled unlike the tracker's, and a book
// tracker matches every query word, so one bad word hides a release that is there.
// The title check downstream still keeps only this book's releases.
//
// Author and title also travel as parts, for an indexer whose caps take t=book.
//
// A title or author edited by hand is searched first: an edit is most often a correction
// to what trackers call the book (a catalogue's illustrator credited as the author, a
// UK title for a US release). When that finds nothing, the catalogue's own title and
// author — kept underneath the edit — are tried too, so an edit can only widen the
// search. Either way the book is the same book: its catalogue keys never change, and the
// title check downstream accepts a release named either way.
func (c *Coordinator) searchBook(ctx context.Context, b books.Book, edition string) (indexer.SearchResult, error) {
	res, err := c.searchBookAs(ctx, b, edition)
	if err != nil || len(res.Releases) > 0 {
		return res, err
	}
	m := b.Matched()
	if m.Title == b.Title && m.Author == b.Author {
		return res, err
	}
	c.log.Info("book: nothing under the edited details — trying the catalogue's", "title", m.Title, "author", m.Author)
	return c.searchBookAs(ctx, m, edition)
}

// searchBookAs is one round of searchBook for one set of names: author + title, then the
// title alone.
func (c *Coordinator) searchBookAs(ctx context.Context, b books.Book, edition string) (indexer.SearchResult, error) {
	res, err := c.search(ctx, indexer.SearchQuery{
		Text: bookQuery(b), Author: b.Author, BookTitle: b.Title,
		MediaType: indexer.MediaBook, BookEdition: edition, Limit: 60})
	// A search that couldn't run (every indexer down, none serving books) says nothing
	// about the author: retrying with the title alone would only fail the same way.
	if err != nil || b.Author == "" || len(res.Releases) > 0 {
		return res, err
	}
	c.log.Info("book: nothing for author + title — trying the title alone", "title", b.Title, "author", b.Author)
	return c.search(ctx, indexer.SearchQuery{
		Text: b.Title, BookTitle: b.Title, MediaType: indexer.MediaBook, BookEdition: edition, Limit: 60})
}

// pickBestBookForKind ranks releases of the given edition by the profile: format
// score + keyword score combine (so "graphic audio +100" or a preferred format
// wins), seeders break ties. Hard-reject terms, unwanted formats and releases in a
// language the owner doesn't read are skipped.
//
// wantTitle is the book being searched for; a release that bundles it with others (an
// omnibus, a trilogy) ranks below one of just that book — see bookShape. nets decides
// between a usenet and a torrent release that score the same (netPolicy.beats).
func pickBestBookForKind(nets netPolicy, sp quality.StoredProfile, allowed bookLangs, releases []indexer.Release, kind, wantTitle string) *indexer.Release {
	var best *indexer.Release
	var bestScore, bestShape int
	for i := range releases {
		if books.EditionOf(releaseBookFormat(releases[i])) != kind {
			continue
		}
		score, ok := bookRelScore(sp, allowed, releases[i])
		if !ok {
			continue
		}
		shape := bookShape(releases[i].Title, wantTitle)
		if best == nil || nets.betterBook(releases[i], shape, score, *best, bestShape, bestScore) {
			best, bestScore, bestShape = &releases[i], score, shape
		}
	}
	return best
}

// Release shapes, best first.
const (
	shapeSingle     = iota // the book on its own
	shapeCollection        // the book bundled with others: an omnibus, a trilogy, a box set
	shapePartial           // one part of a split release
)

// bookShape sorts a release title into a shape. A collection still holds the book, so
// it stays grabbable when nothing else turns up, but it loses to the book on its own:
// a search for Shift took "The Wool Trilogy: Wool, Shift, Dust" over a copy of Shift
// with fewer seeders, the import rightly held it as not being Shift, and every Reject &
// find another chose a bundle again. A word the wanted title itself uses ("The Wool
// Trilogy") doesn't count against it.
func bookShape(title, wantTitle string) int {
	if isPartialBook(title) {
		return shapePartial
	}
	want := strings.ToLower(wantTitle)
	for _, m := range reBookCollection.FindAllString(title, -1) {
		if !strings.Contains(want, strings.ToLower(m)) {
			return shapeCollection
		}
	}
	return shapeSingle
}

// reBookCollection matches the words trackers use for a multi-book release.
var reBookCollection = regexp.MustCompile(`(?i)\b(?:omnibus|duology|trilogy|quartet|quadrilogy|box\s*set|boxset|collection|complete\s+series|books?\s*\d+\s*(?:-|–|to)\s*\d+)\b`)

// betterBook reports whether a candidate rel (shape, score) should replace the current
// best (bshape, bs): the better shape always wins, then the higher score (beats: seeders
// only between two torrents). This keeps a "(Part 1 of 6)" split from being grabbed over
// the complete book, and a trilogy over the one book that was asked for.
func (p netPolicy) betterBook(rel indexer.Release, shape, score int, best indexer.Release, bshape, bs int) bool {
	if shape != bshape {
		return shape < bshape
	}
	return p.beats(rel, score, best, bs)
}

// rePartialBook matches the "N of M" / "Part N of M" markers trackers use for a
// split release (e.g. MyAnonaMouse's "(Part 3 of 6)", "(2of5)").
var rePartialBook = regexp.MustCompile(`(?i)\b(?:part\s*)?\d+\s*of\s*\d+\b`)

// isPartialBook reports whether a release title looks like one part of a split
// set rather than the complete book.
func isPartialBook(title string) bool { return rePartialBook.MatchString(title) }

// releaseBookFormat prefers the indexer's structured file type (e.g. MyAnonaMouse
// exposes it directly) and falls back to parsing the release title for trackers
// that only put the format in the name.
func releaseBookFormat(rel indexer.Release) string {
	if f := strings.ToUpper(rel.Format); f != "" && books.EditionOf(f) != "" {
		return f
	}
	return detectBookFormat(rel.Title)
}

// bookScoreText is the text keyword and reject rules are matched against. It
// spans the structured fields — not just the title — so a "GraphicAudio"
// preference matches the narrator field even when the release name doesn't
// contain it (which is exactly how MyAnonaMouse presents full-cast productions).
func bookScoreText(rel indexer.Release) string {
	return strings.Join([]string{rel.Title, rel.Author, rel.Narrator, rel.Series, rel.Description}, " ")
}

// bookRelScore ranks a book release under a profile — format score plus keyword
// score (so preferences like "graphic audio +100" combine with format
// preference), seeders as the low-order tiebreak. ok=false when the format isn't
// wanted (score ≤ 0 or unknown), the release states a language outside allowed, or a
// hard-reject term is present.
func bookRelScore(sp quality.StoredProfile, allowed bookLangs, rel indexer.Release) (int, bool) {
	score, code := bookRelVerdict(sp, allowed, rel)
	return score, code == ""
}

// bookRelVerdict is bookRelScore saying why a release was turned down: DropNotWanted for
// a format the profile doesn't want (or can't tell), DropWrongLanguage for a language
// outside the book languages, quality.RejectTerm for a reject term, "" when it's
// acceptable. A search attempt counts by the code.
func bookRelVerdict(sp quality.StoredProfile, allowed bookLangs, rel indexer.Release) (int, string) {
	f := releaseBookFormat(rel)
	if f == "" {
		return 0, DropNotWanted
	}
	fs, ok := sp.FormatScores[f]
	if !ok || fs <= 0 {
		return 0, DropNotWanted
	}
	if !allowed.allows(rel.Language) {
		return 0, DropWrongLanguage
	}
	text := bookScoreText(rel)
	if quality.Rejects(sp.Rejected, text) {
		return 0, quality.RejectTerm
	}
	return (fs+quality.KeywordScore(sp.Keywords, text))*1_000_000 + rel.Seeders, ""
}

// noteBookVerdicts tells the search attempt what pickBestBookForKind makes of each
// release: the other edition, turned down by the profile (and why), or acceptable.
func noteBookVerdicts(n *searchNotes, sp quality.StoredProfile, allowed bookLangs, releases []indexer.Release, kind string) {
	if n == nil {
		return
	}
	for _, rel := range releases {
		if books.EditionOf(releaseBookFormat(rel)) != kind {
			n.mark(rel.Title, DropOutOfScope)
			continue
		}
		if _, code := bookRelVerdict(sp, allowed, rel); code != "" {
			n.mark(rel.Title, code)
		} else {
			n.mark(rel.Title, classEligible)
		}
	}
}

// bookProfile resolves the book's quality profile the way every other path does — its
// own, or the default book profile when it has none or it was deleted — and falls back
// to a sensible ebook-preferring set of scores only when no book profile exists at all.
func (c *Coordinator) bookProfile(ctx context.Context, profileRef string) quality.StoredProfile {
	ref := c.effectiveProfile(ctx, profileRef, quality.MediaBook)
	if sp, err := c.quality.GetStored(ctx, ref); err == nil && len(sp.FormatScores) > 0 {
		return sp
	}
	return quality.StoredProfile{FormatScores: map[string]int{"EPUB": 40, "AZW3": 30, "MOBI": 20, "PDF": 10}}
}

// ImportBookDownloads imports finished book downloads: for each completed torrent in
// the book category, match it to a book, group its files by edition (ebook vs
// audiobook), and hardlink each edition into the library.
func (c *Coordinator) ImportBookDownloads(ctx context.Context) {
	if c.books == nil || c.imp == nil {
		return
	}
	completed, err := c.downloads.CompletedInCategory(ctx, bookCategory)
	if err != nil {
		c.log.Warn("book import: couldn't list completed downloads — skipping this cycle", "err", err)
		return
	}
	for _, it := range completed {
		if it.ContentPath == "" {
			continue
		}
		// Without this the sweep re-hardlinked and re-marked every completed book
		// torrent every 30 seconds for as long as it seeded — pure disk/IO churn.
		if c.hashAlreadyImported(ctx, it.Hash) && !c.regrabbedForVersion(ctx, it.Hash) {
			continue
		}
		if c.hasReview(ctx, it.Hash) {
			continue // already held for review (or resolved) — don't re-flag or import
		}
		b, ok := c.books.MatchByRelease(ctx, it.Name)

		// If this download was grabbed for a specific book, verify its content really is
		// that book before importing. The target was otherwise re-derived from the torrent
		// name alone, so a release that resolves to a different book than the one we asked
		// for landed there silently — even though the grab row records the book id and info
		// hash needed to catch it. Mirrors the series import gate.
		if gid, idx, grabbed := c.grabbedMediaForHash(ctx, it.Hash, it.Name, "book"); grabbed {
			expected, gerr := c.books.Get(ctx, gid)
			// A release the admin picked by hand (interactive search or an uploaded
			// torrent) goes into the book it was picked for. The name gate exists to catch
			// the automation's mistakes; for a hand pick it only bounced releases whose
			// name spells the title differently (an accent, an alternate or translated
			// title) into Review. grabForce matches on the info hash alone, so a name
			// coincidence can never make an automatic grab look hand-picked.
			if gerr == nil && c.grabForce(ctx, it.Hash).On {
				b, ok = expected, true
			}
			if gerr == nil && (!ok || b.ID != expected.ID) {
				parsed := bookParsedTitle(it.Name)
				c.log.Warn("book import: download doesn't look like the book it was grabbed for — sending to review",
					"expected", expected.Title, "release", it.Name, "parsed_title", parsed)
				c.addReview(ctx, Review{
					Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "book", ReasonCode: ReasonMismatch,
					ExpectedID: expected.ID, ExpectedTitle: expected.Title, ParsedTitle: parsed,
					Reason:    fmt.Sprintf("Grabbed for %q but the download looks like %q", expected.Title, parsed),
					SizeBytes: it.SizeBytes, Indexer: idx,
				})
				continue
			}
		}
		if !ok {
			// The sweep runs every 30 seconds, so an unmatchable download used to be
			// rescanned in silence forever. Log once, and after enough attempts hand it
			// to review so a human sees it (same escalation as the series sweep).
			n := c.noteUnmatched(it.Hash)
			switch {
			case n == 1:
				c.log.Info("book import: no matching library book", "release", it.Name)
			case n == unmatchedReviewAfter:
				parsed := bookParsedTitle(it.Name)
				c.log.Warn("book import: download still matches no book — sending to review",
					"release", it.Name, "parsed_title", parsed, "attempts", n)
				c.addReview(ctx, Review{
					Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "book", ReasonCode: ReasonUnmatched,
					ParsedTitle: parsed, SizeBytes: it.SizeBytes,
					Reason: fmt.Sprintf("Parsed as %q, which matches no book in your library", parsed),
				})
			}
			continue
		}
		imported, hadFiles, waiting := c.importBookContentWaiting(ctx, b, it.ContentPath, it.Hash, it.Name)
		switch {
		case waiting:
			// A book it belongs to is being changed; the next sweep tries again. Said once,
			// when the book was first found busy.
		case imported:
			// At least one edition actually landed — drop it from the downloads view.
			c.recordImportedHash(ctx, it.Hash, it.Name, it.SizeBytes)
		case hadFiles:
			// Files were found but every import failed. Recording the hash here would
			// make the sweep skip this torrent forever while the book stays missing;
			// leave it unrecorded so the next sweep retries.
			c.log.Warn("book import: found files but no edition imported — will retry next sweep",
				"book", b.Title, "release", it.Name)
		default:
			// No ebook or audiobook files at all — a still-archived release we couldn't
			// extract, or content the container can't read. This case had no branch, so the
			// sweep did nothing whatsoever: no log, no blocklist, no review, retried every
			// 30 seconds forever while the book showed as still missing.
			// >= rather than ==: this counter is shared with the "no matching book" branch
			// above, so a download that sat unmatched until the user added its book would
			// already be past the threshold and would otherwise never escalate. Escalating
			// repeatedly is impossible — the hasReview check at the top of the loop skips
			// this download once a review exists.
			n := c.noteUnmatched(it.Hash)
			switch {
			case n == 1:
				c.log.Warn("book import: download holds no ebook or audiobook files",
					"book", b.Title, "release", it.Name, "path", it.ContentPath)
			case n >= unmatchedReviewAfter:
				c.log.Warn("book import: still nothing importable — sending to review",
					"book", b.Title, "release", it.Name, "attempts", n)
				c.addReview(ctx, Review{
					Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "book", ReasonCode: ReasonNoMedia,
					ExpectedID: b.ID, ExpectedTitle: b.Title, ParsedTitle: bookParsedTitle(it.Name),
					SizeBytes: it.SizeBytes,
					Reason:    "Downloaded but holds no ebook or audiobook files — still archived, or unreadable",
				})
			}
		}
	}
}

// importBookContent groups a download's files by edition and imports each into b. Shared by
// the automatic sweep and the review "import anyway" path so the two can't drift.
//
// imported reports whether any edition actually landed; hadFiles reports whether the
// download held any recognisable book files at all — the caller needs to tell "import
// failed, retry" apart from "nothing here to import".
func (c *Coordinator) importBookContent(ctx context.Context, b books.Book, contentPath, hash, name string) (imported, hadFiles bool) {
	imported, hadFiles, _ = c.importBookContentWaiting(ctx, b, contentPath, hash, name)
	return imported, hadFiles
}

// importBookContentWaiting is importBookContent that also reports waiting: a book the
// download's files belong to was being changed (a rename, a merge), so the download is
// left for the next sweep — which says nothing more about it each time.
func (c *Coordinator) importBookContentWaiting(ctx context.Context, b books.Book, contentPath, hash, name string) (imported, hadFiles, waiting bool) {
	// Scene ebook and audiobook packs routinely ship inside a RAR set. Without this the
	// archive yielded no recognised book files and the download was never imported.
	if fi, err := os.Stat(contentPath); err == nil && fi.IsDir() {
		if n, xerr := extract.ExtractTree(contentPath); xerr != nil {
			c.log.Warn("book import: archive extraction failed", "path", contentPath, "err", xerr)
		} else if n > 0 {
			c.log.Info("book import: extracted archives before import", "count", n, "path", contentPath)
		}
	}
	found := library.FindBookFiles(contentPath)
	// A "Red Rising" grab routinely turns out to be the whole trilogy, and every file in
	// it used to be hardlinked into Red Rising's folder — three books filed as one, with
	// the other two still showing as missing. Split off anything that names a DIFFERENT
	// book in the library and import it where it belongs.
	found, diverted, waiting := c.divertForeignBookFiles(ctx, b, found, hash, name)
	if waiting {
		// Another book in the pack is being changed (a rename, a merge): its files wait
		// for the next sweep, and so does the download — counting it imported now would
		// leave that book's files behind for good. What did import is found again.
		return false, true, true
	}

	var ebooks, audio []library.FoundFile
	for _, f := range found {
		if library.IsAudiobookFile(f.Path) {
			audio = append(audio, f)
		} else {
			ebooks = append(ebooks, f)
		}
	}
	hadFiles = len(ebooks) > 0 || len(audio) > 0
	// Containment heuristic: audiobook releases routinely ship a companion PDF
	// (artwork/booklet) next to the M4B. Importing that PDF as the book's EBOOK
	// edition satisfied the edition forever, so a real EPUB was never searched
	// again. A download holding BOTH kinds is an audiobook release: import only
	// the audio and leave the ebook edition unclaimed. Ebook-only and audio-only
	// downloads import as before.
	if len(audio) > 0 && len(ebooks) > 0 {
		skipped := make([]string, 0, len(ebooks))
		for _, f := range ebooks {
			skipped = append(skipped, filepath.Base(f.Path))
		}
		c.log.Info("book import: audiobook release carries ebook-extension companion files — importing audio only, not claiming the ebook edition",
			"book", b.Title, "release", name, "skipped", strings.Join(skipped, ", "))
		ebooks = nil
	}
	if !hadFiles && len(diverted) > 0 {
		// Everything in the download belonged to other books and has been filed there.
		// This used to report "nothing imported, nothing here", so the sweep never marked
		// the download done and re-filed the other book every 30 seconds until it went to
		// review — whose Import then failed the same way. The download is finished with:
		// say so, stop searching this release for b, and let seeding rules take it.
		c.finishForeignOnlyDownload(ctx, b, hash, name, diverted)
		return true, true, false
	}
	errEbook := c.importEdition(ctx, b, books.KindEbook, ebooks, hash, name)
	var errAudio error
	if v := c.audioVersionForDownload(ctx, b, hash, name); v != nil && len(audio) > 0 {
		errAudio = c.importVersion(ctx, b, *v, audio, hash, name)
	} else {
		errAudio = c.importEdition(ctx, b, books.KindAudiobook, audio, hash, name)
	}
	if errors.Is(errEbook, ErrBookBusy) || errors.Is(errAudio, ErrBookBusy) {
		return false, true, true // the book is being changed: the whole download waits for the next sweep
	}
	return errEbook == nil || errAudio == nil, hadFiles, false
}

// divertForeignBookFiles pulls the files that belong to a DIFFERENT library book out of a
// download and imports them into that book, returning what's left for b.
//
// Multi-book packs are the norm on book trackers — a series grab hands back the whole
// trilogy — and importing the lot under the book that was asked for both mis-files two of
// them AND leaves them counted as missing, so the searcher keeps hunting for what's
// already on disk.
//
// The bar for diverting is deliberately high: a file moves only when it names a specific
// OTHER book that is already in the library. Anything ambiguous — generic chapter files,
// "Part 1 of 2", a book not in the library — stays with b, which is the existing
// behaviour and the safe one for a single title split across many files.
//
// It also returns the titles of the other books that actually received files, so the
// caller can tell a download that held nothing at all from one that held only other books.
func (c *Coordinator) divertForeignBookFiles(ctx context.Context, b books.Book, found []library.FoundFile, hash, name string) (keep []library.FoundFile, diverted []string, waiting bool) {
	if len(found) < 2 || c.books == nil {
		return found, nil, false // a single file is whatever the download was matched as
	}
	// One audiobook file and at most one ebook-type file (the usual companion PDF) is one
	// book, whatever else its name mentions. "Fire & Blood … 300 Years Before A Game of
	// Thrones.m4b" plus its PDF was split off to A Game of Thrones, leaving Fire & Blood
	// with nothing.
	var nAudio, nEbook int
	for _, f := range found {
		if library.IsAudiobookFile(f.Path) {
			nAudio++
		} else {
			nEbook++
		}
	}
	if nAudio <= 1 && nEbook <= 1 {
		return found, nil, false
	}
	// The file matcher goes by the title a file leads with, not the longest title in it.
	match := c.books.FileMatcher(ctx)
	keep = make([]library.FoundFile, 0, len(found))
	foreign := map[int64][]library.FoundFile{}
	titles := map[int64]books.Book{}
	for _, f := range found {
		// Match on the file's own name AND its parent folder: a pack usually separates the
		// books into folders and names the files generically inside them.
		other, ok := matchBookFile(match, f.Path)
		if !ok || other.ID == b.ID {
			keep = append(keep, f)
			continue
		}
		foreign[other.ID] = append(foreign[other.ID], f)
		titles[other.ID] = other
	}
	for id, files := range foreign {
		other := titles[id]
		c.log.Info("book import: pack holds another library book — importing it into its own folder",
			"grabbed_for", b.Title, "also_holds", other.Title, "files", len(files), "release", name)
		var ebooks, audio []library.FoundFile
		for _, f := range files {
			if library.IsAudiobookFile(f.Path) {
				audio = append(audio, f)
			} else {
				ebooks = append(ebooks, f)
			}
		}
		if len(audio) > 0 && len(ebooks) > 0 {
			ebooks = nil // same companion-PDF rule the main path applies
		}
		errE := c.importEdition(ctx, other, books.KindEbook, ebooks, hash, name)
		errA := c.importEdition(ctx, other, books.KindAudiobook, audio, hash, name)
		if errors.Is(errE, ErrBookBusy) || errors.Is(errA, ErrBookBusy) {
			waiting = true
		}
		if errE == nil || errA == nil {
			diverted = append(diverted, other.Title)
		}
	}
	sort.Strings(diverted)
	return keep, diverted, waiting
}

// matchBookFile resolves one file inside a pack to a library book, trying its own name
// first and then its containing folder — a pack commonly puts each book in a folder and
// names the files inside it generically ("01 - Chapter 1.mp3").
func matchBookFile(match func(string) (books.Book, bool), path string) (books.Book, bool) {
	if b, ok := match(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))); ok {
		return b, true
	}
	if dir := filepath.Base(filepath.Dir(path)); dir != "." && dir != string(filepath.Separator) {
		if b, ok := match(dir); ok {
			return b, true
		}
	}
	return books.Book{}, false
}

// pickScanMatch chooses the lookup result that really is the book in this folder.
//
// A metadata search is a fuzzy, ranked guess: "Pierce Brown Golden Son" can rank Red Rising
// first, because the author and series match and the provider weights popularity. Accepting
// the top hit unconditionally files a folder under whichever book the provider felt like
// naming, and when that book is already in the library the scan looks like it succeeded.
//
// So require the titles to actually correspond. Exact key equality first; failing that, a
// result whose title contains the folder's (an edition subtitled "Golden Son: Red Rising
// Book 2" is still Golden Son) — never the reverse, or "Dune" would swallow a folder
// holding "Dune Messiah".
func pickScanMatch(folderTitle, author string, results []metadata.BookResult) (metadata.BookResult, bool) {
	want := parser.TitleKey(folderTitle)
	if want == "" {
		return metadata.BookResult{}, false
	}
	// A guide, summary or workbook ABOUT the book is not the book — unless that is what
	// the folder itself says it holds.
	guide := scanGuideRe.MatchString(folderTitle)
	pick := func(accept func(key string) bool) (metadata.BookResult, bool) {
		var first *metadata.BookResult
		for i := range results {
			r := &results[i]
			k := parser.TitleKey(r.Title)
			if k == "" || !accept(k) {
				continue
			}
			if !guide && scanGuideRe.MatchString(r.Title) {
				continue
			}
			// The folder's author, when it has one, settles a title several people used.
			if author != "" && r.Author != "" && books.AuthorsOverlap(author, r.Author) {
				return *r, true
			}
			if first == nil {
				first = r
			}
		}
		if first != nil {
			return *first, true
		}
		return metadata.BookResult{}, false
	}
	if r, ok := pick(func(k string) bool { return k == want }); ok {
		return r, true
	}
	// A subtitled or series-tagged edition starts with the folder's title ("Golden Son:
	// Red Rising Book 2"). Merely containing it is not enough: that is how a folder
	// named "Harry Potter and the Goblet of Fire by J.K. Rowling" became a study guide
	// whose title happened to contain the whole phrase.
	return pick(func(k string) bool { return strings.HasPrefix(k, want) })
}

// scanGuideRe marks a title as being about a book rather than the book.
var scanGuideRe = regexp.MustCompile(`(?i)\b(?:guide|summary|summaries|study|analysis|notes|workbook|companion|lesson|lessons|sparknotes|cliffsnotes|litcharts)\b`)

// bookParsedTitle reduces a release name to a readable title guess for a review row:
// separators become spaces and format tags are stripped.
func bookParsedTitle(name string) string {
	t := strings.NewReplacer(".", " ", "_", " ").Replace(name)
	t = reBookFormat.ReplaceAllString(t, "")
	return strings.Join(strings.Fields(t), " ")
}

// importBookEdition hardlinks one edition's files into the library and records it,
// reporting whether the edition actually imported — callers must only mark the
// download handled when something did.
func (c *Coordinator) importBookEdition(ctx context.Context, b books.Book, kind string, files []library.FoundFile, infoHash, downloadName string) bool {
	return c.importEdition(ctx, b, kind, files, infoHash, downloadName) == nil
}

// errNothingToImport is an import handed no files of the kind.
var errNothingToImport = errString("no files to import")

// importEdition is importBookEdition saying why it didn't: ErrBookBusy when another change
// to the book's files is running (the next sweep retries it).
func (c *Coordinator) importEdition(ctx context.Context, b books.Book, kind string, files []library.FoundFile, infoHash, downloadName string) error {
	if len(files) == 0 {
		return errNothingToImport
	}
	unlock, ok := c.tryLockBook(b.ID)
	if !ok {
		if c.firstBusy(b.ID) {
			c.log.Info("book: import waits — the book's files are being changed", "title", b.Title, "edition", kind)
		}
		return ErrBookBusy
	}
	defer unlock()
	// Checked under the lock, so nothing about the book changes between the check and
	// the import.
	name := bookName(b, nil, kind == books.KindAudiobook)
	if err := c.bookImportRefusal(ctx, b, name, files); err != nil {
		c.log.Warn("book: import failed", "title", b.Title, "edition", kind, "err", err)
		return err
	}
	bi, err := c.imp.Using(ctx).ImportBook(name, files)
	if err != nil {
		c.log.Warn("book: import failed", "title", b.Title, "edition", kind, "err", err)
		return err
	}
	if err := c.books.MarkImported(ctx, b.ID, kind, bi.TargetPath, bi.Format, bi.SizeBytes, bi.FileCount); err != nil {
		c.log.Warn("book: recording the imported edition failed", "title", b.Title, "edition", kind, "err", err)
		return err
	}
	c.log.Info("book: imported", "title", b.Title, "edition", kind, "format", bi.Format, "files", bi.FileCount)
	// Say so when the release was hand-picked: it skipped the name check, so the
	// timeline is where a mis-click shows up.
	c.books.AddEvent(ctx, b.ID, "imported", bookImportDetail(kind, bi.Format, bi.FileCount, downloadName, c.grabForce(ctx, infoHash).On))
	c.markBookGrabImported(ctx, b.ID, infoHash, downloadName) // flip THIS grab (not siblings) for seed cleanup
	c.bookImported(ctx, b.ID, kind)
	c.bus.Publish("book.imported", map[string]any{
		"title": b.Title, "id": b.ID, "edition": kind, "author": b.Author, "year": b.Year, "format": bi.Format, "size_bytes": bi.SizeBytes,
	})
	return nil
}

// GrabForBook grabs a chosen release for a book (interactive search) into the book
// category. versionID > 0 files the download as that audiobook version.
func (c *Coordinator) GrabForBook(ctx context.Context, bookID, versionID int64, indexerName, downloadURL, title string) error {
	label := ""
	if versionID > 0 {
		v, err := c.books.GetAudioVersion(ctx, bookID, versionID)
		if err != nil {
			return err
		}
		label = v.Label
	}
	hash, err := c.grabTo(ctx, indexerName, downloadURL, title, bookCategory)
	if err != nil {
		return err
	}
	if b, err := c.books.Get(ctx, bookID); err == nil {
		c.recordBookGrab(ctx, bookID, versionID, title, indexerName, b.QualityProfile, hash)
		// The admin chose this release for this book: import it there even when its
		// name doesn't spell the library title the same way.
		c.markGrabManual(ctx, hash)
	}
	what := "Grabbed by hand from " + indexerName + ": " + title
	if label != "" {
		what = fmt.Sprintf("Grabbed by hand as the %q audiobook from %s: %s", label, indexerName, title)
	}
	c.books.AddEvent(ctx, bookID, "grabbed", what)
	return nil
}

// RankBookReleases runs an interactive search for a book and returns ebook + audiobook
// releases ranked by format preference (no grabbing).
func (c *Coordinator) RankBookReleases(ctx context.Context, bookID int64) (ReleaseList, error) {
	if c.books == nil {
		return ReleaseList{}, nil
	}
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return ReleaseList{}, err
	}
	sp := c.bookProfile(ctx, b.QualityProfile)
	allowed := c.bookLanguages(ctx)
	// Both passes (and each pass's title-only retry) report into one collector.
	ctx, notes := withSearchNotes(ctx)
	// Dedup by download URL — the unique per-torrent link. Deduping by title
	// wrongly collapsed distinct editions that render the same display name (e.g. a
	// GraphicAudio M4B and a standard-narration M4B both "<Author> - <Title> [M4B]"),
	// hiding valid options.
	seen := map[string]bool{}
	var all []indexer.Release
	// Two passes: everything, then audiobooks specifically. The edition travels as a field
	// rather than a suffix on the text, so each indexer narrows it the way its own API can
	// — a category on a book tracker, an extra query word on a general one. Appending the
	// word unconditionally made the second pass return nothing at all on MyAnonaMouse.
	answered := false
	var searchErr error
	for _, edition := range []string{"", books.KindAudiobook} {
		res, err := c.searchBook(ctx, b, edition)
		if err != nil {
			searchErr = err
			continue
		}
		answered = true
		for _, rel := range res.Releases {
			key := rel.DownloadURL
			if key == "" {
				key = rel.Title
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, rel)
		}
	}
	// Neither pass could run: say why rather than show an empty list that reads as
	// "this book doesn't exist anywhere".
	if !answered && searchErr != nil {
		if allIndexersFailed(searchErr) {
			return ReleaseList{Profile: b.QualityProfile}.withIssues(notes), nil
		}
		return ReleaseList{}, searchErr
	}

	// Score each release once (keyword scoring now spans narrator/series/author,
	// so a GraphicAudio preference actually fires) and keep the score for ordering.
	nets := c.networks(ctx)
	type ranked struct {
		rr       RankedRelease
		rel      indexer.Release
		score    int
		eligible bool
		partial  bool
		version  *books.AudioVersion // the audiobook version this release belongs to
	}
	items := make([]ranked, 0, len(all))
	for _, rel := range all {
		f := releaseBookFormat(rel)
		if f == "" {
			continue // not an identifiable book release
		}
		edition := books.EditionOf(f)
		score, eligible := bookRelScore(sp, allowed, rel)
		if edition == books.KindAudiobook {
			// A version's release is scored the way its own search scores it: audio
			// always acceptable, and its own words not vetoed by a profile reject.
			if v := books.VersionFor(b.AudioVersions, bookScoreText(rel)); v != nil {
				score, eligible = bookRelScore(versionProfile(sp, *v), allowed, rel)
			}
		}
		// Another language is listed, not hidden: the automation won't take it, but the
		// admin may want exactly that edition and can grab it by hand.
		var reject string
		if !allowed.allows(rel.Language) {
			reject = bookLangRejectReason(rel.Language)
		}
		if !nets.canGrab(rel) {
			eligible = false // no client can take it, so it is never the recommended pick
		}
		narrator := rel.Narrator
		if narrator == "" && edition == books.KindAudiobook {
			narrator = parseNarrator(rel.Title + " " + rel.Description)
		}
		items = append(items, ranked{
			rr: withIndexerFacts(RankedRelease{
				Title: rel.Title, Indexer: rel.Indexer, DownloadURL: rel.DownloadURL, InfoHash: rel.InfoHash,
				InfoURL: safeInfoURL(rel.InfoURL, rel.DownloadURL), SizeGB: rel.SizeGB(),
				Seeders: rel.Seeders, Summary: summarizeBook(f), RejectReason: reject,
				Eligible: eligible, Edition: edition, Format: f, Narrator: narrator,
				Author: rel.Author, Series: rel.Series, Language: rel.Language,
			}, rel, nets),
			rel: rel,
			version: func() *books.AudioVersion {
				if edition != books.KindAudiobook {
					return nil
				}
				return books.VersionFor(b.AudioVersions, bookScoreText(rel))
			}(),
			score: score, eligible: eligible, partial: isPartialBook(rel.Title),
		})
	}
	// Order by the profile's preference: eligible first, then complete before
	// split ("Part N of M") releases, then by combined score.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].eligible != items[j].eligible {
			return items[i].eligible
		}
		if items[i].partial != items[j].partial {
			return !items[i].partial
		}
		return nets.beats(items[i].rel, items[i].score, items[j].rel, items[j].score)
	})
	// Mark the best eligible release of each edition — and of each audiobook version —
	// as the recommended pick.
	recommended := map[string]bool{}
	out := make([]RankedRelease, 0, len(items))
	for i := range items {
		rr := items[i].rr
		group := rr.Edition
		if v := items[i].version; v != nil {
			rr.VersionID, rr.Version = v.ID, v.Label
			group = fmt.Sprintf("%s:%d", rr.Edition, v.ID)
		}
		if items[i].eligible && !recommended[group] {
			rr.Recommended = true
			recommended[group] = true
		}
		out = append(out, rr)
	}
	return ReleaseList{Profile: b.QualityProfile, Releases: out}.withIssues(notes), nil
}

// reNarrator pulls a narrator name from an audiobook release title or description
// ("Narrated by Michael Kramer", "Read by Kate Reading", "Narrator: …").
var reNarrator = regexp.MustCompile(`(?i)(?:narrated by|read by|narrator[:\s]+)\s*([A-Z][\p{L}.'-]+(?:\s+(?:&|and|,)?\s*[A-Z][\p{L}.'-]+){0,3})`)

// parseNarrator returns the first narrator credit found in text, or "".
func parseNarrator(text string) string {
	m := reNarrator.FindStringSubmatch(text)
	if len(m) > 1 {
		return strings.TrimSpace(strings.Trim(m[1], " ,&"))
	}
	return ""
}

func summarizeBook(format string) string {
	edition := "Ebook"
	if books.IsAudiobookFormat(format) {
		edition = "Audiobook"
	}
	return edition + " · " + format
}

// RescanBook re-reads the book's library folder and records the ebook/audiobook
// editions present. A multi-file audiobook is recorded as its folder + a file count.
func (c *Coordinator) RescanBook(ctx context.Context, bookID int64) error {
	if c.books == nil || c.imp == nil {
		return errBooksNotReady
	}
	unlock, ok := c.tryLockBook(bookID)
	if !ok {
		return ErrBookBusy
	}
	defer unlock()
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return err
	}
	keep, err := c.notOthersFilter(ctx, editionKey(b.ID, 0, books.KindEbook), editionKey(b.ID, 0, books.KindAudiobook))
	if err != nil {
		c.log.Warn("book rescan: couldn't read the library", "book_id", bookID, "err", err)
		return err
	}
	var ebooks, audio []library.FoundFile
	for _, f := range keepFiles(c.imp.BookLibraryFiles(bookName(b, nil, true), editionFormat(b.Ebook), editionFormat(b.Audiobook)), keep) {
		if library.IsAudiobookFile(f.Path) {
			audio = append(audio, f)
		} else {
			ebooks = append(ebooks, f)
		}
	}
	c.recordEdition(ctx, bookID, books.KindEbook, ebooks)
	c.recordEdition(ctx, bookID, books.KindAudiobook, audio)
	c.rescanAudioVersions(ctx, b)
	return nil
}

// recordEdition marks an edition present from on-disk files: a single file uses its
// path; multiple files use their shared folder + the file count.
func (c *Coordinator) recordEdition(ctx context.Context, bookID int64, kind string, files []library.FoundFile) {
	if len(files) == 0 {
		return
	}
	if len(files) == 1 {
		f := files[0]
		_ = c.books.MarkImported(ctx, bookID, kind, f.Path, library.BookFileFormat(f.Path), f.Size, 1)
		c.books.AddEvent(ctx, bookID, "imported", "Found the "+kind+" edition during a library scan ("+library.BookFileFormat(f.Path)+")")
		return
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	dir := filepath.Dir(files[0].Path)
	_ = c.books.MarkImported(ctx, bookID, kind, dir, library.BookFileFormat(files[0].Path), total, len(files))
	c.books.AddEvent(ctx, bookID, "imported", fmt.Sprintf("Found the %s edition during a library scan (%s, %d files)", kind, library.BookFileFormat(files[0].Path), len(files)))
}

// BookImportCandidate is an on-disk book file that can be manually imported.
type BookImportCandidate struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	Edition   string `json:"edition"` // ebook | audiobook
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
}

// BookImportCandidates lists importable book files under dir, up to
// library.ListMaxResults; truncated says there were more (or the walk was stopped). It
// stops early when ctx ends — the caller checks ctx.Err().
func (c *Coordinator) BookImportCandidates(ctx context.Context, dir string) ([]BookImportCandidate, bool) {
	files, truncated, _ := library.FindBookFilesCtx(ctx, dir, library.ListMaxResults, library.ListMaxVisited)
	out := make([]BookImportCandidate, 0, len(files))
	for _, f := range files {
		edition := books.KindEbook
		if library.IsAudiobookFile(f.Path) {
			edition = books.KindAudiobook
		}
		out = append(out, BookImportCandidate{
			Path: f.Path, Filename: filepath.Base(f.Path), Edition: edition,
			Format: library.BookFileFormat(f.Path), SizeBytes: f.Size,
		})
	}
	return out, truncated
}

// ManualImportBook imports one on-disk file into a book as the correct edition, or as
// an audiobook version when versionID > 0.
func (c *Coordinator) ManualImportBook(ctx context.Context, bookID, versionID int64, path string) error {
	if versionID > 0 {
		return c.ManualImportAudioVersion(ctx, bookID, versionID, path)
	}
	if c.books == nil || c.imp == nil {
		return errBooksNotReady
	}
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return err
	}
	kind := books.KindEbook
	if library.IsAudiobookFile(path) {
		kind = books.KindAudiobook
	}
	unlock, ok := c.tryLockBook(bookID)
	if !ok {
		return ErrBookBusy
	}
	defer unlock()
	name := bookName(b, nil, kind == books.KindAudiobook)
	files := []library.FoundFile{{Path: path}}
	if err := c.bookImportRefusal(ctx, b, name, files); err != nil {
		return err
	}
	bi, err := c.imp.ImportBook(name, files)
	if err != nil {
		return err
	}
	if err := c.books.MarkImported(ctx, bookID, kind, bi.TargetPath, bi.Format, bi.SizeBytes, bi.FileCount); err != nil {
		return err
	}
	c.books.AddEvent(ctx, bookID, "imported", "Imported the "+kind+" edition by hand ("+bi.Format+") from "+filepath.Base(path))
	// The same follow-up as an automatic import: its requester hears, and a new
	// audiobook shows up in listening apps straight away.
	c.bookImported(ctx, bookID, kind)
	return nil
}

// DeleteBookEdition removes an edition's file(s) from disk and forgets the edition.
func (c *Coordinator) DeleteBookEdition(ctx context.Context, bookID int64, kind string) error {
	if c.books == nil {
		return errBooksNotReady
	}
	unlock, ok := c.tryLockBook(bookID)
	if !ok {
		return ErrBookBusy
	}
	defer unlock()
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return err
	}
	var bf *books.BookFile
	if kind == books.KindAudiobook {
		bf = b.Audiobook
	} else {
		bf = b.Ebook
	}
	// Even a delete that stops part way has moved files; the audiobook catalogue re-reads.
	defer c.bookFilesChanged(ctx, bookID)
	if bf != nil && bf.Path != "" {
		if fi, err := os.Stat(bf.Path); err == nil && fi.IsDir() {
			// Multi-file edition in a (possibly shared) folder — remove only this
			// edition's files, never the sibling edition's.
			var paths []string
			for _, f := range library.FindBookFiles(bf.Path) {
				if library.IsAudiobookFile(f.Path) == (kind == books.KindAudiobook) {
					paths = append(paths, f.Path)
				}
			}
			if err := c.removeBookFiles(ctx, bookID, paths); err != nil {
				return err
			}
			_ = os.Remove(bf.Path) // succeeds only if now empty
		} else {
			if err := c.removeBookFile(bf.Path); err != nil {
				return err
			}
			_ = os.Remove(filepath.Dir(bf.Path)) // prune if empty
		}
	}
	return c.books.ClearEdition(ctx, bookID, kind)
}

// removeBookFiles moves several of a book's files to the bin, checking the bin first so
// a broken one fails before anything moves. It stops at the first file the bin refuses,
// leaving the edition recorded, and notes on the book how far it got.
func (c *Coordinator) removeBookFiles(ctx context.Context, bookID int64, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := library.CheckBin(c.bin(), paths[0]); err != nil {
		return err
	}
	for i, p := range paths {
		if err := c.removeBookFile(p); err != nil {
			if i > 0 {
				c.books.AddEvent(ctx, bookID, "delete.failed",
					fmt.Sprintf("Stopped deleting after %d of %d file(s) went to the recycle bin: %v", i, len(paths), err))
			}
			return err
		}
	}
	return nil
}

// removeBookFile moves one book file to the recycle bin, or deletes it when the bin is
// deliberately off. If the bin refuses it, the file stays and the error says why — it
// never falls back to a permanent delete.
func (c *Coordinator) removeBookFile(path string) error {
	dst, err := library.RemoveToBin(c.bin(), path)
	if err != nil {
		c.log.Warn("book: the recycle bin refused a file — kept it", "path", path, "err", err)
		return err
	}
	if dst != "" {
		c.log.Info("book: moved to recycle bin", "from", path, "to", dst)
	}
	return nil
}

// BookScanResult summarizes a book library scan.
type BookScanResult struct {
	Imported  int      `json:"imported"`
	Skipped   int      `json:"skipped"`
	Unmatched []string `json:"unmatched,omitempty"`
}

// ScanBookLibrary catalogs books already in the library folder. Each book folder is
// matched to Open Library, added unmonitored, and assigned the profile that matches the
// editions found on disk (ebook-only → Ebook, audio-only → Audiobook, both → both).
//
// progress, when set, hears how many book folders have been looked up out of how many
// there are. A cancelled or timed-out scan stops between folders and returns an error
// saying how far it got: a book it started adding is finished (so it never sits in the
// library without the files the scan found for it), nothing after it is touched, and
// the folders whose lookups were cut short aren't reported as unrecognised.
func (c *Coordinator) ScanBookLibrary(ctx context.Context, ebookRoot, audiobookRoot string, progress func(done, total int)) (BookScanResult, error) {
	var res BookScanResult
	if c.books == nil || c.imp == nil || !c.books.MetadataAvailable() {
		return res, nil
	}
	if progress == nil {
		progress = func(int, int) {}
	}
	// By every key a library book has had, so a folder the catalogue still answers with
	// a book's old key backfills that book instead of trying to add it again.
	existing := map[string]books.Book{}
	var allBooks []books.Book
	if list, err := c.books.List(ctx); err == nil {
		allBooks = list
		existing = c.books.KeyIndex(ctx, list)
	}
	var folders []library.BookFolder
	if ebookRoot != "" || audiobookRoot != "" {
		folders = c.imp.FindBookFoldersIn(ebookRoot, audiobookRoot)
	} else {
		folders = c.imp.FindBookFolders()
	}

	// Group folders by the metadata book they match, so an ebook folder and an
	// audiobook folder for the same title — often in separate roots, sometimes with
	// different casing — become ONE book with both editions, not one edition each.
	type pending struct {
		match  metadata.BookResult
		ebooks []library.FoundFile
		audio  []library.FoundFile
	}
	byKey := map[string]*pending{}
	var order []string
	known := knownBookPaths(allBooks)
	stopped := func(done, total int, what string) (BookScanResult, error) {
		return res, fmt.Errorf("the scan stopped after %d of %d %s: %w", done, total, what, ctx.Err())
	}
	for i, bf := range folders {
		progress(i, len(folders))
		if ctx.Err() != nil {
			return stopped(i, len(folders), "book folders")
		}
		if bf.Title == "" || (len(bf.Ebooks) == 0 && len(bf.Audiobooks) == 0) {
			continue
		}
		// Every file here is already a book's: nothing to catalogue. Without this a folder
		// a naming scheme wrote ("02 - Golden Son" under a series folder) was looked up by
		// its name all over again, and listed as unmatched when the name didn't read back.
		if known.covers(bf) {
			res.Skipped++
			continue
		}
		// "<Title> (<Label>)" is one of a book's audiobook versions, not a book of its
		// own: the title lookup would prefix-match it to the book and, with no standard
		// audiobook on file yet, record the full-cast production as the standard one.
		if vb, v, ok := versionForScanFolder(bf.Title, allBooks); ok {
			if v.File == nil && len(bf.Audiobooks) > 0 {
				if !c.underBookLock(vb.ID, func() {
					c.recordVersionFiles(context.WithoutCancel(ctx), vb, v, bf.Audiobooks, "during a library scan")
				}) {
					res.Skipped++ // being changed right now: the next scan records it
					continue
				}
				res.Imported++
			} else {
				res.Skipped++
			}
			continue
		}
		// An ISBN in the folder or file names is an exact answer; try it before
		// guessing from the title.
		var match metadata.BookResult
		matched := false
		if isbn := scanISBN(bf); isbn != "" {
			if r, err := c.books.LookupISBN(ctx, isbn); err == nil && r != nil {
				match, matched = *r, true
				c.log.Info("book scan: matched by ISBN", "folder_title", bf.Title, "isbn", isbn, "book", r.Title)
			}
		}
		query := bf.Title
		if bf.Author != "" {
			query = bf.Author + " " + bf.Title
		}
		var results []metadata.BookResult
		if !matched {
			var err error
			results, err = c.books.Lookup(ctx, query)
			if ctx.Err() != nil {
				// Cut short, not unrecognised: say how far it got instead.
				return stopped(i, len(folders), "book folders")
			}
			if err != nil || len(results) == 0 {
				res.Unmatched = append(res.Unmatched, bf.Title)
				continue
			}
			match, matched = pickScanMatch(bf.Title, bf.Author, results)
		}
		if !matched {
			// Better to leave a folder uncatalogued and say so than to file it under the
			// wrong book. Taking results[0] on faith meant a lookup for "Golden Son" that
			// answered with its more famous series-mate silently folded that folder's
			// files into the WRONG book — and because that book was already in the
			// library, the scan reported nothing amiss and Golden Son never appeared.
			c.log.Info("book scan: no lookup result matched the folder's title — leaving it uncatalogued",
				"folder_title", bf.Title, "author", bf.Author, "best_guess", results[0].Title)
			res.Unmatched = append(res.Unmatched, bf.Title)
			continue
		}
		p := byKey[match.Key]
		if p == nil {
			p = &pending{match: match}
			byKey[match.Key] = p
			order = append(order, match.Key)
		}
		p.ebooks = append(p.ebooks, bf.Ebooks...)
		p.audio = append(p.audio, bf.Audiobooks...)
	}

	progress(len(folders), len(folders))
	for i, key := range order {
		if ctx.Err() != nil {
			return stopped(i, len(order), "matched books")
		}
		// Each book is finished once started: adding it and recording its files is one
		// step, so a cancel can't leave a book in the library without its editions.
		wctx := context.WithoutCancel(ctx)
		p := byKey[key]
		hasE, hasA := len(p.ebooks) > 0, len(p.audio) > 0
		if b, ok := existing[key]; ok {
			// Already in the library — backfill any edition present on disk but not
			// yet recorded (e.g. the ebook when only the audiobook was found before).
			merged := false
			if !c.underBookLock(b.ID, func() {
				if hasE && b.Ebook == nil {
					c.recordEdition(wctx, b.ID, books.KindEbook, p.ebooks)
					merged = true
				}
				if hasA && b.Audiobook == nil {
					c.recordEdition(wctx, b.ID, books.KindAudiobook, p.audio)
					merged = true
				}
			}) {
				res.Skipped++ // being changed right now: the next scan records it
				continue
			}
			if merged {
				// Widen the profile so both on-disk editions count as wanted/included.
				wantE := hasE || b.Ebook != nil
				wantA := hasA || b.Audiobook != nil
				_ = c.books.SetQualityProfile(wctx, b.ID, c.bookProfileFor(wctx, wantE, wantA))
				res.Imported++
				c.log.Info("book scan: added missing edition", "title", b.Title, "ebook", hasE, "audio", hasA)
			} else {
				res.Skipped++
			}
			continue
		}
		profile := c.bookProfileFor(wctx, hasE, hasA)
		added, err := c.books.Add(wctx, p.match.Key, profile, false, p.match)
		if errors.Is(err, books.ErrExists) && added.ID > 0 {
			// Same book, different catalogue key: file the editions under the existing row.
			if !c.underBookLock(added.ID, func() {
				if hasE && added.Ebook == nil {
					c.recordEdition(wctx, added.ID, books.KindEbook, p.ebooks)
				}
				if hasA && added.Audiobook == nil {
					c.recordEdition(wctx, added.ID, books.KindAudiobook, p.audio)
				}
			}) {
				res.Skipped++
				continue
			}
			res.Imported++
			c.log.Info("book scan: matched an existing book under another catalogue key", "title", added.Title, "key", p.match.Key)
			continue
		}
		if err != nil {
			res.Unmatched = append(res.Unmatched, p.match.Title)
			continue
		}
		c.underBookLock(added.ID, func() { // a new book: nothing else knows it yet
			c.recordEdition(wctx, added.ID, books.KindEbook, p.ebooks)
			c.recordEdition(wctx, added.ID, books.KindAudiobook, p.audio)
		})
		res.Imported++
		c.log.Info("book scan: imported", "title", added.Title, "ebooks", len(p.ebooks), "audio", len(p.audio))
	}
	return res, nil
}

// bookProfileFor returns the book profile ref whose wanted editions match the files
// found on disk, falling back to the default book profile. The same rule a book
// request's Read / Listen / Both choice goes through.
func (c *Coordinator) bookProfileFor(ctx context.Context, wantEbook, wantAudio bool) string {
	return c.quality.BookProfileFor(ctx, wantEbook, wantAudio)
}

// BookFileEntry is one file inside an edition (for the collapsible file list).
type BookFileEntry struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// EditionFiles lists the individual files of an edition (a multi-file audiobook shows
// all its chapter files).
func (c *Coordinator) EditionFiles(ctx context.Context, bookID int64, kind string) []BookFileEntry {
	if c.books == nil {
		return nil
	}
	b, err := c.books.Get(ctx, bookID)
	if err != nil {
		return nil
	}
	var bf *books.BookFile
	if kind == books.KindAudiobook {
		bf = b.Audiobook
	} else {
		bf = b.Ebook
	}
	if bf == nil {
		return nil
	}
	if fi, err := os.Stat(bf.Path); err == nil && fi.IsDir() {
		files := library.FindBookFiles(bf.Path)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		out := make([]BookFileEntry, 0, len(files))
		for _, f := range files {
			// The ebook and audiobook editions can share a folder — only list files of
			// the requested edition's kind.
			if library.IsAudiobookFile(f.Path) != (kind == books.KindAudiobook) {
				continue
			}
			out = append(out, BookFileEntry{Name: filepath.Base(f.Path), SizeBytes: f.Size})
		}
		return out
	}
	return []BookFileEntry{{Name: filepath.Base(bf.Path), SizeBytes: bf.SizeBytes}}
}

// MergeAudiobookAvailable reports whether ffmpeg is present for the merge feature.
func (c *Coordinator) MergeAudiobookAvailable() bool { return audiobook.Available() }

// sortNatural orders paths the way a person reads chapter numbers: digit runs compare as
// numbers, everything else byte-wise and case-insensitively. "Chapter 2" before
// "Chapter 10", "Part 1 - 09" before "Part 1 - 10".
//
// Audiobook rips number their files however they like — padded, unpadded, prefixed,
// suffixed — and the ONE thing the merge cannot get wrong is the order, because the
// sources are deleted once it succeeds.
func sortNatural(paths []string) {
	sort.Slice(paths, func(i, j int) bool { return naturalLess(paths[i], paths[j]) })
}

// naturalLess compares two strings treating consecutive digits as one number.
func naturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(la) && j < len(lb) {
		ca, cb := la[i], lb[j]
		if isDigit(ca) && isDigit(cb) {
			// Compare the whole digit runs as numbers. Leading zeros don't change the
			// value, so "09" and "9" tie here and fall through to the next difference.
			si, sj := i, j
			for i < len(la) && isDigit(la[i]) {
				i++
			}
			for j < len(lb) && isDigit(lb[j]) {
				j++
			}
			na := strings.TrimLeft(la[si:i], "0")
			nb := strings.TrimLeft(lb[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb) // more digits = bigger number
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	// One is a prefix of the other, or they're equal ignoring case — shorter first, then
	// fall back to the raw bytes so the order is total and stable.
	if len(la)-i != len(lb)-j {
		return len(la)-i < len(lb)-j
	}
	return a < b
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func sanitizeName(s string) string {
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(repl.Replace(s))
}

// pendingBookGrabTitles returns the normalized titles of a book's still-pending grabs
// (grabbed but not yet imported or failed) — the DB-backed twin of the queue-based
// bookDownloading guard, mirroring pendingGrabTitles for movies. When the download
// client's queue can't be read, or a torrent's name doesn't match back to the book,
// this still stops the same release being grabbed again. Bounded to a day so a grab
// stuck 'grabbed' forever (torrent removed by hand, stall timeout unset) can't block
// re-grabbing that release permanently.
func (c *Coordinator) pendingBookGrabTitles(ctx context.Context, bookID int64) (map[string]bool, error) {
	return c.pendingTitlesOf(ctx, bookID, "book")
}

// dropUngrabbableBook takes out of releases everything blocklisted for the book and
// everything already grabbed for it and still in flight. An error means one of those
// lists couldn't be read; the caller grabs nothing for the book this round.
func (c *Coordinator) dropUngrabbableBook(ctx context.Context, bookID int64, releases []indexer.Release) ([]indexer.Release, error) {
	releases = c.networks(ctx).grabbable(releases) // what no client can take never competes
	releases, err := c.dropBlockedBook(ctx, bookID, releases)
	if err != nil {
		return nil, err
	}
	pending, err := c.pendingBookGrabTitles(ctx, bookID)
	if err != nil {
		return nil, err
	}
	return dropPendingBook(releases, pending), nil
}

// dropUngrabbableNoted is dropUngrabbableBook followed by the exclude filter (a stall
// fail-over's stalled release), telling the context's search attempt which releases went
// for being blocklisted and which for already downloading. The filters reuse their input's
// array, so each step compares against a copy.
func (c *Coordinator) dropUngrabbableNoted(ctx context.Context, bookID int64, releases []indexer.Release, exclude map[string]bool) ([]indexer.Release, error) {
	notes := notesFrom(ctx)
	nets := c.networks(ctx)
	before := append([]indexer.Release(nil), releases...)
	releases = nets.grabbable(releases) // what no client can take never competes
	nets.noteDropped(notes, before, releases)
	before = append(before[:0], releases...)
	kept, err := c.dropBlockedBook(ctx, bookID, releases)
	if err != nil {
		return nil, err
	}
	notes.dropped(before, kept, DropBlocklisted)
	pending, err := c.pendingBookGrabTitles(ctx, bookID)
	if err != nil {
		return nil, err
	}
	before = append(before[:0], kept...)
	kept = dropPendingBook(kept, pending)
	notes.dropped(before, kept, DropPending)
	before = append(before[:0], kept...)
	kept = dropPendingBook(kept, exclude) // same normalized-title filter
	notes.dropped(before, kept, DropBlocklisted)
	return kept, nil
}

// dropPendingBook removes releases whose normalized title is already pending as a grab.
func dropPendingBook(releases []indexer.Release, pending map[string]bool) []indexer.Release {
	if len(pending) == 0 {
		return releases
	}
	out := releases[:0]
	for _, rel := range releases {
		if !pending[normTitle(rel.Title)] {
			out = append(out, rel)
		}
	}
	return out
}

// recordBookGrab tracks a book grab for seed cleanup (media_type=book, movie_id=bookID).
func (c *Coordinator) recordBookGrab(ctx context.Context, bookID, versionID int64, title, indexer, profile, infoHash string) {
	defer c.grabsChanged()
	notesFrom(ctx).grabbed(title) // the search attempt this grab came from, if any
	// Recorded under the profile the book actually runs under, so a deleted one still
	// gets the default's stall window.
	profile = c.effectiveProfile(ctx, profile, quality.MediaBook)
	transport, seedEnabled, seedRatio, seedHours := c.grabPolicy(ctx, indexer, infoHash)
	// stall_minutes must come from the profile. It was written as a literal 0, and
	// detectStalledBook returns immediately when it isn't positive — so the entire book
	// stall fail-over was dead code: a 0-seeder grab was never blocklisted or removed, and
	// once the 24h pending guard expired the identical release was grabbed again, forever.
	// (The series path carried this exact bug and fixes it the same way.)
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO grabs (movie_id, version_id, title, indexer, quality_profile, stall_minutes, seed_enabled, seed_ratio, seed_hours, media_type, info_hash, transport)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'book', ?, ?)`,
		bookID, versionID, title, indexer, profile, c.quality.StallMinutes(ctx, profile),
		boolToInt(seedEnabled), seedRatio, seedHours, infoHash, transport)
	if err != nil {
		c.log.Warn("book: record grab failed", "err", err)
		return
	}
	c.noteHashless("book", title, infoHash)
	c.noteHandoff(ctx, "book", bookID, title, infoHash)
	c.grabbed(ctx, "book", bookID)
}

// markBookGrabImported flips the ONE grab this download came from to imported —
// matching by info hash first (the torrent's real identity), then by normalized
// release name for rows without one. Mirrors markSeriesGrabImported.
//
// It used to flip EVERY pending book grab for the book, which marked a sibling
// edition's torrent — the audiobook still downloading while the ebook landed — as
// imported before its data existed. Seed cleanup only considers imported grabs and
// removes finished ones WITH their data, so the sibling was deleted the moment it
// completed and the edition re-grabbed: the same grab → delete → re-grab loop the
// series path fixed. A grab matching neither hash nor name stays pending;
// detectStalledBook resolves it once its own edition lands.
func (c *Coordinator) markBookGrabImported(ctx context.Context, bookID int64, infoHash, downloadName string) {
	defer c.grabsChanged()
	rows, err := c.db.QueryContext(ctx,
		`SELECT id, title, info_hash FROM grabs WHERE movie_id = ? AND media_type = 'book' AND `+inFlightWhere, bookID)
	if err != nil {
		return
	}
	wantHash := strings.ToLower(infoHash)
	want := normRelease(downloadName)
	var byHash, byName []int64
	for rows.Next() {
		var id int64
		var title, hash string
		if rows.Scan(&id, &title, &hash) != nil {
			continue
		}
		if wantHash != "" && hash != "" && strings.ToLower(hash) == wantHash {
			byHash = append(byHash, id)
		} else if normRelease(title) == want {
			byName = append(byName, id)
		}
	}
	ids := byHash
	if len(ids) == 0 {
		ids = byName
	}
	rows.Close() // close before writing — SQLite won't take a write while a read is open
	for _, id := range ids {
		if _, err := c.db.ExecContext(ctx, `UPDATE grabs SET status = ? WHERE id = ?`, grabStatusImported, id); err != nil {
			c.log.Warn("book: mark grab imported failed", "err", err)
		}
	}
}

var errBooksNotReady = errString("books module not ready")

type errString string

func (e errString) Error() string { return string(e) }

// dropBlockedBook removes releases blocklisted for this book (so a stalled/rejected one isn't
// re-grabbed).
func (c *Coordinator) dropBlockedBook(ctx context.Context, bookID int64, releases []indexer.Release) ([]indexer.Release, error) {
	blocked, err := c.blockedSetBook(ctx, bookID)
	if err != nil {
		return nil, err
	}
	if len(blocked) == 0 {
		return releases, nil
	}
	out := releases[:0]
	for _, rel := range releases {
		if !blocked[normTitle(rel.Title)] {
			out = append(out, rel)
		}
	}
	return out, nil
}

// bookEditionLanded reports whether the edition a grab was for is now on disk. The edition
// is read back from the release's own format tag — the same classification pickBestBookForKind
// used to choose it — because the shared grabs table has no edition column.
//
// When the format can't be told, it falls back to "either edition present": a grab we can't
// classify is better treated as done than left to be blocklisted as stalled.
func bookEditionLanded(b books.Book, releaseTitle string) bool {
	switch books.EditionOf(detectBookFormat(releaseTitle)) {
	case books.KindEbook:
		return b.Ebook != nil
	case books.KindAudiobook:
		return b.Audiobook != nil
	default:
		return b.HasFile
	}
}

// RSSSyncBooks polls indexer RSS feeds for freshly-uploaded releases matching a wanted book
// edition and grabs them — the fast path between the slower targeted missing-sweep runs.
func (c *Coordinator) RSSSyncBooks(ctx context.Context) {
	if c.books == nil {
		return
	}
	all, err := c.books.List(ctx)
	if err != nil {
		return
	}
	res, err := c.indexers.Recent(ctx, 100)
	if err != nil || len(res.Releases) == 0 {
		return
	}
	if c.log.Enabled(ctx, slog.LevelDebug) {
		c.log.Debug(fmt.Sprintf("rss: %d recent releases (%d from MyAnonaMouse)",
			len(res.Releases), c.countFromKind(ctx, res.Releases, indexer.KindMAM)))
	}
	queue, qerr := c.downloads.Queue(ctx)
	if qerr != nil {
		// Same reasoning as SearchBooksMissing: an unreadable queue reads as empty,
		// which would let this path stack duplicate grabs. Skip the cycle.
		c.log.Warn("rss: couldn't read the download queue — skipping the book RSS sync this cycle", "err", qerr)
		return
	}
	// One library snapshot for the whole cycle: the in-flight check and every book's
	// release filter resolve names against it, with the same word-boundary matcher the
	// search path and the importer use.
	match := books.MatcherOver(all)
	downloading := booksDownloading(queue, match)
	allowed := c.bookLanguages(ctx)
	nets := c.networks(ctx)
	for _, b := range all {
		if !b.Monitored {
			continue
		}
		if downloading[b.ID] {
			continue // already downloading for this book — don't stack another grab
		}
		sp := c.bookProfile(ctx, b.QualityProfile)
		wantE, wantA := books.WantedEditions(sp.FormatScores)
		matched := rssReleasesForBook(match, b, res.Releases)
		if len(matched) == 0 {
			continue
		}
		// Blocklisted releases go, and so does anything already grabbed for this book
		// and still pending (the DB belt to bookDownloading's braces).
		matched, err := c.dropUngrabbableBook(ctx, b.ID, matched)
		if err != nil {
			c.skipUnreadable(b.Title, err)
			continue
		}
		if len(matched) == 0 {
			continue
		}
		// Grab any edition the profile wants that the book still lacks — the same editions
		// the search path asks for, so an Ebook-only book never gets an audiobook here.
		for _, kind := range []string{books.KindEbook, books.KindAudiobook} {
			if (kind == books.KindEbook && (!wantE || b.Ebook != nil)) ||
				(kind == books.KindAudiobook && (!wantA || b.Audiobook != nil)) {
				continue // not wanted, or already have this edition
			}
			pool := matched
			if kind == books.KindAudiobook {
				pool = dropVersionReleases(b, matched)
			}
			best := pickBestBookForKind(nets, sp, allowed, pool, kind, b.Title)
			if best == nil {
				continue
			}
			hash, err := c.grabTo(ctx, best.Indexer, best.DownloadURL, best.Title, bookCategory)
			if err != nil {
				c.log.Warn("rss: book grab failed", "title", b.Title, "edition", kind, "err", err)
				continue
			}
			c.recordBookGrab(ctx, b.ID, 0, best.Title, best.Indexer, b.QualityProfile, hash)
			c.learnBookSeries(ctx, b, *best)
			c.books.AddEvent(ctx, b.ID, "grabbed", fmt.Sprintf("Grabbed the %s edition from %s: %s", kind, best.Indexer, best.Title))
			c.log.Info("rss: grabbing book", "title", b.Title, "edition", kind, "release", best.Title)
			c.recordRSSGrab(ctx, AttemptBook, b.ID, kind, best.Title)
		}
		for _, v := range b.AudioVersions {
			if !versionWanted(v) {
				continue
			}
			best := pickBestBookForKind(nets, versionProfile(sp, v), allowed, releasesForVersion(v, matched), books.KindAudiobook, b.Title)
			if best == nil {
				continue
			}
			hash, err := c.grabTo(ctx, best.Indexer, best.DownloadURL, best.Title, bookCategory)
			if err != nil {
				c.log.Warn("rss: audiobook version grab failed", "title", b.Title, "version", v.Label, "err", err)
				continue
			}
			c.recordBookGrab(ctx, b.ID, v.ID, best.Title, best.Indexer, b.QualityProfile, hash)
			c.books.AddEvent(ctx, b.ID, "grabbed", fmt.Sprintf("Grabbed the %q audiobook from %s: %s", v.Label, best.Indexer, best.Title))
			c.log.Info("rss: grabbing audiobook version", "title", b.Title, "version", v.Label, "release", best.Title)
			c.recordRSSGrab(ctx, AttemptBook, b.ID, fmt.Sprintf("v%d", v.ID), best.Title)
		}
	}
}

// countFromKind counts the releases that came from indexers of one kind, for the RSS
// debug line that confirms MyAnonaMouse's feed is being read.
func (c *Coordinator) countFromKind(ctx context.Context, releases []indexer.Release, kind indexer.Kind) int {
	idxs, err := c.indexers.List(ctx)
	if err != nil {
		return 0
	}
	names := map[string]bool{}
	for _, ix := range idxs {
		if ix.Kind == kind {
			names[ix.Name] = true
		}
	}
	n := 0
	for _, rel := range releases {
		if names[rel.Indexer] {
			n++
		}
	}
	return n
}

// withoutBookUploads drops book uploads (releases carrying an ebook/audiobook file type,
// which MyAnonaMouse's feed is made of) from a shared RSS feed before a movie or series
// sweep matches against it. A book titled like a film, with no year to tell them apart,
// must never be grabbed as the film.
func withoutBookUploads(releases []indexer.Release) []indexer.Release {
	out := make([]indexer.Release, 0, len(releases))
	for _, rel := range releases {
		if rel.Format == "" {
			out = append(out, rel)
		}
	}
	return out
}

// rssReleasesForBook is releasesForBookWith plus the author, for the RSS feed.
//
// A search was asked for "<author> <title>", so its results already lean to the right
// writer. A feed is every new upload on the tracker, so a short title collides far more
// often ("It" by someone else entirely): when the book has an author, the release must
// name them too — in its title or the indexer's own author field.
//
// The old filter here was a substring test over keys with the spaces squeezed out, so
// "It" matched "The Institute" and "Dune" matched "Dune Messiah"; the word-boundary
// matcher with its longest-title rule is what stops both.
func rssReleasesForBook(match func(string) (books.Book, bool), b books.Book, releases []indexer.Release) []indexer.Release {
	out := releasesForBookWith(match, b, releases)
	if strings.TrimSpace(b.Author) == "" {
		return out
	}
	kept := out[:0]
	for _, rel := range out {
		if books.AuthorsOverlap(b.Author, rel.Title+" "+rel.Author) {
			kept = append(kept, rel)
		}
	}
	return kept
}

// bookImportDetail renders the history line for an imported edition: what landed, in what
// format, and which release it came from.
func bookImportDetail(kind, format string, files int, release string, handPicked bool) string {
	d := fmt.Sprintf("Imported the %s edition (%s", kind, format)
	if files > 1 {
		d += fmt.Sprintf(", %d files", files)
	}
	d += ")"
	if handPicked {
		d += " (hand-picked)"
	}
	if release != "" {
		d += " from " + release
	}
	return d
}
