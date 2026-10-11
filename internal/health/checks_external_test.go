package health

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var extNow = time.Date(2026, 10, 11, 14, 0, 0, 0, time.Local)

func fixedNow() time.Time { return extNow }

func failingRow(id int64, name string, prowlarr bool, failures int, since time.Duration, err string) IndexerState {
	return IndexerState{ID: id, Name: name, Prowlarr: prowlarr, Failures: failures, FailingSince: extNow.Add(-since), LastError: err}
}

// An indexer counts as failing once it has failed three times, is backing off, or has
// been failing for half an hour; one recent failure is a blip.
func TestIndexerFailingThresholds(t *testing.T) {
	cases := []struct {
		name string
		s    IndexerState
		want bool
	}{
		{"working", IndexerState{}, false},
		{"one failure a minute ago", failingRow(1, "A", false, 1, time.Minute, "timeout"), false},
		{"one failure 40 minutes ago", failingRow(1, "A", false, 1, 40*time.Minute, "timeout"), true},
		{"three in a row", failingRow(1, "A", false, 3, time.Minute, "timeout"), true},
		{"backing off", IndexerState{Failures: 2, FailingSince: extNow.Add(-time.Minute), BackingOff: true}, true},
	}
	for _, c := range cases {
		if got := c.s.failing(extNow); got != c.want {
			t.Errorf("%s: failing = %v, want %v", c.name, got, c.want)
		}
	}
}

func outageCheck(states []IndexerState, err error) []Finding {
	return indexerOutageCheck(func(context.Context) ([]IndexerState, error) { return states, err }, fixedNow).Run(context.Background())
}

// Every enabled indexer failing is one error; anything less (or nothing to judge) is
// quiet here — the per-indexer lines speak for single trackers.
func TestIndexerOutageCheck(t *testing.T) {
	tl := failingRow(1, "TorrentLeech", false, 3, 2*time.Hour, "login failed")
	x := failingRow(2, "1337x", false, 1, 40*time.Minute, "Cloudflare")
	ok := IndexerState{ID: 3, Name: "Working"}

	got := outageCheck([]IndexerState{tl, x}, nil)
	if len(got) != 1 || got[0].Key != "indexers.all_failing" || got[0].Level != LevelError || got[0].Fix != FixIndexerStatus {
		t.Fatalf("all failing: %+v", got)
	}
	if !strings.Contains(got[0].Message, "Every indexer is failing") || !strings.Contains(got[0].Message, "1337x") {
		t.Errorf("message %q should say all fail and name the latest failure", got[0].Message)
	}
	if got := outageCheck([]IndexerState{tl, x, ok}, nil); len(got) != 0 {
		t.Fatalf("one still working: %+v", got)
	}
	if got := outageCheck(nil, nil); len(got) != 0 {
		t.Fatalf("no indexers is the other check's job: %+v", got)
	}
	if got := outageCheck(nil, errors.New("db")); len(got) != 0 {
		t.Fatalf("read error: %+v", got)
	}

	// Every one a Prowlarr row with the same error: Prowlarr is the cause, said once.
	var rows []IndexerState
	for i := 1; i <= 12; i++ {
		rows = append(rows, failingRow(int64(i), fmt.Sprintf("Prowlarr · T%d", i), true, 3, time.Hour, "prowlarr: connect failed"))
	}
	got = outageCheck(rows, nil)
	if len(got) != 1 || !strings.Contains(got[0].Message, "Prowlarr is unreachable: prowlarr: connect failed") {
		t.Fatalf("all Prowlarr rows down: %+v", got)
	}
}

func prowlarrFindings(st ProwlarrState) []Finding {
	return prowlarrCheck(func(context.Context) ProwlarrState { return st }, fixedNow).Run(context.Background())
}

