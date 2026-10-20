package plexwatch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/plex"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/store"
)

const testToken = "owner-token-secret"

// fakePlex is Plex's discover service: a watchlist of a movie with its tmdb id, a show
// known only by its TVDB id, a movie sent without ids (found by its rating key), an
// album (ignored) and a movie Plex can't place. It checks the token arrives in a header.
func fakePlex(t *testing.T) *atomic.Int32 {
	t.Helper()
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != testToken || r.URL.Query().Get("X-Plex-Token") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/library/sections/watchlist/all":
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[
				{"ratingKey":"a","type":"movie","title":"Dune: Part Two","year":2024,"Guid":[{"id":"imdb://tt15239678"},{"id":"tmdb://693134"}]},
				{"ratingKey":"b","type":"show","title":"Shogun","year":2024,"Guid":[{"id":"tvdb://392573"}]},
				{"ratingKey":"c","type":"movie","title":"Heat","year":1995},
				{"ratingKey":"d","type":"album","title":"Noise"},
				{"ratingKey":"e","type":"movie","title":"Nowhere","year":2001}]}}`)
		case "/library/metadata/c":
			lookups.Add(1)
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"c","type":"movie","title":"Heat","Guid":[{"id":"tmdb://949"}]}]}}`)
		case "/library/metadata/e":
			lookups.Add(1)
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"e","type":"movie","title":"Nowhere"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(plex.SetDiscoverBaseForTest(srv.URL))
	return &lookups
}

type access struct{ token string }

func (a access) WatchlistAccess(context.Context) (string, string, string) {
	if a.token == "" {
		return "", "", ""
	}
	return "client", a.token, "42"
}

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) Get(_ context.Context, k, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[k]; ok {
		return v
	}
	return def
}
func (s *memSettings) Set(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}
func (s *memSettings) GetBool(ctx context.Context, k string, def bool) bool {
	v := s.Get(ctx, k, "")
	if v == "" {
		return def
	}
	return v == "true"
}
func (s *memSettings) SetBool(ctx context.Context, k string, v bool) error {
	return s.Set(ctx, k, strconv.FormatBool(v))
}

type details struct{}

func (details) MediaDetails(_ context.Context, media string, id int) (*metadata.MediaDetail, error) {
	return &metadata.MediaDetail{MediaType: media, TMDBID: id, Title: "T" + strconv.Itoa(id), Year: 2020, PosterURL: "https://image.tmdb.org/p.jpg"}, nil
}

type users struct{ u *auth.User }

func (f users) UserByID(_ context.Context, id int64) (*auth.User, error) {
	if f.u != nil && f.u.ID == id {
		return f.u, nil
	}
	return nil, nil
}

// fakeRequests records each Create; quotaAfter > 0 refuses every ask past that many.
type fakeRequests struct {
	mu         sync.Mutex
	got        []requests.Request
	opts       []requests.CreateOptions
	quotaAfter int
	declined   map[int]bool
}

func (f *fakeRequests) Create(_ context.Context, in requests.Request, opts requests.CreateOptions) (requests.Request, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.declined[in.TMDBID] {
		return requests.Request{}, false, &requests.NeedsNoteError{}
	}
	if f.quotaAfter > 0 && len(f.got) >= f.quotaAfter {
		return requests.Request{}, false, &requests.ErrQuotaExceeded{Kind: "movie", Limit: f.quotaAfter}
	}
	f.got = append(f.got, in)
	f.opts = append(f.opts, opts)
	return in, false, nil
}

func newService(t *testing.T, token string, u *auth.User, reqs *fakeRequests, inLib map[int]bool) (*Service, *memSettings) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	set := &memSettings{m: map[string]string{}}
	return New(Deps{
		DB: st.DB(), Access: access{token: token}, Settings: set, Details: details{}, Users: users{u: u}, Requests: reqs,
		InLibrary: func(_ context.Context, _ string, id int) bool { return inLib[id] },
		TVDB: func(_ context.Context, tvdb int) (int, error) {
			if tvdb == 392573 {
				return 126308, nil
			}
			return 0, fmt.Errorf("unknown")
		},
	}), set
}

