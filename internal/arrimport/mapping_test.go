package arrimport

import (
	"testing"
)

func TestRemap(t *testing.T) {
	maps := []PathMap{
		{From: "/movies", To: "/storage/movies"},
		{From: "/movies/kids/", To: "/storage/kids"},
		{From: `D:\Media\TV`, To: "/storage/tv"},
		{From: "/", To: "/root-of-everything"},
	}
	for _, c := range []struct{ in, want string }{
		{"/movies/Heat (1995)/Heat.mkv", "/storage/movies/Heat (1995)/Heat.mkv"},
		{"/movies/kids/Up (2009)/Up.mkv", "/storage/kids/Up (2009)/Up.mkv"}, // the longest map wins
		{"/movies", "/storage/movies"},
		{"/moviesextra/x.mkv", "/root-of-everything/moviesextra/x.mkv"}, // not under /movies
		{`d:\media\tv\Show\Season 1\e1.mkv`, "/storage/tv/Show/Season 1/e1.mkv"},
	} {
		if got := Remap(c.in, maps); got != c.want {
			t.Errorf("Remap(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Remap("/tv/Show/e1.mkv", nil); got != "/tv/Show/e1.mkv" {
		t.Errorf("no maps: %q", got)
	}
}

func TestCleanPathMaps(t *testing.T) {
	got, err := CleanPathMaps([]PathMap{{From: " /movies/ ", To: "/storage/movies/"}, {}, {From: `C:\TV`, To: "/tv"}})
	if err != nil || len(got) != 2 || got[0] != (PathMap{From: "/movies", To: "/storage/movies"}) || got[1].From != "C:/TV" {
		t.Fatalf("clean = %+v, %v", got, err)
	}
	for _, bad := range [][]PathMap{
		{{From: "/movies", To: ""}},
		{{From: "", To: "/x"}},
		{{From: "/movies", To: "storage/movies"}},
		{{From: "/movies", To: "/a"}, {From: "/movies/", To: "/b"}},
		{{From: `C:\TV`, To: "/a"}, {From: `c:/tv`, To: "/b"}},
	} {
		if _, err := CleanPathMaps(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestSuggestPathMaps(t *testing.T) {
	zero, three := int64(0), int64(3)
	roots := []Mapping{
		{Root: Root{Path: "/movies"}, FolderID: &zero},
		{Root: Root{Path: "/kids"}, FolderID: &three},
		{Root: Root{Path: "/storage/same"}, FolderID: &three},
		{Root: Root{Path: "/unmatched"}},
	}
	got := SuggestPathMaps(roots, []Folder{{ID: 0, Path: "/storage/movies"}, {ID: 3, Path: "/storage/same"}})
	if len(got) != 2 || got[0] != (PathMap{From: "/movies", To: "/storage/movies"}) || got[1] != (PathMap{From: "/kids", To: "/storage/same"}) {
		t.Fatalf("suggested = %+v", got)
	}
}

func TestSuggestProfile(t *testing.T) {
	targets := []Target{
		{Key: "custom:1", Name: "Standard", MaxResolution: 1080, Default: true},
		{Key: "custom:2", Name: "HD 720p", MaxResolution: 720},
		{Key: "custom:3", Name: "4K HDR", MaxResolution: 2160},
		{Key: "custom:4", Name: "Remux 1080", MaxResolution: 1080},
	}
	for _, c := range []struct {
		p    Profile
		want string
	}{
		{Profile{Name: "HD-720p", MaxResolution: 720}, "custom:2"},   // same name, loosely
		{Profile{Name: "Ultra-HD", MaxResolution: 2160}, "custom:3"}, // same resolution
		{Profile{Name: "HD-1080p", MaxResolution: 1080}, "custom:1"}, // same resolution: the default first
		{Profile{Name: "SD", MaxResolution: 480}, "custom:1"},        // nothing close: the default
		{Profile{Name: "Any", MaxResolution: 0}, "custom:1"},
	} {
		if got := SuggestProfile(c.p, targets); got != c.want {
			t.Errorf("%s → %q, want %q", c.p.Name, got, c.want)
		}
	}
	if got := SuggestProfile(Profile{Name: "x"}, nil); got != "" {
		t.Errorf("no targets → %q", got)
	}
	if got := MaxResolution([]string{"720p", "2160p", "sd"}); got != 2160 {
		t.Errorf("MaxResolution = %d", got)
	}
}

func TestAvailabilityAndMonitorNew(t *testing.T) {
	for in, want := range map[string]string{"announced": "announced", "tba": "announced", "inCinemas": "inCinemas", "released": "released", "preDB": "released", "": "released"} {
		if got := availability(in); got != want {
			t.Errorf("availability(%q) = %q", in, got)
		}
	}
	if !monitorNew(Series{MonitorNewItems: "all"}) || monitorNew(Series{MonitorNewItems: "none", Seasons: []Season{{SeasonNumber: 2, Monitored: true}}}) {
		t.Error("Sonarr 4's monitorNewItems not followed")
	}
	v3 := Series{Seasons: []Season{{SeasonNumber: 1, Monitored: false}, {SeasonNumber: 3, Monitored: true}, {SeasonNumber: 2}}}
	if !monitorNew(v3) {
		t.Error("Sonarr 3: new seasons should follow the latest season")
	}
}
