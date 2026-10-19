package automation

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// QueueStall is the queue's "stalled" verdict: an unfinished torrent that is trying to
// download, receiving nothing, that something says is stuck (a grab's stall clock, or the
// client's own stalled state), and whose latest sign of life is the fail-over's grace old.
func TestQueueStall(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	unix := func(d time.Duration) int64 { return ago(d).Unix() }
	torrent := func(raw string) download.Item {
		state := "downloading"
		switch raw {
		case "pausedDL", "stoppedDL":
			state = "paused"
		case "checkingDL":
			state = "checking"
		case "error", "missingFiles":
			state = "error"
		}
		return download.Item{Hash: "h", State: state, RawState: raw, Progress: 0.4, RemainingBytes: 600, SizeBytes: 1000}
	}
	with := func(it download.Item, f func(*download.Item)) download.Item { f(&it); return it }
	idle := func(raw string, d time.Duration) download.Item {
		return with(torrent(raw), func(i *download.Item) { i.LastActivity = unix(d) })
	}
	grab := func(d time.Duration) StallClocks { return StallClocks{Grab: ago(d)} }

	cases := []struct {
		name      string
		it        download.Item
		clocks    StallClocks
		guard     bool
		wantSince time.Time // zero: not stalled
	}{
		{"grab clock past the grace", torrent("stalledDL"), grab(2 * time.Hour), false, ago(2 * time.Hour)},
		{"grab clock inside the grace", torrent("stalledDL"), grab(10 * time.Minute), false, time.Time{}},
		{"grab clock at exactly the grace", torrent("downloading"), grab(QueueStallGrace), false, ago(QueueStallGrace)},
		// The fail-over's clock says no progress even though qBittorrent calls it downloading.
		{"downloading by name, no progress on the grab clock", torrent("downloading"), grab(3 * time.Hour), false, ago(3 * time.Hour)},
		// Bytes are arriving: progress, whatever the clock says.
		{"receiving data", with(torrent("downloading"), func(i *download.Item) { i.DownSpeed = 1 }), grab(3 * time.Hour), false, time.Time{}},
		// The latest sign of life wins: a slow pack between whole percents moved data 10s ago.
		{"slow pack, fresh last activity", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = now.Add(-10 * time.Second).Unix() }), grab(40 * time.Minute), false, time.Time{}},
		// A grab's clock held while it was paused or queued: resumed a minute ago.
		{"resumed, grab clock held", idle("stalledDL", 8*time.Hour), grab(time.Minute), false, time.Time{}},
		{"both clocks old, the later is since", idle("stalledDL", time.Hour), grab(4 * time.Hour), false, ago(time.Hour)},
		{"both clocks old, grab's the later", idle("stalledDL", 4*time.Hour), grab(time.Hour), false, ago(time.Hour)},
		// A grab's clock survives a restart, so this process's watch isn't needed for it.
		{"grab ignores this run's watch", idle("stalledDL", 3*time.Hour), StallClocks{Grab: ago(2 * time.Hour), Trying: ago(5 * time.Minute)}, false, ago(2 * time.Hour)},
		// No grab: the client's own stalled state, once nothing has moved for the grace.
		{"client stalledDL, idle past the grace", idle("stalledDL", time.Hour), StallClocks{}, false, ago(time.Hour)},
		{"client stalledDL, active a moment ago", idle("stalledDL", time.Minute), StallClocks{}, false, time.Time{}},
		{"client metaDL, never active, added long ago", with(torrent("metaDL"), func(i *download.Item) { i.AddedOn = unix(5 * time.Hour) }), StallClocks{}, false, ago(5 * time.Hour)},
		// Queued for hours, started ten minutes ago: watched trying only that long.
		{"no grab, just started trying", idle("stalledDL", 3*time.Hour), StallClocks{Trying: ago(10 * time.Minute)}, false, time.Time{}},
		{"no grab, trying past the grace", idle("stalledDL", 3*time.Hour), StallClocks{Trying: ago(time.Hour)}, false, ago(time.Hour)},
		// A plain "downloading" torrent no grab knows has only the client's word, and the
		// client doesn't call it stalled.
		{"client downloading, no grab", idle("downloading", time.Hour), StallClocks{}, false, time.Time{}},
		// The states the fail-over holds its clock through are never stalls.
		{"paused", torrent("pausedDL"), grab(5 * time.Hour), false, time.Time{}},
		{"queued for a slot", torrent("queuedDL"), grab(5 * time.Hour), false, time.Time{}},
		{"checking", torrent("checkingDL"), grab(5 * time.Hour), false, time.Time{}},
		{"moving", torrent("moving"), grab(5 * time.Hour), false, time.Time{}},
		{"held by the disk guard", torrent("stalledDL"), grab(5 * time.Hour), true, time.Time{}},
		// An errored torrent is Failed, not stalled.
		{"errored", torrent("error"), grab(5 * time.Hour), false, time.Time{}},
		{"finished", with(torrent("stalledUP"), func(i *download.Item) { i.Progress, i.RemainingBytes = 1, 0 }), grab(5 * time.Hour), false, time.Time{}},
		// A usenet job never fails over for making no progress, so it never reads stalled.
		{"usenet", with(torrent("Downloading"), func(i *download.Item) { i.Transport = download.TransportUsenet }), grab(5 * time.Hour), false, time.Time{}},
		// A clock in the future (a skewed client) proves nothing.
		{"future clock", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = now.Add(time.Hour).Unix() }), StallClocks{}, false, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			since, stalled := QueueStall(tc.it, tc.clocks, tc.guard, now)
			if stalled != !tc.wantSince.IsZero() || !since.Equal(tc.wantSince) {
				t.Errorf("QueueStall = (%v, %v), want (%v, %v)", since, stalled, tc.wantSince, !tc.wantSince.IsZero())
			}
		})
	}
}

