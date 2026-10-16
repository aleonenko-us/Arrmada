package plexscan

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// A change in an extra Movies folder is resolved against that folder — its own Plex
// library, here matched by name — rather than scanning every movie library.
func TestScanExtraFolderHitsItsOwnLibrary(t *testing.T) {
	h := newHarness(t)
	h.plex.libs = append(h.plex.libs, plex.Library{Key: "3", Title: "Kids Movies", Type: "movie", Locations: []string{"/data/media/kids"}})
	h.s.SetExtraRoots(func(context.Context) map[string][]string {
		return map[string][]string{KindMovie: {"/kids"}}
	})
	h.s.Request(KindMovie, "/kids/Up (2009)")
	h.advance(time.Minute)
	got := h.plex.got()
	if len(got) != 1 || got[0] != (refresh{"3", "/data/media/kids/Up (2009)"}) {
		t.Fatalf("refreshes = %v, want just the Kids library's folder", got)
	}
	// The settings card lists the extra folder too.
	v := h.s.View(context.Background())
	var kinds []string
	for _, r := range v.Roots {
		kinds = append(kinds, r.Kind+":"+r.ArrmadaRoot)
	}
	if len(v.Roots) != 3 || v.Roots[1].ArrmadaRoot != "/kids" || v.Roots[1].Sections[0] != "Kids Movies" {
		t.Errorf("view roots = %v (%+v)", kinds, v.Roots)
	}
	// Test / Scan now for the extra folder, and only for a real one.
	if rv, err := h.s.ScanRootNow(context.Background(), KindMovie, "/kids", false); err != nil || rv.ArrmadaRoot != "/kids" {
		t.Errorf("scan root now = %+v, %v", rv, err)
	}
	if _, err := h.s.ScanRootNow(context.Background(), KindMovie, "/elsewhere", false); err == nil {
		t.Error("Scan now accepted a folder that isn't Arrmada's")
	}
}

func TestRootForPicksTheLongest(t *testing.T) {
	roots := []string{"/media/movies", "/media/movies-kids", "/media/movies/4k"}
	for dir, want := range map[string]string{
		"/media/movies/Heat (1995)":    "/media/movies",
		"/media/movies-kids/Up (2009)": "/media/movies-kids",
		"/media/movies/4k/Dune (2021)": "/media/movies/4k",
		"/elsewhere/x":                 "/media/movies", // nothing holds it: the default
	} {
		if got := RootFor(roots, dir); got != want {
			t.Errorf("RootFor(%q) = %q, want %q", dir, got, want)
		}
	}
	if got := RootFor(nil, "/x"); got != "" {
		t.Errorf("no roots = %q", got)
	}
}
