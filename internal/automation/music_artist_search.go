package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/music"
)

// Search now for an artist (MUS-09), and the album searches' lines in the artist's History.

// ArtistSearch is what searching an artist's wanted albums came to.
type ArtistSearch struct {
	Albums  int      `json:"albums"`  // albums searched
	Grabbed int      `json:"grabbed"` // of those, ones a release was grabbed for
	Titles  []string `json:"grabbed_titles,omitempty"`
	Busy    int      `json:"busy,omitempty"`    // skipped: already downloading or being searched
	Failed  int      `json:"failed,omitempty"`  // searches that failed on our side
	Waiting int      `json:"waiting,omitempty"` // monitored albums not out yet
}

// Message words the result for the page's toast.
func (r ArtistSearch) Message() string {
	if r.Albums == 0 {
		switch {
		case r.Busy > 0:
			return fmt.Sprintf("Nothing searched — %d %s already downloading or being searched", r.Busy, pluralize(r.Busy, "album"))
		case r.Waiting > 0:
			return fmt.Sprintf("Nothing to search for yet — %d wanted %s not out yet", r.Waiting, pluralize(r.Waiting, "album"))
		}
		return "Nothing to search for — every monitored album is here"
	}
	msg := fmt.Sprintf("Searched %d %s", r.Albums, pluralize(r.Albums, "album"))
	if r.Grabbed > 0 {
		msg += fmt.Sprintf(": grabbed %d", r.Grabbed)
	} else {
		msg += ": nothing grabbed"
	}
	if r.Busy > 0 {
		msg += fmt.Sprintf("; %d already downloading or being searched", r.Busy)
	}
	if r.Failed > 0 {
		msg += fmt.Sprintf("; %d couldn't be searched", r.Failed)
	}
	return msg
}

// SearchArtistNow searches each of an artist's monitored albums that is missing tracks and
// out, one at a time, the way Search now does for one album (each its own recorded
// attempt). A person asked, so the backoff is ignored; albums already downloading or being
// searched are left alone, and an indexer outage stops the run rather than searching the
// rest into it. Complete albums aren't searched: an artist's Search now fetches what's
// missing, and upgrades are the upgrade sweep's (or one album's Search now). progress,
// when set, is told after each album.
func (c *Coordinator) SearchArtistNow(ctx context.Context, artistID int64, progress func(done, total int)) (ArtistSearch, error) {
	var res ArtistSearch
	if c.music == nil || !c.moduleOn(ctx, "music") {
		return res, ErrModuleOff
	}
	if _, err := c.music.GetArtist(ctx, artistID); err != nil {
		return res, err
	}
	albums, err := c.music.WantedAlbums(ctx)
	if err != nil {
		return res, err
	}
	today := c.clock().UTC().Format("2006-01-02")
	var todo []music.Album
	for _, al := range albums {
		if al.ArtistID != artistID || al.Complete() {
			continue
		}
		if !albumReleased(al, today) {
			res.Waiting++
			continue
		}
		todo = append(todo, al)
	}
	for i, al := range todo {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		out, err := c.SearchAlbumNow(ctx, al.ID)
		switch {
		case errors.Is(err, ErrAlreadySearching), out.Reason == ReasonAlreadyDownloading:
			res.Busy++
		case out.Reason == ReasonIndexersFailed || out.Reason == ReasonIndexersPaused || indexer.IsOutage(err):
			res.Albums++
			res.Failed++
			c.log.Warn("music: no indexer could answer — stopping the artist's search", "album", al.Title, "err", err)
			return res, nil
		case err != nil:
			res.Albums++
			res.Failed++
			c.log.Warn("music: an album search failed", "album", al.Title, "err", err)
		default:
			res.Albums++
			if out.Grabbed > 0 {
				res.Grabbed++
				res.Titles = append(res.Titles, out.GrabbedTitles...)
			}
		}
		if progress != nil {
			progress(i+1, len(todo))
		}
	}
	return res, nil
}

// albumSearchedEvent puts an album search on its artist's History when its result differs
// from the album's last one ("OK Computer: 14 releases, none for this album"): a sweep
// every half hour would otherwise bury the History in identical lines. A grab writes its
// own line, and an upgrade search that found nothing better is the normal case.
func (c *Coordinator) albumSearchedEvent(ctx context.Context, albumID int64, trigger, kind string, out SearchOutcome, err error, same bool) {
	if c.music == nil || same || kind == OutcomeGrabbed || trigger == TriggerUpgrade || trigger == TriggerStall {
		return
	}
	al, aerr := c.music.AlbumRecord(ctx, albumID)
	if aerr != nil {
		return
	}
	c.music.AddEvent(ctx, al.ArtistID, "searched", al.Title+": "+out.OutcomeLine(kind, err))
}