// The grace is the fail-over's own, so the queue and the automation never disagree about
// how long one reading of the client must hold.
func TestQueueStallGraceIsTheFailoverGrace(t *testing.T) {
	if QueueStallGrace != stallEvidenceGrace {
		t.Fatalf("QueueStallGrace = %v, want the fail-over's evidence grace %v", QueueStallGrace, stallEvidenceGrace)
	}
}

// stallRig drives one torrent in a fake qBittorrent through ReconcileAcquisitions on a
// moving clock, the way the 30-second task does, and asks QueueStall about it.
type stallRig struct {
	t    *testing.T
	c    *Coordinator
	qb   *fakeQbit
	hash string
	at   time.Time
}

func newStallRig(t *testing.T, grabbed bool) *stallRig {
	t.Helper()
	qb := &fakeQbit{}
	c := newQbitCoord(t, qb)
	r := &stallRig{t: t, c: c, qb: qb, hash: "abcdef0123456789abcdef0123456789abcdef01", at: time.Now().Add(-12 * time.Hour)}
	c.now = func() time.Time { return r.at }
	if grabbed {
		if _, err := c.db.Exec(`INSERT INTO grabs (id, movie_id, version_id, title, media_type, info_hash) VALUES (1, 7, 3, 'X', 'movie', ?)`, r.hash); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// set puts the torrent in the client: its raw state, progress and when it last moved
// data (zero: never).
func (r *stallRig) set(state string, progress float64, lastActivity time.Time) {
	var last int64
	if !lastActivity.IsZero() {
		last = lastActivity.Unix()
	}
	r.qb.mu.Lock()
	r.qb.torrents = []map[string]any{{"hash": r.hash, "name": "X", "state": state, "progress": progress, "size": 100000,
		"amount_left": int64((1 - progress) * 100000), "last_activity": last, "added_on": time.Now().Add(-13 * time.Hour).Unix()}}
	r.qb.mu.Unlock()
}

// run advances the clock by d in 30-second reconcile ticks.
func (r *stallRig) run(d time.Duration, each func(at time.Time)) {
	r.t.Helper()
	for end := r.at.Add(d); r.at.Before(end); r.at = r.at.Add(30 * time.Second) {
		if each != nil {
			each(r.at)
		}
		r.c.downloads.InvalidateSnapshot()
		if _, err := r.c.ReconcileAcquisitions(context.Background()); err != nil {
			r.t.Fatal(err)
		}
	}
}

// stall is QueueStall's verdict on the torrent now, with the clocks the feed would pass.
func (r *stallRig) stall() (time.Duration, bool) {
	r.t.Helper()
	r.c.downloads.InvalidateSnapshot()
	snap, err := r.c.downloads.Snapshot(context.Background())
	if err != nil || len(snap.Items) != 1 {
		r.t.Fatalf("snapshot: %v, %d items", err, len(snap.Items))
	}
	var clocks StallClocks
	if a, ok, _ := r.c.ByHash(context.Background(), r.hash); ok {
		clocks.Grab = a.ProgressAt
	}
	clocks.Trying = r.c.TryingSince()[r.hash]
	since, stalled := QueueStall(snap.Items[0], clocks, false, r.at)
	return r.at.Sub(since), stalled
}

// A torrent queued for three hours that has just started is not three hours without
// progress — its grab's clock is held while it waits, as the fail-over's own check does —
// but it is stalled once it has been trying for the grace with nothing arriving.
func TestQueueStallQueuedThenStarted(t *testing.T) {
	for _, grabbed := range []bool{true, false} {
		r := newStallRig(t, grabbed)
		queuedAt := r.at
		r.set("queuedDL", 0, time.Time{})
		r.run(3*time.Hour, nil)
		r.set("stalledDL", 0, queuedAt)
		r.run(time.Minute, nil)
		if age, stalled := r.stall(); stalled {
			t.Fatalf("grabbed=%v: just started after 3h queued reads stalled for %v", grabbed, age)
		}
		// Genuinely dead: nothing arrives for the grace.
		r.run(QueueStallGrace, nil)
		if age, stalled := r.stall(); !stalled || age < QueueStallGrace || age > QueueStallGrace+7*time.Minute {
			t.Fatalf("grabbed=%v: dead for %v after starting: stalled=%v for %v", grabbed, QueueStallGrace, stalled, age)
		}
	}
}

// A grab paused overnight (by hand, or by the disk guard) and resumed this minute isn't
// eight hours without progress.
func TestQueueStallResumedAfterPause(t *testing.T) {
	r := newStallRig(t, true)
	pausedAt := r.at
	r.set("pausedDL", 0.3, pausedAt)
	r.run(8*time.Hour, nil)
	r.set("stalledDL", 0.3, pausedAt)
	r.run(time.Minute, nil)
	if age, stalled := r.stall(); stalled {
		t.Fatalf("resumed a minute ago reads stalled for %v", age)
	}
}

// A big pack moving slowly — under a percent in forty minutes — is moving: its clock follows
// sub-percent progress, and qBittorrent's last activity is seconds old even on a read
// where its rate sample is zero (stalledDL).
func TestQueueStallSlowPackIsMoving(t *testing.T) {
	r := newStallRig(t, true)
	start := r.at
	p := 0.20
	r.run(40*time.Minute, func(at time.Time) {
		p = 0.20 + 0.009*float64(at.Sub(start))/float64(40*time.Minute)
		r.set("downloading", p, at)
	})
	a, _, _ := r.c.ByHash(context.Background(), r.hash)
	if age := r.at.Sub(a.ProgressAt); age > reconcileRewriteAfter+time.Minute {
		t.Errorf("stall clock is %v old on a download that moved a moment ago", age)
	}
	r.set("stalledDL", p, r.at.Add(-10*time.Second))
	if age, stalled := r.stall(); stalled {
		t.Fatalf("slow but moving pack reads stalled for %v", age)
	}
}

// The trying clock forgets a download that stops trying, and one gone from a complete read.
func TestNoteTrying(t *testing.T) {
	c := &Coordinator{}
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return at }
	live := download.Item{Hash: "AA", State: "downloading", RawState: "stalledDL", Progress: 0.1, RemainingBytes: 9}
	queued := download.Item{Hash: "BB", State: "downloading", RawState: "queuedDL", Progress: 0.1, RemainingBytes: 9}
	c.noteTrying([]download.Item{live, queued}, true)
	if got := c.TryingSince(); !got["aa"].Equal(at) || !got["bb"].IsZero() {
		t.Fatalf("first read: %v", got)
	}
	at = at.Add(time.Hour)
	c.noteTrying([]download.Item{live}, true)
	if got := c.TryingSince(); !got["aa"].Equal(at.Add(-time.Hour)) {
		t.Fatalf("still trying keeps its start: %v", got)
	}
	c.noteTrying(nil, false) // a partial read keeps what it can't see
	if _, ok := c.TryingSince()["aa"]; !ok {
		t.Fatal("a partial read forgot a download")
	}
	c.noteTrying(nil, true)
	if len(c.TryingSince()) != 0 {
		t.Fatal("a complete read without it kept it")
	}
	if (*Coordinator)(nil).TryingSince() != nil {
		t.Fatal("a nil coordinator knows something")
	}
}
