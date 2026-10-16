package automation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// When an indexer blocks Arrmada's own NZB download — Cloudflare in front of it admitting
// only the apps the indexer chose, SABnzbd among them — the grab hands SABnzbd the link to
// fetch the NZB itself (docs/roadmap/USENET.md). Arrmada still tries first: fetching it
// here validates it and keeps the indexer's key out of SABnzbd's history, and only a block
// (indexer.BlockedError) changes that. A refused key, a limit, a gone item or a dead
// connection keeps failing the grab as before.
//
// The grab says so where grabs are shown: a line in the title's history next to "grabbed",
// and a log line. If SABnzbd can't fetch it either, the job fails into its history and the
// usual usenet failure handling takes it from there (sabfail.go: "URL Fetching failed").

// handoffEvent is the history line's event name; History files it with the grabs.
const handoffEvent = "handoff"

// handoffNoteTTL is how long a hand-off waits for its grab to be recorded. The record
// follows the add in the same call; this only stops a grab that was never recorded (an
// error between the two) from leaving its note behind for long.
const handoffNoteTTL = 10 * time.Minute

// handoffNotes are the hand-offs whose grab hasn't been recorded yet, keyed by download key
// (or "title:<release>" when the client didn't say the key).
type handoffNotes struct {
	mu    sync.Mutex
	notes map[string]handoffNote
}

type handoffNote struct {
	indexer string
	at      time.Time
}

func handoffKey(key, title string) string {
	if key != "" {
		return strings.ToLower(key)
	}
	return "title:" + normTitle(title)
}

func (h *handoffNotes) put(key, title, indexerName string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.notes == nil {
		h.notes = map[string]handoffNote{}
	}
	for k, n := range h.notes {
		if now.Sub(n.at) > handoffNoteTTL {
			delete(h.notes, k)
		}
	}
	h.notes[handoffKey(key, title)] = handoffNote{indexer: indexerName, at: now}
}

// take returns and forgets the hand-off for a grab being recorded.
func (h *handoffNotes) take(key, title string, now time.Time) (handoffNote, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range []string{handoffKey(key, title), handoffKey("", title)} {
		if n, ok := h.notes[k]; ok {
			delete(h.notes, k)
			if now.Sub(n.at) <= handoffNoteTTL {
				return n, true
			}
		}
	}
	return handoffNote{}, false
}

// handoffDetail is the history line for a hand-off.
func handoffDetail(indexerName, title string) string {
	return fmt.Sprintf("%s blocked Arrmada's download; SABnzbd fetched the NZB itself — %s", indexerName, title)
}

// addNZBLink hands the usenet client the link the indexer blocked Arrmada from fetching, to
// fetch itself — with the same name and category an uploaded NZB gets, and through the
// same fail-over within usenet clients. The link carries the indexer's key and the client's
// history keeps it (the accepted price of this fallback); nothing here logs it or puts it
// in an error.
func (c *Coordinator) addNZBLink(ctx context.Context, blocked *indexer.BlockedError, title, category, indexerName string) (string, error) {
	added, err := c.downloads.AddDownload(ctx, download.AddRequest{
		Name: title, SavePath: c.downloadsPath(), Category: category,
		URL: blocked.ClientLink(), ClientFetches: true, Transport: download.TransportUsenet,
	})
	if err != nil {
		return "", fmt.Errorf("%s, and handing the link to SABnzbd failed too: %s", blocked.Error(), blocked.Redact(err.Error()))
	}
	c.log.Warn("usenet: the indexer blocked Arrmada's NZB download; SABnzbd fetches the NZB itself",
		"indexer", indexerName, "release", title, "status", blocked.Status, "key", added.Key)
	c.handoffs.put(added.Key, title, indexerName, c.clock())
	c.clearRemovedImport(ctx, added.Key)
	c.bus.Publish("release.grabbed", map[string]any{"title": title, "indexer": indexerName, "network": string(download.TransportUsenet)})
	return added.Key, nil
}

// noteHandoff writes the hand-off's history line for a grab just recorded, if it was one.
// mediaType and itemID are the grab's (a music grab's item is its album; the line goes on
// the album's artist, whose history it is).
func (c *Coordinator) noteHandoff(ctx context.Context, mediaType string, itemID int64, title, key string) {
	n, ok := c.handoffs.take(key, title, c.clock())
	if !ok {
		return
	}
	detail := handoffDetail(n.indexer, title)
	switch mediaType {
	case "movie":
		if c.movies != nil {
			c.movies.AddEvent(ctx, itemID, handoffEvent, detail)
		}
	case "series":
		if c.series != nil {
			c.series.AddEvent(ctx, itemID, handoffEvent, detail)
		}
	case "book":
		if c.books != nil {
			c.books.AddEvent(ctx, itemID, handoffEvent, detail)
		}
	case "music":
		if c.music != nil {
			if al, err := c.music.GetAlbum(ctx, itemID); err == nil && al.ArtistID > 0 {
				c.music.AddEvent(ctx, al.ArtistID, handoffEvent, detail)
			}
		}
	}
}
