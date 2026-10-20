// Package plexwatch is Discover's "Your Plex watchlist": the server owner's Plex watchlist
// as TMDB titles, and "Request everything on my Plex watchlist automatically", a scheduled
// pass that asks for each new watchlist title through the normal request path (so
// approvals, quotas and notifications apply), once per title.
//
// Arrmada holds one Plex token, the owner's (Settings → Media servers), so the watchlist
// is the owner's: it shows to the Arrmada account linked to the owner's Plex account (or,
// until one is, to admins). People who sign in with Plex don't leave a token behind, so
// their own watchlists can't be read.
package plexwatch

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/adultfilter"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/plex"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// Settings keys (the settings service). Auto is "Request everything on my Plex watchlist
// automatically" (off by default); AutoUser is the account that switched it on, whose
// requests they are.
const (
	KeyAuto     = "plex_watchlist_auto"
	KeyAutoUser = "plex_watchlist_auto_user"
)

const (
	watchlistMax    = 100              // titles read from the watchlist
	idLookupsPerRun = 25               // rating-key lookups for titles Plex sent without ids
	rowTTL          = 10 * time.Minute // the row and the list behind it
	rowMax          = 40               // cards on the row
	autoPerRun      = 10               // requests one automatic pass may make
)

// ErrNotLinked is no Plex connection to read a watchlist with.
var ErrNotLinked = errors.New("plex isn't connected")

// Access is the owner's Plex identity (insights.Service.WatchlistAccess).
type Access interface {
	WatchlistAccess(ctx context.Context) (clientID, token, ownerID string)
}

// Settings is the slice of the settings service the switch needs.
type Settings interface {
	Get(ctx context.Context, key, def string) string
	Set(ctx context.Context, key, value string) error
	GetBool(ctx context.Context, key string, def bool) bool
	SetBool(ctx context.Context, key string, value bool) error
}

// Details is TMDB's record of a title (the Discover provider).
type Details interface {
	MediaDetails(ctx context.Context, media string, tmdbID int) (*metadata.MediaDetail, error)
}

// Users finds the account requests are made as.
type Users interface {
	UserByID(ctx context.Context, id int64) (*auth.User, error)
}

// Requester is the request service.
type Requester interface {
	Create(ctx context.Context, in requests.Request, opts requests.CreateOptions) (requests.Request, bool, error)
}

// Deps is what the service is built from. Fetch, IDs and TVDB default to the real Plex
// and are swapped in tests.
type Deps struct {
	DB       *sql.DB
	Access   Access
	Settings Settings
	Details  Details
	Users    Users
	Requests Requester
	// InLibrary reports whether a title is already in the Movies or TV library.
	InLibrary func(ctx context.Context, media string, tmdbID int) bool
	// TVDB finds a show's TMDB id from its TVDB id. nil = shows Plex only knows by their
	// TVDB id are skipped.
	TVDB func(ctx context.Context, tvdbID int) (int, error)
	Log  *slog.Logger
}

// Item is a watchlist title as a TMDB title.
type Item struct {
	MediaType string // movie | series
	TMDBID    int
	Title     string
	Year      int
}

// Service reads the watchlist and runs the automatic requests.
type Service struct {
	d     Deps
	fetch func(ctx context.Context, clientID, token string, max int) ([]plex.WatchlistItem, error)
	ids   func(ctx context.Context, clientID, token, ratingKey string) (plex.WatchlistItem, error)

	mu      sync.Mutex
	itemsAt time.Time
	items   []Item
	rowAt   time.Time
	row     []metadata.DiscoverItem
	known   map[string]plex.WatchlistItem // rating key -> its ids (they never change)
	autoMu  sync.Mutex                    // one automatic pass at a time
}

// New builds the service.
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{d: d, fetch: plex.Watchlist, ids: plex.WatchlistIDs, known: map[string]plex.WatchlistItem{}}
}

// Linked reports whether there is a Plex connection to read a watchlist with.
func (s *Service) Linked(ctx context.Context) bool {
	if s == nil || s.d.Access == nil {
		return false
	}
	_, token, _ := s.d.Access.WatchlistAccess(ctx)
	return token != ""
}

// OwnerPlexID is the owner's Plex account id, "" until known.
func (s *Service) OwnerPlexID(ctx context.Context) string {
	if s == nil || s.d.Access == nil {
		return ""
	}
	_, _, owner := s.d.Access.WatchlistAccess(ctx)
	return owner
}

