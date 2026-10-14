package automation

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// withUsenet adds a usenet client (the fake) to a stall harness.
func withUsenet(t *testing.T, h *stallHarness) (*fakeUsenet, int64) {
	t.Helper()
	sab := &fakeUsenet{}
	h.c.downloads.SetDownloader(download.KindSABnzbd, sab)
	cl, err := h.c.downloads.Create(h.ctx, download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	return sab, cl.ID
}

// addUsenetGrab records a movie grab of a usenet job made `ago`, with a one-hour stall window.
func (h *stallHarness) addUsenetGrab(t *testing.T, mid int64, release, key string, ago time.Duration) int64 {
	t.Helper()
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash, grabbed_at, transport, seed_enabled)
		VALUES (?, ?, 'NZBIdx', 60, 'movie', ?, ?, 'usenet', 0)`, mid, release, key, time.Now().Add(-ago).UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	return gid
}

func failedJob(key, name, msg string) download.Item {
	return download.Item{Hash: key, Name: name, State: "error", RawState: "Failed", FailMessage: msg, SizeBytes: 4 << 30, RemainingBytes: 4 << 30}
}

const notOnServer = "Download failed - Not on your server(s) - https://sabnzbd.org/not-complete"

// A job SABnzbd failed for the release's sake is blocklisted, removed and replaced on the
// stall check that sees it — minutes after the grab, whatever the stall window says.
func TestUsenetFailedIsBlocklistedAndReplaced(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
	const alt = "Rare.Film.1971.1080p.WEB-DL.x264-NEW"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	key := download.UsenetKey("SABnzbd_nzo_fail1")
	gid := h.addUsenetGrab(t, mid, rel, key, 5*time.Minute)
	sab.set(failedJob(key, rel, notOnServer))
	h.ix.offer(rel, alt)

	h.observe()

	if s := h.status(t, gid); s != grabStatusFailed {
		t.Fatalf("grab = %q, want failed", s)
	}
	var reason string
	if err := h.c.db.QueryRow(`SELECT reason FROM blocklist WHERE norm_title = ?`, normTitle(rel)).Scan(&reason); err != nil {
		t.Fatalf("not blocklisted: %v", err)
	}
	if reason != "usenet download failed: articles are missing from your provider" {
		t.Errorf("blocklist reason = %q", reason)
	}
	if rm := sab.removeLog(); len(rm) != 1 || rm[0].hash != key || !rm[0].deleteData {
		t.Errorf("usenet removals = %+v, want the failed job with its partial files", rm)
	}
	if calls := h.qbit.callLog(); len(calls) != 1 || calls[0] != "add "+magnetFor(alt) {
		t.Errorf("torrent client calls = %q, want the replacement", calls)
	}
	if h.movieEvents(t, mid, rel+" failed in SABnzbd (articles are missing from your provider) — blocklisted, replaced by "+alt) != 1 {
		t.Error("history should say why it failed and what replaced it")
	}
	evs := h.stallEvents()
	if len(evs) != 1 || evs[0]["usenet"] != true || evs[0]["replacement"] != alt {
		t.Errorf("download.stalled events = %+v", evs)
	}
	// Settled: the next check finds nothing to do.
	h.observe()
	if n := h.blocklistCount(t); n != 1 {
		t.Errorf("blocklist rows = %d after a second check", n)
	}
}

// With nothing else on offer the failed job still goes — it can't finish — and the grab
// is closed, so the next sweep searches again; the release stays blocklisted.
func TestUsenetFailedWithNothingElse(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	key := download.UsenetKey("SABnzbd_nzo_fail2")
	gid := h.addUsenetGrab(t, mid, rel, key, time.Minute)
	sab.set(failedJob(key, rel, "Unpacking failed, CRC error"))
	h.ix.offer(rel)

	h.observe()
	if s := h.status(t, gid); s != grabStatusFailed || h.blocklistCount(t) != 1 || len(sab.removeLog()) != 1 {
		t.Errorf("status %q, blocklist %d, removals %+v", s, h.blocklistCount(t), sab.removeLog())
	}
	if h.movieEvents(t, mid, "— blocklisted, nothing else found yet") != 1 {
		t.Error("history should say nothing else was found")
	}
}

// A full disk (or a folder SABnzbd can't write) is no fault of the release: nothing is
// blocklisted, removed or searched for, and the grab waits for a person and a Retry.
func TestUsenetDiskFullWaitsForAPerson(t *testing.T) {
	for _, msg := range []string{"Unpacking failed, write error or disk is full?", "[Errno 13] Permission denied: '/downloads/complete/x'"} {
		t.Run(msg, func(t *testing.T) {
			h := newStallHarness(t)
			sab, _ := withUsenet(t, h)
			const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
			mid := h.addMovie(t, 1, "Rare Film", 1971)
			key := download.UsenetKey("SABnzbd_nzo_disk")
			gid := h.addUsenetGrab(t, mid, rel, key, 8*time.Hour)
			sab.set(failedJob(key, rel, msg))
			h.ix.offer(rel, "Rare.Film.1971.1080p.WEB-DL.x264-NEW")
			log := h.captureLog()

			h.observe()
			h.tick()
			h.later(stallEvidenceGrace)
			h.tick()

			if s := h.status(t, gid); s != grabStatusGrabbed {
				t.Errorf("grab = %q, want still grabbed", s)
			}
			if n := h.blocklistCount(t); n != 0 {
				t.Errorf("blocklist rows = %d, want none", n)
			}
			if rm, calls, n := sab.removeLog(), h.qbit.callLog(), h.ix.searchCount(); len(rm) != 0 || len(calls) != 0 || n != 0 {
				t.Errorf("removals %+v, torrent calls %q, searches %d — want nothing done", rm, calls, n)
			}
			if got := strings.Count(log.String(), "SABnzbd couldn't save a download"); got != 1 {
				t.Errorf("logged the failure %d times, want once", got)
			}
		})
	}
}

// A reason in no wording Arrmada knows (a SABnzbd not set to English) is a bad release, and
// its raw text is logged.
func TestUsenetUnknownReasonIsABadRelease(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	key := download.UsenetKey("SABnzbd_nzo_nl")
	gid := h.addUsenetGrab(t, mid, rel, key, time.Minute)
	sab.set(failedJob(key, rel, "Download mislukt - Niet op uw server(s)"))
	log := h.captureLog()

	h.observe()
	if s := h.status(t, gid); s != grabStatusFailed || h.blocklistCount(t) != 1 {
		t.Errorf("status %q, blocklist %d", s, h.blocklistCount(t))
	}
	if !strings.Contains(log.String(), "Niet op uw server") || !strings.Contains(log.String(), "English") {
		t.Errorf("the raw reason and the language hint should be logged:\n%s", log.String())
	}
}

// A usenet job making no progress is never failed over — not after the window, not after
// many — and its row says fail-over is off; unpacking holds the stall clock.
func TestUsenetNoProgressNeverFailsOver(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	key := download.UsenetKey("SABnzbd_nzo_slow")
	gid := h.addUsenetGrab(t, mid, rel, key, 9*time.Hour)
	sab.set(download.Item{Hash: key, Name: rel, State: "downloading", RawState: "Downloading", Progress: 0.1, SizeBytes: 4 << 30, RemainingBytes: 3 << 30})
	h.ix.offer(rel, "Rare.Film.1971.1080p.WEB-DL.x264-NEW")

	for range 4 {
		h.observe()
		h.tick()
		h.later(time.Hour)
	}
	if s := h.status(t, gid); s != grabStatusGrabbed || h.blocklistCount(t) != 0 || len(sab.removeLog()) != 0 || h.ix.searchCount() != 0 {
		t.Fatalf("status %q, blocklist %d, removals %+v, searches %d — want nothing done",
			s, h.blocklistCount(t), sab.removeLog(), h.ix.searchCount())
	}
	if st, ok := h.c.StallInfo(h.ctx)[key]; !ok || !st.Off || st.FailoverInMinutes != 0 {
		t.Errorf("stall info = %+v (%v), want fail-over off", st, ok)
	}

	// Unpacking: the clock is held (restarted without counting as progress).
	old := time.Now().Add(-5 * time.Hour).UnixMilli()
	if _, err := h.c.db.Exec(`UPDATE grabs SET progress_at = ? WHERE id = ?`, old, gid); err != nil {
		t.Fatal(err)
	}
	sab.set(download.Item{Hash: key, Name: rel, State: download.PhaseUnpacking, RawState: "Extracting", Progress: 1, SizeBytes: 4 << 30})
	h.observe()
	var at int64
	if err := h.c.db.QueryRow(`SELECT progress_at FROM grabs WHERE id = ?`, gid).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at <= old {
		t.Error("unpacking didn't hold the stall clock")
	}
	if s := h.status(t, gid); s != grabStatusGrabbed {
		t.Errorf("grab = %q while unpacking", s)
	}
}

// The idle clock: jobs waiting and nothing moving for 30 minutes is a health warning; a
// job that moves, or nothing left waiting, clears it. Paused and propagating jobs aren't
// waiting to download.
func TestUsenetIdleClock(t *testing.T) {
	h := newStallHarness(t)
	c := h.c
	u := download.TransportUsenet
	job := func(key string, got int64, raw string, speed int64) download.Item {
		state := "downloading"
		if raw == "Paused" {
			state = "paused"
		}
		return download.Item{Hash: key, State: state, RawState: raw, Transport: u, ClientID: 7, DownloadedBytes: got, DownSpeed: speed}
	}
	c.noteUsenetQueue([]download.Item{job("sab-a", 100, "Downloading", 0), job("sab-b", 0, "Queued", 0)})
	h.later(29 * time.Minute)
	c.noteUsenetQueue([]download.Item{job("sab-a", 100, "Downloading", 0), job("sab-b", 0, "Queued", 0)})
	if idle := c.UsenetIdle(); len(idle) != 0 {
		t.Fatalf("idle after 29m = %+v", idle)
	}
	h.later(2 * time.Minute)
	c.noteUsenetQueue([]download.Item{job("sab-a", 100, "Downloading", 0), job("sab-b", 0, "Queued", 0)})
	if idle := c.UsenetIdle(); len(idle) != 1 || idle[0].ClientID != 7 || idle[0].Jobs != 2 {
		t.Fatalf("idle after 31m = %+v, want client 7 with 2 jobs", idle)
	}
	// Bytes arrive: not idle any more, and the clock starts over.
	c.noteUsenetQueue([]download.Item{job("sab-a", 5000, "Downloading", 0), job("sab-b", 0, "Queued", 0)})
	if idle := c.UsenetIdle(); len(idle) != 0 {
		t.Errorf("idle after progress = %+v", idle)
	}
	h.later(40 * time.Minute)
	c.noteUsenetQueue([]download.Item{job("sab-a", 5000, "Downloading", 1024)})
	if idle := c.UsenetIdle(); len(idle) != 0 {
		t.Errorf("idle while it has speed = %+v", idle)
	}
	// Only paused, propagating, unpacking or torrent downloads: nothing is waiting.
	c.noteUsenetQueue([]download.Item{job("sab-a", 5000, "Paused", 0), job("sab-c", 0, "Propagating", 0),
		{Hash: "sab-d", State: download.PhaseUnpacking, Transport: u, ClientID: 7}, {Hash: "abc", State: "downloading", RawState: "stalledDL", ClientID: 1}})
	h.later(time.Hour)
	c.noteUsenetQueue([]download.Item{job("sab-a", 5000, "Paused", 0)})
	if idle := c.UsenetIdle(); len(idle) != 0 {
		t.Errorf("idle with nothing waiting = %+v", idle)
	}
}

// A job that left SABnzbd's history before it was imported (archived by hand) is a vanished
// download: once it has stayed gone for the grace, the grab fails over without a blocklist
// entry and vanished_at is stamped.
func TestUsenetVanishedFromHistory(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
	const alt = "Rare.Film.1971.1080p.WEB-DL.x264-NEW"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	key := download.UsenetKey("SABnzbd_nzo_gone")
	gid := h.addUsenetGrab(t, mid, rel, key, 7*time.Hour)
	sab.set() // archived elsewhere: no longer listed
	h.ix.offer(rel, alt)

	h.observe()
	if s := h.status(t, gid); s != grabStatusGrabbed {
		t.Fatalf("acted on the first read: %q", s)
	}
	h.later(stallEvidenceGrace)
	h.observe()

	if s := h.status(t, gid); s != grabStatusFailed {
		t.Errorf("grab = %q, want failed", s)
	}
	var vanished int64
	if err := h.c.db.QueryRow(`SELECT vanished_at FROM grabs WHERE id = ?`, gid).Scan(&vanished); err != nil || vanished == 0 {
		t.Errorf("vanished_at = %d (%v), want stamped", vanished, err)
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist rows = %d, want none on a first disappearance", n)
	}
	if calls := h.qbit.callLog(); len(calls) != 1 || calls[0] != "add "+magnetFor(alt) {
		t.Errorf("torrent client calls = %q, want the replacement", calls)
	}
}

// Seed clean-up never waits on a usenet job: imported, it is archived with its folder
// deleted; a folder outside where Arrmada may delete is left (the job archived anyway),
// and once that happens twice in a row the health check hears of it. A tidy clean-up
// clears that.
func TestUsenetCleanupAfterImport(t *testing.T) {
	h := newStallHarness(t)
	sab, clientID := withUsenet(t, h)
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	done := func(key, name string) download.Item {
		return download.Item{Hash: key, Name: name, State: download.StateCompleted, RawState: "Completed", Progress: 1,
			SizeBytes: 1 << 30, DownloadedBytes: 1 << 30, ContentPath: "/downloads/complete/" + name}
	}
	grab := func(key, name, status string, seed int) int64 {
		res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, status, transport, seed_enabled, seed_ratio)
			VALUES (?, ?, 'NZBIdx', 'movie', ?, ?, 'usenet', ?, 5)`, mid, name, key, status, seed)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	run := func() {
		h.c.downloads.InvalidateSnapshot()
		h.c.ManageSeeding(h.ctx)
	}

	// Imported, and a row that (wrongly) says it seeds: archived at once, folder and all.
	k1 := download.UsenetKey("SABnzbd_nzo_one")
	g1 := grab(k1, "Film.One.2020", grabStatusImported, 1)
	// Dismissed in Review: archived, its files kept.
	k2 := download.UsenetKey("SABnzbd_nzo_two")
	g2 := grab(k2, "Film.Two.2020", grabStatusDismissed, 0)
	sab.set(done(k1, "Film.One.2020"), done(k2, "Film.Two.2020"))
	run()
	rm := sab.removeLog()
	if len(rm) != 2 || rm[0] != (removeCall{k1, true}) || rm[1] != (removeCall{k2, false}) {
		t.Fatalf("removals = %+v, want %s with its folder and %s without", rm, k1, k2)
	}
	if h.status(t, g1) != grabStatusSeeded || h.status(t, g2) != grabStatusSeeded {
		t.Errorf("grab statuses = %q, %q, want both out of the client", h.status(t, g1), h.status(t, g2))
	}
	if h.movieEvents(t, mid, "usenet download cleared from SABnzbd after import, its folder deleted") != 1 {
		t.Error("history should say the job was cleared and its folder deleted")
	}

	// A folder outside the guarded area: left in place, the job archived without it.
	sab.removeErr = func(_ string, deleteData bool) error {
		if deleteData {
			return fmt.Errorf("left /elsewhere/x in place: %w", download.ErrLeftoverOutside)
		}
		return nil
	}
	k3 := download.UsenetKey("SABnzbd_nzo_three")
	g3 := grab(k3, "Film.Three.2020", grabStatusImported, 0)
	sab.set(done(k3, "Film.Three.2020"))
	run()
	if rm := sab.removeLog()[2:]; len(rm) != 2 || rm[0] != (removeCall{k3, true}) || rm[1] != (removeCall{k3, false}) {
		t.Fatalf("removals = %+v, want the folder refused, then the job archived without it", rm)
	}
	if s := h.status(t, g3); s != grabStatusSeeded {
		t.Errorf("grab = %q, want out of the client", s)
	}
	if got := h.c.UsenetLeftovers(); len(got) != 0 {
		t.Errorf("one leftover is reported already: %+v", got)
	}
	// It repeats (here: archived, but the folder couldn't be deleted): now it's reported.
	sab.removeErr = func(string, bool) error {
		return fmt.Errorf("SABnzbd archived the job, but %w (/downloads/x): permission denied", download.ErrLeftoverNotDeleted)
	}
	k4 := download.UsenetKey("SABnzbd_nzo_four")
	g4 := grab(k4, "Film.Four.2020", grabStatusImported, 0)
	sab.set(done(k4, "Film.Four.2020"))
	run()
	if s := h.status(t, g4); s != grabStatusSeeded {
		t.Errorf("grab = %q, want out of the client", s)
	}
	got := h.c.UsenetLeftovers()
	if len(got) != 1 || got[0].ClientID != clientID || got[0].Count != 2 || got[0].Example.Name != "Film.Four.2020" ||
		!strings.Contains(got[0].Example.Detail, "permission denied") {
		t.Fatalf("leftovers = %+v, want two for the usenet client", got)
	}
	// A clean-up that tidies up again clears it.
	sab.removeErr = nil
	k5 := download.UsenetKey("SABnzbd_nzo_five")
	grab(k5, "Film.Five.2020", grabStatusImported, 0)
	sab.set(done(k5, "Film.Five.2020"))
	run()
	if got := h.c.UsenetLeftovers(); len(got) != 0 {
		t.Errorf("leftovers after a clean removal = %+v", got)
	}
}

