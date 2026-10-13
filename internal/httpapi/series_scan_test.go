package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

// noSeriesMeta finds nothing: every show folder a scan looks at ends up "needs review".
type noSeriesMeta struct{}

func (noSeriesMeta) Available() bool { return true }
func (noSeriesMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return nil, nil
}
func (noSeriesMeta) GetSeries(context.Context, int) (*metadata.SeriesDetails, error) {
	return nil, nil
}

// A second TV scan while one runs is a 409 naming the running job, like the movie scan's,
// so Settings → Library follows that scan instead of starting another.
func TestConcurrentSeriesScanRejected(t *testing.T) {
	fj := newFakeJobs(false)
	s := newRouteServer(t, func(d *Deps) { d.Jobs = fj })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	if rec := s.do("POST", "/api/v1/series/scan", mgr); rec.Code != http.StatusAccepted {
		t.Fatalf("first scan: HTTP %d %s", rec.Code, rec.Body)
	}
	rec := s.do("POST", "/api/v1/series/scan", mgr)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || body["message"] != "a TV library scan is already running" || body["job_id"] != float64(1) {
		t.Fatalf("second scan: HTTP %d %v", rec.Code, body)
	}
	if n := len(fj.specs()); n != 1 {
		t.Fatalf("%d scans submitted, want 1", n)
	}
}

// The TV scan reports its progress through the job (folder counts, never paths) on the
// live socket's job.updated, and announces its result as library.scanned when it ends.
func TestScanPublishesProgressAndResult(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Show A (2020)", "Show B (2021)"} {
		p := filepath.Join(root, d, "Season 01", "Show - S01E01.mkv")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Truncate(200 << 20) // sparse: big enough to count as an episode, no disk used
		_ = f.Close()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(log)
	events, cancel := bus.Subscribe("job.updated", "library.scanned")
	defer cancel()
	var runner *jobs.Runner
	s := newRouteServer(t, func(d *Deps) {
		r, err := jobs.New(context.Background(), d.Store.DB(), log, bus)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Shutdown(2 * time.Second) })
		runner = r
		d.Jobs = r
		d.Bus = bus
		d.Series = series.NewService(d.Store.DB(), noSeriesMeta{}, root, d.Log)
		d.Config.TVDir = root
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	rec := s.do("POST", "/api/v1/series/scan", mgr)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scan: HTTP %d %s", rec.Code, rec.Body)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second) // a ceiling, not a speed check: CI's runners under -race are slow
	defer stop()
	if err := runner.Wait(ctx, started.JobID); err != nil {
		t.Fatalf("scan job never finished: %v", err)
	}
	job, err := runner.Get(context.Background(), started.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != jobs.StatusSucceeded || job.Progress != 1 || job.Message == "" {
		t.Fatalf("finished scan job = %+v, want succeeded at 100%% with a summary", job)
	}

	var sawProgress, sawResult bool
	deadline := time.After(2 * time.Second)
	for !(sawProgress && sawResult) {
		select {
		case ev := <-events:
			data, _ := json.Marshal(ev.Data)
			if strings.Contains(string(data), root) {
				t.Errorf("%s carries a folder path: %s", ev.Topic, data)
			}
			switch ev.Topic {
			case "job.updated":
				if strings.Contains(string(data), "show folders") {
					sawProgress = true
				}
			case "library.scanned":
				var got struct {
					Media     string `json:"media"`
					Unmatched int    `json:"unmatched"`
				}
				_ = json.Unmarshal(data, &got)
				if got.Media != "series" || got.Unmatched != 2 {
					t.Errorf("library.scanned = %s, want series with 2 unmatched", data)
				}
				sawResult = true
			}
		case <-deadline:
			t.Fatalf("progress seen %v, result seen %v", sawProgress, sawResult)
		}
	}
}
