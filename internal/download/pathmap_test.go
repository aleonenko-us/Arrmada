package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// INT-25: remote path mappings for an external or seedbox qBittorrent.

func TestPathMapsToLocal(t *testing.T) {
	maps := PathMaps{
		{Remote: "/downloads", Local: "/media/downloads"},
		{Remote: "/downloads/tv/", Local: "/media/tv-downloads/"}, // longer: wins inside it
		{Remote: `D:\Torrents`, Local: "/mnt/pc"},
	}
	for _, tc := range []struct{ in, want string }{
		{"/downloads/Movie.2024/movie.mkv", "/media/downloads/Movie.2024/movie.mkv"},
		{"/downloads", "/media/downloads"},
		{"/downloads/", "/media/downloads"},
		{"/downloads/tv/Show.S01", "/media/tv-downloads/Show.S01"},
		{"/downloadsx/Movie", "/downloadsx/Movie"}, // a sibling, not inside
		{"/other/Movie", "/other/Movie"},           // no mapping: as given
		{`D:\Torrents\Film (2020)\film.mkv`, "/mnt/pc/Film (2020)/film.mkv"},
		{`d:\torrents\Film`, "/mnt/pc/Film"}, // Windows paths compare without case
		{"relative/path", "relative/path"},
		{"", ""},
		// A path that climbs out of the mapped folder is not inside it, so it isn't mapped
		// (and can't be walked out of the local side either).
		{"/downloads/../etc/passwd", "/downloads/../etc/passwd"},
		{"/downloads/a/../../etc", "/downloads/a/../../etc"},
	} {
		if got := maps.ToLocal(tc.in); got != filepath.FromSlash(tc.want) && got != tc.want {
			t.Errorf("ToLocal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A path that stays inside after cleaning maps to its cleaned form.
	if got := maps.ToLocal("/downloads/a/../b"); got != filepath.FromSlash("/media/downloads/b") {
		t.Errorf("cleaned inside = %q", got)
	}
}

func TestPathMapsToRemote(t *testing.T) {
	maps := PathMaps{
		{Remote: "/downloads", Local: "/media/downloads"},
		{Remote: "/seedbox/tv", Local: "/media/downloads/tv"},
		{Remote: `D:\Torrents`, Local: "/mnt/pc"},
	}
	for _, tc := range []struct{ in, want string }{
		{"/media/downloads", "/downloads"},
		{"/media/downloads/", "/downloads"},
		{"/media/downloads/sub", "/downloads/sub"},
		{"/media/downloads/tv/Show", "/seedbox/tv/Show"}, // longest local prefix
		{"/mnt/pc/movies", `D:\Torrents\movies`},
		{"/media/downloadsx", "/media/downloadsx"},
		{"/elsewhere", "/elsewhere"},
	} {
		if got := maps.ToRemote(filepath.FromSlash(tc.in)); got != tc.want {
			t.Errorf("ToRemote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Without mappings both directions are the identity, byte for byte.
func TestNoPathMapsIsANoOp(t *testing.T) {
	var none PathMaps
	for _, p := range []string{"/downloads/x", "/downloads/../x", `D:\x`, "", "rel"} {
		if none.ToLocal(p) != p || none.ToRemote(p) != p {
			t.Errorf("%q changed with no mappings", p)
		}
	}
}

func TestValidatePathMaps(t *testing.T) {
	ok, err := ValidatePathMaps([]PathMap{
		{Remote: "/downloads/", Local: "/media/downloads/"},
		{Remote: " ", Local: ""}, // a blank row is dropped
		{Remote: `d:\Torrents\`, Local: "/mnt/pc"},
	}, "/config")
	if err != nil {
		t.Fatal(err)
	}
	want := PathMaps{{Remote: "/downloads", Local: filepath.Clean("/media/downloads")}, {Remote: `D:\Torrents`, Local: filepath.Clean("/mnt/pc")}}
	if fmt.Sprint(ok) != fmt.Sprint(want) {
		t.Fatalf("normalised = %v, want %v", ok, want)
	}

	for _, tc := range []struct {
		name string
		maps []PathMap
		why  string
	}{
		{"relative remote", []PathMap{{"downloads", "/media/d"}}, "full path"},
		{"relative local", []PathMap{{"/downloads", "media/d"}}, "full path"},
		{"remote root", []PathMap{{"/", "/media/d"}}, "whole of"},
		{"remote drive root", []PathMap{{`D:\`, "/media/d"}}, "whole of"},
		{"local root", []PathMap{{"/downloads", "/"}}, "whole filesystem"},
		{"local is the data dir", []PathMap{{"/downloads", "/config"}}, "data folder"},
		{"local inside the data dir", []PathMap{{"/downloads", "/config/media"}}, "data folder"},
		{"local holds the data dir", []PathMap{{"/downloads", "/config/.."}}, "whole filesystem"},
		{"local under /data", []PathMap{{"/downloads", "/data/downloads"}}, "data folder"},
		{"local is /data", []PathMap{{"/downloads", "/data"}}, "data folder"},
		{"duplicate remote", []PathMap{{"/downloads", "/a"}, {"/downloads/", "/b"}}, "twice"},
		{"NUL", []PathMap{{"/down\x00loads", "/a"}}, "full path"},
	} {
		_, err := ValidatePathMaps(tc.maps, "/config")
		if !errors.Is(err, ErrBadPathMap) || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: err = %v, want one saying %q", tc.name, err, tc.why)
		}
	}
	// A local folder that holds the data dir is refused too.
	if _, err := ValidatePathMaps([]PathMap{{"/downloads", "/srv"}}, "/srv/arrmada"); !errors.Is(err, ErrBadPathMap) {
		t.Errorf("parent of the data dir accepted: %v", err)
	}
	many := make([]PathMap, maxPathMaps+1)
	for i := range many {
		many[i] = PathMap{Remote: fmt.Sprintf("/r%d", i), Local: fmt.Sprintf("/l%d", i)}
	}
	if _, err := ValidatePathMaps(many, "/config"); !errors.Is(err, ErrBadPathMap) {
		t.Error("too many mappings accepted")
	}
}

func TestPathMapsStored(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	maps := PathMaps{{Remote: "/downloads", Local: "/media/downloads"}}
	c, err := svc.Create(ctx, Client{Name: "seedbox", Kind: KindQbittorrent, URL: "http://sb", Enabled: true, PathMaps: maps})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, c.ID)
	if fmt.Sprint(got.PathMaps) != fmt.Sprint(maps) {
		t.Fatalf("stored = %v", got.PathMaps)
	}
	got.PathMaps = nil
	if _, err := svc.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Get(ctx, c.ID); len(got.PathMaps) != 0 {
		t.Fatalf("cleared = %v", got.PathMaps)
	}
	if decodePathMaps("{not json") != nil || encodePathMaps(nil) != "" {
		t.Fatal("column encoding")
	}
}

// fakeMappedQbit is a qBittorrent stub that reports its save path and torrents in its own
// paths and records the savepath of every add.
func fakeMappedQbit(t *testing.T, save string, info string) (url string, savepaths func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	var logins int32
	srv := qbitTestServer(t, &logins, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/preferences":
			_ = json.NewEncoder(w).Encode(map[string]any{"save_path": save, "listen_port": 6881})
		case "/api/v2/torrents/info":
			fmt.Fprint(w, info)
		case "/api/v2/torrents/add":
			_ = r.ParseMultipartForm(1 << 20)
			mu.Lock()
			got = append(got, r.FormValue("savepath"))
			mu.Unlock()
			fmt.Fprint(w, "Ok.")
		case "/api/v2/app/version":
			fmt.Fprint(w, "v5.0.0")
		default:
			http.NotFound(w, r)
		}
	})
	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

const mappedInfo = `[
  {"hash":"aaa","name":"Film.2020","size":100,"progress":1.0,"state":"stalledUP","category":"arrmada",
   "completed":100,"amount_left":0,"added_on":200,"content_path":"/downloads/Film.2020"},
  {"hash":"bbb","name":"Old.2019","size":100,"progress":1.0,"state":"stalledUP","category":"arrmada",
   "completed":100,"amount_left":0,"added_on":100,"content_path":"/downloads/Old.2019"},
  {"hash":"ccc","name":"Busy","size":100,"progress":0.5,"state":"downloading","category":"arrmada",
   "completed":50,"amount_left":50,"added_on":300,"content_path":"/downloads/Busy"}
]`

// With /downloads → /media/downloads, adds send savepath=/downloads and the queue reports
// /media/downloads/...; the bundled-style client without mappings is untouched.
func TestServiceAppliesPathMaps(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	url, saves := fakeMappedQbit(t, "/downloads", mappedInfo)
	_, err := svc.Create(ctx, Client{Name: "seedbox", Kind: KindQbittorrent, URL: url, Enabled: true,
		PathMaps: PathMaps{{Remote: "/downloads", Local: "/media/downloads"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Add(ctx, AddRequest{Name: "Film", URL: "magnet:?xt=urn:btih:abc", SavePath: "/media/downloads"}); err != nil {
		t.Fatal(err)
	}
	if got := saves(); len(got) != 1 || got[0] != "/downloads" {
		t.Fatalf("savepath sent = %v, want /downloads", got)
	}
	items, complete, err := svc.QueueComplete(ctx)
	if err != nil || !complete {
		t.Fatalf("queue: %v complete=%v", err, complete)
	}
	for _, it := range items {
		if !strings.HasPrefix(it.ContentPath, filepath.FromSlash("/media/downloads/")) {
			t.Errorf("%s content path = %q, want it mapped", it.Name, it.ContentPath)
		}
	}

	// No mappings: exactly what the client said, and the savepath as given.
	plain, _ := newTestService(t)
	url2, saves2 := fakeMappedQbit(t, "/downloads", mappedInfo)
	_, _ = plain.Create(ctx, Client{Name: "bundled", Kind: KindQbittorrent, URL: url2, Enabled: true, Bundled: true})
	_ = plain.Add(ctx, AddRequest{Name: "Film", URL: "magnet:?xt=urn:btih:abc", SavePath: "/downloads"})
	if got := saves2(); len(got) != 1 || got[0] != "/downloads" {
		t.Fatalf("unmapped savepath = %v", got)
	}
	items, _, _ = plain.QueueComplete(ctx)
	if len(items) != 3 || items[0].ContentPath != "/downloads/Film.2020" {
		t.Fatalf("unmapped content path = %+v", items)
	}
}

// TestPaths reads qBittorrent's preferences and says which paths Arrmada can see, with a
// mapping that would fix the ones it can't.
func TestTestPathsReportsAndSuggests(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	downloads := filepath.Join(root, "media", "downloads")
	library := filepath.Join(root, "media", "movies")
	for _, d := range []string{filepath.Join(downloads, "Film.2020"), library} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := PathEnv{Downloads: downloads, Libraries: []string{library}, DataDir: filepath.Join(root, "data")}
	svc, _ := newTestService(t)
	url, _ := fakeMappedQbit(t, "/downloads", mappedInfo)
	c, _ := svc.Create(ctx, Client{Name: "seedbox", Kind: KindQbittorrent, URL: url, Enabled: true})

	rep, err := svc.TestPaths(ctx, c.ID, env)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Checks) != 2 || rep.Checks[0].Visible || rep.Checks[1].Visible {
		t.Fatalf("unmapped report = %+v", rep)
	}
	if rep.Checks[0].Remote != "/downloads" || rep.Checks[1].Remote != "/downloads/Film.2020" {
		t.Fatalf("checks = %+v (want the save folder and the newest finished download)", rep.Checks)
	}
	if rep.Suggestion == nil || rep.Suggestion.Remote != "/downloads" || rep.Suggestion.Local != downloads {
		t.Fatalf("suggestion = %+v", rep.Suggestion)
	}
	if !strings.Contains(rep.Summary, "can't see") || !strings.Contains(rep.Summary, "/downloads → "+downloads) {
		t.Fatalf("summary = %q", rep.Summary)
	}

	// With the suggested mapping saved, everything is visible.
	c.PathMaps = PathMaps{*rep.Suggestion}
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.TestPaths(ctx, c.ID, env)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.Suggestion != nil || !rep.Checks[0].Visible || rep.Checks[0].Local != downloads {
		t.Fatalf("mapped report = %+v", rep)
	}
	if !strings.Contains(rep.Summary, "Arrmada sees it as "+downloads+" ✓") {
		t.Fatalf("summary = %q", rep.Summary)
	}
}

// The suggestion comes from the newest download when its folder sits deeper than the save
// path, and is never one the rules would refuse.
func TestSuggestMapping(t *testing.T) {
	root := t.TempDir()
	downloads := filepath.Join(root, "dl")
	if err := os.MkdirAll(filepath.Join(downloads, "complete", "Film"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := Client{Name: "qb"}
	newest := &Item{ContentPath: "/srv/torrents/complete/Film"}
	rep := checkPaths(c, "/srv/torrents/incomplete", newest, PathEnv{Downloads: downloads, DataDir: filepath.Join(root, "data")})
	if rep.Suggestion == nil || rep.Suggestion.Remote != "/srv/torrents" {
		t.Fatalf("suggestion = %+v", rep.Suggestion)
	}
	// Arrmada's downloads folder inside the data dir is never suggested.
	rep = checkPaths(c, "/srv/torrents", newest, PathEnv{Downloads: downloads, DataDir: root})
	if rep.Suggestion != nil {
		t.Fatalf("suggested a mapping into the data dir: %+v", rep.Suggestion)
	}
	// A newest download missing from a folder Arrmada can see was moved or deleted: shown,
	// but no mapping problem.
	saveDir := filepath.Join(root, "seen")
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapped := Client{Name: "qb", PathMaps: PathMaps{{Remote: "/srv/torrents", Local: saveDir}}}
	rep = checkPaths(mapped, "/srv/torrents", &Item{ContentPath: "/srv/torrents/Deleted"}, PathEnv{Downloads: downloads})
	if !rep.OK || !rep.Checks[1].Gone || rep.Checks[1].Visible || rep.Suggestion != nil || !strings.HasSuffix(rep.Summary, "✓") {
		t.Fatalf("deleted download = %+v", rep)
	}
	// One outside the save folder that isn't visible is a real problem.
	rep = checkPaths(mapped, "/srv/torrents", &Item{ContentPath: "/elsewhere/Film"}, PathEnv{Downloads: downloads})
	if rep.OK || rep.Checks[1].Gone {
		t.Fatalf("unseen download elsewhere = %+v", rep)
	}
	// A client that can't report paths says so.
	svc, _ := newTestService(t)
	svc.registry.impls[KindQbittorrent] = &fakeClient{}
	cl, _ := svc.Create(context.Background(), Client{Name: "x", Kind: KindQbittorrent, URL: "http://x", Enabled: true})
	if _, err := svc.TestPaths(context.Background(), cl.ID, PathEnv{}); !errors.Is(err, ErrNoPathCheck) {
		t.Fatalf("no path reader = %v", err)
	}
}
