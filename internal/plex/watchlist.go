package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

// The Plex watchlist lives on Plex's discover service, not on a Media Server: it belongs
// to the plex.tv account the token is for. The list carries each title's Plex rating key
// and, when Plex includes them, its outside ids (tmdb://, tvdb://, imdb://); a title that
// comes without them is looked up once by its rating key.

// discoverBase is Plex's discover service. Tests point it at a fake.
var discoverBase atomic.Pointer[string]

func init() {
	b := "https://discover.provider.plex.tv"
	discoverBase.Store(&b)
}

// SetDiscoverBaseForTest points the watchlist calls at base (an httptest server) and
// returns the function that puts the real address back. Tests only.
func SetDiscoverBaseForTest(base string) (restore func()) {
	old := discoverBase.Load()
	discoverBase.Store(&base)
	return func() { discoverBase.Store(old) }
}

// WatchlistItem is one title on a Plex watchlist. Type is "movie" or "show"; the ids are
// zero (or "") when Plex didn't say.
type WatchlistItem struct {
	RatingKey string
	Type      string
	Title     string
	Year      int
	TMDBID    int
	TVDBID    int
	IMDBID    string
}

// HasIDs reports whether the item carries an id a TMDB title can be found by.
func (w WatchlistItem) HasIDs() bool { return w.TMDBID > 0 || w.TVDBID > 0 }

type watchlistMeta struct {
	RatingKey string `json:"ratingKey"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Guids     []struct {
		ID string `json:"id"`
	} `json:"Guid"`
}

func (m watchlistMeta) item() WatchlistItem {
	w := WatchlistItem{RatingKey: m.RatingKey, Type: m.Type, Title: m.Title, Year: m.Year}
	for _, g := range m.Guids {
		applyGuid(&w, g.ID)
	}
	return w
}

// applyGuid reads one outside id ("tmdb://603", "tvdb://81189", "imdb://tt0133093").
func applyGuid(w *WatchlistItem, guid string) {
	scheme, val, ok := strings.Cut(guid, "://")
	if !ok || val == "" {
		return
	}
	switch scheme {
	case "tmdb":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			w.TMDBID = n
		}
	case "tvdb":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			w.TVDBID = n
		}
	case "imdb":
		w.IMDBID = val
	}
}

// Watchlist reads the first max titles of the token's account watchlist, most recently
// added first, with whatever ids Plex includes.
func Watchlist(ctx context.Context, clientID, token string, max int) ([]WatchlistItem, error) {
	q := url.Values{}
	q.Set("includeGuids", "1")
	q.Set("includeCollections", "0")
	q.Set("includeExternalMedia", "0")
	q.Set("sort", "watchlistedAt:desc")
	q.Set("X-Plex-Container-Start", "0")
	q.Set("X-Plex-Container-Size", strconv.Itoa(max))
	var payload struct {
		MediaContainer struct {
			Metadata []watchlistMeta `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := discoverGet(ctx, clientID, token, "/library/sections/watchlist/all?"+q.Encode(), &payload); err != nil {
		return nil, err
	}
	out := make([]WatchlistItem, 0, len(payload.MediaContainer.Metadata))
	for _, m := range payload.MediaContainer.Metadata {
		if m.Type != "movie" && m.Type != "show" {
			continue
		}
		out = append(out, m.item())
		if len(out) >= max {
			break
		}
	}
	return out, nil
}

// WatchlistIDs looks one watchlist title up by its rating key for its outside ids.
func WatchlistIDs(ctx context.Context, clientID, token, ratingKey string) (WatchlistItem, error) {
	if ratingKey == "" || strings.ContainsAny(ratingKey, "/?#") {
		return WatchlistItem{}, fmt.Errorf("plex: bad rating key")
	}
	var payload struct {
		MediaContainer struct {
			Metadata []watchlistMeta `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := discoverGet(ctx, clientID, token, "/library/metadata/"+url.PathEscape(ratingKey)+"?includeGuids=1", &payload); err != nil {
		return WatchlistItem{}, err
	}
	if len(payload.MediaContainer.Metadata) == 0 {
		return WatchlistItem{}, fmt.Errorf("plex: no such title")
	}
	return payload.MediaContainer.Metadata[0].item(), nil
}

// discoverGet GETs a discover-service path with the account token and decodes its JSON.
// The token goes in a header, never the URL, so no error can carry it.
func discoverGet(ctx context.Context, clientID, token, pathAndQuery string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *discoverBase.Load()+pathAndQuery, nil)
	if err != nil {
		return err
	}
	plexHeaders(req, clientID, "")
	req.Header.Set("X-Plex-Token", token)
	resp, err := plexHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("plex watchlist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrWatchlistToken
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("plex watchlist: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}

// ErrWatchlistToken is Plex refusing the token for the watchlist.
var ErrWatchlistToken = errors.New("plex watchlist: the Plex token was refused")
