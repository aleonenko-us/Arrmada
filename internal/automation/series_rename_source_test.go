package automation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/quality"
)

type renameSeriesNaming struct{ n library.SeriesNaming }

func (s renameSeriesNaming) SeriesNaming() library.SeriesNaming { return s.n }

// stubFacts answers Convert's probed facts for chosen paths.
type stubFacts map[string]quality.FileFacts

func (s stubFacts) FactsForPath(_ context.Context, path string, _ int64) (quality.FileFacts, bool) {
	f, ok := s[path]
	return f, ok
}

// Episodes are renamed from their recorded release too: a scheme that grows
// {quality}/{codec}/{group} fills them from it, even when the release is a season pack and
// the file's name says nothing but its episode. Convert's probed codec wins after a
// re-encode, and a file with no release keeps naming itself.
func TestSeriesRenamePreviewReadsSourceRelease(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 1}, [2]int{1, 2}, [2]int{1, 3})
	f.c.imp.SetSeriesNaming(renameSeriesNaming{library.SeriesNaming{
		Folder: "{title} ({year})", SeasonFolder: "Season {season}",
		EpisodeFile: "{title} - {episode} [{quality}][{codec}]-{group}",
	}})
	dir := filepath.Join(f.root, f.folder, "Season 1")
	e1 := filepath.Join(dir, "Show - S01E01.mkv")
	e2 := filepath.Join(dir, "Show - S01E02.mkv")
	e3 := filepath.Join(dir, "Show.S01E03.720p.HDTV.x264-KILLERS.mkv")
	f.place(t, 1, 1, e1, "one")
	f.place(t, 1, 2, e2, "two")
	f.place(t, 1, 3, e3, "three")
	const pack = "Show.2020.S01.1080p.WEB-DL.DDP5.1.H.264-NTb"
	for _, ep := range []int{1, 2} {
		if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, ep, pack); err != nil {
			t.Fatal(err)
		}
	}
	// Convert re-encoded E02 to AV1 before it restamped releases.
	f.c.SetFileFacts(stubFacts{e2: {Resolution: "1080p", Codec: "av1"}})

	items, err := f.c.SeriesRenamePreview(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, it := range items {
		if it.Conflict != "" {
			t.Fatalf("unexpected conflict: %+v", it)
		}
		got[it.Episode] = filepath.Base(it.To)
	}
	want := map[int]string{
		1: "Show - S01E01 [1080p WEB-DL][x264]-NTb.mkv",
		2: "Show - S01E02 [1080p WEB-DL][AV1]-NTb.mkv",
		3: "Show - S01E03 [720p HDTV][x264]-KILLERS.mkv",
	}
	for ep, w := range want {
		if got[ep] != w {
			t.Errorf("E%02d → %q, want %q", ep, got[ep], w)
		}
	}
	// The preview moved nothing.
	if mustRead(t, e1) != "one" || mustRead(t, e2) != "two" || mustRead(t, e3) != "three" {
		t.Fatal("a preview must not move files")
	}

	// The rename after a renumber runs unattended and names from the file name alone: none
	// of these files has a new episode, so it has nothing to do.
	quiet, err := f.c.seriesRenamePreview(f.ctx, f.id, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range quiet {
		if it.Episode == 1 || it.Episode == 2 {
			t.Errorf("the name-only preview proposed %s → %s", filepath.Base(it.From), filepath.Base(it.To))
		}
	}
}

// episodeSized makes a fixture file big enough (sparse) for a rescan to take it for an
// episode rather than a sample.
func episodeSized(t *testing.T, path string) {
	t.Helper()
	if err := os.Truncate(path, 200<<20); err != nil {
		t.Fatal(err)
	}
}

func withTokenScheme(f *renameFixture) {
	f.c.imp.SetSeriesNaming(renameSeriesNaming{library.SeriesNaming{
		Folder: "{title} ({year})", SeasonFolder: "Season {season}",
		EpisodeFile: "{title} - {episode} [{quality}][{codec}]-{group}",
	}})
}

func episodeRow(t *testing.T, f *renameFixture, season, episode int) (path, release string) {
	t.Helper()
	s, err := f.c.series.Get(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range s.Seasons {
		for _, e := range sn.Episodes {
			if e.SeasonNumber == season && e.EpisodeNumber == episode {
				return e.FilePath, e.SourceRelease
			}
		}
	}
	t.Fatalf("no S%02dE%02d", season, episode)
	return "", ""
}

// A file replaced by hand and adopted by a rescan is another file: it must not inherit the
// old one's release, and the rename names it from its own name.
func TestSeriesRescanAdoptingAnotherFileDropsItsRelease(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 1})
	withTokenScheme(f)
	dir := filepath.Join(f.root, f.folder, "Season 1")
	old := filepath.Join(dir, "Show - S01E01 [1080p WEB-DL][x264]-OLDGRP.mkv")
	f.place(t, 1, 1, old, "old")
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 1, "Show.S01E01.1080p.WEB-DL.x264-OLDGRP"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	newf := filepath.Join(dir, "Show.S01E01.1080p.BluRay.x265-NEWGRP.mkv")
	if err := os.WriteFile(newf, []byte("a different file"), 0o644); err != nil {
		t.Fatal(err)
	}
	episodeSized(t, newf)
	if err := f.c.RescanSeries(f.ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if p, rel := episodeRow(t, f, 1, 1); p != newf || rel != "" {
		t.Fatalf("after rescan: %q / release %q, want the new file with no release", p, rel)
	}
	items, err := f.c.SeriesRenamePreview(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Show - S01E01 [1080p BluRay][x265]-NEWGRP.mkv"; len(items) != 1 || filepath.Base(items[0].To) != want {
		t.Fatalf("preview = %+v, want %q", items, want)
	}
}

// The same file renamed outside Arrmada (same size) keeps its release through a rescan.
func TestSeriesRescanAdoptingRenamedFileKeepsItsRelease(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 1})
	dir := filepath.Join(f.root, f.folder, "Season 1")
	old := filepath.Join(dir, "Show - S01E01.mkv")
	f.place(t, 1, 1, old, "same bytes")
	episodeSized(t, old)
	if err := f.c.series.MarkEpisodeImported(f.ctx, f.id, 1, 1, old, 200<<20); err != nil {
		t.Fatal(err)
	}
	const rel = "Show.S01E01.1080p.WEB-DL.x264-GRP"
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 1, rel); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(dir, "Show S01E01 renamed by hand 1080p.mkv")
	if err := os.Rename(old, renamed); err != nil {
		t.Fatal(err)
	}
	if err := f.c.RescanSeries(f.ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if p, got := episodeRow(t, f, 1, 1); p != renamed || got != rel {
		t.Fatalf("after rescan: %q / release %q, want the renamed file keeping %q", p, got, rel)
	}
}

