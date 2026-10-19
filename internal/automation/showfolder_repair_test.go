package automation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A file the bug put in <TV>/S01/Season 1 goes back into the show's own season folder
// ("S01", as the show names it), its subtitle with it; the folder it left is removed once
// empty, while <TV>/S01 — still holding another show's file — stays. History says so.
func TestMoveMisplacedBackPreviewThenApply(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	own := filepath.Join(tv, "Show (2020)")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "S01", "Show - S01E01.mkv")), "Show.S01E01.720p")
	lost := libFile(t, filepath.Join(tv, "S01", "Season 1", "Show - S01E02.mkv"))
	if err := os.WriteFile(strings.TrimSuffix(lost, ".mkv")+".en.srt", []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	setEpisodeFile(t, h.c, h.seriesID, 1, 2, lost, "Show.S01E02.1080p")
	res, err := h.c.db.Exec(`INSERT INTO series (tmdb_id, title, monitored) VALUES (99, 'Other', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	otherFile := libFile(t, filepath.Join(tv, "S01", "Season 3", "Other - S03E01.mkv"))
	setEpisodeFile(t, h.c, other, 3, 1, otherFile, "Other.S03E01")

	plan, err := h.c.MisplacedPreview(h.ctx, h.seriesID)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(own, "S01", "Show - S01E02.mkv")
	if plan.ShowFolder != own || plan.Why != "" || len(plan.Items) != 1 || plan.Items[0].To != want || plan.Items[0].Action != MisplacedMove {
		t.Fatalf("plan = %+v, want one move to %s", plan, want)
	}
	if !exists(lost) {
		t.Fatal("the preview moved something")
	}

	got, err := h.c.MoveMisplacedBack(h.ctx, h.seriesID, plan.Items)
	if err != nil {
		t.Fatal(err)
	}
	if got.Moved != 1 || len(got.Skipped) != 0 {
		t.Fatalf("result = %+v", got)
	}
	if !exists(want) || exists(lost) || !exists(strings.TrimSuffix(want, ".mkv")+".en.srt") {
		t.Error("the file (and its subtitle) didn't end up in the show's S01 folder")
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != want {
		t.Errorf("S01E02 points at %s", cur.Path)
	}
	if exists(filepath.Join(tv, "S01", "Season 1")) || !exists(otherFile) {
		t.Error("the emptied misplaced folder should go, the other show's file must stay")
	}
	if len(got.RemovedFolders) != 1 || got.RemovedFolders[0] != filepath.Join(tv, "S01", "Season 1") {
		t.Errorf("removed %v", got.RemovedFolders)
	}
	evs, _ := h.c.series.Events(h.ctx, h.seriesID, 10)
	found := false
	for _, e := range evs {
		if e.Event == "files.repaired" && strings.Contains(e.Detail, "S01E02 moved back from "+filepath.Join(tv, "S01", "Season 1")) {
			found = true
		}
	}
	if !found {
		t.Errorf("no History event for the repair: %+v", evs)
	}
	// Nothing left to do.
	if plan, _ := h.c.MisplacedPreview(h.ctx, h.seriesID); len(plan.Items) != 0 {
		t.Errorf("after the repair: %+v", plan.Items)
	}
}

// Where the show's folder already has the episode, nothing is overwritten: an identical or
// lower-quality misplaced copy goes to the recycle bin (the episode then points at the copy
// that stays), and a better or unclear one is left for the person. A changed plan is
// skipped, never applied blind.
func TestMoveMisplacedBackConflicts(t *testing.T) {
	h := newLifecycleHarness(t)
	h.c.recycle = t.TempDir()
	tv := h.c.imp.TVRoot()
	own := filepath.Join(tv, "Show")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "Season 1", "Show - S01E01.mkv")), "Show.S01E01.720p")

	// S01E02: the misplaced copy is 720p, the show's folder has a 1080p one → recycled.
	lower := filepath.Join(tv, "S01", "Season 1", "Show - S01E02 - 720p.mkv")
	sparseFile(t, lower, 60<<20)
	setEpisodeFile(t, h.c, h.seriesID, 1, 2, lower, "Show.S01E02.720p.WEB-DL")
	better := filepath.Join(own, "Season 1", "Show - S01E02 - 1080p.mkv")
	sparseFile(t, better, 70<<20)
	// S02E01: the misplaced copy is the better one → left.
	best := filepath.Join(tv, "S02", "Season 2", "Show - S02E01 - 1080p.mkv")
	sparseFile(t, best, 61<<20)
	setEpisodeFile(t, h.c, h.seriesID, 2, 1, best, "Show.S02E01.1080p")
	sparseFile(t, filepath.Join(own, "Season 2", "Show - S02E01 - 720p.mkv"), 60<<20)
	// S02E02: an identical copy under another name → recycled.
	twin := filepath.Join(tv, "S02", "Season 2", "Show - S02E02 - WEB.mkv")
	sparseFile(t, twin, 50<<20)
	setEpisodeFile(t, h.c, h.seriesID, 2, 2, twin, "Show.S02E02")
	stays := filepath.Join(own, "Season 2", "Show - S02E02.mkv")
	sparseFile(t, stays, 50<<20)

	plan, err := h.c.MisplacedPreview(h.ctx, h.seriesID)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, it := range plan.Items {
		actions[filepath.Base(it.From)] = it.Action
	}
	wantActions := map[string]string{
		"Show - S01E02 - 720p.mkv":  MisplacedRecycle,
		"Show - S02E01 - 1080p.mkv": MisplacedLeave,
		"Show - S02E02 - WEB.mkv":   MisplacedRecycle,
	}
	for name, a := range wantActions {
		if actions[name] != a {
			t.Errorf("%s: action %q, want %q (plan %+v)", name, actions[name], a, plan.Items)
		}
	}

	// One item changed since the preview: it's skipped.
	sent := append([]MisplacedItem(nil), plan.Items...)
	for i := range sent {
		if filepath.Base(sent[i].From) == "Show - S02E02 - WEB.mkv" {
			sent[i].To = filepath.Join(own, "elsewhere.mkv")
		}
	}
	res, err := h.c.MoveMisplacedBack(h.ctx, h.seriesID, sent)
	if err != nil {
		t.Fatal(err)
	}
	if res.Recycled != 1 || res.Moved != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != reasonChangedPreview {
		t.Fatalf("result = %+v", res)
	}
	if exists(lower) || !exists(better) {
		t.Error("the 720p misplaced copy should be in the bin and the 1080p one kept")
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2); cur.Path != better {
		t.Errorf("S01E02 points at %s, want the copy that stayed", cur.Path)
	}
	if !exists(best) || !exists(twin) {
		t.Error("the better copy and the changed item must stay where they are")
	}
	bin, _ := os.ReadDir(h.c.recycle)
	if len(bin) == 0 {
		t.Error("nothing went to the recycle bin")
	}
}

// With the recycle bin off, a duplicate copy is left alone — never deleted for good.
func TestMoveMisplacedBackBinOff(t *testing.T) {
	h := newLifecycleHarness(t)
	h.c.recycle = ""
	tv := h.c.imp.TVRoot()
	own := filepath.Join(tv, "Show")
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, libFile(t, filepath.Join(own, "Season 1", "Show - S01E01.mkv")), "Show.S01E01")
	lower := filepath.Join(tv, "S01", "Season 1", "Show - S01E02 - 720p.mkv")
	sparseFile(t, lower, 60<<20)
	setEpisodeFile(t, h.c, h.seriesID, 1, 2, lower, "Show.S01E02.720p")
	sparseFile(t, filepath.Join(own, "Season 1", "Show - S01E02 - 1080p.mkv"), 70<<20)
	plan, err := h.c.MisplacedPreview(h.ctx, h.seriesID)
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Action != MisplacedLeave || !strings.Contains(plan.Items[0].Note, "recycle bin is off") {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := h.c.MoveMisplacedBack(h.ctx, h.seriesID, plan.Items); err != nil || !exists(lower) {
		t.Fatalf("the copy was removed with the bin off (%v)", err)
	}
}

// A show whose every file is misplaced gets a new folder from the naming scheme; once the
// files are back, its next import goes there instead of to Review.
func TestMoveMisplacedBackIntoNewFolder(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	lost := libFile(t, filepath.Join(tv, "S01", "Season 1", "Show - S01E01.mkv"))
	setEpisodeFile(t, h.c, h.seriesID, 1, 1, lost, "Show.S01E01")
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if _, _, err := h.c.seriesPlacement(h.ctx, s); err == nil {
		t.Fatal("a show with only misplaced files has a folder to import into")
	}
	plan, err := h.c.MisplacedPreview(h.ctx, h.seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ShowFolder != filepath.Join(tv, "Show") || !plan.NewFolder || len(plan.Items) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if res, err := h.c.MoveMisplacedBack(h.ctx, h.seriesID, plan.Items); err != nil || res.Moved != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if !exists(filepath.Join(tv, "Show", "Season 1", "Show - S01E01.mkv")) || exists(filepath.Join(tv, "S01")) {
		t.Error("the file isn't in the new show folder, or the empty misplaced folders stayed")
	}
	if _, folder, err := h.c.seriesPlacement(h.ctx, s); err != nil || folder != "Show" {
		t.Errorf("after the repair: folder %q, %v", folder, err)
	}
}
