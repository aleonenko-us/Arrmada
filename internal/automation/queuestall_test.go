package automation

import (
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// QueueStall is the queue's "stalled" verdict: an unfinished torrent that is trying to
// download, receiving nothing, and has gone the fail-over's grace without progress by the
// grab's stall clock or the client's own stalled state.
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

	cases := []struct {
		name      string
		it        download.Item
		clock     time.Time
		guard     bool
		wantSince time.Time // zero: not stalled
	}{
		{"grab clock past the grace", torrent("stalledDL"), ago(2 * time.Hour), false, ago(2 * time.Hour)},
		{"grab clock inside the grace", torrent("stalledDL"), ago(10 * time.Minute), false, time.Time{}},
		{"grab clock at exactly the grace", torrent("downloading"), ago(QueueStallGrace), false, ago(QueueStallGrace)},
		// The fail-over's clock says no progress even though qBittorrent calls it downloading.
		{"downloading by name, no progress on the grab clock", torrent("downloading"), ago(3 * time.Hour), false, ago(3 * time.Hour)},
		// Bytes are arriving: progress, whatever the clock says.
		{"receiving data", with(torrent("downloading"), func(i *download.Item) { i.DownSpeed = 1 }), ago(3 * time.Hour), false, time.Time{}},
		// No grab: the client's own stalled state, once nothing has moved for the grace.
		{"client stalledDL, idle past the grace", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = unix(time.Hour) }), time.Time{}, false, time.Unix(unix(time.Hour), 0)},
		{"client stalledDL, active a moment ago", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = unix(time.Minute) }), time.Time{}, false, time.Time{}},
		{"client metaDL, never active, added long ago", with(torrent("metaDL"), func(i *download.Item) { i.AddedOn = unix(5 * time.Hour) }), time.Time{}, false, time.Unix(unix(5*time.Hour), 0)},
		// A plain "downloading" torrent no grab knows has only the client's word, and the
		// client doesn't call it stalled.
		{"client downloading, no grab", with(torrent("downloading"), func(i *download.Item) { i.LastActivity = unix(time.Hour) }), time.Time{}, false, time.Time{}},
		// Both clocks: the earlier wins.
		{"both clocks, client's earlier", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = unix(4 * time.Hour) }), ago(time.Hour), false, time.Unix(unix(4*time.Hour), 0)},
		{"both clocks, grab's earlier", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = unix(time.Hour) }), ago(4 * time.Hour), false, ago(4 * time.Hour)},
		// The states the fail-over holds its clock through are never stalls.
		{"paused", torrent("pausedDL"), ago(5 * time.Hour), false, time.Time{}},
		{"queued for a slot", torrent("queuedDL"), ago(5 * time.Hour), false, time.Time{}},
		{"checking", torrent("checkingDL"), ago(5 * time.Hour), false, time.Time{}},
		{"moving", torrent("moving"), ago(5 * time.Hour), false, time.Time{}},
		{"held by the disk guard", torrent("stalledDL"), ago(5 * time.Hour), true, time.Time{}},
		// An errored torrent is Failed, not stalled.
		{"errored", torrent("error"), ago(5 * time.Hour), false, time.Time{}},
		{"finished", with(torrent("stalledUP"), func(i *download.Item) { i.Progress, i.RemainingBytes = 1, 0 }), ago(5 * time.Hour), false, time.Time{}},
		// A usenet job never fails over for making no progress, so it never reads stalled.
		{"usenet", with(torrent("Downloading"), func(i *download.Item) { i.Transport = download.TransportUsenet }), ago(5 * time.Hour), false, time.Time{}},
		// A clock in the future (a skewed client) proves nothing.
		{"future clock", with(torrent("stalledDL"), func(i *download.Item) { i.LastActivity = now.Add(time.Hour).Unix() }), time.Time{}, false, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			since, stalled := QueueStall(tc.it, tc.clock, tc.guard, now)
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