// Seed clean-up leaves alone a usenet job still downloading or unpacking, and clears a
// failed one whose title landed another way.
func TestUsenetCleanupWaitsForTheFinish(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	for i, st := range []string{"Downloading", "Extracting", "Failed"} {
		key := download.UsenetKey(fmt.Sprintf("SABnzbd_nzo_%d", i))
		if _, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, status, transport, seed_enabled)
			VALUES (?, ?, 'NZBIdx', 'movie', ?, 'imported', 'usenet', 0)`, mid, "Film."+st, key); err != nil {
			t.Fatal(err)
		}
	}
	sab.set(
		download.Item{Hash: download.UsenetKey("SABnzbd_nzo_0"), Name: "Film.Downloading", State: "downloading", RawState: "Downloading", Progress: 0.5},
		download.Item{Hash: download.UsenetKey("SABnzbd_nzo_1"), Name: "Film.Extracting", State: download.PhaseUnpacking, RawState: "Extracting", Progress: 1},
		failedJob(download.UsenetKey("SABnzbd_nzo_2"), "Film.Failed", notOnServer),
	)
	h.c.downloads.InvalidateSnapshot()
	h.c.ManageSeeding(h.ctx)
	if rm := sab.removeLog(); len(rm) != 1 || rm[0] != (removeCall{download.UsenetKey("SABnzbd_nzo_2"), true}) {
		t.Errorf("removals = %+v, want only the failed job (with its partial files)", rm)
	}
}

// A key-less usenet grab (the add answer was lost) is paired with the job named exactly
// after it, and only that: two candidates is ambiguous, a torrent grab is never one, and a
// torrent is never paired with a usenet grab.
func TestUsenetKeyAdoption(t *testing.T) {
	h := newStallHarness(t)
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	add := func(title, transport string) int64 {
		res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, media_type, info_hash, transport) VALUES (?, ?, 'NZBIdx', 'movie', '', ?)`, mid, title, transport)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	hashOf := func(id int64) string {
		var hsh string
		if err := h.c.db.QueryRow(`SELECT info_hash FROM grabs WHERE id = ?`, id).Scan(&hsh); err != nil {
			t.Fatal(err)
		}
		return hsh
	}
	one := add("Rare.Film.1971.1080p.BluRay.x264-NZB", "usenet")
	twinA := add("Rare.Film.1971.2160p.UHD.x265-TWIN", "usenet")
	twinB := add("Rare Film 1971 2160p UHD x265-TWIN", "usenet")
	tor := add("Rare.Film.1971.720p.WEB-DL-TOR", "torrent")
	u := download.TransportUsenet
	k1, k2, k3 := download.UsenetKey("SABnzbd_nzo_a"), download.UsenetKey("SABnzbd_nzo_b"), download.UsenetKey("SABnzbd_nzo_c")
	queue := []download.Item{
		{Hash: k1, Name: "Rare.Film.1971.1080p.BluRay.x264-NZB", Transport: u},
		{Hash: k2, Name: "Rare.Film.1971.2160p.UHD.x265-TWIN", Transport: u},
		{Hash: k3, Name: "Rare.Film.1971.720p.WEB-DL-TOR", Transport: u},
		{Hash: hashFor("x"), Name: "Rare.Film.1971.1080p.BluRay.x264-NZB", Transport: download.TransportTorrent},
	}
	if n := h.c.AdoptTorrentHashes(h.ctx, queue); n != 1 {
		t.Errorf("adopted %d, want 1", n)
	}
	if got := hashOf(one); got != k1 {
		t.Errorf("the usenet grab got %q, want %q", got, k1)
	}
	for _, id := range []int64{twinA, twinB, tor} {
		if got := hashOf(id); got != "" {
			t.Errorf("grab %d got %q, want none (ambiguous, or not a usenet grab)", id, got)
		}
	}
}

