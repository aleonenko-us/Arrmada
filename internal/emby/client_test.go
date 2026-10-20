package emby

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fixtures in testdata/ are representative Emby-shaped JSON used by the unit tests;
// they are not captures from a live Emby server.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fake is a Emby stand-in: a route table of "METHOD /path" → handler, recording each
// request it saw.
type fake struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []*http.Request
	bodies []string
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, r)
		f.bodies = append(f.bodies, string(body))
		h := f.routes[r.Method+" "+r.URL.Path]
		if strings.HasPrefix(r.URL.Path, "/emby/") {
			h = f.routes[r.Method+" "+strings.TrimPrefix(r.URL.Path, "/emby")]
		}
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) json(route, file string) {
	f.routes[route] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(f.t, file))
	}
}

func (f *fake) status(route string, code int) {
	f.routes[route] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

func (f *fake) last() (*http.Request, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		f.t.Fatal("no request reached the fake")
	}
	return f.seen[len(f.seen)-1], f.bodies[len(f.bodies)-1]
}

func (f *fake) client() *Client { return New(f.srv.URL+"/", "the-api-key", "dev-1", "0.9.1", nil) }

var bg = context.Background()

// authParts reads an Emby authorization metadata header into its fields.
func authParts(t *testing.T, h string) map[string]string {
	t.Helper()
	rest, ok := strings.CutPrefix(h, "Emby ")
	if !ok {
		t.Fatalf("authorization %q isn't Emby", h)
	}
	out := map[string]string{}
	for _, part := range strings.Split(rest, ", ") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			t.Fatalf("malformed part %q in %q", part, h)
		}
		out[k] = v[1 : len(v)-1]
	}
	return out
}

func TestAuthorizationHeader(t *testing.T) {
	f := newFake(t)
	f.json("GET /System/Info", "system_info.json")
	if _, err := f.client().SystemInfo(bg); err != nil {
		t.Fatal(err)
	}
	r, _ := f.last()
	got := authParts(t, r.Header.Get("X-Emby-Authorization"))
	want := map[string]string{"Client": "Arrmada", "Device": "Arrmada", "DeviceId": "dev-1", "Version": "0.9.1"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (header %q)", k, got[k], v, r.Header.Get("X-Emby-Authorization"))
		}
	}
	// The key travels in Emby's documented API key header, never in the URL.
	if strings.Contains(r.URL.RawQuery, "the-api-key") || r.Header.Get("X-Emby-Token") != "the-api-key" {
		t.Errorf("key leaked into %q / X-Emby-Token", r.URL.RawQuery)
	}

	// No token (sign-in): the device fields still go, the Token field doesn't.
	h := New("http://x", "", "dev-1", "0.9.1", nil).authorization()
	if strings.Contains(h, "Token=") || !strings.Contains(h, `DeviceId="dev-1"`) {
		t.Errorf("anonymous header = %q", h)
	}
	// A value can't break out of its quotes.
	h = New("http://x", `k"ey, Evil="1`, "d,1", "", nil).authorization()
	p := authParts(t, h)
	if len(p) != 4 || p["Evil"] != "" {
		t.Errorf("escaped header = %q → %v", h, p)
	}

}

func TestSystemInfoFixtures(t *testing.T) {
	f := newFake(t)
	f.json("GET /System/Info", "system_info.json")
	f.json("GET /System/Info/Public", "system_info_public.json")
	c := f.client()
	si, err := c.SystemInfo(bg)
	if err != nil {
		t.Fatal(err)
	}
	if si.ID != "5e7e75e7e75e7e75e7e75e7e75e7e75e" || si.ServerName != "arrmada-it" || si.Version != "4.9.0.43" || si.ProductName != "Emby Server" || !si.StartupWizardCompleted {
		t.Errorf("system info = %+v", si)
	}
	pub, err := New(f.srv.URL, "", "", "", nil).PublicSystemInfo(bg)
	if err != nil {
		t.Fatal(err)
	}
	if pub.ID != si.ID || pub.Version != "4.9.0.43" {
		t.Errorf("public = %+v", pub)
	}
	if r, _ := f.last(); strings.Contains(r.Header.Get("X-Emby-Authorization"), "Token=") {
		t.Error("public info sent a token")
	}
	// The keyed call refuses to go out without a key or address.
	if _, err := New(f.srv.URL, "", "", "", nil).SystemInfo(bg); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no key = %v", err)
	}
	if _, err := New("", "k", "", "", nil).PublicSystemInfo(bg); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no url = %v", err)
	}
}

