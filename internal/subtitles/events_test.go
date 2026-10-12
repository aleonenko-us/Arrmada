package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// changeLog records what the module told Plex's scanner.
type changeLog struct {
	mu  sync.Mutex
	got []string // "<kind> <dir>"
}

func (c *changeLog) hook(kind, dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, kind+" "+dir)
}

func (c *changeLog) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.got
	c.got = nil
	return out
}

// Every way a sidecar changes — written by the ladder, repaired, adopted, retired by a
// replace — asks Plex to rescan the video's folder.
func TestSidecarWritePublishesEvent(t *testing.T) {
	var log changeLog

	// Written by the ladder (AI here).
	s, video := ladderFixture(t, "en", englishAudio(), nil, &fakeAI{avail: true, transcribe: true})
	s.SetLibraryChanged(log.hook)
	runJob(s)
	if got := log.take(); len(got) != 1 || got[0] != "movie "+filepath.Dir(video) {
		t.Errorf("after a write: %v, want one movie scan of the folder", got)
	}

	// Nothing written: nothing to tell.
	s2, _ := ladderFixture(t, "en", englishAudio(), nil, &fakeAI{})
	s2.SetLibraryChanged(log.hook)
	runJob(s2)
	if got := log.take(); len(got) != 0 {
		t.Errorf("a job that wrote nothing asked for %v", got)
	}

	// An episode is a show folder.
	s3, ep := ladderFixture(t, "en", englishAudio(), nil, &fakeAI{avail: true, transcribe: true})
	s3.SetLibraryChanged(log.hook)
	job := s3.enqueue(&Job{Kind: "episode", SeriesID: 1, Season: 1, Episode: 1, Title: "Show"})
	s3.pop()
	s3.process(context.Background(), job)
	if got := log.take(); len(got) != 1 || got[0] != "show "+filepath.Dir(ep) {
		t.Errorf("after an episode write: %v", got)
	}

	// Repaired.
	r, rv, _ := repairFixture(t)
	r.SetLibraryChanged(log.hook)
	if err := os.WriteFile(sidecarPath(rv, "en"), []byte(srtOf(12)), 0o644); err != nil {
		t.Fatal(err)
	}
	runRepairJob(r, "en")
	if got := log.take(); len(got) != 1 {
		t.Errorf("after a repair: %v", got)
	}

	// Adopted.
	f := newOrphanFixture(t, "en")
	f.s.SetLibraryChanged(log.hook)
	av := f.movie(t, 1, "Heat (1995).mkv", "Heat.eng.srt")
	if _, err := f.s.AdoptOrphan(context.Background(), 1, "Heat.eng.srt", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := log.take(); len(got) != 1 || got[0] != "movie "+filepath.Dir(av) {
		t.Errorf("after an adopt: %v", got)
	}

	// Replaced: the write and the orphan's removal (the scanner folds the two together).
	f2 := newOrphanFixture(t, "en")
	f2.s.SetLibraryChanged(log.hook)
	f2.movie(t, 1, "Heat (1995).mkv", "Heat.eng.srt")
	if _, err := f2.s.ReplaceOrphan(context.Background(), 1, "Heat.eng.srt"); err != nil {
		t.Fatal(err)
	}
	f2.runQueued()
	if got := log.take(); len(got) != 2 {
		t.Errorf("after a replace: %v, want the write and the removal", got)
	}

	// No hook set: nothing happens, nothing panics.
	s4, _ := ladderFixture(t, "en", englishAudio(), nil, &fakeAI{avail: true, transcribe: true})
	s4.SetLibraryChanged(log.hook)
	s4.SetLibraryChanged(nil)
	runJob(s4)
	if got := log.take(); len(got) != 0 {
		t.Errorf("a cleared hook still heard %v", got)
	}
}
