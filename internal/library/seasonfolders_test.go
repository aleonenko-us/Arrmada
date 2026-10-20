package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// Season folders under the names people actually use read as seasons; shows named like a
// number, or with a season word inside a longer name, don't (#56).
func TestSeasonFolderNumber(t *testing.T) {
	for name, want := range map[string]int{
		"Season 1": 1, "Season 01": 1, "season 12": 12, "Season.01": 1, "Season_3": 3, "Season-4": 4, "Season04": 4,
		"S1": 1, "S01": 1, "s13": 13, "S 02": 2,
		"Stagione 1": 1, "Saison 01": 1, "Staffel 01": 1, "Temporada 2": 2, "Series 1": 1,
		"Seizoen 3": 3, "Sezon 4": 4, "Säsong 5": 5, "Kausi 6": 6, "Sæson 7": 7, "Sesong 8": 8, "Сезон 9": 9,
		"Season 1 (2019)": 1, "S01 [1080p]": 1, "Specials": 0, "Special": 0, "Season 0": 0, "Season 00": 0,
	} {
		if n, ok := SeasonFolderNumber(name); !ok || n != want {
			t.Errorf("SeasonFolderNumber(%q) = (%d, %v), want (%d, true)", name, n, ok, want)
		}
	}
	for _, name := range []string{
		"1992", "24", "1883", "S.W.A.T.", "S.W.A.T. (2017)", "Season 4 Show", "Seasons", "Season", "S",
		"Extras", "Series 7 The Contenders", "Pechino Express", "SAS Rogue Heroes", "S Club 7",
	} {
		if n, ok := SeasonFolderNumber(name); ok {
			t.Errorf("SeasonFolderNumber(%q) = %d, want not a season folder", name, n)
		}
	}
	if !LooksLikeSeasonFolder("Extras") || !LooksLikeSeasonFolder("Featurettes") || LooksLikeSeasonFolder("1992") {
		t.Error("LooksLikeSeasonFolder: Extras/Featurettes are inside a show, 1992 is a show")
	}
}

// The configured season template counts too, except that a bare number never decides a
// show's folder.
func TestSeasonFolderTemplate(t *testing.T) {
	im := NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{SeasonFolder: "{title} Season {season00}"}})
	if !im.IsSeasonFolderName("The Pitt Season 01") {
		t.Error(`"The Pitt Season 01" isn't read as a season folder under "{title} Season {season00}"`)
	}
	if n, ok := im.seasonNumberOf("The Pitt Season 02"); !ok || n != 2 {
		t.Errorf("seasonNumberOf = (%d, %v), want 2", n, ok)
	}
	if im.IsSeasonFolderName("The Pitt") || im.IsSeasonFolderName("Extras") {
		t.Error("a show name read as a season folder")
	}
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{SeasonFolder: "{title} - {season}"}})
	if im.IsSeasonFolderName("Show - 2005") || !im.IsSeasonFolderName("Show - 2") {
		t.Error(`"{title} - {season}": a year read as a season, or a season missed`)
	}
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{SeasonFolder: "{season}"}})
	if im.IsSeasonFolderName("1992") {
		t.Error(`with a "{season}" template, the show "1992" was read as a season folder`)
	}
	if n, ok := im.seasonNumberOf("3"); !ok || n != 3 {
		t.Errorf(`"3" under a "{season}" template = (%d, %v)`, n, ok)
	}
}

// An episode joins the season folder the show already has, under its own name — "S13",
// "Stagione 2" — rather than a parallel "Season 13".
func TestSeasonDirInPrefersTheExistingFolder(t *testing.T) {
	im := NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	show := filepath.Join(t.TempDir(), "Pechino Express")
	for _, d := range []string{"S12", "S13", "Stagione 2"} {
		if err := os.MkdirAll(filepath.Join(show, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for season, want := range map[int]string{13: "S13", 12: "S12", 2: "Stagione 2", 14: "Season 14", 0: "Specials"} {
		if got := im.SeasonDirIn(show, "Pechino Express", 2012, season); got != want {
			t.Errorf("season %d: SeasonDirIn = %q, want %q", season, got, want)
		}
	}
}