func TestVirtualFoldersFixture(t *testing.T) {
	f := newFake(t)
	f.json("GET /Library/VirtualFolders", "virtual_folders.json")
	vfs, err := f.client().VirtualFolders(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(vfs) != 2 {
		t.Fatalf("%d libraries", len(vfs))
	}
	m, tv := vfs[0], vfs[1]
	if m.Name != "Movies" || m.CollectionType != "movies" || m.ItemID != "f137a2dd21bbc1b99aa5c0f6bf02a805" ||
		len(m.Locations) != 1 || m.Locations[0] != "/media/Movies" || m.RefreshStatus != "Idle" {
		t.Errorf("movies = %+v", m)
	}
	if tv.Name != "TV" || tv.CollectionType != "tvshows" || len(tv.Locations) != 1 || tv.Locations[0] != "/media/TV" {
		t.Errorf("tv = %+v", tv)
	}
}

func TestItemsQueryValues(t *testing.T) {
	v := ItemsQuery{IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true}.values()
	if v.Get("userId") != "" || v.Get("includeItemTypes") != "Movie,Series" || v.Get("recursive") != "true" ||
		v.Get("fields") != "ProviderIds,DateCreated,Path,SeriesId" || v.Get("startIndex") != "" || v.Get("limit") != "" {
		t.Errorf("values = %v", v)
	}
	v = ItemsQuery{UserID: "u1", ParentID: "p1", Fields: []string{}, SortBy: []string{"DateCreated"}, SortDescending: true, StartIndex: 10, Limit: 5}.values()
	if v.Get("userId") != "u1" || v.Get("parentId") != "p1" || v.Has("fields") || v.Get("sortBy") != "DateCreated" ||
		v.Get("sortOrder") != "Descending" || v.Get("startIndex") != "10" || v.Get("limit") != "5" {
		t.Errorf("values = %v", v)
	}
}

// AllItems walks the pages by startIndex until TotalRecordCount is reached.
func TestAllItemsPaging(t *testing.T) {
	var page ItemsPage
	if err := json.Unmarshal(fixture(t, "items_index.json"), &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalRecordCount != 17 || len(page.Items) != 17 {
		t.Fatalf("fixture has %d/%d items", len(page.Items), page.TotalRecordCount)
	}
	raw := fixture(t, "items_index.json")
	var all struct {
		Items []json.RawMessage
	}
	_ = json.Unmarshal(raw, &all)

	f := newFake(t)
	f.routes["GET /Items"] = func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("startIndex"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		end := min(start+limit, len(all.Items))
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": all.Items[start:end], "TotalRecordCount": len(all.Items), "StartIndex": start})
	}
	old := itemsPageSize
	itemsPageSize = 5
	defer func() { itemsPageSize = old }()

	items, err := f.client().AllItems(bg, ItemsQuery{IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 17 || len(f.seen) != 4 {
		t.Fatalf("%d items in %d requests", len(items), len(f.seen))
	}
	byTMDB := map[int]Item{}
	for _, it := range items {
		byTMDB[it.ProviderIDs.TMDB] = it
	}
	m := byTMDB[603]
	if m.Name != "The Matrix" || m.Type != TypeMovie || m.ProviderIDs.IMDB != "tt0133093" ||
		m.Path != "/media/Movies/The Matrix (1999) {tmdb-603}/The Matrix (1999).mkv" || m.DateCreated.IsZero() || m.ProductionYear != 1999 {
		t.Errorf("matrix = %+v", m)
	}
	// Ids from a single-file folder's name, in both tag styles.
	if byTMDB[578].ID == "" || byTMDB[348].ID == "" {
		t.Errorf("folder-tag ids missing: jaws %+v alien %+v", byTMDB[578], byTMDB[348])
	}
	bb := byTMDB[1396]
	if bb.Type != TypeSeries || bb.ProviderIDs.TVDB != 81189 || bb.Path != "/media/TV/Breaking Bad (2008) {tvdb-81189}" {
		t.Errorf("series = %+v", bb)
	}
}

// The versions page shows Emby's grouping rule (see TestIT_NamingGrouping).
func TestItemsVersionsFixture(t *testing.T) {
	var p ItemsPage
	if err := json.Unmarshal(fixture(t, "items_versions.json"), &p); err != nil {
		t.Fatal(err)
	}
	groups := map[string][]Item{}
	for _, it := range p.Items {
		groups[it.Name] = append(groups[it.Name], it)
	}
	if len(groups["Dune"]) != 3 || len(groups["Dune"][0].MediaSources) != 1 {
		t.Errorf("dune = %d items", len(groups["Dune"]))
	}
	mem := groups["Memento (2000)"]
	if len(mem) != 1 || len(mem[0].MediaSources) != 2 || mem[0].MediaSources[0].Name != "2160p" ||
		mem[0].MediaSources[1].Path != "/media/Movies/Memento (2000)/Memento (2000) - 1080p.mkv" {
		t.Fatalf("memento = %+v", mem)
	}
	src := mem[0].MediaSources[0]
	if src.ID == "" || src.Container != "mkv" || src.Size <= 0 || src.Bitrate <= 0 || len(src.Streams) == 0 {
		t.Errorf("media source = %+v", src)
	}
	z := groups["Zodiac (2007)"]
	if len(z) != 1 || len(z[0].MediaSources) != 2 || z[0].MediaSources[1].Name != "1080p {edition-Director's Cut}" {
		t.Errorf("zodiac = %+v", z)
	}
}

func TestRecentlyAddedFixture(t *testing.T) {
	f := newFake(t)
	f.json("GET /Items", "recently_added.json")
	items, err := f.client().RecentlyAdded(bg, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := f.last()
	q := r.URL.Query()
	if q.Get("sortBy") != "DateCreated" || q.Get("sortOrder") != "Descending" || q.Get("limit") != "60" ||
		q.Get("includeItemTypes") != "Movie,Episode" || q.Get("recursive") != "true" || q.Has("userId") {
		t.Errorf("query = %v", q)
	}
	var ep *Item
	for i := range items {
		if items[i].Type == TypeEpisode {
			ep = &items[i]
		}
	}
	if ep == nil || ep.Name != "Pilot" || ep.SeriesID != "220a6b5ae3ead33e2967c7bdcc007b3a" || ep.ProviderIDs.TVDB != 349232 {
		t.Errorf("episode = %+v", ep)
	}
}

func TestSessionsDirectPlayFixture(t *testing.T) {
	f := newFake(t)
	f.json("GET /Sessions", "sessions_directplay.json")
	ss, err := f.client().Sessions(bg, 960)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := f.last(); r.URL.Query().Get("activeWithinSeconds") != "960" {
		t.Errorf("query = %q", r.URL.RawQuery)
	}
	if len(ss) != 2 {
		t.Fatalf("%d sessions", len(ss))
	}
	s := ss[0]
	if !s.Playing() || ss[1].Playing() {
		t.Fatalf("playing = %v / %v", s.Playing(), ss[1].Playing())
	}
	if s.UserName != "ituser" || s.UserID == "" || s.Client != "Arrmada" || s.DeviceName != "Arrmada" || s.RemoteEndPoint != "172.21.0.3" || s.ID == "" {
		t.Errorf("identity = %+v", s)
	}
	ps := s.PlayState
	if ps.PlayMethod != "DirectPlay" || !ps.IsPaused || ps.Position() != 4*time.Second || ps.MediaSourceID != s.NowPlayingItem.ID {
		t.Errorf("play state = %+v", ps)
	}
	np := s.NowPlayingItem
	if np.Name != "The Matrix" || np.Type != TypeMovie || np.ProviderIDs.TMDB != 603 || TicksToDuration(np.RunTimeTicks).Round(time.Second) != 10*time.Second {
		t.Errorf("now playing = %+v", np)
	}
	v := np.VideoStream()
	if v == nil || v.Codec != "h264" || v.BitDepth != 8 || v.Profile != "Constrained Baseline" || v.BitRate <= 0 || v.Width != 320 || v.VideoRange != "SDR" {
		t.Errorf("video = %+v", v)
	}
	// Direct play: no TranscodingInfo, and no bitrate anywhere but the source's streams.
	if s.TranscodingInfo != nil {
		t.Errorf("direct play has transcoding info %+v", s.TranscodingInfo)
	}
}

func TestSessionsTranscodeFixture(t *testing.T) {
	f := newFake(t)
	f.json("GET /Sessions", "sessions_transcode.json")
	ss, err := f.client().Sessions(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := f.last(); r.URL.RawQuery != "" {
		t.Errorf("query = %q", r.URL.RawQuery)
	}
	var s *Session
	for i := range ss {
		if ss[i].Playing() {
			s = &ss[i]
		}
	}
	if s == nil || s.TranscodingInfo == nil {
		t.Fatal("no transcoding session")
	}
	ti := s.TranscodingInfo
	if ti.VideoCodec != "h264" || ti.AudioCodec != "aac" || ti.Container != "ts" || ti.Bitrate != 364000 ||
		ti.HardwareAccelerationType != "none" || ti.IsVideoDirect || ti.Width != 320 || ti.CompletionPercentage <= 0 {
		t.Errorf("transcoding = %+v", ti)
	}
	v := s.NowPlayingItem.VideoStream()
	if v == nil || v.Codec != "hevc" || v.BitDepth != 10 || v.Profile != "Main 10" {
		t.Errorf("source video = %+v", v)
	}
	var none *NowPlayingItem
	if none.VideoStream() != nil {
		t.Error("nil item has a video stream")
	}
}

func TestUsersFixture(t *testing.T) {
	f := newFake(t)
	f.json("GET /Users", "users.json")
	us, err := f.client().Users(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != 2 || us[0].Name != "itadmin" || !us[0].Policy.IsAdministrator || us[1].Name != "ituser" ||
		us[1].Policy.IsAdministrator || us[1].ID == "" || !us[1].HasPassword || us[0].LastLoginDate == nil {
		t.Errorf("users = %+v", us)
	}
}

func TestAuthenticateByName(t *testing.T) {
	f := newFake(t)
	f.routes["POST /Users/AuthenticateByName"] = func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Username, Pw string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Username != "itadmin" || b.Pw != "s3cret pass" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Error processing request."))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "authenticate_by_name.json"))
	}
	c := f.client()
	r, err := c.AuthenticateByName(bg, "itadmin", "s3cret pass")
	if err != nil {
		t.Fatal(err)
	}
	if r.User.Name != "itadmin" || !r.User.Policy.IsAdministrator || r.AccessToken == "" || r.ServerID == "" {
		t.Errorf("auth = %+v", r)
	}
	req, body := f.last()
	if p := authParts(t, req.Header.Get("X-Emby-Authorization")); req.Header.Get("X-Emby-Token") != "" || p["DeviceId"] != "dev-1" {
		t.Errorf("sign-in presented %v (the admin key must not go with it)", p)
	}
	if !strings.Contains(body, `"Pw":"s3cret pass"`) || req.URL.RawQuery != "" {
		t.Errorf("body %q query %q", body, req.URL.RawQuery)
	}

	_, err = c.AuthenticateByName(bg, "itadmin", "wrong-horse")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-horse") || strings.Contains(err.Error(), "the-api-key") {
		t.Errorf("error %q carries a credential", err)
	}
	if _, err := c.AuthenticateByName(bg, "  ", "x"); err == nil {
		t.Error("empty username accepted")
	}
}

func TestQuickConnectCalls(t *testing.T) {
	f := newFake(t)
	f.routes["GET /QuickConnect/Enabled"] = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("true")) }
	f.json("POST /QuickConnect/Initiate", "quickconnect_initiate.json")
	f.json("GET /QuickConnect/Connect", "quickconnect_connect.json")
	f.routes["POST /QuickConnect/Authorize"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strconv.FormatBool(r.URL.Query().Get("code") == "864725")))
	}
	f.json("POST /Users/AuthenticateWithQuickConnect", "authenticate_by_name.json")
	c := f.client()

	if on, err := c.QuickConnectEnabled(bg); err != nil || !on {
		t.Fatalf("enabled = %v %v", on, err)
	}
	st, err := c.QuickConnectInitiate(bg)
	if err != nil || st.Code != "864725" || st.Secret == "" || st.Authenticated {
		t.Fatalf("initiate = %+v %v", st, err)
	}
	if r, _ := f.last(); strings.Contains(r.Header.Get("X-Emby-Authorization"), "Token=") {
		t.Error("initiate presented the key")
	}
	st, err = c.QuickConnectConnect(bg, "sec ret")
	if err != nil || !st.Authenticated {
		t.Fatalf("connect = %+v %v", st, err)
	}
	if r, _ := f.last(); r.URL.Query().Get("secret") != "sec ret" {
		t.Errorf("connect query = %q", r.URL.RawQuery)
	}
	if err := c.WithToken("admin-token").QuickConnectAuthorize(bg, "864725"); err != nil {
		t.Error(err)
	}
	if r, _ := f.last(); r.Header.Get("X-Emby-Token") != "admin-token" {
		t.Error("authorize didn't act as the approving user")
	}
	if err := c.WithToken("admin-token").QuickConnectAuthorize(bg, "000000"); err == nil {
		t.Error("an unapproved code reported success")
	}
	r, err := c.AuthenticateWithQuickConnect(bg, "sec ret")
	if err != nil || r.AccessToken == "" {
		t.Fatalf("authenticate = %+v %v", r, err)
	}
	if _, body := f.last(); body != `{"Secret":"sec ret"}` {
		t.Errorf("body = %q", body)
	}
	for _, err := range []error{
		func() error { _, err := c.QuickConnectConnect(bg, ""); return err }(),
		func() error { _, err := c.AuthenticateWithQuickConnect(bg, ""); return err }(),
		c.QuickConnectAuthorize(bg, ""),
	} {
		if err == nil {
			t.Error("empty secret/code accepted")
		}
	}
}

func TestLogoutUsesTheUserToken(t *testing.T) {
	f := newFake(t)
	f.status("POST /Sessions/Logout", http.StatusNoContent)
	if err := f.client().WithToken("user-token").Logout(bg); err != nil {
		t.Fatal(err)
	}
	r, _ := f.last()
	if r.Header.Get("X-Emby-Token") != "user-token" {
		t.Errorf("logout header = %q", r.Header.Get("X-Emby-Authorization"))
	}
}

func TestLibraryMediaUpdated(t *testing.T) {
	f := newFake(t)
	f.status("POST /Library/Media/Updated", http.StatusNoContent)
	c := f.client()
	err := c.LibraryMediaUpdated(bg, []MediaUpdate{
		{Path: "/media/Movies/Law & Order (1990)", UpdateType: UpdateCreated},
		{Path: "/media/Movies/C++ [2020]/x.mkv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, body := f.last()
	if r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content type %q", r.Header.Get("Content-Type"))
	}
	var got struct {
		Updates []MediaUpdate `json:"Updates"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Updates) != 2 || got.Updates[0].Path != "/media/Movies/Law & Order (1990)" || got.Updates[0].UpdateType != "Created" ||
		got.Updates[1].UpdateType != "Modified" {
		t.Errorf("body = %s", body)
	}
	n := len(f.seen)
	if err := c.LibraryMediaUpdated(bg, nil); err != nil || len(f.seen) != n {
		t.Errorf("an empty update still called the server")
	}
}

func TestRefreshCalls(t *testing.T) {
	f := newFake(t)
	f.status("POST /Library/Refresh", http.StatusNoContent)
	f.status("POST /Items/abc/Refresh", http.StatusNoContent)
	c := f.client()
	if err := c.RefreshLibrary(bg); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshItem(bg, "abc", false); err != nil {
		t.Fatal(err)
	}
	r, _ := f.last()
	q := r.URL.Query()
	if q.Get("recursive") != "true" || q.Get("metadataRefreshMode") != "Default" || q.Get("replaceAllMetadata") != "false" {
		t.Errorf("default refresh query = %v", q)
	}
	if err := c.RefreshItem(bg, "abc", true); err != nil {
		t.Fatal(err)
	}
	r, _ = f.last()
	if q := r.URL.Query(); q.Get("metadataRefreshMode") != "FullRefresh" || q.Get("replaceAllImages") != "true" {
		t.Errorf("full refresh query = %v", q)
	}
	if err := c.RefreshItem(bg, "missing", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item = %v", err)
	}
	if err := c.RefreshItem(bg, "", false); err == nil {
		t.Error("empty id accepted")
	}
}

func TestErrorKinds(t *testing.T) {
	f := newFake(t)
	f.status("GET /a", http.StatusUnauthorized)
	f.status("GET /b", http.StatusForbidden)
	f.routes["GET /c"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"x","title":"Database is locked","status":500}`))
	}
	f.routes["GET /d"] = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>not json")) }
	c := f.client()
	get := func(p string) error {
		return c.call(bg, http.MethodGet, p, url.Values{"secret": {"qc-secret"}}, nil, &struct{}{}, true)
	}

	cases := []struct {
		path string
		kind error
		msg  string
	}{
		{"/a", ErrUnauthorized, "rejected the key"},
		{"/b", ErrForbidden, "HTTP 403"},
		{"/nope", ErrNotFound, "GET /nope"},
		{"/c", nil, "HTTP 500 on GET /c: Database is locked"},
		{"/d", nil, "unexpected answer"},
	}
	for _, tc := range cases {
		err := get(tc.path)
		var je *Error
		if !errors.As(err, &je) {
			t.Fatalf("%s: %v is not an *Error", tc.path, err)
		}
		if tc.kind != nil && !errors.Is(err, tc.kind) {
			t.Errorf("%s: %v, want %v", tc.path, err, tc.kind)
		}
		if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: %q lacks %q", tc.path, err, tc.msg)
		}
		if strings.Contains(err.Error(), "qc-secret") || strings.Contains(err.Error(), "the-api-key") {
			t.Errorf("%s: %q leaks a secret", tc.path, err)
		}
	}

	// Nothing listening: ErrUnreachable, and the URL's query (a Quick Connect secret) stays out of the message.
	addr := f.srv.URL
	f.srv.Close()
	err := New(addr, "the-api-key", "", "", nil).call(bg, http.MethodGet, "/QuickConnect/Connect", url.Values{"secret": {"qc-secret"}}, nil, nil, false)
	if !errors.Is(err, ErrUnreachable) || strings.Contains(err.Error(), "qc-secret") || strings.Contains(err.Error(), "the-api-key") {
		t.Errorf("unreachable = %v", err)
	}
	// A call cut short by its context is unreachable too (TEST-NET-1 never answers).
	ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	if _, err := New("http://192.0.2.1:8096", "k", "", "", nil).SystemInfo(ctx); !errors.Is(err, ErrUnreachable) {
		t.Errorf("timeout = %v", err)
	}
	if _, err := New("http://bad host", "k", "", "", nil).SystemInfo(bg); !errors.Is(err, ErrNotConfigured) && !errors.Is(err, ErrUnreachable) {
		t.Errorf("bad url = %v", err)
	}
}

