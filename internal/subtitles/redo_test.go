package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
)

// redoFixture is a movie with English (and Spanish) sidecars already there, a recycle bin,
// and a provider offering two English uploads.
func redoFixture(t *testing.T, langs string, ai *fakeAI, results ...SubtitleResult) (*Service, string, string, *syncProvider) {
	t.Helper()
	files := map[string]string{}
	for _, r := range results {
		files[r.FileID] = "1\n00:00:01,000 --> 00:00:02,000\nfrom " + r.FileID + "\n"
	}
	prov := &syncProvider{results: map[string][]SubtitleResult{"en": results}, files: files}
	mi := englishAudio()
	mi.FPS = 23.976
	s, video := ladderFixture(t, langs, mi, prov, ai)
	s.resolve = func(context.Context, *Job) (fileRef, bool) {
		return fileRef{Path: video, Title: "Movie", Year: 2020, MediaKey: "m:1", SourceRelease: bluray2160}, true
	}
	bin := t.TempDir()
	s.bin = library.SingleBin(bin)
	s.blocklist = s.ledgerBlocklist // as NewService wires it
	return s, video, bin, prov
}

func runRedo(s *Service, mode string, langs ...string) *Job {
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Mode: mode, Langs: langs, Priority: PrioManual})
	s.pop()
	s.process(context.Background(), job)
	return job
}

func writeOld(t *testing.T, s *Service, video, name, body string, row *SubtitleFile) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(video), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if row != nil {
		row.Path, row.MediaKey, row.Kind, row.VideoPath = p, "m:1", "movie", video
		if _, err := s.ledger.RecordFile(context.Background(), *row); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// binHolds reports whether the recycle bin holds a file by that name.
func binHolds(t *testing.T, bin, name string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(bin, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && fi.Name() == name {
			found = true
		}
		return nil
	})
	return found
}

// Redo of a downloaded subtitle blocklists it, fetches the other one, and bins the old.
func TestRedoBlocklistsAndPicksNext(t *testing.T) {
	s, video, bin, prov := redoFixture(t, "en", &fakeAI{}, candidate("a"), candidate("b"))
	old := writeOld(t, s, video, "Movie (2020).en.srt", "old a", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, Provider: providerOpenSubtitles, ProviderFileID: "a"})
	job := runRedo(s, ModeRedoNext, "en")
	if job.State != StateDone || strings.Join(prov.downloads, ",") != "b" {
		t.Fatalf("state %q note %q downloads %v", job.State, job.Note, prov.downloads)
	}
	if raw, _ := os.ReadFile(old); !strings.Contains(string(raw), "from b") {
		t.Errorf("sidecar now %q", raw)
	}
	if !binHolds(t, bin, "Movie (2020).en.srt") {
		t.Error("the old subtitle isn't in the recycle bin")
	}
	ctx := context.Background()
	if blocked, _ := s.ledger.BlockedFor(ctx, "m:1"); len(blocked) != 1 || blocked[0].FileID != "a" || blocked[0].Reason != BlockRedo {
		t.Errorf("blocklist %+v", blocked)
	}
	if f, ok, _ := s.ledger.LiveFile(ctx, old); !ok || f.ProviderFileID != "b" {
		t.Errorf("live row %+v", f)
	}
	if !strings.HasPrefix(job.Note, "redo en (next download) — ") {
		t.Errorf("note %q", job.Note)
	}
	// The replaced file never comes back for this video.
	prov.downloads = nil
	runRedo(s, ModeRedoNext, "en")
	for _, id := range prov.downloads {
		if id == "a" {
			t.Error("a redo fetched the blocklisted file again")
		}
	}
}

// Redo of English leaves Spanish alone.
func TestRedoOnlyTouchesNamedLanguage(t *testing.T) {
	s, video, _, _ := redoFixture(t, "en,es", &fakeAI{}, candidate("a"), candidate("b"))
	writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "a"})
	es := writeOld(t, s, video, "Movie (2020).es.srt", "spanish", nil)
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(es, past, past)
	runRedo(s, ModeRedoNext, "en")
	fi, err := os.Stat(es)
	if err != nil || !fi.ModTime().Equal(past) {
		t.Errorf("Spanish sidecar touched: %v %v", fi.ModTime(), err)
	}
}

// "Re-generate with AI" runs whisper even with OpenSubtitles configured.
func TestRedoAIModeForcesAIRung(t *testing.T) {
	ai := &fakeAI{avail: true, transcribe: true}
	s, video, _, prov := redoFixture(t, "en", ai, candidate("a"))
	writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "a"})
	job := runRedo(s, ModeRedoAI, "en")
	if job.State != StateDone || len(ai.calls) != 1 || len(prov.downloads) != 0 {
		t.Errorf("state %q note %q ai %v downloads %v", job.State, job.Note, ai.calls, prov.downloads)
	}
}

