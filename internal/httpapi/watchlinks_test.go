package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/apikeys"
	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/jellyfinsvc"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/plexscan"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
)

// fakeMediaServer is one media server whose index knows titles by "movie:603".
type fakeMediaServer struct {
	kind  mediaserver.Kind
	ready bool
	have  map[string]string // "movie:603" → watch url
}

func (f *fakeMediaServer) Kind() mediaserver.Kind          { return f.kind }
func (f *fakeMediaServer) Name(context.Context) string     { return "" }
func (f *fakeMediaServer) Configured(context.Context) bool { return true }
func (f *fakeMediaServer) IndexReady(context.Context) bool { return f.ready }
func (f *fakeMediaServer) IndexBuiltAt() time.Time         { return time.Unix(1, 0) }
func (f *fakeMediaServer) IndexStale(time.Duration)        {}
func (f *fakeMediaServer) OnIndexRebuilt(func())           {}
func (f *fakeMediaServer) WatchURL(_ context.Context, media string, ids mediaserver.ExternalIDs) string {
	return f.have[media+":"+itoa(ids.TMDB)]
}
func (f *fakeMediaServer) Locate(ctx context.Context, media string, ids mediaserver.ExternalIDs) (mediaserver.Item, bool) {
	return mediaserver.Item{Server: f.kind}, f.WatchURL(ctx, media, ids) != ""
}
func (f *fakeMediaServer) RecentlyAddedTMDB(context.Context, int) ([]mediaserver.RecentTMDB, error) {
	return nil, nil
}