// Twelve Prowlarr rows failing with one error collapse into one line (and the per-row
// lines leave them out); a sync that has failed twice in a row is a warning; one failed
// sync, or a Prowlarr never synced, says nothing.
func TestProwlarrCheck(t *testing.T) {
	var rows []IndexerState
	for i := 1; i <= 12; i++ {
		rows = append(rows, failingRow(int64(i), fmt.Sprintf("Prowlarr · T%d", i), true, 3, time.Hour, "prowlarr: connect failed"))
	}
	native := IndexerState{ID: 99, Name: "TorrentLeech"} // still working, so not "all failing"
	st := ProwlarrState{Synced: true, Indexers: append(append([]IndexerState{}, rows...), native)}

	got := prowlarrFindings(st)
	if len(got) != 1 || got[0].Key != "prowlarr.unreachable" || got[0].Level != LevelWarning || got[0].Fix != FixProwlarr {
		t.Fatalf("collapse: %+v", got)
	}
	if !strings.Contains(got[0].Message, "Prowlarr is unreachable (12 indexers): prowlarr: connect failed") {
		t.Errorf("message = %q", got[0].Message)
	}
	o := FindIndexerOutage(st.Indexers, extNow)
	if !o.Covers(5) || o.Covers(99) {
		t.Errorf("Covers: Prowlarr row 5 = %v, native row = %v", o.Covers(5), o.Covers(99))
	}

	// The sync failing too is folded into the same line.
	st.LastSyncAt, st.LastSyncErr, st.FailedRuns = extNow.Add(-time.Hour), "prowlarr: connect failed", 2
	if got := prowlarrFindings(st); len(got) != 1 || !strings.Contains(got[0].Message, "hourly sync is failing too") {
		t.Fatalf("collapse + sync: %+v", got)
	}

	// Only the sync failing (the trackers answer through their cached feeds, say).
	st.Indexers = []IndexerState{native}
	got = prowlarrFindings(st)
	if len(got) != 1 || got[0].Key != "prowlarr.sync" || !strings.Contains(got[0].Message, "failed 2 times in a row") || !strings.Contains(got[0].Message, "13:00") {
		t.Fatalf("sync failing: %+v", got)
	}
	st.FailedRuns = 1
	if got := prowlarrFindings(st); len(got) != 0 {
		t.Fatalf("one failed sync is a blip: %+v", got)
	}
	if got := prowlarrFindings(ProwlarrState{FailedRuns: 5, LastSyncErr: "x"}); len(got) != 0 {
		t.Fatalf("never synced: %+v", got)
	}

	// One failing Prowlarr row is that tracker's own problem, not Prowlarr's.
	one := ProwlarrState{Synced: true, Indexers: []IndexerState{rows[0], native, {ID: 50, Name: "Prowlarr · OK", Prowlarr: true}}}
	if got := prowlarrFindings(one); len(got) != 0 {
		t.Fatalf("one failing row: %+v", got)
	}
}

// Every indexer failing covers every row; the Prowlarr collapse isn't also reported.
func TestIndexerOutageCoversAllWhenAllFail(t *testing.T) {
	rows := []IndexerState{
		failingRow(1, "Prowlarr · A", true, 3, time.Hour, "down"),
		failingRow(2, "Prowlarr · B", true, 3, time.Hour, "down"),
	}
	o := FindIndexerOutage(rows, extNow)
	if !o.AllFailing || !o.Covers(1) || !o.Covers(2) || len(o.Prowlarr) != 0 {
		t.Fatalf("outage = %+v", o)
	}
	if got := prowlarrFindings(ProwlarrState{Synced: true, Indexers: rows}); len(got) != 0 {
		t.Fatalf("the all-failing error already says it: %+v", got)
	}
}