// A Movie.eng.srt is replaced by Movie.en.srt, not joined by it.
func TestRedoReplacesNonCanonicalSidecar(t *testing.T) {
	s, video, bin, _ := redoFixture(t, "en", &fakeAI{}, candidate("a"))
	old := writeOld(t, s, video, "Movie (2020).eng.srt", "old", nil)
	job := runRedo(s, ModeRedoNext, "en")
	if job.State != StateDone {
		t.Fatalf("state %q note %q", job.State, job.Note)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("Movie.eng.srt still beside the video")
	}
	assertSidecar(t, video, "en")
	if !binHolds(t, bin, "Movie (2020).eng.srt") {
		t.Error("the old file isn't in the recycle bin")
	}
}

// Without a bin nothing is replaced: the old file stays and nothing is searched.
func TestRedoAbortsWhenRetireFails(t *testing.T) {
	s, video, _, prov := redoFixture(t, "en", &fakeAI{avail: true, transcribe: true}, candidate("a"))
	s.bin = nil
	old := writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "z"})
	job := runRedo(s, ModeRedoNext, "en")
	if raw, _ := os.ReadFile(old); string(raw) != "old" || len(prov.downloads) != 0 {
		t.Errorf("old file %q downloads %v", raw, prov.downloads)
	}
	if !strings.Contains(job.Note, "kept the old subtitle") {
		t.Errorf("note %q", job.Note)
	}
}

// When every candidate is blocklisted the redo falls through to AI and says so.
func TestRedoFallsToAIWhenAllBlocked(t *testing.T) {
	ai := &fakeAI{avail: true, transcribe: true}
	s, video, _, prov := redoFixture(t, "en", ai, candidate("a"))
	writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "a"})
	job := runRedo(s, ModeRedoNext, "en")
	if len(prov.downloads) != 0 || len(ai.calls) != 1 || !strings.Contains(job.Note, "no usable OpenSubtitles match → AI-generated") {
		t.Errorf("downloads %v ai %v note %q", prov.downloads, ai.calls, job.Note)
	}
}

// Nothing better found: the old subtitle comes back out of the bin, with its provenance.
func TestRedoPutsOldBackWhenNothingMade(t *testing.T) {
	s, video, bin, _ := redoFixture(t, "en", &fakeAI{}, candidate("a"))
	old := writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "a"})
	job := runRedo(s, ModeRedoNext, "en")
	if raw, _ := os.ReadFile(old); string(raw) != "old" {
		t.Fatalf("old subtitle not put back: %q", raw)
	}
	if binHolds(t, bin, "Movie (2020).en.srt") {
		t.Error("the bin still holds it")
	}
	if f, ok, _ := s.ledger.LiveFile(context.Background(), old); !ok || f.ProviderFileID != "a" {
		t.Errorf("row %+v ok=%v", f, ok)
	}
	if job.State == StateDone || !strings.Contains(job.Note, "kept the old subtitle") {
		t.Errorf("state %q note %q", job.State, job.Note)
	}
}

// Re-extract takes the chosen track, another one than an embedded sidecar came from on a
// "next" redo.
func TestRedoExtractChoosesTrack(t *testing.T) {
	s, video, _, _ := redoFixture(t, "en", &fakeAI{}, candidate("a"))
	mi := englishAudio()
	mi.Subs = []SubTrack{{Index: 0, Lang: "eng", Text: true}, {Index: 1, Lang: "eng", Text: true, SDH: true}, {Index: 2, Lang: "und", Text: true}}
	s.probe = func(context.Context, string) (*mediaInfo, error) { return mi, nil }
	var took []int
	s.extract = func(_ context.Context, _ string, picks []extractPick) error {
		for _, p := range picks {
			took = append(took, p.Index)
			_ = os.WriteFile(p.Path, []byte(fakeSRT), 0o644)
		}
		return nil
	}
	idx := 0
	writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceEmbedded, EmbeddedIndex: &idx})
	runRedo(s, ModeRedoNext, "en")
	if len(took) != 1 || took[0] != 1 {
		t.Errorf("next redo extracted %v, want the other English track 1", took)
	}
	took = nil
	two := 2
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Mode: ModeRedoExtract, Langs: []string{"en"}, TrackIndex: &two, Priority: PrioManual})
	s.pop()
	s.process(context.Background(), job)
	if len(took) != 1 || took[0] != 2 || job.State != StateDone {
		t.Errorf("extract redo took %v (%q %q)", took, job.State, job.Note)
	}
}

