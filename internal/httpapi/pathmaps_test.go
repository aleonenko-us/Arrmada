package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/download"
)

// INT-25: per-client path mappings are saved, checked and refused through the client API,
// the client Test reports which paths Arrmada can see, and health names a client whose
// finished downloads aren't visible.

// fakePathQbit is an httptest qBittorrent that saves to save and reports torrents (JSON).
func fakePathQbit(t *testing.T, save, torrents string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "Ok.") })
	mux.HandleFunc("/api/v2/app/version", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "v5.0.0") })
	mux.HandleFunc("/api/v2/app/preferences", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"save_path": save})
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, torrents) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func finishedTorrent(hash, contentPath string, added int) string {
	return fmt.Sprintf(`{"hash":%q,"name":%q,"size":10,"progress":1.0,"state":"stalledUP","category":"arrmada","completed":10,"amount_left":0,"added_on":%d,"content_path":%q}`,
		hash, filepath.Base(contentPath), added, contentPath)
}

func TestClientPathMapsSavedAndRefused(t *testing.T) {
	s, dl := newClientServer(t)
	ctx := context.Background()
	c, err := dl.Create(ctx, download.Client{Name: "seedbox", Kind: download.KindQbittorrent, URL: "http://sb:8080", Password: "pw", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	path := fmt.Sprintf("/api/v1/downloadclients/%d", c.ID)
	put := func(extra string) *httptest.ResponseRecorder {
		return s.doBody("PUT", path, `{"name":"seedbox","kind":"qbittorrent","url":"http://sb:8080"`+extra+`}`, mgr)
	}

	rec := put(`,"path_maps":[{"remote":"/downloads/","local":"/media/downloads/"}]`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path_maps":[{"remote":"/downloads","local":"/media/downloads"}]`) {
		t.Fatalf("save: HTTP %d %s", rec.Code, rec.Body)
	}
	// Leaving path_maps out (an on/off toggle) keeps them.
	if rec := put(`,"enabled":false`); rec.Code != http.StatusOK {
		t.Fatalf("toggle: HTTP %d", rec.Code)
	}
	if got, _ := dl.Get(ctx, c.ID); len(got.PathMaps) != 1 || got.Enabled {
		t.Fatalf("after toggle = %+v", got)
	}

	for _, tc := range []struct{ local, why string }{
		{"/data/downloads", "data folder"},
		{s.deps.Config.DataDir, "data folder"},
		{filepath.Join(s.deps.Config.DataDir, "media"), "data folder"},
		{"/", "whole filesystem"},
		{"relative", "full path"},
	} {
		body, _ := json.Marshal([]download.PathMap{{Remote: "/downloads", Local: tc.local}})
		rec := put(`,"path_maps":` + string(body))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.why) {
			t.Errorf("local %q: HTTP %d %s, want 400 saying %q", tc.local, rec.Code, rec.Body, tc.why)
		}
	}
	if got, _ := dl.Get(ctx, c.ID); len(got.PathMaps) != 1 || got.PathMaps[0].Local != "/media/downloads" {
		t.Fatalf("a refused mapping changed what's stored: %+v", got.PathMaps)
	}
	// An empty list clears them.
	if rec := put(`,"path_maps":[]`); rec.Code != http.StatusOK {
		t.Fatalf("clear: HTTP %d", rec.Code)
	}
	if got, _ := dl.Get(ctx, c.ID); len(got.PathMaps) != 0 {
		t.Fatalf("not cleared: %+v", got.PathMaps)
	}

	// The bundled qBittorrent shares Arrmada's folders and takes none.
	if err := dl.EnsureBundled(ctx, bundledQbitURL); err != nil {
		t.Fatal(err)
	}
	clients, _ := dl.List(ctx)
	var bundled download.Client
	for _, x := range clients {
		if x.Bundled {
			bundled = x
		}
	}
	rec = s.doBody("PUT", fmt.Sprintf("/api/v1/downloadclients/%d", bundled.ID),
		`{"name":"b","kind":"qbittorrent","url":"`+bundledQbitURL+`","path_maps":[{"remote":"/downloads","local":"/media/downloads"}]}`, mgr)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bundled") {
		t.Fatalf("bundled with a mapping: HTTP %d %s", rec.Code, rec.Body)
	}
}

func TestClientTestReportsPaths(t *testing.T) {
	downloads := t.TempDir()
	if err := os.MkdirAll(filepath.Join(downloads, "Film.2020"), 0o755); err != nil {
		t.Fatal(err)
	}
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		d.Config.DownloadsDir = downloads
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	ctx := context.Background()
	url := fakePathQbit(t, "/downloads", "["+finishedTorrent("a1", "/downloads/Film.2020", 5)+"]")
	c, _ := dl.Create(ctx, download.Client{Name: "seedbox", Kind: download.KindQbittorrent, URL: url, Enabled: true})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	rec := s.doBody("POST", fmt.Sprintf("/api/v1/downloadclients/%d/test", c.ID), "", mgr)
	var out struct {
		OK    bool                `json:"ok"`
		Paths download.PathReport `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("test: HTTP %d %s", rec.Code, rec.Body)
	}
	if !out.OK || out.Paths.OK || len(out.Paths.Checks) != 2 || out.Paths.Checks[0].Remote != "/downloads" {
		t.Fatalf("report = %+v", out)
	}
	if out.Paths.Suggestion == nil || out.Paths.Suggestion.Remote != "/downloads" || out.Paths.Suggestion.Local != downloads {
		t.Fatalf("suggestion = %+v", out.Paths.Suggestion)
	}

	// The card's Test says it too, as a failure with the fix.
	rec = s.doBody("POST", fmt.Sprintf("/api/v1/connections/download_client/%d/test", c.ID), "", mgr)
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "can't see") {
		t.Fatalf("connection test: %s", rec.Body)
	}
}

func TestHealthNamesClientWithUnseenDownloads(t *testing.T) {
	seen := t.TempDir()
	var dl *download.Service
	s := newRouteServer(t, func(d *Deps) {
		dl = download.NewService(d.Store.DB(), d.Log)
		d.Downloads = dl
	})
	a := &api{deps: s.deps}
	ctx := context.Background()
	remote := fakePathQbit(t, "/downloads", "["+finishedTorrent("a1", "/downloads/Gone.2020", 2)+","+finishedTorrent("a2", seen, 1)+"]")
	bundled := fakePathQbit(t, "/downloads", "["+finishedTorrent("b1", "/downloads/AlsoGone", 1)+"]")
	_, _ = dl.Create(ctx, download.Client{Name: "seedbox", Kind: download.KindQbittorrent, URL: remote, Enabled: true})
	_, _ = dl.Create(ctx, download.Client{Name: "bundled", Kind: download.KindQbittorrent, URL: bundled, Enabled: true, Bundled: true})

	got, err := a.unseenDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ClientName != "seedbox" || got[0].Count != 1 || got[0].Example != "/downloads/Gone.2020" {
		t.Fatalf("unseen = %+v (want only the seedbox's missing download; the bundled client is left out)", got)
	}
}
