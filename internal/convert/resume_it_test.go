//go:build linux

package convert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// TestResumablePiecedEncodeIT runs resumable pieced encodes (chunk.go) with the real
// encoders on synthetic lavfi films — never library files. Each film's video starts a frame
// after its audio, as in many remuxes. Two are stopped part-way by a "restart" (the service
// is shut down and a new one started on the same database and scratch folder) and must carry
// on from the next piece; one runs straight through. Every result must have the source's
// frame count, evenly spaced frames, the original A/V offset, and pass the SSIM gate and the
// full-decode check (a job only ends Done if both passed). It needs the app image's ffmpeg,
// x265 and hdr10plus_tool, so it only runs with ARRMADA_FFMPEG_IT set.
func TestResumablePiecedEncodeIT(t *testing.T) {
	if os.Getenv("ARRMADA_FFMPEG_IT") == "" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 inside the app image to run real encodes")
	}
	ctx := context.Background()
	dir := t.TempDir()
	lib, recycle, scratch := filepath.Join(dir, "lib"), filepath.Join(dir, "recycle"), filepath.Join(dir, "scratch")
	for _, d := range []string{lib, recycle, scratch} {
		must(t, os.MkdirAll(d, 0o755))
	}
	st, err := store.Open(filepath.Join(dir, "db"))
	must(t, err)
	defer st.Close()
	db := st.DB()
	log := testLogger()
	cfg := settings.NewService(db)
	mv := movies.NewService(db, nil, nil, lib, recycle, eventbus.New(log), log)

	// Ten-second pieces, so these short films are cut up like a feature; and rehearsed,
	// so the resumed run can show it doesn't rehearse again.
	chunkSeconds = 10
	defer func() { chunkSeconds = 300 }()
	preflightMinDuration = 30
	defer func() { preflightMinDuration = 20 * 60 }()

	newSvc := func() *Service {
		sv := NewService(db, mv, nil, cfg, "ffmpeg", "ffprobe", scratch, recycle, log)
		sv.SetHoldRoots(func() []libroots.Root { return []libroots.Root{{Name: "movies", Path: lib}} }, filepath.Join(dir, "db"))
		return sv
	}
	base := newSvc() // indexing, settings and probes; never run
	set(t, base, map[string]string{keyResumable: "true", keyAllowAV1: "false", keyTenBit: TenBitAlways})

	ff := func(args ...string) {
		t.Helper()
		out, err := exec.Command("ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	addMovie := func(id int, title, path string) {
		_, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`, id, 1000+id, title, path)
		must(t, err)
		must(t, base.IndexMovie(ctx, int64(id)))
	}

	type running struct {
		s        *Service
		stop     context.CancelFunc
		watching *atomic.Bool
	}
	start := func() *running {
		c, stop := context.WithCancel(ctx)
		r := &running{s: newSvc(), stop: stop, watching: &atomic.Bool{}}
		r.s.SetWatching(r.watching.Load, func() bool { return true })
		go r.s.Run(c)
		return r
	}
	// shutDown stops a service the way a restart does and waits for its job to end.
	shutDown := func(r *running) {
		r.stop()
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			active := false
			for _, j := range r.s.Jobs() {
				active = active || activeState(j.State)
			}
			if !active {
				time.Sleep(time.Second) // the killed tools' last exit
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("the stopped service's job never ended")
	}
	waitDone := func(s *Service, id int64, limit time.Duration) Job {
		t.Helper()
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			for _, j := range s.Jobs() {
				if j.MovieID == id && !activeState(j.State) {
					return j
				}
			}
			time.Sleep(time.Second)
		}
		for _, l := range s.Logs() {
			t.Log(l.Msg)
		}
		t.Fatalf("movie %d didn't finish within %s", id, limit)
		return Job{}
	}
	logsFor := func(s *Service, title, sub string) []string {
		var out []string
		for _, l := range s.Logs() {
			if strings.Contains(l.Msg, title) && strings.Contains(l.Msg, sub) {
				out = append(out, l.Msg)
			}
		}
		return out
	}
	dumpLogs := func(s *Service, title string) {
		for _, l := range s.Logs() {
			if strings.Contains(l.Msg, title) {
				t.Logf("  log: %s", l.Msg)
			}
		}
	}
	// verify checks a finished film against its source.
	verify := func(s *Service, j Job, title, path string, srcFrames int, srcStart float64) {
		t.Helper()
		if j.State != StateDone {
			dumpLogs(s, title)
			t.Fatalf("%s: %s — %s", title, j.State, j.Note)
		}
		if len(logsFor(s, title, "decoding the whole converted file")) == 0 {
			t.Errorf("%s: the full-decode check should have run on the joined file", title)
		}
		if len(s.resumableKeys(ctx)) != 0 {
			t.Errorf("%s: a finished film's pieces should be cleaned up", title)
		}
		checkTiming(t, path, srcFrames, srcStart)
		t.Logf("%s: done — %s → %s, SSIM %.4f, %s", title, humanBytes(j.SrcBytes), humanBytes(j.OutBytes), j.SSIM, j.Encoder)
	}
	// restartMidway converts a film with a restart once some of its pieces are done, and
	// returns the job of the run that carried on, and that run's service (still running).
	restartMidway := func(id int64, title string) (Job, *running) {
		t.Helper()
		key := movieKey(id)
		r1 := start()
		requestOK(t, r1.s.Request(ctx, key))
		deadline := time.Now().Add(15 * time.Minute)
		for {
			if done, _ := r1.s.resumePieces(ctx, key); done >= 2 {
				break
			}
			if time.Now().After(deadline) {
				dumpLogs(r1.s, title)
				t.Fatalf("%s: the pieced encode never got going", title)
			}
			time.Sleep(300 * time.Millisecond)
		}
		// Frozen (someone "watching") so the restart lands between pieces 2 and the end.
		r1.watching.Store(true)
		r1.s.applyPauses(ctx)
		done1, total := r1.s.resumePieces(ctx, key)
		if done1 >= total {
			t.Fatalf("%s: all %d pieces finished before the restart — the film is too short for the test", title, total)
		}
		shutDown(r1)
		done2, _ := base.resumePieces(ctx, key)
		if done2 < done1 {
			t.Fatalf("%s: a restart must keep the finished pieces: %d before, %d after", title, done1, done2)
		}
		t.Logf("%s: restarted with %d of %d pieces encoded", title, done2, total)
		r2 := start() // the request survived the restart, so this picks the film up again
		j := waitDone(r2.s, id, 20*time.Minute)
		resumedLines := logsFor(r2.s, title, "resuming from piece")
		if len(resumedLines) == 0 {
			dumpLogs(r2.s, title)
			t.Errorf("%s: the restarted encode should have carried on from the pieces already done", title)
		}
		for _, l := range resumedLines {
			t.Logf("%s: %s", title, l)
		}
		if len(logsFor(r2.s, title, "carrying on with the encode a restart interrupted")) == 0 {
			t.Errorf("%s: the resumed job should reuse the settled plan", title)
		}
		if n := len(logsFor(r2.s, title, "test encode at CRF")); n != 1 {
			t.Errorf("%s: rehearsed %d times, want once — before the restart only", title, n)
		}
		return j, r2
	}

	// --- 1: SDR, 23.976 fps, a restart in the middle -------------------------------------
	sdr := filepath.Join(lib, "Resume (2023).mkv")
	ff("-itsoffset", "0.041708", "-f", "lavfi", "-i", "testsrc2=s=1280x720:r=24000/1001:d=60,noise=alls=3:allf=u",
		"-f", "lavfi", "-i", "sine=f=440:d=60.1", "-map", "0:v", "-map", "1:a",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "0", "-pix_fmt", "yuv420p", "-c:a", "ac3", sdr)
	addMovie(8, "Resume", sdr)
	sdrInfo := mustProbe(t, sdr)
	if sdrInfo.VideoStartSec < 0.03 {
		t.Fatalf("the synthetic source should start its video a frame late, got %.3f", sdrInfo.VideoStartSec)
	}
	sdrFrames := base.countFrames(ctx, sdr)
	j, r := restartMidway(8, "Resume")
	verify(r.s, j, "Resume", sdr, sdrFrames, sdrInfo.VideoStartSec)
	shutDown(r)

	// --- 2: HDR10+, 24 fps, a restart in the middle ---------------------------------------
	// The HDR10+ pipeline's raw-stream pieces, its metadata kept with them, and the remux
	// restoring the offset.
	hdrX265 := "log-level=0:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:" +
		"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400"
	pqTags := []string{"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc"}
	hdrSrc := "testsrc2=s=640x360:r=24:d=45,noise=alls=3:allf=u"
	h10p := filepath.Join(lib, "Plus (2024).mkv")
	h10es, h10json, h10inj, h10mp4 := filepath.Join(dir, "h.hevc"), filepath.Join(dir, "h.json"), filepath.Join(dir, "h.inj.hevc"), filepath.Join(dir, "h.mp4")
	ff(append([]string{"-f", "lavfi", "-i", hdrSrc, "-c:v", "libx265", "-preset", "ultrafast", "-crf", "2",
		"-x265-params", hdrX265 + ":pools=none:bframes=0", "-pix_fmt", "yuv420p10le"}, append(pqTags, "-f", "hevc", h10es)...)...)
	var scenes []string
	for i := 0; i < 45*24; i++ {
		scenes = append(scenes, fmt.Sprintf(`{"LuminanceParameters":{"AverageRGB":1000,"LuminanceDistributions":{"DistributionIndex":[1,5,10,25,50,75,90,95,99],"DistributionValues":[10,50,100,500,1000,2000,4000,6000,9000]},"MaxScl":[9000,9000,9000]},"NumberOfWindows":1,"TargetedSystemDisplayMaximumLuminance":0,"SceneFrameIndex":%d,"SceneId":0,"SequenceFrameIndex":%d}`, i, i))
	}
	must(t, os.WriteFile(h10json, []byte(fmt.Sprintf(`{"JSONInfo":{"HDR10plusProfile":"A","Version":"1.0"},"SceneInfo":[%s],"SceneInfoSummary":{"SceneFirstFrameIndex":[0],"SceneFrameNumbers":[%d]},"ToolInfo":{"Tool":"hdr10plus_tool","Version":"1.7.2"}}`, strings.Join(scenes, ","), len(scenes))), 0o644))
	run(t, "hdr10plus_tool", "inject", "-i", h10es, "-j", h10json, "-o", h10inj)
	ff("-r", "24", "-i", h10inj, "-c", "copy", "-tag:v", "hvc1", h10mp4)
	ff("-itsoffset", "0.041667", "-i", h10mp4, "-f", "lavfi", "-i", "sine=f=440:d=45.1", "-map", "0:v", "-map", "1:a",
		"-c:v", "copy", "-c:a", "ac3", h10p)
	addMovie(9, "Plus", h10p)
	plusInfo := mustProbe(t, h10p)
	if plusInfo.HDR != "HDR10+" || plusInfo.VideoStartSec < 0.03 {
		t.Fatalf("synthetic HDR10+ source probed as %s, video start %.3f", plusInfo.HDR, plusInfo.VideoStartSec)
	}
	plusFrames := base.countFrames(ctx, h10p)
	j, r = restartMidway(9, "Plus")
	if out := mustProbe(t, h10p); out.VideoCodec != "hevc" || out.HDR != "HDR10+" || out.HDR10 == nil || out.HDR10.MaxCLL != "1000,400" {
		t.Errorf("HDR10+ film: want HEVC with HDR10+ and its static base, got codec %s hdr %s meta %+v", out.VideoCodec, out.HDR, out.HDR10)
	}
	verify(r.s, j, "Plus", h10p, plusFrames, plusInfo.VideoStartSec)

	// --- 3: HDR10, straight through ----------------------------------------------------
	hdr10 := filepath.Join(lib, "Static (2024).mkv")
	ff(append([]string{"-itsoffset", "0.041667", "-f", "lavfi", "-i", hdrSrc, "-f", "lavfi", "-i", "sine=f=440:d=45.1",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx265", "-preset", "ultrafast", "-crf", "2",
		"-x265-params", hdrX265 + ":pools=none", "-pix_fmt", "yuv420p10le", "-c:a", "ac3"}, append(pqTags, hdr10)...)...)
	addMovie(10, "Static", hdr10)
	staticInfo := mustProbe(t, hdr10)
	if staticInfo.HDR != "HDR10" {
		t.Fatalf("synthetic HDR10 source probed as %s", staticInfo.HDR)
	}
	staticFrames := base.countFrames(ctx, hdr10)
	requestOK(t, r.s.Request(ctx, movieKey(10)))
	j = waitDone(r.s, 10, 20*time.Minute)
	if len(logsFor(r.s, "Static", "resuming from piece")) != 0 {
		t.Error("an uninterrupted encode has nothing to resume")
	}
	if out := mustProbe(t, hdr10); out.VideoCodec != "hevc" || out.HDR != "HDR10" || out.HDR10 == nil || out.HDR10.MaxCLL != "1000,400" {
		t.Errorf("HDR10 film: want HEVC HDR10 with its mastering data, got codec %s hdr %s meta %+v", out.VideoCodec, out.HDR, out.HDR10)
	}
	verify(r.s, j, "Static", hdr10, staticFrames, staticInfo.VideoStartSec)
	shutDown(r)
}

// TestResumablePiecesAV1IT encodes a synthetic film to AV1 in pieces and joins them — the
// SVT-AV1 form of the pieced encode, which the runner only picks when the format test does.
// The joined file must hold every frame, evenly spaced, at the original A/V offset, and
// decode end to end without an error.
func TestResumablePiecesAV1IT(t *testing.T) {
	if os.Getenv("ARRMADA_FFMPEG_IT") == "" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 inside the app image to run real encodes")
	}
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db"))
	must(t, err)
	defer st.Close()
	s := NewService(st.DB(), nil, nil, settings.NewService(st.DB()), "ffmpeg", "ffprobe", filepath.Join(dir, "scratch"), filepath.Join(dir, "recycle"), testLogger())
	chunkSeconds = 10
	defer func() { chunkSeconds = 300 }()

	src := filepath.Join(dir, "AV1 (2025).mkv")
	out, err := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-itsoffset", "0.041708",
		"-f", "lavfi", "-i", "testsrc2=s=640x360:r=24000/1001:d=35,noise=alls=3:allf=u", "-f", "lavfi", "-i", "sine=f=440:d=35.1",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "veryfast", "-crf", "0", "-pix_fmt", "yuv420p", "-c:a", "ac3", src).CombinedOutput()
	if err != nil {
		t.Fatalf("making the source: %v\n%s", err, out)
	}
	mi := mustProbe(t, src)
	spans := chunkPlan(mi)
	if len(spans) < 3 {
		t.Fatalf("the source should be cut into pieces, got %d", len(spans))
	}
	srcFrames := s.countFrames(ctx, src)
	job := &Job{ID: 1, Key: movieKey(1), Kind: "movie", MovieID: 1, Title: "AV1"}
	s.record(job).resume = &resumeRun{}
	dst := filepath.Join(s.activeScratch(ctx), "convert-1.mkv")
	must(t, s.encodeResumable(ctx, job, src, dst, mi, cpuEncoder("av1"), Plan{VideoCodec: "av1", Quality: 30}, spans))
	outInfo := mustProbe(t, dst)
	if outInfo.VideoCodec != "av1" || outInfo.AudioTracks != 1 {
		t.Fatalf("joined file: codec %s, %d audio tracks", outInfo.VideoCodec, outInfo.AudioTracks)
	}
	checkTiming(t, dst, srcFrames, mi.VideoStartSec)
	res, err := s.decodeVideo(ctx, nil, dst, outInfo, "")
	if err != nil || len(res.errors) > 0 || res.frames != int64(srcFrames) {
		t.Errorf("full decode of the joined AV1: %d frames of %d, errors %v, err %v", res.frames, srcFrames, res.errors, err)
	}
	t.Logf("AV1: %d pieces joined, %d frames decoded cleanly", len(spans), res.frames)
}

// checkTiming verifies a converted film is frame-exact and in sync: every frame present,
// evenly spaced (no seam drift), and the video starting where the original's did relative
// to the audio.
func checkTiming(t *testing.T, path string, wantFrames int, wantStart float64) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "frame=pts_time",
		"-of", "csv=p=0", path).Output()
	must(t, err)
	var pts []float64
	for _, ln := range strings.Fields(string(out)) {
		var v float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(ln, ","), "%g", &v); err == nil {
			pts = append(pts, v)
		}
	}
	if len(pts) != wantFrames {
		t.Errorf("%s: %d frames, want %d", filepath.Base(path), len(pts), wantFrames)
	}
	irregular := 0
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d < 0.040 || d > 0.044 {
			irregular++
			if irregular <= 5 {
				t.Logf("%s: frame %d at %.3f, %.4f s after the one before", filepath.Base(path), i, pts[i], d)
			}
		}
	}
	if irregular > 0 {
		t.Errorf("%s: %d unevenly spaced frames — the pieces' seams drifted", filepath.Base(path), irregular)
	}
	mi := mustProbe(t, path)
	if d := mi.VideoStartSec - wantStart; d < -0.002 || d > 0.002 {
		t.Errorf("%s: video starts %.3f s after the file, want %.3f — the sound would be off the picture", filepath.Base(path), mi.VideoStartSec, wantStart)
	}
	t.Logf("%s: %d frames, %d unevenly spaced, video start %.3f s (source %.3f)", filepath.Base(path), len(pts), irregular, mi.VideoStartSec, wantStart)
}
