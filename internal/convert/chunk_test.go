package convert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A long progressive CFR film is cut into equal pieces by frame, the last running to the
// end; anything that can't be cut frame-exactly, or isn't long enough to matter, isn't.
func TestChunkPlan(t *testing.T) {
	mi := film("hevc", 3840, 2160, 70000)
	mi.FrameRateRat, mi.DurationSec = "24000/1001", 13691.4
	spans := chunkPlan(mi)
	if len(spans) != 46 {
		t.Fatalf("a 3h48 film: %d pieces, want 46 of five minutes", len(spans))
	}
	if spans[0] != (chunkSpan{0, 7193}) || spans[1].start != 7193 || spans[45].frames != 0 {
		t.Errorf("pieces: first %+v, second %+v, last %+v", spans[0], spans[1], spans[45])
	}
	// Frame-exact and covering the film: each piece starts on the frame after the last one
	// ended, the first on frame 0, and the last (open-ended) starts inside the film.
	total := int(mi.DurationSec / frameDuration(mi))
	for i := 1; i < len(spans); i++ {
		if prev := spans[i-1]; spans[i].start != prev.start+prev.frames {
			t.Fatalf("piece %d starts at frame %d, want %d", i+1, spans[i].start, prev.start+prev.frames)
		}
	}
	if last := spans[len(spans)-1]; last.start >= total || total-last.start > 2*7193 {
		t.Errorf("the last piece starts at frame %d of %d", last.start, total)
	}
	for name, mod := range map[string]func(*MediaInfo){
		"VFR":        func(m *MediaInfo) { m.VFR = true },
		"interlaced": func(m *MediaInfo) { m.Interlaced = true },
		"short":      func(m *MediaInfo) { m.DurationSec = 500 },
		"no rate":    func(m *MediaInfo) { m.FrameRateRat = "" },
	} {
		m := *mi
		mod(&m)
		if chunkPlan(&m) != nil {
			t.Errorf("%s film should be encoded in one go", name)
		}
	}
}

// Resume folders are fingerprinted by everything that shapes the pieces: an encode at
// another quality, crop, pixel format, encoder or pipeline — or of another file or item —
// can never pick up the wrong pieces.
func TestResumeDirFingerprint(t *testing.T) {
	mi := film("h264", 1920, 1080, 30000) // 8-bit SDR: the pixel format is a choice
	plan := Plan{VideoCodec: "hevc", Quality: 18}
	at := func(key, src string, enc Encoder, p Plan, kind string) string {
		return resumeDir("/scratch", key, src, mi, enc, p, kind)
	}
	a := at("movie:1", "/m/a.mkv", cpuEncoder("hevc"), plan, "mkv")
	if a != at("movie:1", "/m/a.mkv", cpuEncoder("hevc"), plan, "mkv") {
		t.Fatal("the same encode must find its own folder again")
	}
	if !strings.HasPrefix(filepath.Base(a), "resume-") {
		t.Fatalf("folder %s", a)
	}
	tighter := plan
	tighter.Quality = 16
	cropped := plan
	cropped.Crop = &Crop{W: 1920, H: 800, Y: 140}
	eightBit := plan
	eightBit.MatchSourceDepth = true
	for name, b := range map[string]string{
		"quality":      at("movie:1", "/m/a.mkv", cpuEncoder("hevc"), tighter, "mkv"),
		"crop":         at("movie:1", "/m/a.mkv", cpuEncoder("hevc"), cropped, "mkv"),
		"pixel format": at("movie:1", "/m/a.mkv", cpuEncoder("hevc"), eightBit, "mkv"),
		"encoder":      at("movie:1", "/m/a.mkv", Encoder{Name: "hevc_vaapi"}, plan, "mkv"),
		"pipeline":     at("movie:1", "/m/a.mkv", cpuEncoder("hevc"), plan, "hevc"),
		"file":         at("movie:1", "/m/b.mkv", cpuEncoder("hevc"), plan, "mkv"),
		"version":      at("movie:1:v:2", "/m/a.mkv", cpuEncoder("hevc"), plan, "mkv"),
	} {
		if b == a {
			t.Errorf("a different %s must not share the folder", name)
		}
	}
	// A 10-bit source is 10-bit either way, so the depth choice doesn't split its folder.
	tenBit := *mi
	tenBit.TenBit = true
	if resumeDir("/s", "movie:1", "/m/a.mkv", &tenBit, cpuEncoder("hevc"), plan, "mkv") !=
		resumeDir("/s", "movie:1", "/m/a.mkv", &tenBit, cpuEncoder("hevc"), eightBit, "mkv") {
		t.Error("a 10-bit source writes the same pixel format under either choice")
	}
}

