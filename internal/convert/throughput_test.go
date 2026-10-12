package convert

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"
)

// With no history the estimate starts from the measured default (3.8 fps for 4K x265 on 16
// cores); once three conversions of the kind have run it's their median instead.
func TestThroughputDefaultsThenMedian(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	set(t, s, map[string]string{keyCPUCores: "16"})
	p := s.prefs(ctx)

	uhd := forecastFacts{video: true, codec: "hevc", dur: 7200, w: 3840, h: 2160, encHDR: "SDR"}
	got := s.estimateSeconds(ctx, p, uhd)
	encode := (7200 + rehearsalContentSecs) * 3840 * 2160 / defaultCPUPixelRate // ≈ 3.8 fps
	if want := int64(math.Ceil(encode + verifySecs4K)); got != want {
		t.Fatalf("4K default estimate = %d s, want %d", got, want)
	}
	if h := float64(got) / 3600; h < 12 || h > 14 {
		t.Errorf("a 2 h 4K film on 16 cores ≈ %.1f h; the measured pace says ~13 h", h)
	}
	// Half the cores, about twice the time.
	set(t, s, map[string]string{keyCPUCores: "8"})
	if half := s.estimateSeconds(ctx, p, uhd); half < got*19/10 {
		t.Errorf("8 cores: %d s vs 16 cores: %d s", half, got)
	}
	set(t, s, map[string]string{keyCPUCores: "16"})

	// Three 1080p conversions on this machine: 1000, 2000 and 3000 s for an hour of film.
	info := jsonString(&MediaInfo{VideoCodec: "h264", Width: 1920, Height: 1080, DurationSec: 3600})
	for i, secs := range []float64{1000, 3000, 2000} {
		if _, err := s.db.Exec(`INSERT INTO convert_history (item_key, kind, movie_id, title, outcome, codec, encoder,
			src_info_json, started_at, finished_at, encode_secs) VALUES (?, 'movie', ?, 'x', ?, 'hevc', 'CPU (x265)', ?, ?, ?, ?)`,
			movieKey(int64(i+1)), i+1, OutcomeDone, info, 1000+i, 2000+i, secs); err != nil {
			t.Fatal(err)
		}
	}
	// A GPU conversion and a tracks-only one don't count toward the CPU's speed.
	if _, err := s.db.Exec(`INSERT INTO convert_history (item_key, kind, title, outcome, codec, encoder, src_info_json, started_at, finished_at, encode_secs)
		VALUES ('movie:9', 'movie', 'g', ?, 'hevc', 'Intel Quick Sync (HEVC)', ?, 1, 9000, 10), ('movie:10', 'movie', 't', ?, '', 'Copy', ?, 1, 9001, 10)`,
		OutcomeDone, info, OutcomeDone, info); err != nil {
		t.Fatal(err)
	}
	s.tput = throughputCache{} // drop the cached model
	rate := s.pixelRate(ctx, classCPUHEVC, 1920, 1080)
	if want := 3600.0 * 1920 * 1080 / 2000; math.Abs(rate-want) > 1 {
		t.Errorf("median rate = %.0f, want %.0f", rate, want)
	}
	// 4K has no history yet: still the default.
	if r := s.pixelRate(ctx, classCPUHEVC, 3840, 2160); math.Abs(r-defaultCPUPixelRate) > 1 {
		t.Errorf("4K rate = %.0f, want the default %.0f", r, defaultCPUPixelRate)
	}
	// A tracks-only rewrite is just the copy.
	if sec := s.estimateSeconds(ctx, p, forecastFacts{size: 15e9}); sec != 130 {
		t.Errorf("tracks-only estimate = %d s", sec)
	}
}

func TestNights(t *testing.T) {
	day := int64(24 * 3600)
	for _, c := range []struct {
		sec        int64
		start, end string
		want       int
	}{
		{day, "01:00", "07:00", 4},        // 6 h a night
		{day + 1, "01:00", "07:00", 5},    // a second over is another night
		{day, "22:00", "06:00", 3},        // wraps past midnight: 8 h
		{day, "", "", 1},                  // any time: a day each
		{day + 3600, "", "", 2},           //
		{day, "03:00", "03:00", 1},        // start == end means any time too
		{0, "01:00", "07:00", 0},          // nothing to do
		{3600, "nonsense", "07:00", 1},    // unreadable hours count as any time
		{7 * 3600, "23:30", "00:30", 7},   // one hour a night
		{25 * 3600, "00:00", "23:59", 2},  //
		{60, "01:00", "07:00", 1},         //
		{6 * 3600, "01:00", "07:00", 1},   //
		{6*3600 + 1, "01:00", "07:00", 2}, //
	} {
		if got := Nights(c.sec, c.start, c.end); got != c.want {
			t.Errorf("Nights(%d, %q, %q) = %d, want %d", c.sec, c.start, c.end, got, c.want)
		}
	}
}

