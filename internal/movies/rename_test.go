package movies

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readBody(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func writeBody(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func planByVersion(t *testing.T, f *deleteFixture) map[int64]RenameItem {
	t.Helper()
	items, err := f.svc.RenamePlan(context.Background(), []int64{f.id})
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]RenameItem{}
	for _, it := range items {
		out[it.VersionID] = it
	}
	return out
}

// The plan covers the default track and every extra one, each with its own name: two
// tracks at the same quality never get the same file name.
func TestRenamePlanIncludesVersions(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	plan := planByVersion(t, f)
	def, extra := plan[0], plan[f.vid]
	if def.Current != f.main || extra.Current != f.extra {
		t.Fatalf("plan = %+v, want both tracks' current files", plan)
	}
	if def.Status != RenameOK || extra.Status != RenameOK {
		t.Fatalf("statuses = %s / %s (%s / %s), want ok for both", def.Status, extra.Status, def.Reason, extra.Reason)
	}
	if def.Proposed == extra.Proposed {
		t.Fatalf("both tracks were given %q", def.Proposed)
	}
	if !strings.Contains(filepath.Base(extra.Proposed), "Director's Cut") {
		t.Errorf("the extra track's name %q should carry its label", extra.Proposed)
	}
	if filepath.Dir(def.Proposed) != filepath.Join(f.root, "Heat (1995)") {
		t.Errorf("default track planned into %q", filepath.Dir(def.Proposed))
	}
	// Planning moved nothing.
	if readBody(t, f.main) != "x" || readBody(t, f.extra) != "x" {
		t.Error("the plan must not touch the files")
	}
}

// An extra track with an edition gets Plex's {edition-…} tag when its name doesn't say it.
func TestRenamePlanTagsEdition(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	if err := f.svc.repo.UpdateVersion(ctx, f.vid, "Final", "hd", "Final Cut", false); err != nil {
		t.Fatal(err)
	}
	extra := planByVersion(t, f)[f.vid]
	if !strings.Contains(extra.Proposed, "{edition-Final Cut}") {
		t.Fatalf("proposed = %q, want an {edition-Final Cut} tag", extra.Proposed)
	}
}

// A file that appeared at the new name between the preview and Apply is never
// overwritten: the item is skipped and both files are intact.
func TestApplyRenameRefusesExistingTarget(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	def := planByVersion(t, f)[0]
	if def.Status != RenameOK {
		t.Fatalf("status = %s, want ok", def.Status)
	}
	writeBody(t, def.Proposed, "someone else's file")

	res, err := f.svc.ApplyRename(ctx, []RenameItem{def})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Renamed || res[0].Reason == "" {
		t.Fatalf("results = %+v, want one skipped item with a reason", res)
	}
	if readBody(t, def.Proposed) != "someone else's file" || readBody(t, f.main) != "x" {
		t.Error("both files must be intact")
	}
	m, _ := f.svc.Get(ctx, f.id)
	if m.MovieFilePath != f.main {
		t.Errorf("record = %q, want it unchanged", m.MovieFilePath)
	}
}

// An item whose plan changed since the preview (a new naming scheme, another rename) is
// skipped rather than renamed to something the person never saw.
func TestApplyRenameSkipsChangedItems(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	def := planByVersion(t, f)[0]
	def.Proposed = filepath.Join(f.root, "Somewhere else.mkv")
	res, err := f.svc.ApplyRename(context.Background(), []RenameItem{def})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Renamed {
		t.Fatalf("results = %+v, want the stale item skipped", res)
	}
	if readBody(t, f.main) != "x" {
		t.Error("the file must stay where it was")
	}
}

// A rename is the same file under a new name: the release it came from and its cached
// media facts stay with it.
func TestApplyRenameKeepsSourceReleaseAndMedia(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	const rel = "Heat.1995.1080p.BluRay.x265-GRP"
	if err := f.svc.repo.SetSourceRelease(ctx, f.id, rel); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(f.main)
	if err != nil {
		t.Fatal(err)
	}
	media, _ := json.Marshal(MovieFile{Path: f.main, Filename: filepath.Base(f.main), SizeBytes: fi.Size(), MtimeUnix: fi.ModTime().Unix(),
		Codec: "x265", Resolution: "1080p", Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, f.id, string(media)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.SetVersionSourceRelease(ctx, f.vid, "Heat.1995.Directors.Cut.1080p.BluRay-GRP"); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.Rename(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	m, err := f.svc.Get(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if m.MovieFilePath == f.main {
		t.Fatal("the default file wasn't renamed")
	}
	if m.SourceRelease != rel {
		t.Errorf("source_release = %q, want %q kept", m.SourceRelease, rel)
	}
	if m.File == nil || m.File.Codec != "x265" || m.File.Path != m.MovieFilePath {
		t.Errorf("media = %+v, want the cached facts moved to the new path", m.File)
	}
	v, _, err := f.svc.repo.GetVersion(ctx, f.vid)
	if err != nil {
		t.Fatal(err)
	}
	if v.FilePath == f.extra || v.SourceRelease != "Heat.1995.Directors.Cut.1080p.BluRay-GRP" {
		t.Errorf("extra track = %q / %q, want it renamed with its release kept", v.FilePath, v.SourceRelease)
	}
	if readBody(t, m.MovieFilePath) != "x" || readBody(t, v.FilePath) != "x" {
		t.Error("both files must be at their new names")
	}
	// Subtitles travel with their own video, not the other track's.
	base := strings.TrimSuffix(m.MovieFilePath, filepath.Ext(m.MovieFilePath))
	if _, err := os.Stat(base + ".en.srt"); err != nil {
		t.Errorf("the default track's subtitle didn't follow it: %v", err)
	}
	vbase := strings.TrimSuffix(v.FilePath, filepath.Ext(v.FilePath))
	if _, err := os.Stat(vbase + ".en.srt"); err != nil {
		t.Errorf("the extra track's subtitle didn't follow it: %v", err)
	}
}

// A rename into a new folder takes the film's movie.nfo and artwork along and removes the
// emptied old folder, instead of leaving an orphan folder holding them.
func TestApplyRenameMovesNFOAndArtwork(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	clearFixtureFolder(t, f)
	ctx := context.Background()
	oldDir := filepath.Join(f.root, "heat.1995.old")
	main := writeBody(t, filepath.Join(oldDir, "heat.1995.1080p.bluray.mkv"), "film")
	writeBody(t, filepath.Join(oldDir, "movie.nfo"), "<movie/>")
	writeBody(t, filepath.Join(oldDir, "poster.jpg"), "poster")
	writeBody(t, filepath.Join(oldDir, "fanart.jpg"), "fanart")
	if err := f.svc.repo.SetFile(ctx, f.id, main); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.ClearVersionFile(ctx, f.vid); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.Rename(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	m, _ := f.svc.Get(ctx, f.id)
	newDir := filepath.Dir(m.MovieFilePath)
	if newDir == oldDir {
		t.Fatal("the folder should have changed")
	}
	for name, body := range map[string]string{"movie.nfo": "<movie/>", "poster.jpg": "poster", "fanart.jpg": "fanart"} {
		if got := readBody(t, filepath.Join(newDir, name)); got != body {
			t.Errorf("%s = %q in the new folder", name, got)
		}
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("the old folder should be gone, stat = %v", err)
	}
}

// A folder that still holds another video keeps its metadata: it's that video's too.
func TestApplyRenameLeavesFolderFilesWithAnotherVideo(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	oldDir := filepath.Join(f.root, "heat.1995.old")
	main := writeBody(t, filepath.Join(oldDir, "heat.1995.1080p.bluray.mkv"), "film")
	writeBody(t, filepath.Join(oldDir, "untracked.sample.mkv"), "other")
	writeBody(t, filepath.Join(oldDir, "movie.nfo"), "<movie/>")
	if err := f.svc.repo.SetFile(ctx, f.id, main); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.ClearVersionFile(ctx, f.vid); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Rename(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if readBody(t, filepath.Join(oldDir, "movie.nfo")) != "<movie/>" || readBody(t, filepath.Join(oldDir, "untracked.sample.mkv")) != "other" {
		t.Error("the old folder's other video and its metadata must stay")
	}
}

// Two tracks that would land on one name: the second is a conflict, not a second move
// onto the first.
func TestRenamePlanSameTargetIsConflict(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	// An empty label: the per-track name builder (MOV-12) still tells the extra track
	// apart, by the generic "Version", exactly as an import would name it — never the
	// default track's name.
	if err := f.svc.repo.UpdateVersion(ctx, f.vid, "", "hd", "", false); err != nil {
		t.Fatal(err)
	}
	plan := planByVersion(t, f)
	if plan[0].Status != RenameOK || plan[f.vid].Status != RenameOK {
		t.Fatalf("statuses = %s / %s, want ok / ok", plan[0].Status, plan[f.vid].Status)
	}
	if plan[0].Proposed == plan[f.vid].Proposed || !strings.HasSuffix(strings.TrimSuffix(plan[f.vid].Proposed, filepath.Ext(plan[f.vid].Proposed)), " - Version") {
		t.Fatalf("proposed %q / %q; want distinct, the extra one labelled", plan[0].Proposed, plan[f.vid].Proposed)
	}
}

// PlanToken changes when what would be renamed changes, and only then.
func TestPlanToken(t *testing.T) {
	a := []RenameItem{{MovieID: 1, VersionID: 0, Current: "/a", Proposed: "/b", Status: RenameOK}, {MovieID: 2, Current: "/c", Proposed: "/c", Status: RenameSame}}
	b := []RenameItem{a[1], a[0]}
	if PlanToken(a) != PlanToken(b) {
		t.Error("the token must not depend on order or on unchanged items")
	}
	c := []RenameItem{{MovieID: 1, VersionID: 0, Current: "/a", Proposed: "/z", Status: RenameOK}}
	if PlanToken(a) == PlanToken(c) {
		t.Error("a different proposed name must change the token")
	}
}
