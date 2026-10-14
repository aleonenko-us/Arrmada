package automation

import (
	"strings"
	"testing"
	"time"
)

// A grab made by a build without stall fail-over (stall_minutes 0, weeks old) meets a
// qBittorrent that is still loading its session after the update: the list comes back
// empty, then with the torrent, then without it again. Nothing may be blocklisted or
// replaced until the torrent has been gone for the full grace in one stretch.
func TestUpgradedGrabNotFailedOverOnAFlakyFirstRead(t *testing.T) {
	h := newStallHarness(t)
	const rel = "Old.Film.2019.1080p.WEB-DL.x265-GRP"
	const alt = "Old.Film.2019.1080p.BluRay.x264-NEW"
	mid := h.addMovie(t, 1, "Old Film", 2019)
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash, grabbed_at)
		VALUES (?, ?, 'Fake', 0, 'movie', ?, datetime('now', '-40 days'))`, mid, rel, hashFor(rel))
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	h.ix.offer(rel, alt)
	untouched := func(when string) {
		t.Helper()
		if s := h.status(t, gid); s != "grabbed" || h.blocklistCount(t) != 0 || len(h.qbit.callLog()) != 0 {
			t.Fatalf("%s: status %q, blocklist %d, client calls %q — want nothing done", when,
				s, h.blocklistCount(t), h.qbit.callLog())
		}
	}

	h.observe() // the first tick after the update: the client lists nothing yet
	untouched("first check")
	h.later(20 * time.Minute)
	h.observe()
	untouched("twenty minutes in")

	h.qbit.stalledTorrent(hashFor(rel), rel, "arrmada") // the session finished loading
	h.later(15 * time.Minute)
	h.observe()
	untouched("torrent back")

	h.qbit.mu.Lock()
	h.qbit.torrents = nil // and gone again: the grace starts over
	h.qbit.mu.Unlock()
	h.later(5 * time.Minute)
	h.observe()
	h.later(stallEvidenceGrace - time.Minute)
	h.observe()
	untouched("gone again, not yet for the full grace")

	h.later(time.Minute)
	h.observe()
	if s := h.status(t, gid); s != "failed" {
		t.Fatalf("gone for the full grace: grab status = %q, want failed", s)
	}
	// Gone is failed over without a blocklist row the first time (vanished.go).
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist rows = %d, want none", n)
	}
	if calls := h.qbit.callLog(); len(calls) != 1 || calls[0] != "add "+magnetFor(alt) {
		t.Errorf("client calls = %q, want just the alternate added", calls)
	}
}

// A torrent in a hard error (a disk not mounted yet reads as missingFiles) is not replaced
// on the first read either, and a restart starts the watch over; one that stays errored
// for the grace is replaced as before, the replacement added before it is removed.
func TestErroredTorrentWaitsOutTheGrace(t *testing.T) {
	h := newStallHarness(t)
	const rel = "Rare.Film.1971.1080p.BluRay.x264-OLD"
	const alt = "Rare.Film.1971.1080p.WEB-DL.x264-NEW"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	gid := h.addStalledGrab(t, "movie", mid, rel, "arrmada")
	h.qbit.mu.Lock()
	h.qbit.torrents[0]["state"] = "missingFiles"
	h.qbit.torrents[0]["progress"] = 0.4
	h.qbit.mu.Unlock()
	h.ix.offer(rel, alt)

	h.tick()
	if s := h.status(t, gid); s != "grabbed" || len(h.qbit.callLog()) != 0 {
		t.Fatalf("first read of an errored torrent: status %q, client calls %q — want nothing done", s, h.qbit.callLog())
	}

	// A restart forgets what was seen: the grace is watched again in full.
	h.c.stallSeenMu.Lock()
	h.c.stallSeen = nil
	h.c.stallSeenMu.Unlock()
	h.later(stallEvidenceGrace)
	h.tick()
	if s := h.status(t, gid); s != "grabbed" || len(h.qbit.callLog()) != 0 {
		t.Fatalf("first read after a restart: status %q, client calls %q — want nothing done", s, h.qbit.callLog())
	}

	h.later(stallEvidenceGrace)
	h.tick()
	want := []string{"add " + magnetFor(alt), "delete " + hashFor(rel)}
	if calls := h.qbit.callLog(); strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("errored for the full grace: client calls = %q, want %q", calls, want)
	}
	if s := h.status(t, gid); s != "failed" {
		t.Errorf("grab status = %q, want failed", s)
	}
}

// A sighting is kept only while its grab is pending, and a different kind of evidence
// starts its own clock.
func TestStallSightings(t *testing.T) {
	h := newStallHarness(t)
	c := h.c
	if c.stallEvidenceHeld(1, evidenceGone) {
		t.Fatal("first sighting held")
	}
	h.later(stallEvidenceGrace)
	if c.stallEvidenceHeld(1, evidenceError) {
		t.Fatal("a new kind of evidence must start its own clock")
	}
	h.later(stallEvidenceGrace)
	if !c.stallEvidenceHeld(1, evidenceError) {
		t.Fatal("error seen for the full grace should hold")
	}
	if c.stallEvidenceHeld(1, "") {
		t.Fatal("no evidence never holds")
	}
	if c.stallEvidenceHeld(1, evidenceError) {
		t.Fatal("cleared evidence must start over")
	}
	c.stallEvidenceHeld(2, evidenceGone)
	c.pruneStallSightings([]grab{{ID: 2}})
	c.stallSeenMu.Lock()
	_, one := c.stallSeen[1]
	_, two := c.stallSeen[2]
	c.stallSeenMu.Unlock()
	if one || !two {
		t.Errorf("after pruning: grab 1 kept %v (want false), grab 2 kept %v (want true)", one, two)
	}
}
