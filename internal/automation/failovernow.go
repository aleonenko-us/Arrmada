package automation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// Needs you's actions on a stuck download: try another release now, and retry an import
// now. Both act on one torrent, by info hash, and only on what the record says Arrmada
// grabbed or is importing.

var (
	// ErrNotOurDownload: no in-flight grab recorded this torrent (added by hand, or already
	// imported or replaced), so there is nothing to replace it for.
	ErrNotOurDownload = errors.New("Arrmada didn't grab this download, so it has nothing to replace it with")
	// ErrDownloadNotInClient: the torrent isn't in the download client (or the client
	// can't be read just now).
	ErrDownloadNotInClient = errors.New("that download isn't in the download client")
	// ErrDownloadFinished: it has finished downloading; replacing it is the import's call.
	ErrDownloadFinished = errors.New("that download has finished — it's waiting to import, not stuck")
)

// FailOverResult is what a fail-over someone asked for did.
type FailOverResult struct {
	Replacement string `json:"replacement,omitempty"` // the release grabbed instead ("" for none)
	Outage      bool   `json:"outage,omitempty"`      // the indexers couldn't be searched
	Title       string `json:"title"`                 // the title it was for
}

// FailOverNow replaces a stalled or errored download with another release now, whatever
// its stall window says. The stall check's rule holds: the torrent is only removed (and
// its release blocklisted) once something else has been grabbed in its place; with
// nothing better found it is left exactly as it was, and the next automatic attempt is a
// full window away.
func (c *Coordinator) FailOverNow(ctx context.Context, hash string) (FailOverResult, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if hash == "" || c.downloads == nil {
		return FailOverResult{}, ErrNotOurDownload
	}
	pending, err := c.pendingGrabs(ctx)
	if err != nil {
		return FailOverResult{}, err
	}
	var g *grab
	for i := range pending {
		if pending[i].InfoHash != "" && strings.EqualFold(pending[i].InfoHash, hash) {
			g = &pending[i]
		}
	}
	if g == nil {
		return FailOverResult{}, ErrNotOurDownload
	}
	snap, err := c.downloads.Snapshot(ctx)
	if err != nil {
		return FailOverResult{}, ErrDownloadNotInClient
	}
	var item *download.Item
	for i := range snap.Items {
		if strings.EqualFold(snap.Items[i].Hash, hash) {
			item = &snap.Items[i]
			break
		}
	}
	if item == nil {
		return FailOverResult{}, ErrDownloadNotInClient
	}
	// A finished download is the import's to settle — unless the client stopped it with an
	// error (its files went missing, say), when replacing it is still the way out.
	if item.Complete() && item.Phase() != "error" {
		return FailOverResult{}, ErrDownloadFinished
	}
	t, ok := c.stallTargetFor(ctx, *g)
	if !ok {
		return FailOverResult{}, ErrNotOurDownload
	}
	// One search of a title at a time: a sweep or a Search click already on it covers it.
	release, claimed := c.claims.claim(claimKey(t.kind, t.id))
	if !claimed {
		return FailOverResult{Title: t.name}, ErrAlreadySearching
	}
	defer release()
	t.why = "Replaced from Needs you"
	window, on := c.stallWindow(ctx, *g)
	if !on {
		window = DefaultStallMinutes * time.Minute
	}
	repl, outage := c.failOver(ctx, *g, *item, true, window, t)
	return FailOverResult{Replacement: repl, Outage: outage, Title: t.name}, nil
}

// stallTargetFor is grab g's fail-over target, whatever its kind; false when its title
// can't be read (or its module is off).
func (c *Coordinator) stallTargetFor(ctx context.Context, g grab) (stallTarget, bool) {
	switch g.MediaType {
	case "series":
		if c.series == nil {
			return stallTarget{}, false
		}
		return c.seriesStallTarget(ctx, g)
	case "book":
		if c.books == nil {
			return stallTarget{}, false
		}
		b, err := c.books.Get(ctx, g.MovieID)
		if err != nil {
			return stallTarget{}, false
		}
		return c.bookStallTarget(b, g), true
	case "music":
		if c.music == nil {
			return stallTarget{}, false
		}
		al, err := c.music.GetAlbum(ctx, g.MovieID)
		if err != nil {
			return stallTarget{}, false
		}
		return c.musicStallTarget(ctx, al, g)
	}
	if c.movies == nil {
		return stallTarget{}, false
	}
	return c.movieStallTarget(ctx, g)
}

// claimKey is the search claim for a fail-over target's title.
func claimKey(kind string, id int64) string {
	switch kind {
	case "series":
		return seriesKey(id)
	case "book":
		return bookKey(id)
	case "music":
		return albumKey(id)
	}
	return movieKey(id)
}

// RetrySeriesImport forgets the series import sweep's failure count for a download, so
// Needs you stops showing it as failing until the next sweep tries it again (the sweep
// retries every pass; it has no back-off to clear).
func (c *Coordinator) RetrySeriesImport(hash string) { c.clearSeriesImportFail(hash) }
