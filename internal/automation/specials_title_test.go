package automation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

func specialsShow() series.Series {
	return series.Series{ID: 1, Title: "Show", Year: 2015, Seasons: []series.Season{
		{SeasonNumber: 0, Episodes: []series.Episode{
			{SeasonNumber: 0, EpisodeNumber: 1, Title: "The Christmas Special", AirDate: "2019-12-25"},
			{SeasonNumber: 0, EpisodeNumber: 2, Title: "Pilot", AirDate: "2015-01-01"},
			{SeasonNumber: 0, EpisodeNumber: 3, Title: "Show: Patagonia Special", AirDate: "2016-03-01"},
		}},
		{SeasonNumber: 1, Episodes: []series.Episode{{SeasonNumber: 1, EpisodeNumber: 5, Title: "The Christmas Special", AirDate: "2015-12-25"}}},
	}}
}

// A special released by name matches its special — the show, then its title — and
// nothing that carries numbering, a different show, or only part of the title does.
func TestSpecialTitleMatch(t *testing.T) {
	s := specialsShow()
	xmas, pilot, patagonia := s.Seasons[0].Episodes[0], s.Seasons[0].Episodes[1], s.Seasons[0].Episodes[2]
	cases := []struct {
		name string
		ep   series.Episode
		want bool
	}{
		{"Show.The.Christmas.Special.2019.1080p.WEB", xmas, true},
		{"Show.Christmas.Special.1080p.WEB-DL.x264-GRP.mkv", xmas, true},
		{"Show.2015.The.Christmas.Special.720p.HDTV", xmas, true}, // the show's own year
		{"Show.2019.The.Christmas.Special.720p.HDTV", xmas, true}, // the special's air year
		{"Show.Patagonia.Special.720p.HDTV.x264", patagonia, true},
		{"Show.Pilot.1080p.WEB", pilot, true},
		{"Show.Pilot.2015.1080p.WEB", pilot, true},
		// Partial words: the title is matched word by word, never as a substring.
		{"Show.The.Christmas.Spectacular.1080p.WEB", xmas, false},
		{"Show.Christmas.2019.1080p.WEB", xmas, false},
		{"Show.Christmas.Specials.1080p.WEB", xmas, false},
		// A one-word title must be the whole name, not one word of it.
		{"Show.Pilot.Season.Recap.1080p.WEB", pilot, false},
		// Numbering wins over a title: a regular episode, a special by number, a pack.
		{"Show.S01E05.The.Christmas.Special.1080p.WEB", xmas, false},
		{"Show.S00E07.The.Christmas.Special.1080p.WEB", xmas, false},
		{"Show.S02.The.Christmas.Special.1080p.WEB", xmas, false},
		{"Show.Complete.Series.The.Christmas.Special.1080p", xmas, false},
		{"[Grp] Show - 05 The Christmas Special [1080p]", xmas, false},
		// Other shows.
		{"Other.Show.The.Christmas.Special.2019.1080p.WEB", xmas, false},
		{"Showtime.The.Christmas.Special.1080p.WEB", xmas, false},
		{"Show.1999.The.Christmas.Special.1080p.WEB", xmas, false}, // the 1999 show
		{"Show.US.The.Christmas.Special.1080p.WEB", xmas, false},   // a country tag the show isn't known to have
		{"Show.(2015).The.Christmas.Special.1080p.WEB", xmas, true},
		{"[Grp] Show - The Christmas Special [1080p]", xmas, true},
	}
	for _, c := range cases {
		if got := specialTitleMatch(parser.Parse(c.name), c.name, s, c.ep); got != c.want {
			t.Errorf("%s vs %q: %v, want %v", c.name, c.ep.Title, got, c.want)
		}
	}
}

// Two specials a name could be: the more specific title wins, then the air year in the
// name; otherwise it's nobody's.
func TestSpecialByTitleAmbiguity(t *testing.T) {
	s := series.Series{ID: 1, Title: "Show", Seasons: []series.Season{{SeasonNumber: 0, Episodes: []series.Episode{
		{SeasonNumber: 0, EpisodeNumber: 1, Title: "Christmas Special", AirDate: "2019-12-25"},
		{SeasonNumber: 0, EpisodeNumber: 2, Title: "Christmas Special", AirDate: "2020-12-25"},
		{SeasonNumber: 0, EpisodeNumber: 3, Title: "Christmas Special Part 2", AirDate: "2021-12-25"},
	}}}}
	for _, c := range []struct {
		name       string
		want, only int
	}{
		{"Show.Christmas.Special.2020.1080p.WEB", 0, 2},
		{"Show.Christmas.Special.2019.1080p.WEB", 0, 1},
		{"Show.Christmas.Special.1080p.WEB", 0, 0}, // which year? nobody's
		{"Show.Christmas.Special.Part.2.1080p.WEB", 0, 3},
		{"Show.Christmas.Special.2020.1080p.WEB", 1, 0}, // it's special 2, not the one wanted
	} {
		if got := specialByTitle(parser.Parse(c.name), c.name, s, c.want); got != c.only {
			t.Errorf("%s (want %d) = %d, want %d", c.name, c.want, got, c.only)
		}
	}
}

// A Specials search names each special by number and, when its title is worth searching,
// by title.
func TestSpecialQueries(t *testing.T) {
	s := specialsShow()
	got := specialQueries(s, "Show", 1)
	if want := []string{"Show S00E01", "Show The Christmas Special"}; !reflect.DeepEqual(got, want) {
		t.Errorf("one special = %v, want %v", got, want)
	}
	got = specialQueries(s, "Show", 0)
	want := []string{"Show S00E01", "Show The Christmas Special", "Show S00E02", "Show Pilot", "Show S00E03", "Show Show Patagonia Special"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("browse = %v, want %v", got, want)
	}
}

