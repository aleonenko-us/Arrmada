package convert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/outbox"
	"github.com/tristenlammi/arrmada/internal/store"
)

// Everything here runs on small stand-in files in temp folders; nothing is encoded.

// addVersion writes a stand-in file named name into the library and records it as extra
// version track id of movie movieID.
func (r *spaceRig) addVersion(t *testing.T, movieID, id int64, label, name string, size int, fill string) string {
	t.Helper()
	path := filepath.Join(r.lib, name)
	if err := os.WriteFile(path, []byte(strings.Repeat(fill, size)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO movie_versions (id, movie_id, label, has_file, file_path, size_bytes, source_release)
		VALUES (?, ?, ?, 1, ?, ?, ?)`, id, movieID, label, path, size, "Film.2020.2160p.BluRay.REMUX.HEVC-GRP"); err != nil {
		t.Fatal(err)
	}
	return path
}

// cacheProbe records mi as the probe result for the small stand-in file at path as it is
// now, so the indexer reads it without ffprobe. mi keeps the full-length film's size.
func (r *spaceRig) cacheProbe(t *testing.T, path string, mi *MediaInfo) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	r.cache.put(context.Background(), path, fi.Size(), fi.ModTime().Unix(), mi)
}

// indexed is path → version_id for a movie's index rows.
func (r *spaceRig) indexed(t *testing.T, movieID int64) map[string]int64 {
	t.Helper()
	rows, err := r.db.Query(`SELECT path, version_id FROM convert_library WHERE media_type = 'movie' AND movie_id = ?`, movieID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var p string
		var v int64
		if err := rows.Scan(&p, &v); err != nil {
			t.Fatal(err)
		}
		out[p] = v
	}
	return out
}

func TestVersionItemKeys(t *testing.T) {
	it, ok := parseKey("movie:5:v:2")
	if !ok || it.Kind != "movie" || it.MovieID != 5 || it.VersionID != 2 || it.key() != "movie:5:v:2" {
		t.Fatalf("parseKey(movie:5:v:2) = %+v, %v", it, ok)
	}
	if got := ItemKey("movie", 5, 2, 0, 0, 0); got != "movie:5:v:2" {
		t.Fatalf("ItemKey = %q", got)
	}
	// The default file keeps the key it always had, so old requests, skips and failures
	// still name it.
	if got := ItemKey("movie", 5, 0, 0, 0, 0); got != "movie:5" {
		t.Fatalf("default ItemKey = %q", got)
	}
	if it, ok := parseKey("movie:5"); !ok || it.VersionID != 0 || it.key() != "movie:5" {
		t.Fatalf("parseKey(movie:5) = %+v, %v", it, ok)
	}
	for _, bad := range []string{"movie:5:v:0", "movie:5:v:", "movie:5:x:2", "movie:0:v:2", "movie:5:v:-1"} {
		if _, ok := parseKey(bad); ok {
			t.Errorf("parseKey(%q) accepted", bad)
		}
	}
	if k := jobKey(&Job{Kind: "movie", MovieID: 5, VersionID: 2}); k != "movie:5:v:2" {
		t.Fatalf("jobKey = %q", k)
	}
	b := Blocked{Key: "movie:5:v:2"}
	parseItemKey(&b)
	if b.Kind != "movie" || b.MovieID != 5 || b.VersionID != 2 {
		t.Fatalf("parseItemKey = %+v", b)
	}
}

// A movie with a 1080p default file and a 4K remux kept as a version shows both: in the
// Library, the stats and the reclaimable figure, and in Up next. Removing the version
// drops its row and leaves the default's.
func TestIndexMovieIndexesEveryVersion(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	def := r.addMovie(t, 1, "Film", 1000)
	ver := r.addVersion(t, 1, 2, "4K Remux", "Film - 4K Remux.mkv", 3000, "x")
	r.cacheProbe(t, def, film("hevc", 1920, 1080, 6000, aud("eac3", "eng", 6)))
	r.cacheProbe(t, ver, film("h264", 3840, 2160, 85000, aud("truehd", "eng", 8)))

	if err := r.IndexMovie(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := r.indexed(t, 1); len(got) != 2 || got[def] != 0 || got[ver] != 2 {
		t.Fatalf("index rows = %v", got)
	}

	lib, err := r.Library(ctx)
	if err != nil || len(lib) != 2 {
		t.Fatalf("library = %d rows (%v)", len(lib), err)
	}
	if lib[0].Key != "movie:1" || lib[0].VersionID != 0 {
		t.Fatalf("the default comes first under its old key: %+v", lib[0])
	}
	if v := lib[1]; v.Key != "movie:1:v:2" || v.VersionID != 2 || v.VersionLabel != "4K Remux" || v.Title != "Film" || v.Path != ver || !v.Worth {
		t.Fatalf("version row: %+v", v)
	}

	stats, err := r.computeLibraryStats(ctx)
	if err != nil || stats.Movies.Files != 2 || stats.Movies.Convertible < 1 || stats.Movies.Reclaimable <= 0 {
		t.Fatalf("stats: %+v (%v)", stats.Movies, err)
	}

	var found bool
	for _, c := range r.computeCandidates(ctx, r.prefs(ctx)) {
		if c.Key == "movie:1:v:2" {
			found = true
			if c.Title != "Film · 4K Remux" || c.VersionID != 2 || c.VersionLabel != "4K Remux" {
				t.Fatalf("up next: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("the 4K remux isn't in Up next")
	}

	// The version's file goes: its row goes, the default's stays.
	if _, err := r.db.Exec(`UPDATE movie_versions SET has_file = 0, file_path = '' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if err := r.IndexMovie(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := r.indexed(t, 1); len(got) != 1 || got[def] != 0 {
		t.Fatalf("after removing the version: %v", got)
	}

	// A film whose only file is a version's is still indexed.
	if _, err := r.db.Exec(`UPDATE movie_versions SET has_file = 1, file_path = ? WHERE id = 2`, ver); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE movies SET has_file = 0, movie_file_path = '' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := r.IndexMovie(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := r.indexed(t, 1); len(got) != 1 || got[ver] != 2 {
		t.Fatalf("version-only film: %v", got)
	}
}

// The nightly sweep indexes versions, follows a renamed label, and prunes a removed one.
func TestIndexAllIndexesVersions(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	def := r.addMovie(t, 1, "Film", 1000)
	ver := r.addVersion(t, 1, 2, "4K", "Film - 4K.mkv", 3000, "x")
	r.cacheProbe(t, def, film("hevc", 1920, 1080, 6000))
	r.cacheProbe(t, ver, film("h264", 3840, 2160, 85000))

	r.IndexAll(ctx)
	if got := r.indexed(t, 1); len(got) != 2 || got[ver] != 2 {
		t.Fatalf("after the sweep: %v", got)
	}

	if _, err := r.db.Exec(`UPDATE movie_versions SET label = '4K Remux' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	r.IndexAll(ctx)
	var label string
	if err := r.db.QueryRow(`SELECT version_label FROM convert_library WHERE path = ?`, ver).Scan(&label); err != nil || label != "4K Remux" {
		t.Fatalf("label = %q (%v)", label, err)
	}

	if _, err := r.db.Exec(`DELETE FROM movie_versions WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	r.IndexAll(ctx)
	if got := r.indexed(t, 1); len(got) != 1 || got[def] != 0 {
		t.Fatalf("after deleting the version: %v", got)
	}
}

// A version job resolves to that version's file, never the default's; one whose version
// is gone is gone (its row is dropped, the default's kept).
func TestResolveSourceForVersion(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	def := r.addMovie(t, 1, "Film", 1000)
	ver := r.addVersion(t, 1, 2, "4K Remux", "Film - 4K Remux.mkv", 3000, "x")
	r.cacheProbe(t, def, film("hevc", 1920, 1080, 6000))
	r.cacheProbe(t, ver, film("h264", 3840, 2160, 85000))
	if err := r.IndexMovie(ctx, 1); err != nil {
		t.Fatal(err)
	}

	job := &Job{Kind: "movie", MovieID: 1, VersionID: 2}
	src, title, _, ok := r.resolveSource(ctx, job)
	if !ok || src != ver || title != "Film · 4K Remux" {
		t.Fatalf("resolveSource = %q %q %v", src, title, ok)
	}
	if rel := r.sourceRelease(ctx, job, ver); rel != "Film.2020.2160p.BluRay.REMUX.HEVC-GRP" {
		t.Fatalf("source release = %q", rel)
	}
	if src, _, _, ok := r.resolveSource(ctx, &Job{Kind: "movie", MovieID: 1}); !ok || src != def {
		t.Fatalf("default = %q %v", src, ok)
	}
	if _, _, _, ok := r.resolveSource(ctx, &Job{Kind: "movie", MovieID: 1, VersionID: 9}); ok {
		t.Fatal("an unknown version resolved")
	}

	// Asking by hand names the track.
	if err := r.Request(ctx, "movie:1:v:2"); err != nil {
		t.Fatal(err)
	}
	if q := r.requests.list(ctx); len(q) != 1 || q[0].Key != "movie:1:v:2" || q[0].Title != "Film · 4K Remux" {
		t.Fatalf("requests = %+v", q)
	}

	// The version is deleted before the job runs: it's dropped, and only its row goes.
	if _, err := r.db.Exec(`DELETE FROM movie_versions WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	it, _ := parseKey("movie:1:v:2")
	j := r.claim(it, "Film · 4K Remux", true)
	r.process(ctx, j)
	if j.State != StateSkipped {
		t.Fatalf("state %s (%s)", j.State, j.Note)
	}
	if got := r.indexed(t, 1); len(got) != 1 || got[def] != 0 {
		t.Fatalf("index after the version went: %v", got)
	}
	if b, err := os.ReadFile(def); err != nil || len(b) != 1000 {
		t.Fatalf("the default file was touched: %v", err)
	}
}

// captureOutbox records what the movies module queues.
type captureOutbox struct {
	mu      sync.Mutex
	changed []outbox.MovieChanged
}

func (c *captureOutbox) Enqueue(_ context.Context, _ store.Execer, topic string, payload any, _ string) error {
	if ch, ok := payload.(outbox.MovieChanged); ok && topic == outbox.TopicMovieChanged {
		c.mu.Lock()
		c.changed = append(c.changed, ch)
		c.mu.Unlock()
	}
	return nil
}

// Converting a version repoints that version's record, records its baseline, and says so
// with its own paths — the default file and record are untouched.
func TestVersionConversionTouchesOnlyItsTrack(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	def := r.addMovie(t, 1, "Film", 1000)
	if _, err := r.db.Exec(`UPDATE movies SET source_release = 'Film.2020.1080p.WEB.x265-GRP' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	src := r.addVersion(t, 1, 2, "4K Remux", "Film - 4K Remux.m2ts", 1000, "x")
	box := &captureOutbox{}
	r.movies.SetOutbox(box)

	mi := film("h264", 3840, 2160, 85000, aud("truehd", "eng", 8))
	mi.SizeBytes = 1000
	plan := Plan{VideoCodec: "hevc", Quality: 18}
	stubProbe(t, &MediaInfo{VideoCodec: "hevc", DurationSec: mi.DurationSec,
		AudioTracks: len(keptAudio(mi, plan)), SubTracks: len(keptSubs(mi, plan))})
	stubDisks(t, r.scratch, 1<<50, 1<<50)
	dst := filepath.Join(r.scratch, "convert-1.mkv")
	if err := os.WriteFile(dst, []byte(strings.Repeat("y", 500)), 0o644); err != nil {
		t.Fatal(err)
	}

	it, _ := parseKey("movie:1:v:2")
	job := r.claim(it, "Film · 4K Remux", true)
	rec := r.record(job)
	rec.srcPath, rec.srcInfo = src, mi
	rec.srcRelease = r.sourceRelease(ctx, job, src)
	r.update(job, func(j *Job) { j.SrcBytes = mi.SizeBytes })
	r.beginHistory(job)
	r.finalizeOutput(ctx, job, src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("state %s (%s)", job.State, job.Note)
	}
	final := filepath.Join(r.lib, "Film - 4K Remux.mkv")

	var vPath, vFrom string
	if err := r.db.QueryRow(`SELECT file_path, converted_from_release FROM movie_versions WHERE id = 2`).Scan(&vPath, &vFrom); err != nil {
		t.Fatal(err)
	}
	if vPath != final || vFrom != "Film.2020.2160p.BluRay.REMUX.HEVC-GRP" {
		t.Fatalf("version record: %q, baseline %q", vPath, vFrom)
	}
	var mPath, mRel, mFrom string
	if err := r.db.QueryRow(`SELECT movie_file_path, source_release, converted_from_release FROM movies WHERE id = 1`).Scan(&mPath, &mRel, &mFrom); err != nil {
		t.Fatal(err)
	}
	if mPath != def || mRel != "Film.2020.1080p.WEB.x265-GRP" || mFrom != "" {
		t.Fatalf("the default record changed: %q %q %q", mPath, mRel, mFrom)
	}
	if b, err := os.ReadFile(def); err != nil || string(b) != strings.Repeat("x", 1000) {
		t.Fatalf("the default file was touched: %v", err)
	}
	if b, err := os.ReadFile(final); err != nil || len(b) != 500 {
		t.Fatalf("the converted version isn't in place: %v", err)
	}

	box.mu.Lock()
	changed := append([]outbox.MovieChanged(nil), box.changed...)
	box.mu.Unlock()
	if len(changed) != 1 || changed[0].MovieID != 1 || changed[0].VersionID != 2 || changed[0].OldPath != src || changed[0].Path != final {
		t.Fatalf("movie.changed = %+v", changed)
	}

	var key string
	var vid int64
	if err := r.db.QueryRow(`SELECT item_key, version_id FROM convert_history WHERE outcome = ?`, OutcomeDone).Scan(&key, &vid); err != nil || key != "movie:1:v:2" || vid != 2 {
		t.Fatalf("ledger: %q %d (%v)", key, vid, err)
	}
	// The old .m2ts row is gone; the converted version is indexed as the version.
	got := r.indexed(t, 1)
	if _, stale := got[src]; stale || got[final] != 2 {
		t.Fatalf("index after the swap: %v", got)
	}
}

// A version saved as Film.mp4 next to the default's Film.mkv would be converted onto the
// default's file. It's never encoded, and nothing on disk or in the records changes.
func TestConversionNeverLandsOnAnotherVersionsFile(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	def := r.addMovie(t, 1, "Film", 1000)
	src := r.addVersion(t, 1, 2, "Theatrical", "Film.mp4", 800, "x")
	stubProbe(t, film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
	stubDisks(t, r.scratch, 1<<50, 1<<50)

	it, _ := parseKey("movie:1:v:2")
	job := r.claim(it, "Film · Theatrical", true)
	r.process(ctx, job)
	kind, _, _ := r.skipOf(t, "movie:1:v:2")
	if job.State != StateSkipped || kind != SkipTransient || !strings.Contains(job.Note, "another version") {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if strings.Contains(strings.Join(logMsgs(r.Service), "\n"), "Encoding") {
		t.Fatal("an encode started for a file whose result would replace another version")
	}
	for p, n := range map[string]int{def: 1000, src: 800} {
		if b, err := os.ReadFile(p); err != nil || len(b) != n {
			t.Fatalf("%s was touched: %v", filepath.Base(p), err)
		}
	}

	// The same guard holds at the swap itself, for a file that turned up mid-encode.
	r.skips.clear(ctx, "movie:1:v:2")
	mi := film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8))
	mi.SizeBytes = 800
	plan := Plan{VideoCodec: "hevc"}
	stubProbe(t, &MediaInfo{VideoCodec: "hevc", DurationSec: mi.DurationSec,
		AudioTracks: len(keptAudio(mi, plan)), SubTracks: len(keptSubs(mi, plan))})
	dst := filepath.Join(r.scratch, "convert-9.mkv")
	if err := os.WriteFile(dst, []byte(strings.Repeat("y", 300)), 0o644); err != nil {
		t.Fatal(err)
	}
	job = r.claim(it, "Film · Theatrical", true)
	r.finalizeOutput(ctx, job, src, dst, mi, plan)
	if job.State != StateSkipped || !strings.Contains(job.Note, "another version") {
		t.Fatalf("swap: state %s (%s)", job.State, job.Note)
	}
	for p, n := range map[string]int{def: 1000, src: 800} {
		if b, err := os.ReadFile(p); err != nil || len(b) != n {
			t.Fatalf("swap: %s was touched: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(def + ".arrpart"); !os.IsNotExist(err) {
		t.Fatalf("the staged copy was left behind: %v", err)
	}
	var vPath, mPath string
	_ = r.db.QueryRow(`SELECT file_path FROM movie_versions WHERE id = 2`).Scan(&vPath)
	_ = r.db.QueryRow(`SELECT movie_file_path FROM movies WHERE id = 1`).Scan(&mPath)
	if vPath != src || mPath != def {
		t.Fatalf("records changed: version %q, movie %q", vPath, mPath)
	}
}
