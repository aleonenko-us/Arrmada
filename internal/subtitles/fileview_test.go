package subtitles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// threeCues is a small real SRT: styling, a two-line cue and CRLF line ends.
const threeCues = "1\r\n00:00:01,000 --> 00:00:02,500\r\n<i>Hello there.</i>\r\n\r\n" +
	"2\r\n00:00:03,000 --> 00:00:04,000\r\nGeneral\r\nKenobi.\r\n\r\n" +
	"3\r\n00:00:05,000 --> 00:00:06,000\r\nYou are a bold one.\r\n\r\n" +
	"4\r\n00:00:07,000 --> 00:00:08,000\r\nKill him.\r\n"

func movieRef(id int64) MediaRef { return MediaRef{Kind: "movie", MovieID: id} }

// The view lists every paired sidecar with what the folder says and what the ledger
// knows, the orphans apart, and each kept language's state with its backoff.
func TestFileViewJoinsDiskAndLedger(t *testing.T) {
	f := newOrphanFixture(t, "en,es,fr")
	video := f.movie(t, 1, "Heat (1995).mkv", "Heat (1995).en.srt", "Heat (1995).es.sdh.srt", "Old Heat.eng.srt")
	dir := filepath.Dir(video)
	if err := os.WriteFile(filepath.Join(dir, "Heat (1995).en.srt"), []byte(threeCues), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	score, conf := 88, 91
	if _, err := f.s.ledger.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", MovieID: 1, VideoPath: video,
		Path: filepath.Join(dir, "Heat (1995).en.srt"), Lang: "en", Source: SourceOpenSubtitles, Provider: providerOpenSubtitles,
		ProviderFileID: "777", ReleaseName: "Heat.1995.BluRay-GRP", Score: &score, SyncConfidence: &conf, SyncCheckedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ledger.SaveBackoff(ctx, Backoff{MediaKey: "m:1", Lang: "fr", VideoPath: video, PrimaryReason: ResultImpossible,
		Count: 1, ClearOn: []string{EventLangs}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ledger.RecordAttempts(ctx, "r1", "m:1", video, []Outcome{
		{Lang: "fr", Rung: RungOpenSubtitles, Result: ResultNoMatch, Detail: "no OpenSubtitles match"},
		{Lang: "fr", Rung: RungAI, Result: ResultImpossible, Detail: "whisper can't translate into fr"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ledger.RecordAttempts(ctx, "r2", "m:1", video, []Outcome{
		{Lang: "fr", Rung: RungAI, Result: ResultImpossible, Detail: "again"},
	}); err != nil {
		t.Fatal(err)
	}

	v, err := f.s.FileView(ctx, movieRef(1))
	if err != nil {
		t.Fatal(err)
	}
	if v.VideoPath != video || v.MediaKey != "m:1" {
		t.Errorf("video = %q key %q", v.VideoPath, v.MediaKey)
	}
	if len(v.Sidecars) != 2 {
		t.Fatalf("sidecars = %+v, want the two paired ones", v.Sidecars)
	}
	byName := map[string]SidecarView{}
	for _, sv := range v.Sidecars {
		byName[sv.Name] = sv
	}
	en := byName["Heat (1995).en.srt"]
	if !en.Owned || en.Source != SourceOpenSubtitles || en.ProviderFileID != "777" || en.Release != "Heat.1995.BluRay-GRP" ||
		en.Score == nil || *en.Score != 88 || en.Health.Score != 91 || en.Health.State != HealthOK || !en.Shiftable {
		t.Errorf("en sidecar = %+v", en)
	}
	if en.CueCount != 4 || len(en.FirstCues) != 3 || en.FirstCues[0].Text != "Hello there." || en.FirstCues[0].StartMs != 1000 ||
		en.FirstCues[1].Text != "General Kenobi." {
		t.Errorf("en preview = %d %+v", en.CueCount, en.FirstCues)
	}
	es := byName["Heat (1995).es.sdh.srt"]
	if es.Owned || es.Source != SourceUnknown || es.Lang != "es" || es.Variant != VariantSDH || es.Size == 0 || es.Health.State != HealthUnchecked {
		t.Errorf("es sidecar = %+v, want an unowned SDH es file", es)
	}
	if len(v.Orphans) != 1 || v.Orphans[0].Name != "Old Heat.eng.srt" {
		t.Errorf("orphans = %+v", v.Orphans)
	}
	var fr LangStatus
	for _, l := range v.Languages {
		if l.Lang == "fr" {
			fr = l
		}
	}
	if fr.Have || fr.Reason != string(ResultImpossible) || fr.RetryOn != EventLangs {
		t.Errorf("fr = %+v, want missing and parked on a language change", fr)
	}
	if len(v.Backoffs) != 1 || v.Backoffs[0].Lang != "fr" {
		t.Errorf("backoffs = %+v", v.Backoffs)
	}
	// Latest per rung: the AI rung's newer attempt, plus the download's.
	if len(v.Attempts) != 2 || v.Attempts[0].Detail != "again" {
		t.Errorf("attempts = %+v", v.Attempts)
	}
	if _, err := f.s.FileView(ctx, movieRef(99)); !errors.Is(err, ErrMediaNotFound) {
		t.Errorf("unknown movie: err = %v", err)
	}
	if _, err := f.s.FileView(ctx, MediaRef{Kind: "show", MovieID: 1}); !errors.Is(err, ErrBadMediaRef) {
		t.Errorf("bad kind: err = %v", err)
	}
}

// A nudge rewrites the timing lines only (the text, line breaks and CRLFs stay as they
// were), keeps a running total in the ledger, and tells Plex.
func TestShiftRoundTrip(t *testing.T) {
	f := newOrphanFixture(t, "en")
	video := f.movie(t, 1, "Heat (1995).mkv", "Heat (1995).en.srt")
	sub := filepath.Join(filepath.Dir(video), "Heat (1995).en.srt")
	if err := os.WriteFile(sub, []byte(threeCues), 0o644); err != nil {
		t.Fatal(err)
	}
	var told []string
	f.s.SetLibraryChanged(func(kind, dir string) { told = append(told, kind+":"+dir) })
	ctx := context.Background()

	sv, err := f.s.ShiftSidecar(ctx, movieRef(1), "Heat (1995).en.srt", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if sv.FirstCues[0].StartMs != 2000 || sv.FirstCues[0].EndMs != 3500 {
		t.Errorf("preview after +1 s = %+v", sv.FirstCues[0])
	}
	raw, _ := os.ReadFile(sub)
	if !strings.Contains(string(raw), "00:00:02,000 --> 00:00:03,500\r\n<i>Hello there.</i>\r\n") ||
		!strings.Contains(string(raw), "General\r\nKenobi.") {
		t.Errorf("rewritten file:\n%q", raw)
	}
	row, ok, err := f.s.Ledger().LiveFile(ctx, sub)
	if err != nil || !ok || row.Source != SourceUnknown || row.SyncShiftMs == nil || *row.SyncShiftMs != 1000 || row.SyncOffsetMs != nil || row.Lang != "en" {
		t.Fatalf("ledger row after the first shift = %+v ok=%v err=%v", row, ok, err)
	}
	// A check then finds it 200 ms early; the next nudge moves that measurement with it.
	if _, err := f.s.Ledger().RecordSync(ctx, sub, SyncCheck{OffsetMs: -200, Confidence: 80, ShiftMs: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ShiftSidecar(ctx, movieRef(1), "Heat (1995).en.srt", -500); err != nil {
		t.Fatal(err)
	}
	row, _, _ = f.s.Ledger().LiveFile(ctx, sub)
	if row.SyncShiftMs == nil || *row.SyncShiftMs != 500 || deref(row.SyncOffsetMs) != -700 || deref(row.SyncConfidence) != 80 {
		t.Errorf("after −0.5 s: shift %v offset %v confidence %v, want 500, −700, 80", deref(row.SyncShiftMs), deref(row.SyncOffsetMs), deref(row.SyncConfidence))
	}
	if _, err := f.s.ShiftSidecar(ctx, movieRef(1), "Heat (1995).en.srt", -500); err != nil {
		t.Fatal(err)
	}
	if row, _, _ = f.s.Ledger().LiveFile(ctx, sub); row.SyncShiftMs != nil {
		t.Errorf("shifted back to where it was: shift %v, want none", deref(row.SyncShiftMs))
	}
	if n, cues := previewCues(sub, 1); n != 4 || cues[0].StartMs != 1000 {
		t.Errorf("after both shifts: %d cues, first %+v", n, cues)
	}
	if len(told) != 3 || told[0] != "movie:"+filepath.Dir(video) {
		t.Errorf("Plex was told %v", told)
	}

	for _, off := range []int{0, maxShiftMs + 1, -maxShiftMs - 1} {
		if _, err := f.s.ShiftSidecar(ctx, movieRef(1), "Heat (1995).en.srt", off); !errors.Is(err, ErrBadShift) {
			t.Errorf("shift %d: err = %v", off, err)
		}
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// Names are checked against the folder: no paths, no other folders, no non-subtitles,
// nothing that doesn't pair with this video — and nothing on disk changes.
func TestFileHandlersRejectForeignPaths(t *testing.T) {
	f := newOrphanFixture(t, "en")
	video := f.movie(t, 1, "Heat (1995).mkv", "Heat (1995).en.srt", "Heat (1995).en.txt")
	other := f.movie(t, 2, "Ronin (1998).mkv", "Ronin (1998).en.srt")
	ctx := context.Background()
	for _, name := range []string{
		"", ".", "..", "../Ronin (1998)/Ronin (1998).en.srt", "sub/Heat (1995).en.srt", `sub\Heat (1995).en.srt`,
		filepath.Join(filepath.Dir(other), "Ronin (1998).en.srt"), "Ronin (1998).en.srt", // another folder's
		"Heat (1995).en.txt", "Heat (1995).mkv", "Heat (1995).fr.srt", "C:Heat (1995).en.srt",
	} {
		if _, err := f.s.ShiftSidecar(ctx, movieRef(1), name, 500); !errors.Is(err, ErrNotSidecar) {
			t.Errorf("shift %q: err = %v, want ErrNotSidecar", name, err)
		}
		if err := f.s.RemoveSidecar(ctx, movieRef(1), name); !errors.Is(err, ErrNotSidecar) {
			t.Errorf("remove %q: err = %v, want ErrNotSidecar", name, err)
		}
	}
	for _, p := range []string{video, filepath.Join(filepath.Dir(video), "Heat (1995).en.srt"), filepath.Join(filepath.Dir(other), "Ronin (1998).en.srt")} {
		if !exists(p) {
			t.Errorf("%s is gone", p)
		}
	}
	// An .ass sidecar pairs, but only SRT timing is rewritten.
	if err := os.WriteFile(filepath.Join(filepath.Dir(video), "Heat (1995).es.ass"), []byte("[Script Info]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ShiftSidecar(ctx, movieRef(1), "Heat (1995).es.ass", 500); !errors.Is(err, ErrNotShiftable) {
		t.Errorf("shift .ass: err = %v", err)
	}
}

// Delete moves the file into the recycle bin (never deletes it), supersedes its row,
// blocklists a removed download and turns the language missing in the Library.
func TestDeleteRecyclesSidecar(t *testing.T) {
	f := newOrphanFixture(t, "en")
	video := f.movie(t, 1, "Heat (1995).mkv", "Heat (1995).en.srt", "Old.eng.srt")
	dir := filepath.Dir(video)
	sub := filepath.Join(dir, "Heat (1995).en.srt")
	ctx := context.Background()
	if _, err := f.s.ledger.RecordFile(ctx, SubtitleFile{MediaKey: "m:1", Kind: "movie", MovieID: 1, VideoPath: video, Path: sub,
		Lang: "en", Source: SourceOpenSubtitles, Provider: providerOpenSubtitles, ProviderFileID: "555"}); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	if list, _ := f.s.SnapshotMovies(); len(list) != 1 || list[0].Missing != 0 {
		t.Fatalf("before: %+v", list)
	}

	if err := f.s.RemoveSidecar(ctx, movieRef(1), "Heat (1995).en.srt"); err != nil {
		t.Fatal(err)
	}
	if exists(sub) {
		t.Error("the sidecar is still beside the video")
	}
	if !inBin(t, f.bin, "Heat (1995).en.srt") {
		t.Error("the sidecar isn't in the recycle bin")
	}
	if _, ok, _ := f.s.Ledger().LiveFile(ctx, sub); ok {
		t.Error("the ledger row is still live")
	}
	if blocked, _ := f.s.Ledger().Blocklisted(ctx, providerOpenSubtitles, "m:1"); !blocked["555"] {
		t.Error("the removed download isn't blocklisted for this movie")
	}
	if list, _ := f.s.SnapshotMovies(); len(list) != 1 || list[0].Missing != 1 {
		t.Errorf("after: %+v, want en missing", list)
	}
	// Orphans can go too.
	if err := f.s.RemoveSidecar(ctx, movieRef(1), "Old.eng.srt"); err != nil || exists(filepath.Join(dir, "Old.eng.srt")) {
		t.Errorf("orphan remove: err %v", err)
	}

	// No bin: refused, and the file stays.
	keep := filepath.Join(dir, "Heat (1995).es.srt")
	if err := os.WriteFile(keep, []byte(fakeSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	f.s.SetBin(nil)
	if err := f.s.RemoveSidecar(ctx, movieRef(1), "Heat (1995).es.srt"); err == nil || !exists(keep) {
		t.Errorf("with no bin: err %v, file kept %v", err, exists(keep))
	}
}

// A job working on the file blocks a shift or a delete rather than racing it.
func TestFileActionsWaitForRunningJob(t *testing.T) {
	f := newOrphanFixture(t, "en")
	f.movie(t, 1, "Heat (1995).mkv", "Heat (1995).en.srt")
	f.s.busy = map[string]*Job{"m:1": {ID: 1}}
	ctx := context.Background()
	if _, err := f.s.ShiftSidecar(ctx, movieRef(1), "Heat (1995).en.srt", 500); !errors.Is(err, ErrFileBusy) {
		t.Errorf("shift: err = %v", err)
	}
	if err := f.s.RemoveSidecar(ctx, movieRef(1), "Heat (1995).en.srt"); !errors.Is(err, ErrFileBusy) {
		t.Errorf("remove: err = %v", err)
	}
}

// inBin reports whether a file of that name is anywhere under the bin.
func inBin(t *testing.T, bin, name string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(bin, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = true
		}
		return nil
	})
	return found
}
