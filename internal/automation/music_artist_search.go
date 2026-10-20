package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
	// Left are wanted albums this run didn't get to: past the cap, or after it stopped for
	// failing indexers. The sweep searches them in turn.
	Left int `json:"left,omitempty"`
	// Stopped: the indexers kept failing, so the rest were left rather than searched into it.
	Stopped bool `json:"stopped,omitempty"`
}

// artistSearchCap is how many albums one artist's Search now searches: the missing sweep's
// own per-run cap. An artist added with a long discography wanted would otherwise search
// every indexer for every album at once.
const artistSearchCap = musicSweepCap

// artistSearchFailStreak is how many searches in a row no indexer could answer end the run.
const artistSearchFailStreak = 2

// Message words the result for the page's toast.
func (r ArtistSearch) Message() string {
	var msg string
	switch {
	case r.Albums > 0:
		msg = fmt.Sprintf("Searched %d %s", r.Albums, pluralize(r.Albums, "album"))
		if r.Grabbed > 0 {
			msg += fmt.Sprintf(": grabbed %d", r.Grabbed)
		} else {
			msg += ": nothing grabbed"
		}
		if r.Busy > 0 {
			msg += fmt.Sprintf("; %d already downloading or being searched", r.Busy)
		}
	case r.Busy > 0:
		msg = fmt.Sprintf("Nothing searched — %d %s already downloading or being searched", r.Busy, pluralize(r.Busy, "album"))
	case r.Waiting > 0:
		return fmt.Sprintf("Nothing to search for yet — %d wanted %s not out yet", r.Waiting, pluralize(r.Waiting, "album"))
	default:
		return "Nothing to search for — every monitored album is here"
	}
	if r.Failed > 0 {
		msg += fmt.Sprintf("; %d couldn't be searched", r.Failed)
	}
	if r.Stopped {
		msg += "; stopped because indexers kept failing"
	}
	if r.Left > 0 {
		msg += fmt.Sprintf("; %d more %s left to the automatic search", r.Left, pluralize(r.Left, "album"))
	}
	return msg
}

// SearchArtistNow searches an artist's monitored albums that are missing tracks and out,
// one at a time, the way Search now does for one album (each its own recorded attempt),
// at most artistSearchCap of them (never searched first, then the longest-waiting); the
// rest are the sweep's. Albums already downloading or being searched are left alone, and
// two searches in a row that no indexer could answer stop the run rather than searching
// the rest into an outage. Complete albums aren't searched:
// an artist's Search now fetches what's missing; upgrades are the upgrade sweep's. Whether
// the indexers treat it as a person's search is up to ctx (indexer.WithInteractive):
// the add dialog's search is background work. progress, when set, is told after each.
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
	sort.SliceStable(todo, func(i, j int) bool { return parseTime(todo[i].LastSearchAt).Before(parseTime(todo[j].LastSearchAt)) })
	if len(todo) > artistSearchCap {
		res.Left = len(todo) - artistSearchCap
		todo = todo[:artistSearchCap]
	}
	streak := 0
	for i, al := range todo {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		out, err := c.SearchAlbumNow(ctx, al.ID)
		// Failing means nobody could answer: every indexer asked failed, or none could be
		// asked. One indexer paused or erroring while the others answer is a normal search.
		outage := out.Reason == ReasonIndexersFailed || out.Reason == ReasonIndexersPaused ||
			out.Reason == ReasonNoIndexers || indexer.IsOutage(err)
		switch {
		case errors.Is(err, ErrAlreadySearching), out.Reason == ReasonAlreadyDownloading:
			res.Busy++
		case outage:
			res.Albums++
			res.Failed++
			streak++
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
		if !outage || out.Returned > 0 || out.Grabbed > 0 {
			streak = 0
		}
		if !errors.Is(err, ErrAlreadySearching) {
			// Stamped like the sweep's searches, so the next Search now (or sweep) moves on
			// to the albums not searched yet instead of starting over with these.
			c.stampArtistSearch(ctx, al, out.Grabbed > 0)
		}
		if progress != nil {
			progress(i+1, len(todo))
		}
		if streak >= artistSearchFailStreak && i < len(todo)-1 {
			res.Stopped, res.Left = true, res.Left+len(todo)-i-1
			c.log.Warn("music: no indexer could answer twice in a row — stopping the artist's search", "album", al.Title, "err", err)
			return res, nil
		}
	}
	return res, nil
}

// stampArtistSearch records an artist search of one album the way the sweep would: a grab
// clears its backoff; anything else only stamps the time (a person's search doesn't count
// against the album).
func (c *Coordinator) stampArtistSearch(ctx context.Context, al music.Album, grabbed bool) {
	var err error
	if grabbed {
		err = c.music.ResetSearchMisses(ctx, al.ID)
	} else {
		err = c.music.TouchSearch(ctx, al.ID)
	}
	if err != nil {
		c.log.Warn("music: stamping the search time failed", "album", al.Title, "err", err)
	}
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
