package automation

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// seriesKnownAs reports whether a series download grabbed for show is named by one of the
// show's names in another language (series.Service.KnownAs: every alternative title and
// translation TMDB has, with the usual year and country checks) — a German tracker's
// "Haus.des.Geldes.S01E01" grabbed for Money Heist. It only ever confirms the show the
// grab was made for.
//
// A confirmation is remembered for as long as the download is in the client's completed
// list (pruneUnmatched), so a seeding download isn't asked about every 30 seconds — and
// can't be sent to Review after it imported, by a TMDB outage on a later pass.
func (c *Coordinator) seriesKnownAs(ctx context.Context, hash string, show series.Series, p parser.Release) (bool, error) {
	c.unmatchedMu.Lock()
	id, seen := c.knownNames[hash]
	c.unmatchedMu.Unlock()
	if seen && id == show.ID {
		return true, nil
	}
	ok, err := c.series.KnownAs(ctx, show, p)
	if ok {
		c.unmatchedMu.Lock()
		if c.knownNames == nil {
			c.knownNames = map[string]int64{}
		}
		c.knownNames[hash] = show.ID
		c.unmatchedMu.Unlock()
	}
	return ok, err
}