// The import places a download's one unnumbered video on the special its grab named — and
// nothing else: two videos, a numbered file, or a grab that names no single special.
func TestImportSingleFilePlacesOnGrabScopeSpecial(t *testing.T) {
	unnumbered := parser.Parse("Show.The.Christmas.Special.2019.1080p.WEB.mkv")
	special := GrabScope{Season: 0, Episode: 5}
	if ref, ok := grabTargetSpecial(1, unnumbered, special); !ok || ref != (series.EpisodeRef{Season: 0, Episode: 5}) {
		t.Fatalf("single unnumbered file = %v %v, want S00E05", ref, ok)
	}
	for _, c := range []struct {
		why    string
		videos int
		file   parser.Release
		target GrabScope
	}{
		{"two videos", 2, unnumbered, special},
		{"no grab", 1, unnumbered, GrabScope{}},
		{"whole show", 1, unnumbered, WholeShow},
		{"the Specials season", 1, unnumbered, GrabScope{Season: 0}},
		{"a regular episode", 1, unnumbered, GrabScope{Season: 1, Episode: 5}},
		{"a numbered file", 1, parser.Parse("Show.S01E05.1080p.mkv"), special},
		{"an absolute-numbered file", 1, parser.Parse("[Grp] Show - 05 [1080p].mkv"), special},
	} {
		if _, ok := grabTargetSpecial(c.videos, c.file, c.target); ok {
			t.Errorf("%s: placed by the grab's target", c.why)
		}
	}
}

// Searching a special by its title finds it, lists it as resolving to that special, grabs
// it under the special's scope, and the import puts its file on the special without a
// review — while a regular episode carrying the same title is never offered.
func TestSpecialByTitleSearchGrabAndImport(t *testing.T) {
	h := newStallHarness(t)
	repo := series.NewRepo(h.c.db)
	sr, err := repo.Create(h.ctx, series.Series{TMDBID: 9, Title: "Show", Year: 2015, Monitored: true, Status: "Ended"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{
		{SeasonNumber: 0, Episodes: []series.Episode{{SeasonNumber: 0, EpisodeNumber: 5, Title: "The Christmas Special", AirDate: "2019-12-25", Runtime: 60}}},
		{SeasonNumber: 1, Monitored: true, Episodes: []series.Episode{{SeasonNumber: 1, EpisodeNumber: 5, Title: "Christmas", AirDate: "2016-01-01", Monitored: true}}},
	}); err != nil {
		t.Fatal(err)
	}
	const byTitle = "Show.The.Christmas.Special.2019.1080p.WEB-DL.x264-GRP"
	h.ix.offer(byTitle, "Show.S01E05.The.Christmas.Special.1080p.WEB-DL.x264-GRP", "Other.Show.The.Christmas.Special.1080p.WEB-DL.x264-GRP")

	list, err := h.c.RankSeriesReleases(h.ctx, sr.ID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Releases) != 1 || list.Releases[0].Title != byTitle || list.Releases[0].Resolves != "S00E05 (by title)" {
		t.Fatalf("list = %+v, want only %s resolving to S00E05 (by title)", list.Releases, byTitle)
	}

	out, err := h.c.GrabForScope(WithSearchTrigger(context.Background(), TriggerManual), sr.ID, SeriesScope{Season: 0, Episode: 5, Trigger: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Grabbed, []string{byTitle}) {
		t.Fatalf("grabbed %v, want %s", out.Grabbed, byTitle)
	}
	var manual int
	var scope string
	if err := h.c.db.QueryRow(`SELECT manual, scope FROM grabs WHERE title = ?`, byTitle).Scan(&manual, &scope); err != nil {
		t.Fatal(err)
	}
	if manual != 0 || scope != "S00E05" {
		t.Fatalf("grab row = %d/%q, want an automatic grab for S00E05", manual, scope)
	}

	// The download arrives: one video, no numbering.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.c.imp = library.NewImporter(t.TempDir(), log)
	dl := filepath.Join(t.TempDir(), byTitle)
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dl, byTitle+".mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(60 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	s, err := h.c.series.Get(h.ctx, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	force := h.c.grabForce(h.ctx, hashFor(byTitle))
	if force.On {
		t.Fatal("an automatic grab must not skip the import gate")
	}
	if !grabbedSpecialByTitle(parser.Parse(byTitle), byTitle, s, force.Target) {
		t.Fatal("the download isn't routed to the show it was grabbed for")
	}
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(byTitle), "name": byTitle, "state": "uploading", "progress": 1.0,
		"size": 60 << 20, "amount_left": 0, "category": seriesCategory, "content_path": dl,
	})
	h.qbit.mu.Unlock()
	h.c.ImportSeriesDownloads(h.ctx)
	// The next pass leaves the handled download alone: it isn't a pack that could fill the
	// show's other gaps, so it isn't re-processed and blocklisted as one.
	h.c.ImportSeriesDownloads(h.ctx)
	if n := h.blocklistCount(t); n != 0 {
		t.Fatalf("blocklist = %d after the second pass, want 0", n)
	}
	s, err = h.c.series.Get(h.ctx, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ep, _ := specialEpisode(s, 5); !ep.HasFile {
		t.Fatal("the special has no file after its download finished")
	}
	if reviews, _ := h.c.ListReviews(h.ctx); len(reviews) != 0 {
		t.Fatalf("reviews = %+v, want none", reviews)
	}

	// The same file without a grab target is unplaceable, as before.
	if _, _, unresolved, _ := h.c.importSeriesInto(h.ctx, s, dl, forceRule{}); unresolved != 1 {
		t.Fatalf("unresolved = %d without a target, want 1", unresolved)
	}
}