// The watchlist maps to TMDB titles by the ids Plex sends, a rating-key lookup (once) or
// a show's TVDB id; albums and titles with no id are left out.
func TestWatchlistMapping(t *testing.T) {
	lookups := fakePlex(t)
	s, _ := newService(t, testToken, nil, &fakeRequests{}, nil)
	ctx := context.Background()
	items, err := s.Items(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{{"movie", 693134, "Dune: Part Two", 2024}, {"series", 126308, "Shogun", 2024}, {"movie", 949, "Heat", 1995}}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	// A second read inside ten minutes is the cached list; a fresh read doesn't look the
	// same rating key up again.
	s.itemsAt = s.itemsAt.AddDate(0, 0, -1)
	if _, err := s.Items(ctx); err != nil {
		t.Fatal(err)
	}
	if n := lookups.Load(); n != 2 {
		t.Errorf("%d rating-key lookups, want 2 (each key once)", n)
	}
	row, err := s.Row(ctx)
	if err != nil || len(row) != 3 || row[0].PosterURL == "" || row[1].MediaType != "series" {
		t.Errorf("row = %+v, %v", row, err)
	}
}

func TestWatchlistNotLinked(t *testing.T) {
	s, _ := newService(t, "", nil, &fakeRequests{}, nil)
	if s.Linked(context.Background()) {
		t.Error("linked without a token")
	}
	if _, err := s.Items(context.Background()); err != ErrNotLinked {
		t.Errorf("err = %v, want ErrNotLinked", err)
	}
}

// Automatic requests: off by default; on, each title once, as the account that switched
// them on with its own auto-approve and limits; a title in the library or declined before
// is noted, not asked for; a second pass asks for nothing.
func TestAutoRequestOnceEach(t *testing.T) {
	fakePlex(t)
	u := &auth.User{ID: 7, Username: "owner", Role: auth.RoleRequester}
	reqs := &fakeRequests{declined: map[int]bool{949: true}}
	s, _ := newService(t, testToken, u, reqs, map[int]bool{126308: true})
	ctx := context.Background()

	if err := s.AutoRequest(ctx); err != nil || len(reqs.got) != 0 {
		t.Fatalf("off by default: %v, %d asked", err, len(reqs.got))
	}
	if err := s.SetAuto(ctx, true, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AutoRequest(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reqs.got) != 1 || reqs.got[0].TMDBID != 693134 || reqs.got[0].RequestedBy != 7 || reqs.got[0].Title != "T693134" {
		t.Fatalf("asked = %+v", reqs.got)
	}
	if reqs.opts[0].QuotaExempt {
		t.Error("a requester's automatic request skipped their limits")
	}
	if err := s.AutoRequest(ctx); err != nil || len(reqs.got) != 1 {
		t.Errorf("second pass asked again: %+v", reqs.got)
	}
	done, _ := s.handled(ctx)
	if len(done) != 3 || !done["series:126308"] || !done["movie:949"] {
		t.Errorf("handled = %v", done)
	}
}

// The request limit ends a pass without marking the rest, so the next pass (after the
// limit resets) asks for them; a pass never asks for more than its cap.
func TestAutoRequestQuotaAndCap(t *testing.T) {
	fakePlex(t)
	u := &auth.User{ID: 7, Username: "owner", Role: auth.RoleRequester}
	reqs := &fakeRequests{quotaAfter: 1}
	s, _ := newService(t, testToken, u, reqs, nil)
	ctx := context.Background()
	_ = s.SetAuto(ctx, true, u.ID)
	if err := s.AutoRequest(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reqs.got) != 1 {
		t.Fatalf("asked %d, want 1 before the limit", len(reqs.got))
	}
	reqs.quotaAfter = 0
	if err := s.AutoRequest(ctx); err != nil {
		t.Fatal(err)
	}
	if len(reqs.got) != 3 {
		t.Errorf("after the limit reset asked %d in all, want 3", len(reqs.got))
	}

	// The cap: a long watchlist is asked for ten at a time.
	many := make([]Item, 25)
	for i := range many {
		many[i] = Item{MediaType: "movie", TMDBID: 1000 + i, Title: "M"}
	}
	s.mu.Lock()
	s.items = append(s.items, many...)
	s.mu.Unlock()
	before := len(reqs.got)
	_ = s.AutoRequest(ctx)
	if n := len(reqs.got) - before; n != autoPerRun {
		t.Errorf("one pass asked %d, want %d", n, autoPerRun)
	}
}

// Nobody is asked for as an account that can't request (gone, disabled or read-only).
func TestAutoRequestNeedsARequester(t *testing.T) {
	fakePlex(t)
	u := &auth.User{ID: 7, Username: "viewer", Role: auth.RoleReadonly}
	reqs := &fakeRequests{}
	s, _ := newService(t, testToken, u, reqs, nil)
	ctx := context.Background()
	_ = s.SetAuto(ctx, true, u.ID)
	if err := s.AutoRequest(ctx); err != nil || len(reqs.got) != 0 {
		t.Errorf("read-only account asked %d: %v", len(reqs.got), err)
	}
}
