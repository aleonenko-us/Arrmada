package httpapi

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
)

// A transfer on the Activity queue shows the title it is for — poster, name, a link to its
// page — with what part of it the torrent covers and who asked for it, instead of a bare
// release name. All of it comes from the acquisition record (the grab, by info hash);
// a torrent no grab knows keeps its release name and the "Not managed" chip. Names are
// never matched against titles: the record is the one join between queue and library.

// transferItem is the library title a transfer was grabbed for.
type transferItem struct {
	Kind      string `json:"kind"` // movie | series | book | music
	ID        int64  `json:"id"`   // the movie, series, book or album id
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	Byline    string `json:"byline,omitempty"` // a book's author, an album's artist
	// Scope is what part of the title the torrent covers, in words: "S03", "S03E05",
	// "Complete series", "Audiobook", a movie version's label. "" for the whole title.
	Scope string `json:"scope,omitempty"`
	// Upgrade: the grab replaces a file the title already has.
	Upgrade bool `json:"upgrade,omitempty"`
	// RequestedBy names whoever is waiting on the title (open requests' owners and
	// followers). Staff only: this feed is manager-only.
	RequestedBy []string `json:"requested_by,omitempty"`
}

// titleCard is what a transfer card shows of one library title.
type titleCard struct {
	found     bool
	title     string
	year      int
	poster    string
	byline    string
	versions  map[int64]string // movie versions / audiobook versions by id → label
	fetchedAt time.Time
}

// How long the feed reuses what it looked up. Titles, posters and version labels barely
// change; requests change when someone asks or staff decide, and a chip that catches up
// half a minute later is fine. Nothing here is the transfer list itself, which is always
// the live 2-second snapshot.
const (
	titleCardTTL  = 2 * time.Minute
	requestersTTL = 30 * time.Second
)

// transferCache holds the lookups above between feed reads. Its zero value is ready to use.
type transferCache struct {
	mu         sync.Mutex
	titles     map[string]titleCard
	reqs       map[string][]string
	reqsAt     time.Time
	reqsLoaded bool
}

func titleKey(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }

// titleCardFor is one title's card, from the cache or the library.
func (a *api) titleCardFor(ctx context.Context, kind string, id int64, now time.Time) titleCard {
	key := titleKey(kind, id)
	c := &a.transfers
	c.mu.Lock()
	card, ok := c.titles[key]
	c.mu.Unlock()
	if ok && now.Sub(card.fetchedAt) < titleCardTTL {
		return card
	}
	card = a.loadTitleCard(ctx, kind, id)
	card.fetchedAt = now
	c.mu.Lock()
	if c.titles == nil {
		c.titles = map[string]titleCard{}
	}
	// A library that grew past a few thousand cached titles is pruned of stale ones.
	if len(c.titles) > 2000 {
		for k, v := range c.titles {
			if now.Sub(v.fetchedAt) >= titleCardTTL {
				delete(c.titles, k)
			}
		}
	}
	c.titles[key] = card
	c.mu.Unlock()
	return card
}

// loadTitleCard reads one title's own row: no files probed, no episodes loaded. A title
// that can't be read (deleted since the grab, a module off) has found = false.
func (a *api) loadTitleCard(ctx context.Context, kind string, id int64) titleCard {
	switch kind {
	case "movie":
		if a.deps.Movies == nil {
			break
		}
		m, err := a.deps.Movies.Get(ctx, id)
		if err != nil {
			break
		}
		card := titleCard{found: true, title: m.Title, year: m.Year, poster: m.PosterURL}
		if vs, err := a.deps.Movies.VersionRows(ctx, id); err == nil {
			card.versions = map[int64]string{}
			for _, v := range vs {
				if v.ID > 0 {
					card.versions[v.ID] = v.Label
				}
			}
		}
		return card
	case "series":
		if a.deps.Series == nil {
			break
		}
		s, err := a.deps.Series.GetSummary(ctx, id)
		if err != nil {
			break
		}
		return titleCard{found: true, title: s.Title, year: s.Year, poster: s.PosterURL}
	case "book":
		if a.deps.Books == nil {
			break
		}
		b, err := a.deps.Books.Get(ctx, id)
		if err != nil {
			break
		}
		card := titleCard{found: true, title: b.Title, year: b.Year, poster: b.CoverURL, byline: b.Author, versions: map[int64]string{}}
		for _, v := range b.AudioVersions {
			card.versions[v.ID] = v.Label
		}
		return card
	case "music":
		if a.deps.Music == nil {
			break
		}
		al, err := a.deps.Music.AlbumRecord(ctx, id)
		if err != nil {
			break
		}
		card := titleCard{found: true, title: al.Title, year: al.Year, poster: al.CoverURL}
		if art, err := a.deps.Music.GetArtist(ctx, al.ArtistID); err == nil {
			card.byline = art.Name
		}
		return card
	}
	return titleCard{}
}

// requestersByLibrary is who is waiting on each library title ("movie:12" → names), from
// the cache or the requests table. Unreadable: nobody, this once.
func (a *api) requestersByLibrary(ctx context.Context, now time.Time) map[string][]string {
	if a.deps.Requests == nil {
		return nil
	}
	c := &a.transfers
	c.mu.Lock()
	if c.reqsLoaded && now.Sub(c.reqsAt) < requestersTTL {
		reqs := c.reqs
		c.mu.Unlock()
		return reqs
	}
	c.mu.Unlock()
	reqs, err := a.deps.Requests.RequestersByLibrary(ctx)
	if err != nil {
		a.deps.Log.Debug("downloads feed: couldn't read who requested what", "err", err)
		return nil
	}
	c.mu.Lock()
	c.reqs, c.reqsAt, c.reqsLoaded = reqs, now, true
	c.mu.Unlock()
	return reqs
}

// transferItemFor is the title card a grabbed transfer shows, nil when its title can't
// be read (the card then falls back to the release name).
func (a *api) transferItemFor(ctx context.Context, acq automation.Acquisition, reqs map[string][]string, now time.Time) *transferItem {
	if acq.ItemID <= 0 {
		return nil
	}
	card := a.titleCardFor(ctx, acq.MediaType, acq.ItemID, now)
	if !card.found {
		return nil
	}
	return &transferItem{
		Kind: acq.MediaType, ID: acq.ItemID, Title: card.title, Year: card.year, PosterURL: card.poster,
		Byline: card.byline, Scope: scopeLabel(acq, card), Upgrade: acq.Replaces,
		RequestedBy: reqs[titleKey(acq.MediaType, acq.ItemID)],
	}
}

// scopeLabel says in a word or two what part of its title a grab covers. The whole title
// (a movie's main version, an album, a series pack whose season depends on its numbering)
// needs no chip.
func scopeLabel(acq automation.Acquisition, card titleCard) string {
	switch acq.MediaType {
	case "movie":
		if acq.VersionID > 0 {
			if l := card.versions[acq.VersionID]; l != "" {
				return l
			}
			return "Extra version"
		}
	case "series":
		switch acq.Scope {
		case "", "abs":
			return ""
		case "complete":
			return "Complete series"
		}
		return acq.Scope // S03, S03E05, S01-S04
	case "book":
		if acq.VersionID > 0 {
			if l := card.versions[acq.VersionID]; l != "" {
				return "Audiobook · " + l
			}
			return "Audiobook version"
		}
		switch acq.Scope {
		case books.KindEbook:
			return "Ebook"
		case books.KindAudiobook:
			return "Audiobook"
		}
	}
	return ""
}