// FlareSolverr is only checked while a tracker that needs it is enabled; then a missing
// URL or a failed ping is a warning naming those trackers.
func TestFlareSolverrCheck(t *testing.T) {
	pings := 0
	check := func(deps []string, configured bool, pingErr error) []Finding {
		return FlareSolverrCheck(
			func(context.Context) ([]string, error) { return deps, nil },
			func() bool { return configured },
			func(context.Context) error { pings++; return pingErr },
		).Run(context.Background())
	}

	if got := check(nil, true, errors.New("refused")); len(got) != 0 || pings != 0 {
		t.Fatalf("no TorrentLeech or 1337x: %+v, %d pings", got, pings)
	}
	got := check([]string{"TorrentLeech", "1337x"}, true, errors.New("FlareSolverr at http://arrmada-flaresolverr:8191 isn't answering"))
	if len(got) != 1 || got[0].Key != "flaresolverr.down" || got[0].Level != LevelWarning || got[0].Fix != FixFlareSolverr {
		t.Fatalf("ping failed: %+v", got)
	}
	if !strings.Contains(got[0].Message, "1337x and TorrentLeech can't get past Cloudflare") {
		t.Errorf("message = %q", got[0].Message)
	}
	if got := check([]string{"TorrentLeech"}, false, nil); len(got) != 1 || !strings.Contains(got[0].Message, "isn't set up") {
		t.Fatalf("not set up: %+v", got)
	}
	if got := check([]string{"TorrentLeech"}, true, nil); len(got) != 0 {
		t.Fatalf("answering: %+v", got)
	}
}

// A hung FlareSolverr reads as "not answering" well inside the check's own limit.
func TestFlareSolverrCheckHungPing(t *testing.T) {
	c := FlareSolverrCheck(
		func(context.Context) ([]string, error) { return []string{"TorrentLeech"}, nil },
		func() bool { return true },
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	)
	if c.Timeout <= flarePingTimeout {
		t.Fatalf("check timeout %v must leave room after the %v ping", c.Timeout, flarePingTimeout)
	}
	start := time.Now()
	got := c.Run(context.Background())
	if len(got) != 1 || !strings.Contains(got[0].Message, "no reply in time") {
		t.Fatalf("hung ping: %+v", got)
	}
	if d := time.Since(start); d > flarePingTimeout+time.Second {
		t.Fatalf("the check waited %v", d)
	}
}

// Backups: nothing while switched off or in the first day; over 48 hours old is a
// warning, over a week an error, and never having made one (after a day) a warning.
func TestBackupsCheck(t *testing.T) {
	run := func(st BackupState) []Finding {
		return backupsCheck(func(context.Context) BackupState { return st }, fixedNow).Run(context.Background())
	}
	day := 24 * time.Hour
	up := 30 * day

	if got := run(BackupState{Enabled: true, Uptime: up, Newest: extNow.Add(-20 * time.Hour)}); len(got) != 0 {
		t.Fatalf("fresh: %+v", got)
	}
	got := run(BackupState{Enabled: true, Uptime: up, Newest: extNow.Add(-3 * day)})
	if len(got) != 1 || got[0].Key != "backups.age" || got[0].Level != LevelWarning || got[0].Fix != FixBackups || !strings.Contains(got[0].Message, "3 days") {
		t.Fatalf("3 days old: %+v", got)
	}
	if got := run(BackupState{Enabled: true, Uptime: up, Newest: extNow.Add(-8 * day)}); len(got) != 1 || got[0].Level != LevelError {
		t.Fatalf("8 days old: %+v", got)
	}
	if got := run(BackupState{Enabled: true, Uptime: 2 * day}); len(got) != 1 || !strings.Contains(got[0].Message, "has been made yet") {
		t.Fatalf("never made: %+v", got)
	}
	if got := run(BackupState{Enabled: true, Uptime: time.Hour, Newest: extNow.Add(-8 * day)}); len(got) != 0 {
		t.Fatalf("first day up: %+v", got)
	}
	if got := run(BackupState{Enabled: false, Uptime: up, Newest: extNow.Add(-8 * day)}); len(got) != 0 {
		t.Fatalf("switched off: %+v", got)
	}
}
