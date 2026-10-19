package automation

import (
	"context"
	"path/filepath"
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
}