// Encoding time counts only while ffmpeg is reporting: a frozen encode's silence is a pause.
func TestTickActiveSkipsPauses(t *testing.T) {
	s := newTestService(t)
	job := &Job{}
	s.tickActive(job) // the first line starts the clock
	job.lastTick = job.lastTick.Add(-10 * time.Second)
	s.tickActive(job)
	if job.activeSecs < 9.9 || job.activeSecs > 11 {
		t.Fatalf("after a 10 s gap: %.1f s", job.activeSecs)
	}
	job.lastTick = job.lastTick.Add(-6 * time.Hour) // frozen outside the hours
	s.tickActive(job)
	if job.activeSecs > 11 {
		t.Errorf("a 6 h pause counted as encoding: %.0f s", job.activeSecs)
	}
}

// The Status carries a per-file estimate and the backlog in nights of the window, from the
// cached candidates; a running encode reports what a restart would lose.
func TestForecastStatus(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	set(t, s, map[string]string{keyCPUCores: "16", keySweepStart: "01:00", keySweepEnd: "07:00"})
	p := s.prefs(ctx)
	film4K := autoCand{Key: "movie:1", Video: true, Codec: "hevc",
		facts: forecastFacts{video: true, codec: "hevc", dur: 7200, w: 3840, h: 2160, encHDR: "SDR", size: 80e9}}
	tidy := autoCand{Key: "movie:2", facts: forecastFacts{size: 4e9}}
	st := Status{UpNext: []autoCand{film4K}}
	s.forecastStatus(ctx, &st, p, []autoCand{film4K, tidy, film4K})

	one := s.estimateSeconds(ctx, p, film4K.facts)
	if st.UpNext[0].ETASec != one || st.UpNext[0].Nights != Nights(one, "01:00", "07:00") || st.UpNext[0].Nights < 2 {
		t.Errorf("up next = %+v (one file %d s)", st.UpNext[0], one)
	}
	if want := 2*one + s.estimateSeconds(ctx, p, tidy.facts); st.BacklogSec != want {
		t.Errorf("backlog = %d, want %d", st.BacklogSec, want)
	}
	if st.BacklogNights != Nights(st.BacklogSec, "01:00", "07:00") {
		t.Errorf("backlog nights = %d", st.BacklogNights)
	}
	// Wider hours, fewer nights.
	set(t, s, map[string]string{keySweepStart: "", keySweepEnd: ""})
	st2 := Status{}
	s.forecastStatus(ctx, &st2, s.prefs(ctx), []autoCand{film4K, tidy, film4K})
	if st2.BacklogNights >= st.BacklogNights {
		t.Errorf("any-time backlog %d nights vs 01:00–07:00 %d", st2.BacklogNights, st.BacklogNights)
	}

	job, _ := s.claimPending("movie:3", func(id int64) *Job { return &Job{ID: id, State: StateEncoding} })
	s.update(job, func(j *Job) { j.activeSecs = 4 * 3600 })
	st3 := Status{}
	s.forecastStatus(ctx, &st3, p, nil)
	if st3.InFlightSecs != 4*3600 {
		t.Errorf("in flight = %d", st3.InFlightSecs)
	}

	// The JSON the page reads.
	b, _ := json.Marshal(st)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"backlog_sec", "backlog_nights", "in_flight_secs"} {
		if _, ok := m[k]; !ok {
			t.Errorf("status JSON lacks %s", k)
		}
	}
	if up := m["up_next"].([]any)[0].(map[string]any); up["eta_sec"] == nil || up["nights"] == nil {
		t.Errorf("up next JSON = %v", up)
	}
}

// FileForecast (the plan view's estimate) is the same function.
func TestFileForecast(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	set(t, s, map[string]string{keyCPUCores: "16", keySweepStart: "01:00", keySweepEnd: "07:00"})
	mi := film("h264", 1920, 1080, 20000)
	plan := Plan{VideoCodec: "hevc"}
	f := s.FileForecast(ctx, mi, plan)
	if want := s.estimateSeconds(ctx, s.prefs(ctx), factsFor(mi, plan, 0)); f.Seconds != want || f.Nights != Nights(want, "01:00", "07:00") {
		t.Errorf("forecast = %+v, want %d s", f, want)
	}
}