func TestProviderIDs(t *testing.T) {
	var it Item
	if err := json.Unmarshal([]byte(`{"ProviderIds":{"tmdb":" 603 ","TVDB":"81189","Imdb":"tt0133093","Tvdb2":"x","TmdbCollection":"2344"}}`), &it); err != nil {
		t.Fatal(err)
	}
	p := it.ProviderIDs
	if p.TMDB != 603 || p.TVDB != 81189 || p.IMDB != "tt0133093" || p.Raw["TmdbCollection"] != "2344" {
		t.Errorf("ids = %+v", p)
	}
	// Junk never fails the page it came in.
	if err := json.Unmarshal([]byte(`{"Name":"x","ProviderIds":{"Tmdb":"abc","Imdb":"0133093","Tvdb":-1}}`), &it); err != nil {
		t.Fatal(err)
	}
	if it.Name != "x" || it.ProviderIDs.TMDB != 0 || it.ProviderIDs.IMDB != "" || it.ProviderIDs.TVDB != 0 {
		t.Errorf("junk ids = %+v", it.ProviderIDs)
	}
	if err := json.Unmarshal([]byte(`{"ProviderIds":null}`), &it); err != nil || it.ProviderIDs.TMDB != 0 {
		t.Errorf("null ids = %+v %v", it.ProviderIDs, err)
	}
}

