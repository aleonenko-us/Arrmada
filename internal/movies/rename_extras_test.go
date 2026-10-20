package movies

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// renameFixture is one movie (Heat, 1995) with a single file at rel under the Movies
// folder, and the other films given as {title, year, rel}.
func renameFixture(t *testing.T, rel string, files ...string) (*deleteFixture, string) {
	t.Helper()
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
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