// Items is the watchlist as TMDB titles, newest on the list first (cached ten minutes).
// Titles that can't be matched to TMDB are left out.
func (s *Service) Items(ctx context.Context) ([]Item, error) {
	s.mu.Lock()
	if !s.itemsAt.IsZero() && time.Since(s.itemsAt) < rowTTL {
		items := s.items
		s.mu.Unlock()
		return items, nil
	}
	s.mu.Unlock()
	clientID, token, _ := s.d.Access.WatchlistAccess(ctx)
	if token == "" {
		return nil, ErrNotLinked
	}
	raw, err := s.fetch(ctx, clientID, token, watchlistMax)
	if err != nil {
		return nil, err
	}
	items := s.mapItems(ctx, clientID, token, raw)
	s.mu.Lock()
	s.items, s.itemsAt = items, time.Now()
	s.mu.Unlock()
	return items, nil
}

// mapItems turns watchlist entries into TMDB titles: by the tmdb id Plex sent, else the
// one its rating key's record carries (looked up once, a few per pass), else a show's
// TVDB id.
func (s *Service) mapItems(ctx context.Context, clientID, token string, raw []plex.WatchlistItem) []Item {
	out := make([]Item, 0, len(raw))
	seen := map[string]bool{}
	lookups := 0
	for _, w := range raw {
		if !w.HasIDs() && w.RatingKey != "" {
			s.mu.Lock()
			k, ok := s.known[w.RatingKey]
			s.mu.Unlock()
			if !ok && lookups < idLookupsPerRun {
				lookups++
				if got, err := s.ids(ctx, clientID, token, w.RatingKey); err == nil {
					k, ok = got, true
					s.mu.Lock()
					s.known[w.RatingKey] = got
					s.mu.Unlock()
				}
			}
			if ok {
				w.TMDBID, w.TVDBID, w.IMDBID = k.TMDBID, k.TVDBID, k.IMDBID
			}
		}
		it, ok := s.toItem(ctx, w)
		if !ok {
			continue
		}
		key := it.MediaType + ":" + strconv.Itoa(it.TMDBID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}

func (s *Service) toItem(ctx context.Context, w plex.WatchlistItem) (Item, bool) {
	media := "movie"
	if w.Type == "show" {
		media = "series"
	}
	id := w.TMDBID
	if id <= 0 && media == "series" && w.TVDBID > 0 && s.d.TVDB != nil {
		if got, err := s.d.TVDB(ctx, w.TVDBID); err == nil {
			id = got
		}
	}
	if id <= 0 {
		return Item{}, false
	}
	return Item{MediaType: media, TMDBID: id, Title: w.Title, Year: w.Year}, true
}

// Row is the watchlist as Discover cards (TMDB's poster and overview), cached ten minutes.
func (s *Service) Row(ctx context.Context) ([]metadata.DiscoverItem, error) {
	s.mu.Lock()
	if !s.rowAt.IsZero() && time.Since(s.rowAt) < rowTTL {
		row := s.row
		s.mu.Unlock()
		return row, nil
	}
	s.mu.Unlock()
	items, err := s.Items(ctx)
	if err != nil {
		return nil, err
	}
	row := make([]metadata.DiscoverItem, 0, min(len(items), rowMax))
	for _, it := range items {
		if len(row) >= rowMax {
			break
		}
		d, err := s.d.Details.MediaDetails(ctx, it.MediaType, it.TMDBID)
		if err != nil || d == nil || d.Adult || adultfilter.Matches(d.Title) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		di := metadata.DiscoverItem{
			MediaType: it.MediaType, TMDBID: it.TMDBID, Title: d.Title, Year: d.Year, Overview: d.Overview,
			PosterURL: d.PosterURL, BackdropURL: d.BackdropURL, VoteAverage: d.Ratings.TMDB,
		}
		if len(d.Genres) > 0 {
			di.Genres = d.Genres[:min(3, len(d.Genres))]
		}
		row = append(row, di)
	}
	s.mu.Lock()
	s.row, s.rowAt = row, time.Now()
	s.mu.Unlock()
	return row, nil
}

// Auto reports whether automatic requests are on.
func (s *Service) Auto(ctx context.Context) bool {
	return s.d.Settings.GetBool(ctx, KeyAuto, false)
}

// SetAuto switches automatic requests on (as userID: the requests are theirs) or off.
func (s *Service) SetAuto(ctx context.Context, on bool, userID int64) error {
	if on {
		if err := s.d.Settings.Set(ctx, KeyAutoUser, strconv.FormatInt(userID, 10)); err != nil {
			return err
		}
	}
	return s.d.Settings.SetBool(ctx, KeyAuto, on)
}

// Outcomes recorded per title (plex_watchlist_requests.outcome).
const (
	OutcomeRequested   = "requested"
	OutcomeJoined      = "joined"
	OutcomeInLibrary   = "in_library"
	OutcomeDeclined    = "declined"
	OutcomeUnavailable = "unavailable"
)

// AutoRequest is the scheduled pass: when automatic requests are on, each watchlist title
// not dealt with before is requested as the account that switched them on — at most ten a
// pass. A title already in the library is only noted. Hitting the account's request
// limit ends the pass; the rest wait for the next one. Every title is dealt with once.
func (s *Service) AutoRequest(ctx context.Context) error {
	if s == nil || !s.Auto(ctx) || !s.Linked(ctx) {
		return nil
	}
	if !s.autoMu.TryLock() {
		return nil
	}
	defer s.autoMu.Unlock()
	uid, _ := strconv.ParseInt(s.d.Settings.Get(ctx, KeyAutoUser, ""), 10, 64)
	u, err := s.d.Users.UserByID(ctx, uid)
	if err != nil || u == nil || u.Disabled || !u.Role.AtLeast(auth.RoleRequester) {
		s.d.Log.Warn("plex watchlist: automatic requests are on but the account they're made as can't request; switch them off and on again", "user_id", uid)
		return nil
	}
	items, err := s.Items(ctx)
	if err != nil {
		return err
	}
	done, err := s.handled(ctx)
	if err != nil {
		return err
	}
	asked := 0
	for _, it := range items {
		key := it.MediaType + ":" + strconv.Itoa(it.TMDBID)
		if done[key] {
			continue
		}
		if asked >= autoPerRun {
			break
		}
		if s.d.InLibrary != nil && s.d.InLibrary(ctx, it.MediaType, it.TMDBID) {
			s.record(ctx, it, u.ID, OutcomeInLibrary)
			continue
		}
		outcome, err := s.request(ctx, u, it)
		var over *requests.ErrQuotaExceeded
		if errors.As(err, &over) {
			s.d.Log.Info("plex watchlist: request limit reached; the rest wait for the next pass", "user", u.Username)
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.d.Log.Warn("plex watchlist: couldn't request a title; trying again next pass", "title", it.Title, "err", err)
			continue
		}
		asked++
		s.record(ctx, it, u.ID, outcome)
		s.d.Log.Info("plex watchlist: requested automatically", "title", it.Title, "media", it.MediaType, "tmdb_id", it.TMDBID, "outcome", outcome, "as", u.Username)
	}
	return nil
}

// request asks for one title the way Discover's Request button does: TMDB's own title,
// poster and seasons, the adult filter, the account's auto-approve and limits. A title
// declined before isn't re-opened (that takes a note from a person).
func (s *Service) request(ctx context.Context, u *auth.User, it Item) (string, error) {
	d, err := s.d.Details.MediaDetails(ctx, it.MediaType, it.TMDBID)
	if errors.Is(err, metadata.ErrNotFound) {
		return OutcomeUnavailable, nil
	}
	if err != nil {
		return "", err
	}
	if d == nil || d.Adult || adultfilter.Matches(d.Title) || d.Title == "" {
		return OutcomeUnavailable, nil
	}
	in := requests.Request{
		MediaType: it.MediaType, TMDBID: it.TMDBID, Title: d.Title, Year: d.Year, PosterURL: d.PosterURL,
		Overview: d.Overview, RequestedBy: u.ID, RequestedByName: u.Username,
	}
	if it.MediaType == "series" {
		for _, sn := range d.Seasons {
			in.KnownSeasons = append(in.KnownSeasons, sn.Number)
		}
	}
	_, subscribed, err := s.d.Requests.Create(ctx, in, requests.CreateOptions{
		AutoApprove: u.AutoApproves(it.MediaType), QuotaExempt: u.Role.AtLeast(auth.RoleManager),
	})
	var needs *requests.NeedsNoteError
	switch {
	case errors.As(err, &needs):
		return OutcomeDeclined, nil
	case errors.Is(err, requests.ErrAlreadyAvailable):
		return OutcomeInLibrary, nil
	case errors.Is(err, requests.ErrExists):
		return OutcomeJoined, nil
	case err != nil:
		return "", err
	case subscribed:
		return OutcomeJoined, nil
	}
	return OutcomeRequested, nil
}

// handled is every title a pass has dealt with.
func (s *Service) handled(ctx context.Context) (map[string]bool, error) {
	rows, err := s.d.DB.QueryContext(ctx, `SELECT media_type, tmdb_id FROM plex_watchlist_requests`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var media string
		var id int
		if err := rows.Scan(&media, &id); err != nil {
			return nil, err
		}
		out[media+":"+strconv.Itoa(id)] = true
	}
	return out, rows.Err()
}

func (s *Service) record(ctx context.Context, it Item, userID int64, outcome string) {
	if _, err := s.d.DB.ExecContext(ctx, `INSERT INTO plex_watchlist_requests (media_type, tmdb_id, user_id, title, outcome)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (media_type, tmdb_id) DO UPDATE SET outcome = excluded.outcome, at = CURRENT_TIMESTAMP`,
		it.MediaType, it.TMDBID, userID, it.Title, outcome); err != nil {
		s.d.Log.Warn("plex watchlist: couldn't note a title as dealt with", "title", it.Title, "err", err)
	}
}
