package automation

import (
	"errors"
	"strings"
	"testing"
)

// Needs you's "Try another release" runs the stall fail-over now, long before the grab's
// window is up, with the same replace-first rule: the alternate is added before the stuck
// torrent is removed and blocklisted, and the history says it was asked for.
func TestFailOverNowReplacesBeforeRemoving(t *testing.T) {
	h := newStallHarness(t)
	const stuck = "Quiet.Film.1983.1080p.BluRay.x264-OLD"
	const alt = "Quiet.Film.1983.1080p.WEB-DL.x264-NEW"
	mid := h.addMovie(t, 1, "Quiet Film", 1983)
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash)
		VALUES (?, ?, 'Fake', 360, 'movie', ?)`, mid, stuck, hashFor(stuck))
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	h.qbit.stalledTorrent(hashFor(stuck), stuck, "arrmada")
	h.ix.offer(stuck, alt)

	out, err := h.c.FailOverNow(h.ctx, strings.ToUpper(hashFor(stuck)))
	if err != nil {
		t.Fatalf("FailOverNow: %v", err)
	}
	if out.Replacement != alt || out.Title != "Quiet Film" {
		t.Errorf("result = %+v, want the alternate for Quiet Film", out)
	}
	calls := h.qbit.callLog()
	want := []string{"add " + magnetFor(alt), "delete " + hashFor(stuck)}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("client calls = %q, want %q", calls, want)
	}
	if s := h.status(t, gid); s != "failed" {
		t.Errorf("replaced grab status = %q, want failed", s)
	}
	if h.movieEvents(t, mid, "Replaced from Needs you — replaced by "+alt) != 1 {
		t.Errorf("history should say the replacement was asked for")
	}
}

// With nothing else on offer the download is the only copy: it stays, nothing is
// blocklisted, and the answer says nothing was found.
func TestFailOverNowKeepsTheOnlyCopy(t *testing.T) {
	h := newStallHarness(t)
	const stuck = "Lonely.Film.1990.1080p.BluRay.x264-OLD"
	mid := h.addMovie(t, 2, "Lonely Film", 1990)
	if _, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash)
		VALUES (?, ?, 'Fake', 360, 'movie', ?)`, mid, stuck, hashFor(stuck)); err != nil {
		t.Fatal(err)
	}
	h.qbit.stalledTorrent(hashFor(stuck), stuck, "arrmada")
	h.ix.offer(stuck)

	out, err := h.c.FailOverNow(h.ctx, hashFor(stuck))
	if err != nil || out.Replacement != "" {
		t.Fatalf("FailOverNow = %+v, %v; want nothing replaced", out, err)
	}
	if calls := h.qbit.callLog(); len(calls) != 0 {
		t.Errorf("client calls = %q, want none", calls)
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist rows = %d, want 0", n)
	}
}

// A torrent no grab recorded has nothing to be replaced for.
func TestFailOverNowRefusesOthersTorrents(t *testing.T) {
	h := newStallHarness(t)
	h.qbit.stalledTorrent(hashFor("Hand.Added"), "Hand.Added", "linux")
	if _, err := h.c.FailOverNow(h.ctx, hashFor("Hand.Added")); !errors.Is(err, ErrNotOurDownload) {
		t.Errorf("err = %v, want ErrNotOurDownload", err)
	}
	if h.ix.searchCount() != 0 {
		t.Errorf("searched for a torrent Arrmada didn't grab")
	}
}