func TestWatchURL(t *testing.T) {
	got := WatchURL(" https://jf.example.com/ ", "5e7e", "4071f1445812b3d8fba8a91b47dbb5fb")
	if got != "https://jf.example.com/web/#/details?id=4071f1445812b3d8fba8a91b47dbb5fb&serverId=5e7e" {
		t.Errorf("watch url = %s", got)
	}
	if got := WatchURL("http://10.0.0.5:8096/emby", "", "abc"); got != "http://10.0.0.5:8096/emby/web/#/details?id=abc" {
		t.Errorf("subpath url = %s", got)
	}
	if WatchURL("", "s", "i") != "" || WatchURL("http://x", "s", "") != "" {
		t.Error("watch url without a base or item")
	}
}

func TestNewAddsEmbyAPIPrefix(t *testing.T) {
	for input, want := range map[string]string{
		"http://emby.local:8096":           "http://emby.local:8096/emby",
		"http://emby.local:8096/emby/":     "http://emby.local:8096/emby",
		"https://media.example/proxy":      "https://media.example/proxy/emby",
		"https://media.example/proxy/emby": "https://media.example/proxy/emby",
	} {
		if got := New(input, "key", "", "", nil).BaseURL(); got != want {
			t.Errorf("New(%q).BaseURL() = %q, want %q", input, got, want)
		}
	}
}