func linksOf(t *testing.T, rec *httptest.ResponseRecorder) []mediaserver.WatchLink {
	t.Helper()
	var body struct {
		Links []mediaserver.WatchLink `json:"links"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Links == nil {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
	}
	return body.Links
}

func servers(ls []mediaserver.WatchLink) []mediaserver.Kind {
	var out []mediaserver.Kind
	for _, l := range ls {
		out = append(out, l.Server)
	}
	return out
}

// Owner decision: the requester sees a Watch button per server that has the title, their
// own server's first. Someone who watches on Jellyfin gets Jellyfin first everywhere
// (GET /media/links, the requests list, the inbox); everyone else Plex, then Jellyfin.
func TestWatchLinksLeadWithTheViewersServer(t *testing.T) {
	plex := &fakeMediaServer{kind: mediaserver.Plex, ready: true, have: map[string]string{"movie:603": "https://app.plex.tv/p603"}}
	jf := &fakeMediaServer{kind: mediaserver.Jellyfin, ready: true, have: map[string]string{"movie:603": "https://watch.example.com/j603", "movie:604": "https://watch.example.com/j604"}}
	var jfUser int64
	s := newRouteServer(t, func(d *Deps) {
		d.MediaServers = mediaserver.NewRegistry(jf, plex) // the registry orders them
		d.HomeServer = func(_ context.Context, uid int64) mediaserver.Kind {
			if uid == jfUser {
				return mediaserver.Jellyfin
			}
			return mediaserver.Plex
		}
	})
	jfKid, jfCookie := s.user(t, "jf@example.com", auth.RoleRequester)
	jfUser = jfKid.ID
	plexKid, plexCookie := s.user(t, "plex@example.com", auth.RoleRequester)

	both := []mediaserver.Kind{mediaserver.Jellyfin, mediaserver.Plex}
	if got := servers(linksOf(t, s.do("GET", "/api/v1/media/links?media=movie&tmdb=603", jfCookie))); !reflect.DeepEqual(got, both) {
		t.Errorf("Jellyfin requester: %v, want Jellyfin first", got)
	}
	if got := servers(linksOf(t, s.do("GET", "/api/v1/media/links?media=movie&tmdb=603", plexCookie))); !reflect.DeepEqual(got, []mediaserver.Kind{mediaserver.Plex, mediaserver.Jellyfin}) {
		t.Errorf("Plex requester: %v, want Plex first", got)
	}
	if got := linksOf(t, s.do("GET", "/api/v1/media/links?media=movie&tmdb=604", plexCookie)); len(got) != 1 || got[0].Server != mediaserver.Jellyfin || got[0].Label != "Jellyfin" {
		t.Errorf("only on Jellyfin: %+v", got)
	}
	if got := linksOf(t, s.do("GET", "/api/v1/media/links?media=movie&tmdb=9", plexCookie)); len(got) != 0 {
		t.Errorf("on neither: %+v, want an empty list", got)
	}
	for _, p := range []string{"/api/v1/media/links?media=book&tmdb=1", "/api/v1/media/links?media=movie", "/api/v1/media/links?media=movie&tmdb=-3"} {
		if rec := s.do("GET", p, plexCookie); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d, want 400", p, rec.Code)
		}
	}
	if rec := s.do("GET", "/api/v1/media/links?media=movie&tmdb=603", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("signed out: HTTP %d", rec.Code)
	}

	// The requests list and the inbox follow the same order; plex_url stays for older pages.
	a := &api{deps: s.deps}
	list := []requests.Request{{MediaType: "movie", TMDBID: 603, Tracking: &requests.Tracking{Stage: requests.StageAvailable}}}
	a.setRequestWatchLinks(context.Background(), list, jfKid)
	if got := servers(list[0].WatchLinks); !reflect.DeepEqual(got, both) || list[0].PlexURL != "https://app.plex.tv/p603" {
		t.Errorf("request for the Jellyfin requester: %+v (plex_url %q)", list[0].WatchLinks, list[0].PlexURL)
	}
	inbox := []requests.UserNotification{
		{Ref: "movie:603"},
		// Sent while Jellyfin had it under an older link; the live one wins. Plex has
		// nothing for it now and never had.
		{Ref: "movie:604", JellyfinURL: "https://watch.example.com/old604"},
		// A decision: no links.
		{Ref: "movie:603:approved:1700000000"},
	}
	a.inboxWatchLinks(context.Background(), inbox, plexKid)
	if got := servers(inbox[0].WatchLinks); !reflect.DeepEqual(got, []mediaserver.Kind{mediaserver.Plex, mediaserver.Jellyfin}) || inbox[0].PlexURL == "" {
		t.Errorf("inbox 603 = %+v", inbox[0])
	}
	if l := inbox[1].WatchLinks; len(l) != 1 || l[0].URL != "https://watch.example.com/j604" || inbox[1].PlexURL != "" {
		t.Errorf("inbox 604 = %+v", inbox[1])
	}
	if len(inbox[2].WatchLinks) != 0 {
		t.Errorf("a decision notice links: %+v", inbox[2])
	}
	// A server whose index isn't built hides its links, saved ones too.
	jf.ready = false
	inbox = []requests.UserNotification{{Ref: "movie:604", JellyfinURL: "https://watch.example.com/old604"}}
	a.inboxWatchLinks(context.Background(), inbox, jfKid)
	if len(inbox[0].WatchLinks) != 0 {
		t.Errorf("Jellyfin not ready: %+v", inbox[0].WatchLinks)
	}
}

// fixtureJellyfin answers like a real Jellyfin 12.2 (internal/jellyfin's captured
// fixtures), for one key.
func fixtureJellyfin(t *testing.T, key string) *httptest.Server {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "jellyfin", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pub, info, vfs := read("system_info_public.json"), read("system_info.json"), read("virtual_folders.json")
	items, recent := read("items_index.json"), read("recently_added.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/Info/Public" {
			_, _ = w.Write(pub)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), `Token="`+key+`"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/System/Info":
			_, _ = w.Write(info)
		case r.URL.Path == "/Library/VirtualFolders":
			_, _ = w.Write(vfs)
		case r.URL.Path == "/Items" && r.URL.Query().Get("sortBy") == "DateCreated":
			_, _ = w.Write(recent)
		case r.URL.Path == "/Items":
			_, _ = w.Write(items)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// No Watch link answer — /media/links, the requests list, the inbox, the title sheet —
// carries the Jellyfin API key or its connection address: only the public address the
// owner gave for people to open Jellyfin at. Runs the real Jellyfin service over a server
// answering with captured fixtures, its index built by its own worker.
func TestWatchLinkResponsesCarryNoSecrets(t *testing.T) {
	t.Setenv("ARRMADA_JELLYFIN_API_KEY", "")
	const key = "jf-secret-77c1d0"
	const public = "https://watch.example.com"
	upstream := fixtureJellyfin(t, key)
	var jf *jellyfinsvc.Service
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.APIKeys = apikeys.NewStore(d.Settings)
		jf = jellyfinsvc.New(jellyfinsvc.Options{Settings: d.Settings, Keys: d.APIKeys, Version: "test", Log: d.Log,
			Roots: func(context.Context) map[string]string {
				return map[string]string{plexscan.KindMovie: "/movies", plexscan.KindShow: "/tv"}
			}})
		d.Jellyfin = jf
		d.MediaServers = mediaserver.NewRegistry(jf)
		d.Books = books.NewService(db, nil, d.Log)
		d.Quality = quality.NewService(db)
		d.Movies = movies.NewService(db, nil, nil, root, "", nil, d.Log)
		d.Series = series.NewService(db, nil, root, d.Log)
		d.Requests = requests.NewService(db, d.Movies, d.Series, d.Books, nil, d.Quality, nil, "", d.Log)
		d.Discovery = detailStub{stubDiscovery{ok: true}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := jf.Save(ctx, upstream.URL, public, key); err != nil {
		t.Fatal(err)
	}
	go jf.RunIndex(ctx)
	for deadline := time.Now().Add(10 * time.Second); !jf.IndexReady(ctx); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the Jellyfin index never built")
		}
	}
	kid, cookie := s.user(t, "kid@example.com", auth.RoleRequester)
	db := s.st.DB()
	for _, q := range []string{
		// The Matrix matches by id; Dune's three files didn't group, so Jellyfin knows
		// them only by folder (Arrmada's /movies is Jellyfin's /media/Movies).
		`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability, has_file, movie_file_path) VALUES (1, 603, 'The Matrix', 1999, 1, 'announced', 1, '/movies/The Matrix (1999) {tmdb-603}/The Matrix (1999).mkv')`,
		`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability, has_file, movie_file_path) VALUES (2, 438631, 'Dune', 2021, 1, 'announced', 1, '/movies/Dune (2021) {tmdb-438631}/Dune (2021) - 1080p.mkv')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, tmdb := range []int{603, 438631} {
		if _, err := db.Exec(`INSERT INTO requests (media_type, tmdb_id, title, status, requested_by, requested_by_name, ready_at) VALUES ('movie', ?, 'x', 'approved', ?, 'kid', 1700000000)`, tmdb, kid.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO user_notifications (user_id, title, body, media_type, ref, jellyfin_url, created_at) VALUES (?, 'Your request is ready', 'b', 'movie', 'movie:603', '', 1700000000)`, kid.ID); err != nil {
		t.Fatal(err)
	}

	matrix := public + "/web/#/details?id=4071f1445812b3d8fba8a91b47dbb5fb&serverId="
	checkLinks := func(what string, links []mediaserver.WatchLink, want string) {
		t.Helper()
		if len(links) != 1 || links[0].Server != mediaserver.Jellyfin || !strings.HasPrefix(links[0].URL, want) {
			t.Errorf("%s: links %+v, want one Jellyfin link starting %q", what, links, want)
		}
	}
	noJellyfinSecrets := func(what, body string) {
		t.Helper()
		for _, bad := range []string{key, upstream.URL, strings.TrimPrefix(upstream.URL, "http://"), "Token=", "api_key"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: response contains %q: %s", what, bad, body)
			}
		}
	}

	rec := s.do("GET", "/api/v1/media/links?media=movie&tmdb=603", cookie)
	checkLinks("/media/links", linksOf(t, rec), matrix)
	noJellyfinSecrets("/media/links", rec.Body.String())

	rec = s.do("GET", "/api/v1/requests", cookie)
	var list struct {
		Requests []requests.Request `json:"requests"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Requests) != 2 {
		t.Fatalf("/requests: HTTP %d %s", rec.Code, rec.Body)
	}
	for _, rq := range list.Requests {
		want := matrix
		if rq.TMDBID == 438631 {
			want = public + "/web/#/details?id=" // one of Dune's files, found by its folder
		}
		checkLinks("/requests "+itoa(rq.TMDBID), rq.WatchLinks, want)
		if rq.PlexURL != "" {
			t.Errorf("plex_url without Plex: %q", rq.PlexURL)
		}
	}
	noJellyfinSecrets("/requests", rec.Body.String())

	rec = s.do("GET", "/api/v1/me/notifications", cookie)
	var inbox struct {
		Notifications []requests.UserNotification `json:"notifications"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &inbox) != nil || len(inbox.Notifications) != 1 {
		t.Fatalf("/me/notifications: HTTP %d %s", rec.Code, rec.Body)
	}
	checkLinks("inbox", inbox.Notifications[0].WatchLinks, matrix)
	if strings.Contains(rec.Body.String(), "jellyfin_url") {
		t.Errorf("the inbox sends the raw column: %s", rec.Body)
	}
	noJellyfinSecrets("/me/notifications", rec.Body.String())

	a := &api{deps: s.deps}
	detail := mediaDetailJSON(t, a)
	raw, _ := json.Marshal(detail)
	var sheet struct {
		WatchLinks []mediaserver.WatchLink `json:"watch_links"`
	}
	_ = json.Unmarshal(raw, &sheet)
	checkLinks("title sheet", sheet.WatchLinks, matrix)
	noJellyfinSecrets("title sheet", string(raw))
}
