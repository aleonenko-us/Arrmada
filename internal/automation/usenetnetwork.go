package automation

import (
	"context"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/download"
)

// History lines and blocklist rows are free text about a release, written long before
// anyone asked which network it came from; the grab row is what knows (grabs.transport).
// UsenetReleases reads that once per page so each line or row can say "usenet" — the
// pages mark it with a chip. A release is matched by its normalised title, the same key
// the blocklist itself uses.

// UsenetReleases is every release grabbed from Usenet.
type UsenetReleases struct {
	// byItem is the normalised titles per "<kind>:<item id>", as the history feed names
	// items: an album's grabs under its artist (music history is kept per artist).
	byItem map[string][]string
	titles map[string]bool
}

// UsenetReleases reads every usenet grab's release.
func (c *Coordinator) UsenetReleases(ctx context.Context) (UsenetReleases, error) {
	u := UsenetReleases{byItem: map[string][]string{}, titles: map[string]bool{}}
	rows, err := c.db.QueryContext(ctx, `SELECT g.media_type,
			CASE WHEN g.media_type = 'music' THEN COALESCE(al.artist_id, 0) ELSE g.movie_id END, g.title
		  FROM grabs g LEFT JOIN albums al ON g.media_type = 'music' AND al.id = g.movie_id
		 WHERE g.transport = ?`, string(download.TransportUsenet))
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, title string
		var item int64
		if err := rows.Scan(&kind, &item, &title); err != nil {
			return u, err
		}
		n := normTitle(title)
		if n == "" {
			continue
		}
		u.titles[n] = true
		key := kind + ":" + strconv.FormatInt(item, 10)
		u.byItem[key] = append(u.byItem[key], n)
	}
	return u, rows.Err()
}

// Has reports whether a release of this title was grabbed from Usenet.
func (u UsenetReleases) Has(title string) bool {
	n := normTitle(title)
	return n != "" && u.titles[n]
}

// Mentions reports whether text (a history line) names a release grabbed from Usenet for
// this item.
func (u UsenetReleases) Mentions(kind string, itemID int64, text string) bool {
	rels := u.byItem[kind+":"+strconv.FormatInt(itemID, 10)]
	if len(rels) == 0 {
		return false
	}
	n := normTitle(text)
	for _, r := range rels {
		if strings.Contains(n, r) {
			return true
		}
	}
	return false
}

// markUsenetBlocks sets the network of n blocklist rows from their release titles. The
// mark is decoration: an unreadable grabs table leaves the rows unmarked, not the page
// broken.
func (c *Coordinator) markUsenetBlocks(ctx context.Context, n int, title func(i int) string, set func(i int, network string)) {
	if n == 0 {
		return
	}
	u, err := c.UsenetReleases(ctx)
	if err != nil {
		return
	}
	for i := 0; i < n; i++ {
		if nw := u.networkOf(title(i)); nw != "" {
			set(i, nw)
		}
	}
}

// networkOf is the network field for a blocklist row: "usenet" for a usenet release, ""
// otherwise (a torrent, as every block was before).
func (u UsenetReleases) networkOf(title string) string {
	if u.Has(title) {
		return string(download.TransportUsenet)
	}
	return ""
}