// A different file that happens to be the same size, under a name that says nothing, isn't
// taken for the old file renamed: episode rows keep no modification time, so a name must
// confirm the release (its resolution or group) for it to stay.
func TestSeriesRescanSameSizeOtherFileDropsItsRelease(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 1})
	withTokenScheme(f)
	dir := filepath.Join(f.root, f.folder, "Season 1")
	old := filepath.Join(dir, "Show - S01E01 [1080p WEB-DL][x264]-OLDGRP.mkv")
	f.place(t, 1, 1, old, "old")
	episodeSized(t, old)
	if err := f.c.series.MarkEpisodeImported(f.ctx, f.id, 1, 1, old, 200<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 1, "Show.S01E01.1080p.WEB-DL.x264-OLDGRP"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	newf := filepath.Join(dir, "Show S01E01.mkv")
	if err := os.WriteFile(newf, []byte("different content"), 0o644); err != nil {
		t.Fatal(err)
	}
	episodeSized(t, newf)
	if err := f.c.RescanSeries(f.ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if p, rel := episodeRow(t, f, 1, 1); p != newf || rel != "" {
		t.Fatalf("after rescan: %q / release %q, want the new file with no release", p, rel)
	}
	items, err := f.c.SeriesRenamePreview(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if strings.Contains(it.To, "OLDGRP") || strings.Contains(it.To, "WEB-DL") {
			t.Fatalf("preview wrote the old file's facts: %s", filepath.Base(it.To))
		}
	}
}

// The default scheme's own WEB-DL output ("... - 1080p WEB-DL") must not read as group
// "DL": the release supplies the group.
func TestSeriesRenameDefaultWebDLName(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 2})
	withTokenScheme(f)
	dir := filepath.Join(f.root, f.folder, "Season 1")
	f.place(t, 1, 2, filepath.Join(dir, "Show - S01E02 - The Title - 1080p WEB-DL.mkv"), "x")
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 2, "Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-NTb"); err != nil {
		t.Fatal(err)
	}
	items, err := f.c.SeriesRenamePreview(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Show - S01E02 [1080p WEB-DL][x264]-NTb.mkv"; len(items) != 1 || filepath.Base(items[0].To) != want {
		t.Fatalf("preview = %+v, want %q", items, want)
	}
}

