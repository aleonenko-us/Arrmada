package automation

import (
	"context"
	"errors"

	"github.com/tristenlammi/arrmada/internal/music"
)

// "Search for missing tracks" in bulk (MUS-15): a person's choice to search for the albums
// whose track files are gone from disk. Bounded like any bulk search: a few albums a run,
// each re-checked first, and stopped when the indexers stop answering.

// musicMissingPerRun caps the albums one bulk search looks for (the artist search's cap).
const musicMissingPerRun = 25

// musicMissingOutageStreak is how many searches in a row may find every indexer failing
// before the bulk search stops: nobody can answer, so the rest would only fail too.
const musicMissingOutageStreak = 3

// MissingSearchResult is what one bulk search did.
type MissingSearchResult struct {
	Searched int  `json:"searched"`
	Grabbed  int  `json:"grabbed"`
	Skipped  int  `json:"skipped"` // no longer missing anything (the files came back, or were found)
	Left     int  `json:"left"`    // still to search: run it again
	Stopped  bool `json:"stopped"` // the indexers stopped answering
}

// SearchMissingTracks searches for up to musicMissingPerRun albums with track files gone
// from disk, one after another. Each album is read again just before its search, and one
// whose files have come back meanwhile is skipped. progress hears done out of total.
func (c *Coordinator) SearchMissingTracks(ctx context.Context, progress func(done, total int)) (MissingSearchResult, error) {
	var res MissingSearchResult
	if c.music == nil {
		return res, nil
	}
	if !c.moduleOn(ctx, "music") {
		return res, ErrModuleOff
	}
	if progress == nil {
		progress = func(int, int) {}
	}
	albums, err := c.music.AlbumsWithMissingFiles(ctx)
	if err != nil {
		return res, err
	}
	todo := albums
	if len(todo) > musicMissingPerRun {
		todo = todo[:musicMissingPerRun]
	}
	outages := 0
	for i, al := range todo {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		progress(i, len(todo))
		now, err := c.music.AlbumRecord(ctx, al.ID)
		if errors.Is(err, music.ErrNotFound) || (err == nil && now.MissingTracks == 0) {
			res.Skipped++
			continue
		}
		if err != nil {
			return res, err
		}
		out, err := c.SearchAlbumNow(ctx, al.ID)
		switch {
		case errors.Is(err, ErrAlreadySearching):
			continue
		case out.Reason == ReasonIndexersFailed:
			outages++
			if outages >= musicMissingOutageStreak {
				res.Stopped = true
				res.Left = len(albums) - i
				c.log.Warn("music: no indexer could answer — the search for missing tracks stopped", "searched", res.Searched)
				return res, nil
			}
		case err != nil:
			c.log.Warn("music: searching an album with missing files failed", "album", al.Title, "err", err)
			outages = 0
		default:
			outages = 0
		}
		res.Searched++
		res.Grabbed += out.Grabbed
	}
	progress(len(todo), len(todo))
	res.Left = len(albums) - len(todo)
	return res, nil
}
