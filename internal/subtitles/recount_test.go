package subtitles

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// recountFixture is a Service whose library pass is a seam that counts its runs (and can be
// held mid-pass), over real settings with languages "en" and a finished pass for them.
type recountFixture struct {
	s       *Service
	mu      sync.Mutex
	passes  int
	hold    chan struct{} // when set, a pass waits on it
	running chan struct{} // receives once each pass starts
}

func newRecountFixture(t *testing.T) *recountFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &recountFixture{s: queueFixture(), running: make(chan struct{}, 8)}
	f.s.settings = settings.NewService(st.DB())
	if err := f.s.settings.Set(context.Background(), keyLanguages, "en"); err != nil {
		t.Fatal(err)
	}
	f.s.passFn = func(context.Context) ([]FileSubs, []SeriesGroup, error) {
		f.running <- struct{}{}
		f.mu.Lock()
		f.passes++
		hold := f.hold
		f.mu.Unlock()
		if hold != nil {
			<-hold
		}
		return []FileSubs{{Kind: "movie", MovieID: 1}}, nil, nil
	}
	f.s.snap.at = time.Now()
	f.s.snap.langsKey = "en"
	return f
}

func (f *recountFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.passes
}

// waitIdle waits for the running pass (and any follow-up) to finish.
func (f *recountFixture) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.s.Coverage().Scanning {
		if time.Now().After(deadline) {
			t.Fatal("the pass never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSetSettingsLanguagesTriggersRescan(t *testing.T) {
	f := newRecountFixture(t)
	f.hold = make(chan struct{})
	ctx := context.Background()
	if err := f.s.SetSettings(ctx, nil, nil, []string{"en", "es"}); err != nil {
		t.Fatal(err)
	}
	<-f.running
	// While it recounts, the Overview and the Library say the figures are for the old list.
	if c := f.s.Coverage(); !c.Stale || !c.Scanning {
		t.Errorf("coverage during the recount = stale %v scanning %v, want both", c.Stale, c.Scanning)
	}
	if !f.s.Stale() {
		t.Error("Stale() doesn't say the counts are stale")
	}
	close(f.hold)
	f.waitIdle(t)
	if n := f.count(); n != 1 {
		t.Errorf("%d passes, want 1", n)
	}
	if f.s.Coverage().Stale {
		t.Error("still stale after the recount")
	}
}

func TestSameLanguagesNoRescan(t *testing.T) {
	f := newRecountFixture(t)
	auto := false
	if err := f.s.SetSettings(context.Background(), &auto, nil, []string{" EN "}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetSettings(context.Background(), nil, &auto, nil); err != nil {
		t.Fatal(err)
	}
	if f.s.Coverage().Scanning || f.count() != 0 {
		t.Errorf("saving the same languages started a pass (%d)", f.count())
	}
}

// Languages changed while a pass counts against the old ones: exactly one more pass runs
// after it, however many times the change is saved meanwhile.
func TestFollowUpPassWhenScanning(t *testing.T) {
	f := newRecountFixture(t)
	f.hold = make(chan struct{})
	ctx := context.Background()
	if !f.s.Rescan(ctx) {
		t.Fatal("the first pass didn't start")
	}
	<-f.running
	if f.s.Rescan(ctx) {
		t.Error("a second pass started while one was running")
	}
	for _, langs := range [][]string{{"en", "es"}, {"en", "fr"}} {
		if err := f.s.SetSettings(ctx, nil, nil, langs); err != nil {
			t.Fatal(err)
		}
	}
	f.s.snap.mu.Lock()
	followUp := f.s.snap.followUp
	f.s.snap.mu.Unlock()
	if !followUp {
		t.Fatal("no follow-up asked for")
	}
	f.mu.Lock()
	hold := f.hold
	f.hold = nil // the follow-up pass runs straight through
	f.mu.Unlock()
	close(hold)
	f.waitIdle(t)
	if n := f.count(); n != 2 {
		t.Errorf("%d passes, want exactly 2", n)
	}
	f.s.snap.mu.Lock()
	key := f.s.snap.langsKey
	f.s.snap.mu.Unlock()
	if key != "en,fr" {
		t.Errorf("snapshot counted for %q, want the latest languages", key)
	}
}