// Preview, apply, preview again: nothing left to rename, under every scheme. Files with a
// recorded release only: a file with none, under a {group} with no dash before it, can't
// read its group back from the name it was given (as on main), so it isn't asserted here.
func TestSeriesRenameSettlesAfterApply(t *testing.T) {
	schemes := map[string]string{
		"default": library.DefaultEpisodeFile,
		"tokens":  "{title} - {episode} [{quality}][{codec}]-{group}",
		"sonarr":  "{title} - {episode} - {episodetitle} {quality} {codec}-{group}",
		"q-only":  "{title} - {episode} - {quality}",
		"no dash": "{title} - {episode} - {resolution} {source} {codec} {group}",
	}
	files := []struct {
		e             int
		name, release string
		double        bool
	}{
		{1, "Show - S01E01-E02 - Pilot - 1080p WEB-DL.mkv", "Show.S01E01E02.1080p.WEB-DL.DDP5.1.H.264-NTb", true},
		{3, "Show - S01E03.mkv", "Show.2020.S01.1080p.WEB-DL.DDP5.1.H.264-NTb", false},
		{5, "Show - S01E05 - Bluray-1080p.mkv", "Show.S01E05.1080p.BluRay.x265-x0r", false},
		{6, "Show - S01E06 [2160p WEB-DL][x265]-FLUX.mkv", "Show.S01E06.2160p.WEB-DL.DDP5.1.HDR.H.265-FLUX", false},
		{7, "Show - S01E07 - 1080p-3L.mkv", "Show.S01E07.1080p.HDTV.x264-3L", false},
		{8, "Show - S01E08-E09.mkv", "Show.S01.1080p.BluRay.x264-SPARKS", true},
	}
	for label, scheme := range schemes {
		t.Run(label, func(t *testing.T) {
			f := newRenameFixture(t, [2]int{1, 1}, [2]int{1, 2}, [2]int{1, 3}, [2]int{1, 5}, [2]int{1, 6}, [2]int{1, 7}, [2]int{1, 8}, [2]int{1, 9})
			f.c.imp.SetSeriesNaming(renameSeriesNaming{library.SeriesNaming{
				Folder: "{title} ({year})", SeasonFolder: "Season {season}", EpisodeFile: scheme,
			}})
			dir := filepath.Join(f.root, f.folder, "Season 1")
			for i, x := range files {
				p := filepath.Join(dir, x.name)
				body := strings.Repeat("x", 10+i) // distinct sizes
				f.place(t, 1, x.e, p, body)
				eps := []int{x.e}
				if x.double {
					if err := f.c.series.MarkEpisodeImported(f.ctx, f.id, 1, x.e+1, p, int64(len(body))); err != nil {
						t.Fatal(err)
					}
					eps = append(eps, x.e+1)
				}
				for _, ep := range eps {
					if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, ep, x.release); err != nil {
						t.Fatal(err)
					}
				}
			}
			items, err := f.c.SeriesRenamePreview(f.ctx, f.id)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range items {
				b := strings.TrimSuffix(filepath.Base(it.To), ".mkv")
				if strings.HasSuffix(b, "]-DL") || strings.HasSuffix(b, " -DL") || strings.HasSuffix(b, " DL") {
					t.Errorf("a WEB-DL tail became a group: %s", filepath.Base(it.To))
				}
			}
			if _, err := f.c.SeriesRename(f.ctx, f.id, items); err != nil {
				t.Fatal(err)
			}
			again, err := f.c.SeriesRenamePreview(f.ctx, f.id)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range again {
				t.Errorf("second preview: %s → %s %s", filepath.Base(it.From), filepath.Base(it.To), it.Conflict)
			}
		})
	}
}