// The join list states every piece's exact length but the last's.
func TestConcatList(t *testing.T) {
	dir := t.TempDir()
	files := []chunkFile{{filepath.Join(dir, "chunk-0001.mkv"), 7193}, {filepath.Join(dir, "chunk-0002.mkv"), 7193}, {filepath.Join(dir, "chunk-0003.mkv"), 0}}
	list, err := concatList(dir, files, 1001.0/24000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(list)
	got := string(b)
	want := "file 'chunk-0001.mkv'\nduration 300.008041667\nfile 'chunk-0002.mkv'\nduration 300.008041667\nfile 'chunk-0003.mkv'\n"
	if got != want {
		t.Errorf("join list:\n%s\nwant:\n%s", got, want)
	}
}

// A piece's clock starts at its first frame: joined to the crop when there is one.
func TestWithStartPTS(t *testing.T) {
	plain := withStartPTS([]string{"-c:v", "libx265"})
	if strings.Join(plain, " ") != "-vf setpts=PTS-STARTPTS -c:v libx265" {
		t.Errorf("no filter: %v", plain)
	}
	in := []string{"-vf", "crop=1920:800:0:140", "-c:v", "libx265"}
	cropped := withStartPTS(in)
	if strings.Join(cropped, " ") != "-vf setpts=PTS-STARTPTS,crop=1920:800:0:140 -c:v libx265" || in[1] != "crop=1920:800:0:140" {
		t.Errorf("with a crop: %v (input %v)", cropped, in)
	}
}

// resumeFixture writes a resume folder by hand: marker, manifest and finished pieces.
func resumeFixture(t *testing.T, dir string, m *resumeManifest, pieces ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, resumeItemName), []byte(m.Key), 0o644); err != nil {
		t.Fatal(err)
	}
	if m.Version != 0 {
		if err := writeManifest(dir, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range pieces {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("piece"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The startup clean-up removes a cut-short job's debris but never a pieced encode's
// finished pieces: they're what it carries on from.
func TestCleanScratchKeepsResumeFolders(t *testing.T) {
	s := newTestService(t)
	s.scratchDir = t.TempDir()
	keep := filepath.Join(s.scratchDir, "resume-0123456789abcdef")
	resumeFixture(t, keep, &resumeManifest{Version: 1, Key: movieKey(1)}, "chunk-0001.mkv")
	debris := filepath.Join(s.scratchDir, "convert-7.mkv")
	if err := os.WriteFile(debris, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.cleanScratch(context.Background())
	if _, err := os.Stat(debris); err == nil {
		t.Error("a cut-short encode's output should be cleaned up")
	}
	if _, err := os.Stat(filepath.Join(keep, "chunk-0001.mkv")); err != nil {
		t.Error("the finished pieces must survive the clean-up")
	}
}

// pruneResume keeps a live encode's folder and removes the rest: never set up, a month
// untouched, an item no longer in the library — and every one while the setting is off.
func TestPruneResume(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	src := r.addMovie(t, 1, "Live", 10)
	set(t, r.Service, map[string]string{keyResumable: "true"})
	folder := func(name, key string, manifest bool) string {
		dir := filepath.Join(r.scratch, "resume-"+name)
		m := &resumeManifest{Key: key, Src: src}
		if manifest {
			m.Version = 1
		}
		resumeFixture(t, dir, m, "chunk-0001.mkv")
		return dir
	}
	live := folder("live", movieKey(1), true)
	noManifest := folder("nomanifest", movieKey(1), false)
	gone := folder("gone", movieKey(99), true) // no such movie
	stale := folder("stale", movieKey(1), true)
	old := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	noItem := filepath.Join(r.scratch, "resume-noitem")
	if err := os.MkdirAll(noItem, 0o755); err != nil {
		t.Fatal(err)
	}

	r.pruneResume(ctx)
	exists := func(dir string) bool { _, err := os.Stat(dir); return err == nil }
	if !exists(live) {
		t.Error("a live encode's pieces were removed")
	}
	for name, dir := range map[string]string{"no manifest": noManifest, "no item": noItem, "gone": gone, "stale": stale} {
		if exists(dir) {
			t.Errorf("the %s folder should have been removed", name)
		}
	}

	set(t, r.Service, map[string]string{keyResumable: "false"})
	r.pruneResume(ctx)
	if exists(live) {
		t.Error("with the setting off, nothing would ever carry on from the pieces — they should go")
	}
}

// A job that ends only to wait for room keeps its pieces; any other ending drops them.
func TestFinishDropsPiecesUnlessWaitingForRoom(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	s.scratchDir = t.TempDir()
	dir := filepath.Join(s.scratchDir, "resume-x")
	for _, c := range []struct {
		state JobState
		kind  string
		keep  bool
	}{
		{StateSkipped, SkipNoScratch, true},
		{StateSkipped, SkipLibraryFull, true},
		{StateSkipped, SkipHoldFull, true},
		{StateSkipped, SkipQualityGate, false},
		{StateSkipped, SkipNotSmaller, false},
		{StateFailed, "", false},
		{StateCancelled, "", false},
		{StateDone, "", false},
	} {
		resumeFixture(t, dir, &resumeManifest{Version: 1, Key: movieKey(1)}, "chunk-0001.mkv")
		it, _ := parseKey(movieKey(1))
		job := s.claim(it, "Film", true)
		if job == nil {
			t.Fatal("couldn't claim")
		}
		s.record(job).skipKind = c.kind
		s.finish(job, c.state, "note")
		if _, err := os.Stat(dir); (err == nil) != c.keep {
			t.Errorf("%s/%s: pieces kept = %v, want %v", c.state, c.kind, err == nil, c.keep)
		}
		_ = os.RemoveAll(dir)
		s.skips.clear(ctx, movieKey(1))
	}
}

// A film with pieces already encoded is picked before a bigger saving — with the setting
// on. Off, the order is the usual one.
func TestPickJobTakesResumableFirst(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	s.scratchDir = t.TempDir()
	set(t, s, map[string]string{keyAuto: "true"})
	indexMovie(t, s, 1, "Remux", film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
	indexMovie(t, s, 2, "BluRay", film("h264", 1920, 1080, 12000, aud("ac3", "eng", 6)))
	resumeFixture(t, filepath.Join(s.scratchDir, "resume-b"), &resumeManifest{Version: 1, Key: movieKey(2)}, "chunk-0001.mkv")

	job := s.pickJob(ctx)
	if job == nil || job.MovieID != 1 {
		t.Fatalf("setting off: picked %+v, want the bigger saving", job)
	}
	s.releasePending(job.Key, job)

	set(t, s, map[string]string{keyResumable: "true"})
	s.invalidateLibraryCache()
	if job := s.pickJob(ctx); job == nil || job.MovieID != 2 {
		t.Fatalf("setting on: picked %+v, want the film with pieces done", job)
	}
}
