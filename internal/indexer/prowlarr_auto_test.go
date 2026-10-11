package indexer

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/connstatus"
)

// memSettings is the settings service as the Prowlarr sync sees it.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) Get(_ context.Context, key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key]; ok {
		return v
	}
	return def
}

func (s *memSettings) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

// Until a sync has saved a URL and key, the hourly sync does nothing at all: no request,
// no outcome, no status.
func TestScheduledProwlarrSyncNeedsSavedConnection(t *testing.T) {
	f := newFakeProwlarr(t)
	s, tr, _ := healthService(t)
	ctx := context.Background()
	for _, st := range []*memSettings{
		{},
		{m: map[string]string{SettingProwlarrURL: f.srv.URL}},
		{m: map[string]string{SettingProwlarrKey: "prowlarr-key"}},
	} {
		if err := s.ScheduledProwlarrSync(ctx, st, nil); err != nil {
			t.Fatalf("no saved connection should be a no-op, got %v", err)
		}
		if _, ok := ReadProwlarrLastSync(ctx, st); ok {
			t.Fatal("a sync that didn't run left an outcome")
		}
	}
	if _, ok := tr.Get(connstatus.KindProwlarr, ProwlarrStatusRef); ok {
		t.Fatal("a sync that didn't run recorded a status")
	}
	if rows := byProwlarrID(t, s); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

// With a saved connection the hourly sync mirrors Prowlarr, saves what it changed and
// records Prowlarr as working; a failing run saves the error (never the key), counts the
// failed runs and records Prowlarr as failing; the next good run clears both.
func TestScheduledProwlarrSyncRecordsOutcome(t *testing.T) {
	f := newFakeProwlarr(t)
	s, tr, _ := healthService(t)
	ctx := context.Background()
	st := &memSettings{m: map[string]string{SettingProwlarrURL: f.srv.URL, SettingProwlarrKey: "prowlarr-key"}}

	if err := s.ScheduledProwlarrSync(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	ls, ok := ReadProwlarrLastSync(ctx, st)
	if !ok || ls.Added != 3 || ls.Error != "" || ls.Trigger != ProwlarrTriggerSchedule || ls.At.IsZero() || ls.FailedRuns != 0 {
		t.Fatalf("after a good sync: %+v (ok=%v)", ls, ok)
	}
	if cs, ok := tr.Get(connstatus.KindProwlarr, ProwlarrStatusRef); !ok || cs.Phase(cs.LastOKAt) != connstatus.PhaseOK {
		t.Fatalf("status after a good sync = %+v", cs)
	}

	f.mu.Lock()
	f.status = http.StatusUnauthorized
	f.mu.Unlock()
	for i := 1; i <= 2; i++ {
		if err := s.ScheduledProwlarrSync(ctx, st, nil); err == nil {
			t.Fatal("a failing sync should fail the task")
		}
		ls, _ = ReadProwlarrLastSync(ctx, st)
		if ls.FailedRuns != i || !strings.Contains(ls.Error, "invalid API key") {
			t.Fatalf("run %d: %+v", i, ls)
		}
	}
	if strings.Contains(st.m[SettingProwlarrLastSync], "prowlarr-key") {
		t.Fatal("the saved outcome carries the key")
	}
	if cs, _ := tr.Get(connstatus.KindProwlarr, ProwlarrStatusRef); cs.ConsecutiveFailures == 0 || cs.LastError == "" {
		t.Fatalf("status after failures = %+v", cs)
	}
	// Every row is still there and on: a failed sync changes nothing.
	for _, row := range byProwlarrID(t, s) {
		if !row.Enabled {
			t.Fatalf("a failed sync switched %s off", row.Name)
		}
	}

	// A person's failed try with a mistyped key shows, but doesn't count as Prowlarr down.
	s.RecordProwlarrSync(ctx, st, ProwlarrTriggerManual, SyncResult{}, errProwlarrEmpty)
	if ls, _ = ReadProwlarrLastSync(ctx, st); ls.FailedRuns != 2 || ls.Trigger != ProwlarrTriggerManual {
		t.Fatalf("manual failure: %+v", ls)
	}

	f.mu.Lock()
	f.status = 0
	f.mu.Unlock()
	if err := s.ScheduledProwlarrSync(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	if ls, _ = ReadProwlarrLastSync(ctx, st); ls.FailedRuns != 0 || ls.Error != "" {
		t.Fatalf("after recovering: %+v", ls)
	}
	if cs, _ := tr.Get(connstatus.KindProwlarr, ProwlarrStatusRef); cs.ConsecutiveFailures != 0 {
		t.Fatalf("status after recovering = %+v", cs)
	}
}

// Disabling an indexer in Prowlarr reaches Arrmada on the next scheduled run, with no
// click.
func TestScheduledProwlarrSyncReflectsUpstreamDisable(t *testing.T) {
	f := newFakeProwlarr(t)
	s, _, _ := healthService(t)
	ctx := context.Background()
	st := &memSettings{m: map[string]string{SettingProwlarrURL: f.srv.URL, SettingProwlarrKey: "prowlarr-key"}}
	if err := s.ScheduledProwlarrSync(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	f.edit(1, func(m map[string]any) bool { m["enable"] = false; return true })
	if err := s.ScheduledProwlarrSync(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	row := byProwlarrID(t, s)[1]
	if row.Enabled || row.DisabledBy != DisabledByProwlarr || row.ManagedNote != noteDisabledInProwlarr {
		t.Fatalf("row after an upstream disable = %+v", row)
	}
	if ls, _ := ReadProwlarrLastSync(ctx, st); ls.Disabled != 1 {
		t.Fatalf("outcome = %+v", ls)
	}
}
