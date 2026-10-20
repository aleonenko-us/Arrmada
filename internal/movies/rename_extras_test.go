package movies

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// One pass renaming two films into the same new folder (a "{title}" scheme): the first
// takes its poster into the folder it creates; the second finds the first recorded there
// now and leaves its poster where it was, though the preview (made before either moved)
// listed it.
func TestRenamePassSeesWhereEarlierMoviesWent(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	clearFixtureFolder(t, f)
	f.svc.imp.SetNaming(schemeNaming{library.Naming{Folder: "{title}", File: library.DefaultMovieFile}})
	ctx := context.Background()
	if err := f.svc.repo.ClearVersionFile(ctx, f.vid); err != nil {
		t.Fatal(err)
	}
	a := writeBody(t, filepath.Join(f.root, "heat.1995.old", "heat.1995.1080p.bluray.mkv"), "film")
	writeBody(t, filepath.Join(f.root, "heat.1995.old", "poster.jpg"), "1995")
	if err := f.svc.repo.SetFile(ctx, f.id, a); err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.repo.Create(ctx, Movie{TMDBID: 9861, Title: "Heat", Year: 1986, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	b := writeBody(t, filepath.Join(f.root, "heat.1986.old", "heat.1986.720p.hdtv.mkv"), "film")
	writeBody(t, filepath.Join(f.root, "heat.1986.old", "poster.jpg"), "1986")
	if err := f.svc.repo.SetFile(ctx, other.ID, b); err != nil {
		t.Fatal(err)
	}
	plan, err := f.svc.RenamePlan(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var todo []RenameItem
	for _, it := range plan {
		if it.Status == RenameOK {
			if len(it.Extras) != 1 {
				t.Fatalf("%s: extras = %+v, want its poster listed", it.Current, it.Extras)
			}
			todo = append(todo, it)
		}
	}
	if len(todo) != 2 {
		t.Fatalf("plan = %+v, want two clean renames", plan)
	}
	if _, err := f.svc.ApplyRename(ctx, todo); err != nil {
		t.Fatal(err)
	}
	// Whichever went first took its poster; the other's stayed in its old folder.
	first := readBody(t, filepath.Join(f.root, "Heat", "poster.jpg"))
	second := map[string]string{"1995": "heat.1986.old", "1986": "heat.1995.old"}[first]
	if second == "" {
		t.Fatalf("Heat/poster.jpg = %q, want one film's poster", first)
	}
	if got := readBody(t, filepath.Join(f.root, second, "poster.jpg")); got == first {
		t.Errorf("the second film's poster = %q, want its own left in its old folder", got)
	}
}

// Heat (1986)'s record points into "Heat/", but that folder is missing from disk. A
// "{title}" scheme creates "Heat/" for Heat (1995)'s file; the folder is still Heat
// (1986)'s by its record, so Heat (1995)'s poster and .nfo stay in the old folder.
func TestRV2_P1_MissingTargetOfRecord(t *testing.T) {
	f, _ := renameFixture(t, "heat.1995.old/heat.1995.1080p.bluray.mkv",
		"heat.1995.old/poster.jpg", "heat.1995.old/movie.nfo")
	ctx := context.Background()
	other, err := f.svc.repo.Create(ctx, Movie{TMDBID: 99, Title: "Heat", Year: 1986, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.SetFile(ctx, other.ID, filepath.Join(f.root, "Heat", "Heat (1986).mkv")); err != nil {
		t.Fatal(err)
	}
	f.svc.imp.SetNaming(schemeNaming{library.Naming{Folder: "{title}", File: "{title} ({year})"}})
	items := okItems(t, f)
	for _, x := range items[0].Extras {
		if x.Kind == ExtraFolder {
			t.Errorf("the preview moves %s into Heat/, where Heat (1986) is recorded", filepath.Base(x.From))
		}
	}
	if items[0].Kept == "" {
		t.Error("the preview should say why the old folder's files stay")
	}
	if _, err := f.svc.ApplyRename(ctx, items); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"poster.jpg", "movie.nfo"} {
		if exists(filepath.Join(f.root, "Heat", n)) {
			t.Errorf("%s went into Heat/, where Heat (1986) is recorded (file missing)", n)
		}
		if !exists(filepath.Join(f.root, "heat.1995.old", n)) {
			t.Errorf("%s should have stayed in the old folder", n)
		}
	}
}

// The pass's aliasInto (Movies folders' links found once) answers as the probe's does,
// asked about every folder of a layout with links into, beside and below them.
func TestRenameRecordsAliasIntoMatchesProbe(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	clearFixtureFolder(t, f)
	M := f.root
	elsewhere := t.TempDir()
	for _, p := range []string{"A (1995)/a.mkv", "B/b.mkv", "Cat/D/d.mkv", "Cat/E/e.mkv", "file.txt"} {
		writeBody(t, filepath.Join(M, filepath.FromSlash(p)), "x")
	}
	writeBody(t, filepath.Join(elsewhere, "X", "x.mkv"), "x")
	links := [][2]string{
		{filepath.Join(M, "B"), filepath.Join(M, "Alias")},
		{filepath.Join(elsewhere, "nope"), filepath.Join(M, "Dang")},
		{filepath.Join(M, "Cat", "D"), filepath.Join(M, "Cat", "Link")},
		{filepath.Join(elsewhere, "X"), filepath.Join(M, "Out")},
		{filepath.Join(M, "A (1995)", "a.mkv"), filepath.Join(M, "FileLink")},
	}
	for _, l := range links {
		if err := os.Symlink(l[0], l[1]); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}
	recs := &renameRecords{load: func() ([]recordedPath, error) { return nil, nil }}
	if err := recs.ensure(); err != nil {
		t.Fatal(err)
	}
	dirs := []string{
		filepath.Join(M, "A (1995)"), filepath.Join(M, "B"), filepath.Join(M, "Cat"), filepath.Join(M, "Cat", "D"),
		filepath.Join(M, "Cat", "E"), filepath.Join(M, "Nope"), filepath.Join(M, "Alias"), filepath.Join(elsewhere, "X"),
		filepath.Join(M, "Cat", "D", "Sub"), M,
	}
	for _, d := range dirs {
		want := f.svc.aliasInto(newLinkProbe(nil), d)
		got := recs.aliasInto(f.svc.allRoots(), d)
		if (want == "") != (got == "") {
			t.Errorf("%s: pass says %q, probe says %q", d, got, want)
		}
	}
}

// renameFixture is one movie (Heat, 1995) with a single file at rel under the Movies
// folder, and the other films given as {title, year, rel}.
func renameFixture(t *testing.T, rel string, files ...string) (*deleteFixture, string) {
	t.Helper()
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	clearFixtureFolder(t, f)
	ctx := context.Background()
	main := writeBody(t, filepath.Join(f.root, filepath.FromSlash(rel)), "film")
	for _, x := range files {
		writeBody(t, filepath.Join(f.root, filepath.FromSlash(x)), x)
	}
	if err := f.svc.repo.SetFile(ctx, f.id, main); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.ClearVersionFile(ctx, f.vid); err != nil {
		t.Fatal(err)
	}
	return f, main
}

// clearFixtureFolder removes the delete fixture's own "Heat (1995)" folder: the rename
// tests' films move into it, and its videos (no longer recorded) would keep any folder
// files out of it.
func clearFixtureFolder(t *testing.T, f *deleteFixture) {
	t.Helper()
	for _, p := range append([]string{f.main, f.extra, f.extraSub}, f.subs...) {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Dir(f.main)); err != nil {
		t.Fatal(err)
	}
}

func okItems(t *testing.T, f *deleteFixture) []RenameItem {
	t.Helper()
	plan, err := f.svc.RenamePlan(context.Background(), []int64{f.id})
	if err != nil {
		t.Fatal(err)
	}
	var out []RenameItem
	for _, it := range plan {
		if it.Status == RenameOK {
			out = append(out, it)
		}
	}
	if len(out) != 1 {
		t.Fatalf("plan = %+v, want one clean rename", plan)
	}
	return out
}

// Leaving its own folder, the film takes everything that folder holds for it: generic
// artwork keeps its name, a file named for the old file takes the new file's name, and
// the extras folder comes along. The emptied folder goes.
func TestRenameOwnFolderMovesAndRenamesSidecars(t *testing.T) {
	const old = "heat.1995.old/heat.1995.1080p.bluray"
	f, _ := renameFixture(t, old+".mkv",
		old+".en.srt", old+".nfo", old+"-poster.jpg", "heat.1995.old/poster.jpg", "heat.1995.old/movie.nfo",
		"heat.1995.old/extrafanart/fanart1.jpg")
	items := okItems(t, f)
	it := items[0]
	if it.Kept != "" {
		t.Fatalf("kept = %q, want the folder's files to move", it.Kept)
	}
	if len(it.Extras) != 6 {
		t.Fatalf("extras = %+v, want 6", it.Extras)
	}
	if _, err := f.svc.ApplyRename(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Dir(it.Proposed)
	newBase := strings.TrimSuffix(filepath.Base(it.Proposed), filepath.Ext(it.Proposed))
	for _, name := range []string{newBase + ".en.srt", newBase + ".nfo", newBase + "-poster.jpg", "poster.jpg", "movie.nfo", "extrafanart/fanart1.jpg"} {
		if !exists(filepath.Join(newDir, filepath.FromSlash(name))) {
			t.Errorf("%s isn't in the new folder", name)
		}
	}
	if exists(filepath.Join(f.root, "heat.1995.old")) {
		t.Error("the emptied old folder should be gone")
	}
}

// A film loose in a franchise folder leaves the franchise's artwork and its own .nfo
// where they are (the folder isn't provably its own); its paired subtitle follows it.
func TestRenameFranchiseFolderKeepsItsArt(t *testing.T) {
	f, main := renameFixture(t, "Crime/heat.1995.1080p.bluray.mkv",
		"Crime/heat.1995.1080p.bluray.en.srt", "Crime/heat.1995.1080p.bluray.nfo", "Crime/folder.jpg", "Crime/poster.jpg")
	items := okItems(t, f)
	if items[0].Kept == "" {
		t.Error("the preview should say why the folder's files stay")
	}
	for _, x := range items[0].Extras {
		if x.Kind != ExtraSubtitle {
			t.Errorf("extra %+v: only the subtitle may move", x)
		}
	}
	if _, err := f.svc.ApplyRename(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(main)
	for _, name := range []string{"folder.jpg", "poster.jpg", "heat.1995.1080p.bluray.nfo"} {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("%s left the franchise folder", name)
		}
	}
	if exists(filepath.Join(dir, "heat.1995.1080p.bluray.en.srt")) {
		t.Error("the paired subtitle should follow the film")
	}
}

// Applying moves no more than the preview listed: a request without the extras moves
// only the file.
func TestRenameMovesOnlyWhatThePreviewListed(t *testing.T) {
	f, main := renameFixture(t, "heat.1995.old/heat.1995.1080p.bluray.mkv",
		"heat.1995.old/heat.1995.1080p.bluray.en.srt", "heat.1995.old/poster.jpg")
	items := okItems(t, f)
	items[0].Extras = nil
	if _, err := f.svc.ApplyRename(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(main)
	for _, name := range []string{"heat.1995.1080p.bluray.en.srt", "poster.jpg"} {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("%s moved though the preview didn't list it", name)
		}
	}
}

// A file that turns up after the preview (an unknown type) keeps every leftover where it
// is, even ones the preview listed: the folder is looked at again.
func TestRenameRechecksTheFolder(t *testing.T) {
	f, main := renameFixture(t, "heat.1995.old/heat.1995.1080p.bluray.mkv", "heat.1995.old/poster.jpg")
	items := okItems(t, f)
	if len(items[0].Extras) != 1 {
		t.Fatalf("extras = %+v, want the poster", items[0].Extras)
	}
	writeBody(t, filepath.Join(filepath.Dir(main), "notes.docx"), "late")
	if _, err := f.svc.ApplyRename(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(filepath.Dir(main), "poster.jpg")) {
		t.Error("the poster moved though the folder now holds an unknown file")
	}
}

// The plan's token covers what moves with the files: a new leftover changes it.
func TestPlanTokenCoversExtras(t *testing.T) {
	f, main := renameFixture(t, "heat.1995.old/heat.1995.1080p.bluray.mkv", "heat.1995.old/poster.jpg")
	plan, err := f.svc.RenamePlan(context.Background(), []int64{f.id})
	if err != nil {
		t.Fatal(err)
	}
	before := PlanToken(plan)
	writeBody(t, filepath.Join(filepath.Dir(main), "fanart.jpg"), "late")
	plan, err = f.svc.RenamePlan(context.Background(), nil) // the whole-library path
	if err != nil {
		t.Fatal(err)
	}
	if PlanToken(plan) == before {
		t.Error("the token should change when what moves changes")
	}
}

func TestRenamedFor(t *testing.T) {
	r := []baseRename{{"heat.1995", "Heat (1995)"}, {"heat.1995.dc", "Heat (1995) DC"}}
	for in, want := range map[string]string{
		"heat.1995.nfo":        "Heat (1995).nfo",
		"HEAT.1995-poster.jpg": "Heat (1995)-poster.jpg",
		"heat.1995.dc.en.srt":  "Heat (1995) DC.en.srt",
		"heat.19950.nfo":       "heat.19950.nfo",
		"poster.jpg":           "poster.jpg",
	} {
		if got := renamedFor(in, r); got != want {
			t.Errorf("renamedFor(%q) = %q, want %q", in, got, want)
		}
	}
}