func TestItemImage(t *testing.T) {
	const id = "4071f1445812b3d8fba8a91b47dbb5fb"
	if p := ItemImagePath(id, "", "tag1", 300); p != "/Items/"+id+"/Images/Primary?maxWidth=300&quality=90&tag=tag1" {
		t.Errorf("path = %s", p)
	}
	f := newFake(t)
	f.routes["GET /Items/"+id+"/Images/Backdrop"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	}
	c := f.client()
	resp, err := c.Image(bg, id, ImageBackdrop, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "jpeg" {
		t.Errorf("image body = %q", b)
	}
	if r, _ := f.last(); r.Header.Get("X-Emby-Token") != "the-api-key" {
		t.Error("image fetch not keyed")
	}
	if _, err := c.Image(bg, id, ImagePrimary, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("no artwork = %v", err)
	}
	n := len(f.seen)
	for _, bad := range [][2]string{
		{"../System/Info", ImagePrimary}, {id + "?x=1", ImagePrimary}, {"abc", ImagePrimary}, {id, "Primary/../../x"}, {id, "Chapter"},
	} {
		if _, err := c.Image(bg, bad[0], bad[1], 0); err == nil {
			t.Errorf("image %q/%q accepted", bad[0], bad[1])
		}
	}
	if len(f.seen) != n {
		t.Error("a rejected image request reached the server")
	}
}

func TestTicks(t *testing.T) {
	if TicksToDuration(40_000_000) != 4*time.Second || DurationToTicks(4*time.Second) != 40_000_000 {
		t.Error("ticks conversion")
	}
	if ticksPerSecond != int64(time.Second/100) {
		t.Error("ticksPerSecond")
	}
}
