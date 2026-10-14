package automation

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// A release that vanished, was grabbed again by a sweep and vanished again is blocklisted
// the second time, so the grab-and-vanish round ends; the first time it wasn't.
func TestVanishedTwiceIsBlocklisted(t *testing.T) {
	h := newStallHarness(t)
	const gone = "Rare.Film.1971.1080p.BluRay.x264-OLD"
	mid := h.addMovie(t, 1, "Rare Film", 1971)
	first := h.addGoneGrab(t, "movie", mid, gone, grabStatusGrabbed)
	h.ix.offer(gone) // nothing else to grab

	h.goneForTheGrace()
	if s := h.status(t, first); s != grabStatusFailed {
		t.Fatalf("first grab status = %q, want failed", s)
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Fatalf("blocklist rows after the first disappearance = %d, want none", n)
	}

	// A sweep grabs the same release again, and it leaves the client again.
	again := h.addGoneGrab(t, "movie", mid, gone, grabStatusGrabbed)
	h.goneForTheGrace()

	if s := h.status(t, again); s != grabStatusFailed {
		t.Errorf("second grab status = %q, want failed", s)
	}
	var reason string
	if err := h.c.db.QueryRow(`SELECT reason FROM blocklist WHERE norm_title = ?`, normTitle(gone)).Scan(&reason); err != nil {
		t.Fatalf("the release wasn't blocklisted the second time: %v", err)
	}
	if reason != vanishedAgainBlockReason {
		t.Errorf("blocklist reason = %q, want %q", reason, vanishedAgainBlockReason)
	}
	if n := h.movieEvents(t, mid, gone+" disappeared from the download client again"); n != 1 {
		t.Errorf("'again' history lines = %d, want 1", n)
	}
}

// A pending row of a release whose earlier grab vanished is that release grabbed again, not
// a stale copy: it isn't settled quietly as failed (the next sweep would only grab it back)
// but failed over and blocklisted. A twin failed before vanished_at existed still settles
// quietly (TestDuplicateOfASettledGrabClosesQuietly).
func TestRegrabOfVanishedReleaseIsNotSettledQuietly(t *testing.T) {
	h := newStallHarness(t)
	sid := h.addShow(t, false)
	const rel = "Show.S01E01.1080p.WEB-DL.x264-GRP"
	twin := h.addGoneGrab(t, "series", sid, rel, grabStatusFailed)
	if _, err := h.c.db.Exec(`UPDATE grabs SET vanished_at = 1700000000 WHERE id = ?`, twin); err != nil {
		t.Fatal(err)
	}
	regrab := h.addGoneGrab(t, "series", sid, rel, grabStatusGrabbed)
	h.ix.offer("Show.S01E01.1080p.WEB-DL.x264-NEW")

	h.goneForTheGrace()

	if s := h.status(t, regrab); s != grabStatusFailed {
		t.Errorf("re-grab status = %q, want failed", s)
	}
	var n int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM blocklist WHERE reason = ? AND media_type = 'series'`, vanishedAgainBlockReason).Scan(&n); err != nil || n != 1 {
		t.Errorf("'again' blocklist rows = %d (%v), want 1", n, err)
	}
	if h.ix.searchCount() == 0 {
		t.Error("the fail-over should have searched for another release")
	}
}

// The one-time clean-up removes exactly the rows the vanished-download fail-over wrote,
// whatever the media type, and nothing else; it runs once.
func TestPurgeVanishedBlocks(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	ctx := context.Background()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	add := func(mediaType, title, reason string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO blocklist (movie_id, norm_title, title, indexer, reason, media_type) VALUES (1, ?, ?, 'X', ?, ?)`,
			normTitle(title), title, reason, mediaType); err != nil {
			t.Fatal(err)
		}
	}
	const expanse = "The Expanse S01-S06 Complete 1080p WEBRip 10Bit DDP2 0 HEVC-d3g"
	for range 9 {
		add("series", expanse, vanishedBlockReason) // the flood: one row per stale grab
	}
	add("series", "Reacher S04E06 1080p WEB h264-NTb", vanishedBlockReason)
	add("movie", "Some.Film.2020.1080p.WEB-DL-GRP", vanishedBlockReason)
	add("book", "Author - Book [EPUB]", vanishedBlockReason)
	add("music", "Artist - Album [FLAC]", vanishedBlockReason)
	keep := []string{
		vanishedAgainBlockReason,
		"stalled 360 min — replaced by Other.Release-GRP",
		"stalled after 360 min", // pre-ACQ-05: vanished and stalled alike, can't be told apart
		"client error — replaced by Other.Release-GRP",
		"replaced by hand — replaced by Other.Release-GRP",
		"rejected in review",
		"executables",
		"manually blocklisted",
		"wrong title",
		"replaced by regrab",
		"Disappeared from the download client", // not what the fail-over wrote
	}
	for _, r := range keep {
		add("series", "Kept "+r, r)
	}

	n, err := PurgeVanishedBlocks(ctx, db, settings.NewService(db), log)
	if err != nil {
		t.Fatal(err)
	}
	if n != 13 {
		t.Errorf("removed %d rows, want 13", n)
	}
	rows, err := db.QueryContext(ctx, `SELECT reason FROM blocklist ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for rows.Next() {
		var r string
		_ = rows.Scan(&r)
		left = append(left, r)
	}
	_ = rows.Close()
	if strings.Join(left, "|") != strings.Join(keep, "|") {
		t.Errorf("rows left = %q, want every other reason kept:\n%q", left, keep)
	}
	if out := logs.String(); !strings.Contains(out, "rows=13") || !strings.Contains(out, "releases=5") {
		t.Errorf("the startup line should report the count:\n%s", out)
	}

	// Once only: a second start removes nothing, even a row with that reason.
	add("movie", "Late.Row.2021.1080p-GRP", vanishedBlockReason)
	set, err := settings.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := PurgeVanishedBlocks(ctx, db, set, log); err != nil || n != 0 {
		t.Errorf("second run removed %d (%v), want 0", n, err)
	}
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocklist`).Scan(&count)
	if count != len(keep)+1 {
		t.Errorf("blocklist rows after the second start = %d, want %d", count, len(keep)+1)
	}
}
