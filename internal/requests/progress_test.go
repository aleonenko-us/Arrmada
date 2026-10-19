package requests

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Each stage a request can be in, worked out from its grabs and the download queue.
func TestTrackStages(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	db := s.repo.db
	grab := func(mediaType string, id int64, hash, title string, at time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO grabs (movie_id, title, status, media_type, info_hash, grabbed_at) VALUES (?, ?, 'grabbed', ?, ?, ?)`,
			id, title, mediaType, hash, at.UTC().Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-3 * time.Hour)
	grab("movie", 1, "AAA", "Heat.1995.2160p", old)           // downloading
	grab("movie", 2, "BBB", "Alien.1979.1080p", old)          // finished → importing
	grab("movie", 3, "CCC", "Arrival.2016.1080p.WEB-DL", old) // paused
	grab("movie", 4, "DDD", "Gone.2020", old)                 // no longer in the client
	grab("movie", 5, "EEE", "Fresh.2024", time.Now())         // just grabbed, not in the client yet
	grab("movie", 6, "FFF", "Broken.2021", old)               // errored
	grab("series", 7, "GGG", "Show.S02.1080p", old)           // season pack downloading
	queue := []download.Item{
		{Hash: "aaa", Name: "Heat 1995 2160p", State: "downloading", Progress: 0.4, SizeBytes: 1000, DownloadedBytes: 400, DownSpeed: 50, ETASeconds: 12},
		{Hash: "bbb", Name: "Alien", State: "seeding", Progress: 1, SizeBytes: 500, DownloadedBytes: 500},
		{Hash: "ccc", Name: "Arrival.2016.1080p.WEB-DL", State: "paused", Progress: 0.5, SizeBytes: 200, DownloadedBytes: 100},
		{Hash: "fff", Name: "Broken", State: "error", Progress: 0.1, SizeBytes: 100},
		{Hash: "ggg", Name: "Show S02", State: "downloading", Progress: 0.25, SizeBytes: 800, DownloadedBytes: 200, ETASeconds: 8640000},
	}
	reqs := []Request{
		{Title: "pending", Status: StatusPending},
		{Title: "declined", Status: StatusDeclined},
		{Title: "heat", Status: StatusApproved, MediaType: "movie", libID: 1, released: true},
		{Title: "alien", Status: StatusApproved, MediaType: "movie", libID: 2, released: true},
		{Title: "arrival", Status: StatusApproved, MediaType: "movie", libID: 3, released: true},
		{Title: "gone", Status: StatusApproved, MediaType: "movie", libID: 4, released: true},
		{Title: "fresh", Status: StatusApproved, MediaType: "movie", libID: 5, released: true},
		{Title: "broken", Status: StatusApproved, MediaType: "movie", libID: 6, released: true},
		{Title: "show", Status: StatusApproved, MediaType: "series", libID: 7, released: true, Available: true, epHave: 8, epTotal: 18},
		{Title: "unreleased", Status: StatusApproved, MediaType: "movie", libID: 9},
		{Title: "ready", Status: StatusApproved, MediaType: "movie", libID: 1, Available: true, released: true},
		{Title: "some episodes", Status: StatusApproved, MediaType: "series", libID: 10, Available: true, epHave: 3, epTotal: 10, released: true},
		{Title: "whole show", Status: StatusApproved, MediaType: "series", libID: 11, Available: true, epHave: 10, epTotal: 10, released: true},
		{Title: "pending but there", Status: StatusPending, MediaType: "movie", libID: 12, Available: true},
	}
	s.Track(ctx, reqs, queue, true)
	want := map[string]string{
		"pending": StagePending, "declined": StageDeclined, "heat": StageDownloading, "alien": StageImporting,
		"arrival": StagePaused, "gone": StageSearching, "fresh": StageQueued, "broken": StageFailed,
		"show": StageDownloading, "unreleased": StageSearching, "ready": StageAvailable,
		"some episodes": StagePartial, "whole show": StageAvailable, "pending but there": StageAvailable,
	}
	for _, rq := range reqs {
		if rq.Tracking == nil || rq.Tracking.Stage != want[rq.Title] {
			t.Errorf("%s: stage = %+v, want %s", rq.Title, rq.Tracking, want[rq.Title])
		}
	}
	heat := reqs[2].Tracking
	if heat.Progress != 0.4 || heat.ETASeconds != 12 || heat.SpeedBps != 50 || reqs[2].DownloadProgress != 0.4 {
		t.Errorf("heat tracking = %+v", heat)
	}
	if show := reqs[8].Tracking; show.Have != 8 || show.Total != 18 || show.ETASeconds != 0 || show.Note != "Waiting for peers" {
		t.Errorf("show tracking = %+v (qBittorrent's infinite ETA must not show; no speed means waiting)", show)
	}
	if reqs[9].Tracking.Note != "Not out yet" {
		t.Errorf("unreleased note = %q", reqs[9].Tracking.Note)
	}
}

// A download held in Review still reads as "Importing" — it is here, waiting on a person —
// but once the review is resolved, whichever way, the grab is closed out and the requester
// stops seeing "Importing" for it.
func TestTrackStopsImportingOnceTheReviewIsResolved(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	db := s.repo.db
	at := time.Now().Add(-3 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`INSERT INTO grabs (movie_id, title, status, media_type, info_hash, grabbed_at) VALUES (2, 'Alien.1979.1080p', 'held', 'movie', 'BBB', ?)`, at); err != nil {
		t.Fatal(err)
	}
	queue := []download.Item{{Hash: "bbb", Name: "Alien", State: "seeding", Progress: 1, SizeBytes: 500, DownloadedBytes: 500}}
	track := func() *Tracking {
		reqs := []Request{{Title: "alien", Status: StatusApproved, MediaType: "movie", libID: 2, released: true}}
		s.Track(ctx, reqs, queue, true)
		return reqs[0].Tracking
	}
	stage := func() string { return track().Stage }
	// The same word the staff queue uses, with a note instead of the review's reason.
	if got := track(); got.Stage != StageImporting || got.Note != "Being checked" {
		t.Fatalf("held: %+v, want importing, being checked", got)
	}
	for _, closed := range []string{"failed", "dismissed", "imported"} {
		if _, err := db.Exec(`UPDATE grabs SET status = ?`, closed); err != nil {
			t.Fatal(err)
		}
		if got := stage(); got == StageImporting {
			t.Errorf("grab %s: requester still sees importing", closed)
		}
	}
}

// An import that failed and will be retried is "importing, being checked" to the
// requester — the error itself is staff detail. A finished download that's simply
// waiting for the import has no note.
func TestTrackRetryingImportIsBeingChecked(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now().Add(-3 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	for _, g := range [][2]any{{1, "AAA"}, {2, "BBB"}} {
		if _, err := s.repo.db.Exec(`INSERT INTO grabs (movie_id, title, status, media_type, info_hash, grabbed_at) VALUES (?, 'x', 'grabbed', 'movie', ?, ?)`, g[0], g[1], at); err != nil {
			t.Fatal(err)
		}
	}
	s.SetImportFailures(func() []library.FailureInfo {
		return []library.FailureInfo{{Hash: "bbb", Attempts: 3, LastErr: "permission denied"}}
	})
	queue := []download.Item{
		{Hash: "aaa", State: "seeding", Progress: 1, SizeBytes: 500, DownloadedBytes: 500},
		{Hash: "bbb", State: "seeding", Progress: 1, SizeBytes: 500, DownloadedBytes: 500},
	}
	reqs := []Request{
		{Title: "waiting", Status: StatusApproved, MediaType: "movie", libID: 1, released: true},
		{Title: "retrying", Status: StatusApproved, MediaType: "movie", libID: 2, released: true},
	}
	s.Track(ctx, reqs, queue, true)
	if got := reqs[0].Tracking; got.Stage != StageImporting || got.Note != "" {
		t.Errorf("waiting for the import: %+v", got)
	}
	if got := reqs[1].Tracking; got.Stage != StageImporting || got.Note != "Being checked" {
		t.Errorf("retrying import: %+v, want importing, being checked", got)
	}
}

// A usenet download reads to a requester in the same plain words: waiting its turn (it has
// no peers to wait for), importing while SABnzbd unpacks and once it's finished, failed
// when SABnzbd gave up.
func TestTrackUsenetStages(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now().Add(-3 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	keys := []string{"sab-wait", "sab-unpack", "sab-done", "sab-failed"}
	for i, k := range keys {
		if _, err := s.repo.db.Exec(`INSERT INTO grabs (movie_id, title, status, media_type, info_hash, grabbed_at, transport) VALUES (?, 'x', 'grabbed', 'movie', ?, ?, 'usenet')`, i+1, k, at); err != nil {
			t.Fatal(err)
		}
	}
	u := download.TransportUsenet
	queue := []download.Item{
		{Hash: "sab-wait", State: "downloading", RawState: "Queued", Transport: u, SizeBytes: 900},
		{Hash: "sab-unpack", State: "unpacking", RawState: "Extracting", Transport: u, Progress: 1, SizeBytes: 900, DownloadedBytes: 900},
		{Hash: "sab-done", State: "completed", RawState: "Completed", Transport: u, Progress: 1, SizeBytes: 900, DownloadedBytes: 900},
		{Hash: "sab-failed", State: "error", RawState: "Failed", Transport: u, SizeBytes: 900, FailMessage: "Download failed - Not on your server(s)"},
	}
	var reqs []Request
	for i := range keys {
		reqs = append(reqs, Request{Title: keys[i], Status: StatusApproved, MediaType: "movie", libID: int64(i + 1), released: true})
	}
	s.Track(ctx, reqs, queue, true)
	want := []struct{ stage, note string }{
		{StageDownloading, "Waiting its turn"},
		{StageImporting, ""},
		{StageImporting, ""},
		{StageFailed, "The download failed — it will be retried"},
	}
	for i, w := range want {
		if got := reqs[i].Tracking; got.Stage != w.stage || got.Note != w.note {
			t.Errorf("%s: %+v, want %s %q", keys[i], got, w.stage, w.note)
		}
	}
}

// A requester's card says Stalled when every download for it is stalled by the rule the
// staff queue and the stall fail-over share (automation.QueueStall): the grab's stall clock
// or the client's own stalled state, held for the grace. A moment without data is still
// "Waiting for peers", and one download moving keeps the request moving.
func TestTrackStalled(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	at := time.Now().Add(-3 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	clock := time.Now().Add(-2 * time.Hour).UnixMilli()
	for _, g := range []struct {
		id   int64
		hash string
	}{{1, "dead"}, {2, "blip"}, {3, "pack-dead"}, {3, "pack-live"}} {
		if _, err := s.repo.db.Exec(`INSERT INTO grabs (movie_id, title, status, media_type, info_hash, grabbed_at, progress, progress_at)
			VALUES (?, 'x', 'grabbed', 'movie', ?, ?, 0.3, ?)`, g.id, g.hash, at, clock); err != nil {
			t.Fatal(err)
		}
	}
	item := func(hash, raw string, speed int64) download.Item {
		return download.Item{Hash: hash, State: "downloading", RawState: raw, Progress: 0.3, SizeBytes: 1000, DownloadedBytes: 300,
			RemainingBytes: 700, DownSpeed: speed, ETASeconds: 8640000, LastActivity: time.Now().Unix()}
	}
	queue := []download.Item{
		item("dead", "stalledDL", 0),
		item("pack-dead", "stalledDL", 0),
		item("pack-live", "downloading", 900),
	}
	// "blip" has no grab clock past the grace: its clock is the stalledDL moment only.
	if _, err := s.repo.db.Exec(`UPDATE grabs SET progress_at = ? WHERE info_hash = 'blip'`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	queue = append(queue, item("blip", "stalledDL", 0))
	reqs := []Request{
		{Title: "dead", Status: StatusApproved, MediaType: "movie", libID: 1, released: true},
		{Title: "blip", Status: StatusApproved, MediaType: "movie", libID: 2, released: true},
		{Title: "pack", Status: StatusApproved, MediaType: "movie", libID: 3, released: true},
	}
	s.Track(ctx, reqs, queue, true)
	want := []struct {
		stalled bool
		note    string
	}{
		{true, "Nobody is sharing it right now"},
		{false, "Waiting for peers"},
		{false, ""},
	}
	for i, w := range want {
		got := reqs[i].Tracking
		if got.Stage != StageDownloading || got.Stalled != w.stalled || got.Note != w.note {
			t.Errorf("%s: %+v, want downloading, stalled %v, note %q", reqs[i].Title, got, w.stalled, w.note)
		}
	}
}
