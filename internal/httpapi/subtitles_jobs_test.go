package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/subtitles"
)

// GET /subtitles/jobs pages the saved job history with before, state and limit; without
// them it is the live list the Queue tab polls.
func TestJobsPagingHandler(t *testing.T) {
	s := newRouteServer(t, func(d *Deps) {
		// Six finished jobs and one still queued, saved by an earlier run of the app.
		for i := 1; i <= 7; i++ {
			state := "done"
			switch {
			case i == 7:
				state = "queued"
			case i%2 == 0:
				state = "failed"
			}
			if _, err := d.Store.DB().Exec(`INSERT INTO subtitle_jobs (id, media_key, kind, movie_id, title, priority, lane, state, note, outcomes_json, queued_at)
				VALUES (?, ?, 'movie', ?, 'Heat', 1, 'fast', ?, 'en: no OpenSubtitles match', '[{"lang":"en","rung":"opensubtitles","result":"no_match","detail":"no OpenSubtitles match"}]', 1000)`,
				i, "m:"+strconv.Itoa(i), i, state); err != nil {
				t.Fatal(err)
			}
		}
		prov := subtitles.NewOpenSubtitles("", "", "")
		d.Subtitles = subtitles.NewService(d.Store.DB(), nil, nil, d.Settings, prov, "ffmpeg", "ffprobe", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	})
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, viewer := s.user(t, "viewer@example.com", auth.RoleRequester)

	type page struct {
		Jobs       []subtitles.Job `json:"jobs"`
		NextBefore int64           `json:"next_before"`
	}
	get := func(q string) page {
		t.Helper()
		rec := s.do("GET", "/api/v1/subtitles/jobs"+q, mgr)
		var p page
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &p) != nil {
			t.Fatalf("GET jobs%s: HTTP %d %s", q, rec.Code, rec.Body)
		}
		return p
	}
	jobIDs := func(p page) []int64 {
		var out []int64
		for _, j := range p.Jobs {
			out = append(out, j.ID)
		}
		return out
	}

	p := get("?limit=3")
	if got := jobIDs(p); len(got) != 3 || got[0] != 7 || got[2] != 5 || p.NextBefore != 5 {
		t.Fatalf("page 1 = %v next %d, want [7 6 5] next 5", got, p.NextBefore)
	}
	p = get("?limit=3&before=" + strconv.FormatInt(p.NextBefore, 10))
	if got := jobIDs(p); len(got) != 3 || got[0] != 4 || p.NextBefore != 2 {
		t.Errorf("page 2 = %v next %d, want [4 3 2] next 2", got, p.NextBefore)
	}
	p = get("?state=failed")
	if got := jobIDs(p); len(got) != 3 || got[0] != 6 || p.NextBefore != 0 {
		t.Errorf("failed = %v next %d, want [6 4 2] and no next page", got, p.NextBefore)
	}
	if len(p.Jobs[0].Outcomes) != 1 || p.Jobs[0].Outcomes[0].Result != subtitles.ResultNoMatch {
		t.Errorf("outcomes = %+v, want the saved per-language outcome", p.Jobs[0].Outcomes)
	}
	// The live list: the restored queue plus recent history.
	if live := get(""); len(live.Jobs) != 7 || live.Jobs[0].State != subtitles.StateQueued {
		t.Errorf("live list = %v", jobIDs(live))
	}
	for _, q := range []string{"?state=lost", "?limit=0", "?before=x"} {
		if rec := s.do("GET", "/api/v1/subtitles/jobs"+q, mgr); rec.Code != http.StatusBadRequest {
			t.Errorf("GET jobs%s: HTTP %d, want 400", q, rec.Code)
		}
	}
	if rec := s.do("GET", "/api/v1/subtitles/jobs?limit=5", viewer); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
	// The live list carries the AI backlog.
	var live map[string]any
	if rec := s.do("GET", "/api/v1/subtitles/jobs", mgr); json.Unmarshal(rec.Body.Bytes(), &live) != nil || live["gpu_backlog"] != float64(0) || live["gpu_eta_sec"] != float64(0) {
		t.Errorf("live list backlog = %v / %v, want 0 / 0", live["gpu_backlog"], live["gpu_eta_sec"])
	}
	// Clearing is per lane: the AI backlog is empty, the fast lane holds the restored job.
	clear := func(q string) (int, float64) {
		rec := s.do("POST", "/api/v1/subtitles/jobs/clear"+q, mgr)
		var out struct{ Cleared float64 }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Cleared
	}
	if code, _ := clear("?lane=disk"); code != http.StatusBadRequest {
		t.Errorf("clear ?lane=disk: HTTP %d, want 400", code)
	}
	if code, n := clear("?lane=gpu"); code != http.StatusOK || n != 0 {
		t.Errorf("clear gpu: HTTP %d cleared %v, want 0", code, n)
	}
	if code, n := clear("?lane=fast"); code != http.StatusOK || n != 1 {
		t.Errorf("clear fast: HTTP %d cleared %v, want 1", code, n)
	}
}