// Every way an episode loses its file, or gains one Arrmada didn't import, drops the old
// release; a new import records its release with the file in one write.
func TestEpisodeReleaseFollowsTheFile(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 1}, [2]int{1, 2})
	dir := filepath.Join(f.root, f.folder, "Season 1")
	a := filepath.Join(dir, "a.mkv")
	f.place(t, 1, 1, a, "a")
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 1, "Show.S01E01.720p.HDTV.x264-OLD"); err != nil {
		t.Fatal(err)
	}
	// Missing (a rescan that found nothing) clears it.
	if err := f.c.series.MarkEpisodeMissing(f.ctx, f.id, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, rel := episodeRow(t, f, 1, 1); rel != "" {
		t.Fatalf("release %q kept on an episode with no file", rel)
	}
	// Adopting another app's file onto the empty episode: no release.
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 1, "Show.S01E01.720p.HDTV.x264-OLD"); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.c.series.AdoptEpisodeFile(f.ctx, f.id, 1, 1, a, 1); err != nil || !ok {
		t.Fatalf("adopt = %v, %v", ok, err)
	}
	if _, rel := episodeRow(t, f, 1, 1); rel != "" {
		t.Fatalf("an adopted file inherited release %q", rel)
	}
	// An import records file and release together; one with no known release, at a new
	// path, keeps none of the old file's.
	b := filepath.Join(dir, "b.mkv")
	if err := os.WriteFile(b, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.c.series.SupersedeEpisodeFile(f.ctx, f.id, 1, 2, b, 1, "Show.S01E02.1080p.WEB-DL.x264-NEW"); err != nil {
		t.Fatal(err)
	}
	if p, rel := episodeRow(t, f, 1, 2); p != b || rel != "Show.S01E02.1080p.WEB-DL.x264-NEW" {
		t.Fatalf("after import: %q / %q", p, rel)
	}
	c := filepath.Join(dir, "c.mkv")
	if err := os.WriteFile(c, []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.c.series.SupersedeEpisodeFile(f.ctx, f.id, 1, 2, c, 1, ""); err != nil {
		t.Fatal(err)
	}
	if p, rel := episodeRow(t, f, 1, 2); p != c || rel != "" {
		t.Fatalf("after an import with no release: %q / %q, want no release", p, rel)
	}
}

// After a rename, Convert's facts (keyed by path) are gone until the next index; the name
// written from them must not flip back to the release's codec. Renaming twice is stable.
func TestSeriesRenameStableAfterConvert(t *testing.T) {
	f := newRenameFixture(t, [2]int{1, 2})
	withTokenScheme(f)
	dir := filepath.Join(f.root, f.folder, "Season 1")
	e2 := filepath.Join(dir, "Show - S01E02.mkv")
	f.place(t, 1, 2, e2, "two")
	if err := f.c.series.SetEpisodeSourceRelease(f.ctx, f.id, 1, 2, "Show.2020.S01.1080p.WEB-DL.DDP5.1.H.264-NTb"); err != nil {
		t.Fatal(err)
	}
	f.c.SetFileFacts(stubFacts{e2: {Resolution: "1080p", Codec: "av1"}})
	items, err := f.c.SeriesRenamePreview(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := f.c.SeriesRename(f.ctx, f.id, items); err != nil || res.Moved != 1 {
		t.Fatalf("first rename = %+v, %v", res, err)
	}
	if p, _ := episodeRow(t, f, 1, 2); filepath.Base(p) != "Show - S01E02 [1080p WEB-DL][AV1]-NTb.mkv" {
		t.Fatalf("renamed to %q", filepath.Base(p))
	}
	again, err := f.c.SeriesRenamePreview(f.ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second preview = %+v, want nothing to rename", again)
	}
}