// The legacy whole-file redo (?redo=1) is a "next" redo of every kept language.
func TestLegacyRedoIsNextForEveryLanguage(t *testing.T) {
	s, video, _, prov := redoFixture(t, "en", &fakeAI{}, candidate("a"), candidate("b"))
	writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "a"})
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Redo: true, Priority: PrioManual})
	s.pop()
	s.process(context.Background(), job)
	if job.State != StateDone || strings.Join(prov.downloads, ",") != "b" {
		t.Errorf("state %q note %q downloads %v", job.State, job.Note, prov.downloads)
	}
}

// A second redo of the same file and mode joins the waiting one with its language; one
// that chooses another track can't, and stays out.
func TestRedoDedupePerLanguage(t *testing.T) {
	s, _, _, _ := redoFixture(t, "en,es", &fakeAI{})
	a := s.enqueue(&Job{Kind: "movie", MovieID: 1, Mode: ModeRedoNext, Langs: []string{"en"}})
	b := s.enqueue(&Job{Kind: "movie", MovieID: 1, Mode: ModeRedoNext, Langs: []string{"es"}})
	if a != b || strings.Join(a.Langs, ",") != "en,es" {
		t.Errorf("merge: a=%p b=%p langs %v", a, b, a.Langs)
	}
	one, two := 1, 2
	x := s.enqueue(&Job{Kind: "movie", MovieID: 1, Mode: ModeRedoExtract, Langs: []string{"en"}, TrackIndex: &one})
	y := s.enqueue(&Job{Kind: "movie", MovieID: 1, Mode: ModeRedoExtract, Langs: []string{"es"}, TrackIndex: &two})
	if x != y || strings.Join(x.Langs, ",") != "en" || *x.TrackIndex != 1 {
		t.Errorf("another track joined: %v %v", x.Langs, *x.TrackIndex)
	}
}

// With the lanes on, a redo's whisper part waits in the GPU lane: the old file stays in
// place meanwhile, and the GPU job swaps it once the AI subtitle is written.
func TestRedoHandsAIToGPULane(t *testing.T) {
	ai := &fakeAI{avail: true, transcribe: true}
	s, video, bin, prov := redoFixture(t, "en", ai, candidate("a"))
	s.jobsDB = newJobStore(s.ledger.db)
	s.lanes = true
	old := writeOld(t, s, video, "Movie (2020).en.srt", "old", &SubtitleFile{Lang: "en", Source: SourceOpenSubtitles, ProviderFileID: "a"})
	job := runRedo(s, ModeRedoNext, "en")
	if job.Lane != LaneGPU || job.State != StateQueued || len(ai.calls) != 0 || len(prov.downloads) != 0 {
		t.Fatalf("lane %q state %q note %q ai %v downloads %v", job.Lane, job.State, job.Note, ai.calls, prov.downloads)
	}
	if raw, _ := os.ReadFile(old); string(raw) != "old" {
		t.Errorf("the old subtitle isn't back while the AI waits: %q", raw)
	}
	s.releaseLocked(job) // the fast worker's turn is over
	if got := s.popLane(LaneGPU); got != job {
		t.Fatalf("GPU lane gave %v", got)
	}
	s.process(context.Background(), job)
	if job.State != StateDone || len(ai.calls) != 1 || !binHolds(t, bin, "Movie (2020).en.srt") {
		t.Errorf("state %q note %q ai %v", job.State, job.Note, ai.calls)
	}
	if raw, _ := os.ReadFile(old); string(raw) != fakeSRT {
		t.Errorf("sidecar now %q", raw)
	}
}

// "Use this file": the chosen download is saved for that language, locked.
func TestRedoPickSavesChosenFile(t *testing.T) {
	s, video, _, prov := redoFixture(t, "en", &fakeAI{}, candidate("a"), candidate("chosen"))
	writeOld(t, s, video, "Movie (2020).en.srt", "old", nil)
	job := s.enqueue(&Job{Kind: "movie", MovieID: 1, Title: "Movie", Mode: ModeRedoPick, Langs: []string{"en"}, PickFileID: "chosen", Priority: PrioManual})
	s.pop()
	s.process(context.Background(), job)
	if job.State != StateDone || strings.Join(prov.downloads, ",") != "chosen" {
		t.Fatalf("state %q note %q downloads %v", job.State, job.Note, prov.downloads)
	}
	f, ok, _ := s.ledger.LiveFile(context.Background(), sidecarPath(video, "en"))
	if !ok || f.ProviderFileID != "chosen" || !f.Locked {
		t.Errorf("row %+v", f)
	}
}
