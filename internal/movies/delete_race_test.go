package movies

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/store"
)

// GH #59 review probes, as tests: what changes between the preview and the delete, or
// during the delete, never lets more go than the preview listed and a fresh look allows.

// midDeleteBin runs hook once, as the tracked video moves (after the plan was made,
// before any leftover moves). Its first For call is the delete's pre-flight.
type midDeleteBin struct {
	dir   string
	hook  func()
	calls int
}

func (b *midDeleteBin) For(path string) (string, error) {
	if path != "" {
		b.calls++
		if b.calls == 2 && b.hook != nil {
			h := b.hook
			b.hook = nil
			h()
		}
	}
	return b.dir, nil
}

type raceEnv struct {
	top string
	svc *Service
	bin string
}

func newRaceEnv(t *testing.T, files ...string) *raceEnv {
	t.Helper()
	e := &raceEnv{top: t.TempDir()}
	for _, f := range files {
		writeFixture(t, e.abs(f))
	}
	if err := os.MkdirAll(e.abs("Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.bin = filepath.Join(t.TempDir(), "bin")
	e.svc = NewService(st.DB(), nil, nil, e.abs("Movies"), e.bin, eventbus.New(log), log)
	return e
}

func (e *raceEnv) abs(rel string) string { return filepath.Join(e.top, filepath.FromSlash(rel)) }

func (e *raceEnv) movie(t *testing.T, tmdb int, title string, year int, file string) int64 {
	t.Helper()
	ctx := context.Background()
	m, err := e.svc.repo.Create(ctx, Movie{TMDBID: tmdb, Title: title, Year: year, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.repo.SetFile(ctx, m.ID, e.abs(file)); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// preview returns the paths the dialog lists (relative) and the leftovers it sends back.
func (e *raceEnv) preview(t *testing.T, id int64) (listed map[string]bool, leftovers []string) {
	t.Helper()
	plan, err := e.svc.DeletePlan(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	listed = map[string]bool{}
	for _, p := range plan.paths {
		r, _ := filepath.Rel(e.top, p)
		listed[filepath.ToSlash(r)] = true
	}
	for _, x := range plan.Extras {
		leftovers = append(leftovers, x.Path)
	}
	return listed, leftovers
}

// deleteWith deletes the movie with the leftovers and returns what went (relative).
func (e *raceEnv) deleteWith(t *testing.T, id int64, leftovers []string) []string {
	t.Helper()
	before := snapshotTree(t, e.top)
	if err := e.svc.DeleteWith(context.Background(), id, DeleteOptions{Files: true, Leftovers: leftovers}); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, e.top)
	var out []string
	for p := range before {
		if !after[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

const raceHeat = "Movies/Heat (1995)"

// P1: files that turn up after the preview are never moved, and a name that isn't the
// movie's makes every leftover stay.
func TestDeleteNewFilesAfterPreviewStay(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/Heat (1995).mkv", raceHeat+"/poster.jpg")
	id := e.movie(t, 949, "Heat", 1995, raceHeat+"/Heat (1995).mkv")
	listed, leftovers := e.preview(t, id)
	writeFixture(t, e.abs(raceHeat+"/my-own-notes.txt"))
	writeFixture(t, e.abs(raceHeat+"/family-photo.jpg"))
	removed := e.deleteWith(t, id, leftovers)
	for _, r := range removed {
		if !listed[r] {
			t.Errorf("removed %s, which the preview didn't list", r)
		}
	}
	if want := []string{raceHeat + "/Heat (1995).mkv"}; !sameSet(removed, want) {
		t.Errorf("removed %v, want only the video", removed)
	}
}

// P2: the preview said the folder stays (another video in it); that video leaves before
// the confirm. The delete still moves no leftover: the preview listed none.
func TestDeletePreviewSaidStaysIsBinding(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/Heat (1995).mkv", raceHeat+"/poster.jpg", raceHeat+"/movie.nfo", raceHeat+"/Heat.Trailer.mkv")
	id := e.movie(t, 949, "Heat", 1995, raceHeat+"/Heat (1995).mkv")
	_, leftovers := e.preview(t, id)
	if len(leftovers) != 0 {
		t.Fatalf("the preview lists leftovers %v beside another video", leftovers)
	}
	if err := os.Remove(e.abs(raceHeat + "/Heat.Trailer.mkv")); err != nil {
		t.Fatal(err)
	}
	if removed := e.deleteWith(t, id, leftovers); !sameSet(removed, []string{raceHeat + "/Heat (1995).mkv"}) {
		t.Errorf("removed %v, want only the video", removed)
	}
}

// P4: an import lands mid-delete — a new video and a rewritten movie.nfo. Nothing but
// the deleted movie's own video goes.
func TestDeleteImportMidDeleteKeepsLeftovers(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/Heat (1995).mkv", raceHeat+"/movie.nfo", raceHeat+"/poster.jpg")
	id := e.movie(t, 949, "Heat", 1995, raceHeat+"/Heat (1995).mkv")
	_, leftovers := e.preview(t, id)
	if len(leftovers) != 2 {
		t.Fatalf("leftovers = %v, want the nfo and the poster", leftovers)
	}
	e.svc.SetBin(&midDeleteBin{dir: e.bin, hook: func() {
		writeFixture(t, e.abs(raceHeat+"/Heat (1995) - 2160p.mkv"))
		_ = os.WriteFile(e.abs(raceHeat+"/movie.nfo"), []byte("NEW nfo for the 2160p import"), 0o644)
	}})
	if removed := e.deleteWith(t, id, leftovers); !sameSet(removed, []string{raceHeat + "/Heat (1995).mkv"}) {
		t.Errorf("removed %v, want only the video", removed)
	}
	if b, err := os.ReadFile(e.abs(raceHeat + "/movie.nfo")); err != nil || string(b) != "NEW nfo for the 2160p import" {
		t.Errorf("the rewritten nfo went: %v", err)
	}
}

// P4b: a leftover rewritten mid-delete (same name, new content) stays even when nothing
// else changed.
func TestDeleteRewrittenLeftoverStays(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/Heat (1995).mkv", raceHeat+"/movie.nfo", raceHeat+"/poster.jpg")
	id := e.movie(t, 949, "Heat", 1995, raceHeat+"/Heat (1995).mkv")
	_, leftovers := e.preview(t, id)
	e.svc.SetBin(&midDeleteBin{dir: e.bin, hook: func() {
		_ = os.WriteFile(e.abs(raceHeat+"/movie.nfo"), []byte("rewritten, longer than before"), 0o644)
	}})
	removed := e.deleteWith(t, id, leftovers)
	if !sameSet(removed, []string{raceHeat + "/Heat (1995).mkv", raceHeat + "/poster.jpg"}) {
		t.Errorf("removed %v, want the video and the untouched poster", removed)
	}
}

// P5: two different films with the same title and year share the default folder; the
// other's video is missing but its nfo, subtitle and poster remain. Only A's video goes.
func TestDeleteSameTitleYearSharedFolder(t *testing.T) {
	const p = "Movies/Pinocchio (2022)"
	for _, present := range []bool{false, true} {
		files := []string{p + "/Pinocchio (2022) - WEBDL-1080p.mkv", p + "/Pinocchio (2022) - Bluray-2160p.nfo",
			p + "/Pinocchio (2022) - Bluray-2160p.en.srt", p + "/Pinocchio (2022) - Bluray-2160p-poster.jpg", p + "/poster.jpg"}
		if present {
			files = append(files, p+"/Pinocchio (2022) - Bluray-2160p.mkv")
		}
		e := newRaceEnv(t, files...)
		a := e.movie(t, 1, "Pinocchio", 2022, p+"/Pinocchio (2022) - WEBDL-1080p.mkv")
		e.movie(t, 2, "Pinocchio", 2022, p+"/Pinocchio (2022) - Bluray-2160p.mkv")
		_, leftovers := e.preview(t, a)
		if len(leftovers) != 0 {
			t.Errorf("present=%v: the preview lists leftovers %v in a folder another film's record is in", present, leftovers)
		}
		if removed := e.deleteWith(t, a, leftovers); !sameSet(removed, []string{p + "/Pinocchio (2022) - WEBDL-1080p.mkv"}) {
			t.Errorf("present=%v: removed %v, want only A's video", present, removed)
		}
	}
}

// Orphan VobSub files named for another film stay, and so does everything else.
func TestDeleteOrphanSubtitleNamedForAnotherFilmStays(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/Heat (1995).mkv", raceHeat+"/poster.jpg", raceHeat+"/Other (2001).sub", raceHeat+"/Other (2001).idx")
	id := e.movie(t, 949, "Heat", 1995, raceHeat+"/Heat (1995).mkv")
	_, leftovers := e.preview(t, id)
	if len(leftovers) != 0 {
		t.Errorf("leftovers = %v", leftovers)
	}
	if removed := e.deleteWith(t, id, leftovers); !sameSet(removed, []string{raceHeat + "/Heat (1995).mkv"}) {
		t.Errorf("removed %v, want only the video", removed)
	}
}

// P7: extrafanart is swapped for a link to a folder outside the library while the video
// moves. Nothing outside is touched, and the link stays.
func TestDeleteSymlinkSwappedInMidDelete(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/Heat (1995).mkv", raceHeat+"/extrafanart/fanart1.jpg", "outside/fanart1.jpg")
	id := e.movie(t, 949, "Heat", 1995, raceHeat+"/Heat (1995).mkv")
	_, leftovers := e.preview(t, id)
	if len(leftovers) != 1 {
		t.Fatalf("leftovers = %v, want the fanart", leftovers)
	}
	e.svc.SetBin(&midDeleteBin{dir: e.bin, hook: func() {
		if err := os.RemoveAll(e.abs(raceHeat + "/extrafanart")); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(e.abs("outside"), e.abs(raceHeat+"/extrafanart")); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}})
	e.deleteWith(t, id, leftovers)
	if _, err := os.Stat(e.abs("outside/fanart1.jpg")); err != nil {
		t.Errorf("a file outside the movie folder went through a link swapped in mid-delete: %v", err)
	}
	if fi, err := os.Lstat(e.abs(raceHeat + "/extrafanart")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the swapped-in link was removed: %v", err)
	}
}

// The per-file check alone (no re-walk can see it): a folder on the way to a leftover
// that is a link now refuses that leftover.
func TestLeftoverChangedRefusesLinkedFolder(t *testing.T) {
	e := newRaceEnv(t, raceHeat+"/extrafanart/fanart1.jpg", "outside/fanart1.jpg")
	f := purgeFolder{dir: e.abs(raceHeat), root: e.abs("Movies"), stamps: map[string]fileStamp{}}
	p := e.abs(raceHeat + "/extrafanart/fanart1.jpg")
	fi, _ := os.Lstat(p)
	f.stamps[p] = fileStamp{fi.Size(), fi.ModTime()}
	if why := e.svc.leftoverChanged(f, p); why != "" {
		t.Fatalf("an untouched leftover refused: %s", why)
	}
	if err := os.RemoveAll(e.abs(raceHeat + "/extrafanart")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(e.abs("outside"), e.abs(raceHeat+"/extrafanart")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if why := e.svc.leftoverChanged(f, p); why == "" {
		t.Error("a leftover reached through a link swapped in was allowed")
	}
}

// A movie file directly in a TV folder or a downloads folder never lets that folder be
// removed once empty.
func TestDeleteNeverRemovesOtherLibraryOrDownloadsFolder(t *testing.T) {
	for _, dir := range []string{"TV", "downloads"} {
		e := newRaceEnv(t, dir+"/Heat (1995).mkv")
		e.svc.SetProtectedDirs(func() []string { return []string{e.abs("TV"), e.abs("downloads")} })
		id := e.movie(t, 949, "Heat", 1995, dir+"/Heat (1995).mkv")
		_, leftovers := e.preview(t, id)
		if removed := e.deleteWith(t, id, leftovers); !sameSet(removed, []string{dir + "/Heat (1995).mkv"}) {
			t.Errorf("%s: removed %v, want only the video", dir, removed)
		}
	}
}