// A failed job retried (after a full disk was fixed) comes back under a new id: the grab
// follows it when the move is unambiguous, and two grabs of one name are left alone.
func TestRetriedUsenetJobIsFollowed(t *testing.T) {
	h := newStallHarness(t)
	sab, _ := withUsenet(t, h)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-NZB"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	old, retried := download.UsenetKey("SABnzbd_nzo_old"), download.UsenetKey("SABnzbd_nzo_new")
	gid := h.addUsenetGrab(t, mid, rel, old, time.Hour)
	// Another grab, twice over, of a second release: ambiguous, left alone.
	const twin = "Rare.Film.1971.2160p.UHD-TWIN"
	h.addUsenetGrab(t, mid, twin, download.UsenetKey("SABnzbd_nzo_t1"), time.Hour)
	h.addUsenetGrab(t, mid, twin, download.UsenetKey("SABnzbd_nzo_t2"), time.Hour)
	sab.set(
		download.Item{Hash: retried, Name: rel, State: "downloading", RawState: "Queued", SizeBytes: 100, RemainingBytes: 100},
		download.Item{Hash: download.UsenetKey("SABnzbd_nzo_t3"), Name: twin, State: "downloading", RawState: "Queued", SizeBytes: 100, RemainingBytes: 100},
	)
	h.c.downloads.InvalidateSnapshot()
	if _, err := h.c.ReconcileAcquisitions(h.ctx); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := h.c.db.QueryRow(`SELECT info_hash FROM grabs WHERE id = ?`, gid).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != retried {
		t.Errorf("grab key = %q, want the retried job's %q", key, retried)
	}
	var twins int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM grabs WHERE title = ? AND info_hash IN (?, ?)`, twin,
		download.UsenetKey("SABnzbd_nzo_t1"), download.UsenetKey("SABnzbd_nzo_t2")).Scan(&twins); err != nil || twins != 2 {
		t.Errorf("ambiguous grabs re-keyed: %d left (%v)", twins, err)
	}
	// Followed, it's in flight again: the stall check leaves it be.
	h.later(stallEvidenceGrace)
	h.observe()
	if s := h.status(t, gid); s != grabStatusGrabbed {
		t.Errorf("grab = %q after the retry, want still grabbed", s)
	}
}

// An uploaded .nzb whose add answer carried no key is still recorded as usenet — what was
// uploaded says what it is — and its history line doesn't call it a torrent.
func TestKeylessNZBUploadIsUsenet(t *testing.T) {
	h := newUsenetHarness(t)
	h.sab.keyless = true
	title := "Show.S01E02.1080p.WEB-DL-GRP"
	if err := h.c.GrabSeriesTorrent(h.ctx, h.seriesID, []byte(testNZB), "upload.nzb", title); err != nil {
		t.Fatal(err)
	}
	g := grabRowOf(t, h.c, title)
	if g.transport != "usenet" || g.seed || g.hash != "" {
		t.Errorf("key-less NZB grab = %+v, want usenet, no seeding, no key", g)
	}
	var n int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM series_events WHERE detail = ?`, "Uploaded NZB — "+title).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("history lines for the upload = %d, want one saying NZB", n)
	}
}
