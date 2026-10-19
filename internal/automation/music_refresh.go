package automation

import (
	"context"
	"errors"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/music"
)

// The refresh-music-artists task (MUS-13): Music keeps itself current. Each run, on
// MusicBrainz's background lane and within a budget of network calls:
//
//  1. artists not refreshed for musicRefreshEvery, never-refreshed first, are re-read
//     (music.Service.RefreshArtist: new releases added under the release-type and
//     never-surprise-grab rules; merges followed; nothing removed when MusicBrainz is down);
//  2. new releases that are wanted and out are searched once;
//  3. singles waiting for the singles-on-an-album check are settled;
//  4. monitored albums with no listing get one, so their completeness is known;
//  5. a few albums still on the upgrade's legacy edition are re-keyed (the edition pass).
//
// Fresh cached answers cost nothing, so most artists cost no call at all; the budget only
// bites on a cold cache (a big library's first runs), and what doesn't fit waits for the
// next run, oldest first. When MusicBrainz starts refusing (the background lane rests) the
// run stops where it is.
const (
	// musicRefreshEvery is how long an artist counts as current after a refresh.
	musicRefreshEvery = 20 * time.Hour
	// musicRefreshArtists bounds the artists one run looks at.
	musicRefreshArtists = 250
	// musicRefreshCallBudget bounds the MusicBrainz requests one run sends over the
	// network: about four minutes at musicbrainz.org's pace.
	musicRefreshCallBudget = 200
	// musicNewSearches bounds the new releases one run searches at once; the sweep
	// searches any others within half an hour.
	musicNewSearches = 10
	// musicSinglesChecked, musicListingFills and musicEditionPass bound the follow-up
	// steps per run.
	musicSinglesChecked = 20
	musicListingFills   = 30
	musicEditionPass    = 5
)

// MusicRefreshReport is what one refresh-music-artists run did.
type MusicRefreshReport struct {
	Artists         int    // artists refreshed
	Unanswered      int    // of those, MusicBrainz didn't answer for (kept as they were)
	Added           int    // albums added
	Wanted          int    // of those, new releases now wanted
	Stale           int    // albums MusicBrainz no longer lists (flagged, kept)
	Merged          int    // albums re-pointed to a merged release group
	Searched        int    // new releases searched
	SinglesSettled  int    // singles checked against the artist's albums
	SinglesWanted   int    // of those, wanted
	Listings        int    // listings filled
	EditionsTried   int    // legacy albums the edition pass looked at
	EditionsRekeyed int    // of those, now on a real edition
	Calls           int64  // MusicBrainz requests sent over the network
	Stopped         string // "budget" or "musicbrainz_paused" when the run stopped early
}

// RefreshMusicArtists is one run of the refresh-music-artists task.
func (c *Coordinator) RefreshMusicArtists(ctx context.Context) (MusicRefreshReport, error) {
	if c.music == nil || !c.moduleOn(ctx, "music") {
		return MusicRefreshReport{}, nil
	}
	ctx, tr := metadata.WithMBTrace(metadata.WithMBLane(ctx, metadata.MBBackground))
	rep, err := c.refreshMusicArtists(ctx, tr)
	rep.Calls = tr.Fetched.Load()
	return rep, err
}

func (c *Coordinator) refreshMusicArtists(ctx context.Context, tr *metadata.MBTrace) (MusicRefreshReport, error) {
	var rep MusicRefreshReport
	more := func() bool {
		if paused, _ := metadata.MusicBrainzPaused(); paused {
			rep.Stopped = "musicbrainz_paused"
			return false
		}
		if tr.Fetched.Load() >= c.musicCallBudget() {
			rep.Stopped = "budget"
			return false
		}
		return true
	}

	artists, err := c.music.ArtistsDueForRefresh(ctx, musicRefreshEvery, musicRefreshArtists)
	if err != nil {
		return rep, err
	}
	var wanted []music.Album
	for _, a := range artists {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		if !more() {
			break
		}
		res, err := c.music.RefreshArtist(ctx, a.ID, music.RefreshOptions{Scheduled: true})
		if err != nil {
			return rep, err
		}
		rep.Artists++
		if res.Unanswered {
			rep.Unanswered++
		}
		rep.Added += len(res.Added)
		rep.Wanted += len(res.Wanted)
		rep.Stale += len(res.Stale)
		rep.Merged += len(res.Merged)
		wanted = append(wanted, res.Wanted...)
	}

	rep.Searched = c.searchNewReleases(ctx, wanted)

	if rep.Stopped == "" {
		settled, sw, err := c.music.CheckPendingSingles(ctx, musicSinglesChecked, more)
		rep.SinglesSettled, rep.SinglesWanted = settled, sw
		if err != nil {
			return rep, err
		}
	}

	if rep.Stopped == "" {
		albums, err := c.music.AlbumsWithoutListing(ctx, musicListingFills)
		if err != nil {
			return rep, err
		}
		for _, al := range albums {
			if ctx.Err() != nil || !more() {
				break
			}
			if err := c.music.FillListing(ctx, al); err != nil {
				// MusicBrainz didn't answer: the album is tried again on a later run.
				c.log.Debug("music: couldn't fill an album's listing", "album", al.Title, "err", err)
				continue
			}
			rep.Listings++
		}
	}

	if rep.Stopped == "" {
		tried, rekeyed, err := c.music.RekeyLegacyEditions(ctx, musicEditionPass, more)
		rep.EditionsTried, rep.EditionsRekeyed = tried, rekeyed
		if err != nil {
			return rep, err
		}
	}
	return rep, ctx.Err()
}

// musicCallBudget is the run's MusicBrainz call budget (tests lower it).
func (c *Coordinator) musicCallBudget() int64 {
	if c.musicRefreshBudget > 0 {
		return c.musicRefreshBudget
	}
	return musicRefreshCallBudget
}

// searchNewReleases searches the wanted new releases that are out, once each, up to
// musicNewSearches. One not out yet is left to the sweep, which searches it from its
// release day; after an indexer outage nothing more is searched this run, and no miss is
// recorded, so the sweep picks them up once the indexers answer.
func (c *Coordinator) searchNewReleases(ctx context.Context, albums []music.Album) int {
	today := time.Now().UTC().Format("2006-01-02")
	sctx := WithSearchTrigger(ctx, TriggerAdd)
	n := 0
	for _, al := range albums {
		if ctx.Err() != nil || n >= musicNewSearches {
			break
		}
		if !albumReleased(al, today) {
			continue
		}
		n++
		out, err := c.SearchAlbumNow(sctx, al.ID)
		switch {
		case err == nil, errors.Is(err, ErrAlreadySearching):
		case out.Reason == ReasonIndexersFailed:
			c.log.Warn("music: no indexer could answer — leaving the new releases to the scheduled sweep", "err", err)
			return n
		default:
			c.log.Warn("music: searching a new release failed", "album", al.Title, "err", err)
		}
	}
	return n
}
