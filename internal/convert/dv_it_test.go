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

// TestKeepDolbyVisionIT runs the keep-Dolby-Vision pipeline (dv.go) end to end with the real
// encoder, dovi_tool and hdr10plus_tool, on synthetic sources built here — lavfi pictures
// with generated Dolby Vision metadata, never a real film:
//
//   - profile 8.1 over HDR10 → kept as 8.1, with the gates passed;
//   - profile 7 FEL (a dual-layer stream, its RPU written by syntheticRPU) → 8.1;
//   - profile 8.1 over an HDR10+ base → both kept;
//   - metadata on only half the frames → the RPUs can't line up, the original is kept;
//   - bare RPUs that ffmpeg's rewrite mangles → the output gate keeps the original.
//
// It needs the app image's tools, so it runs only with ARRMADA_FFMPEG_IT set.
func TestKeepDolbyVisionIT(t *testing.T) {
	if os.Getenv("ARRMADA_FFMPEG_IT") == "" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 inside the app image to run real encodes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	lib, recycle, scratch, work := filepath.Join(dir, "lib"), filepath.Join(dir, "recycle"), filepath.Join(dir, "scratch"), filepath.Join(dir, "work")
	for _, d := range []string{lib, recycle, scratch, work} {
		must(t, os.MkdirAll(d, 0o755))
	}
	st, err := store.Open(filepath.Join(dir, "db"))
	must(t, err)
	defer st.Close()
	db := st.DB()
	log := testLogger()
	mv := movies.NewService(db, nil, nil, lib, recycle, eventbus.New(log), log)
	s := NewService(db, mv, nil, settings.NewService(db), "ffmpeg", "ffprobe", scratch, recycle, log)
	s.SetHoldRoots(func() []libroots.Root { return []libroots.Root{{Name: "movies", Path: lib}} }, filepath.Join(dir, "db"))
	var watching atomic.Bool
	s.SetWatching(watching.Load, func() bool { return true })
	if s.doviTool == "" || s.hdr10plusTool == "" {
		t.Fatal("this image has no dovi_tool / hdr10plus_tool")
	}

	ff := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	tool := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	w := func(name string) string { return filepath.Join(work, name) }
	const frames = 480 // 20 s at 24 fps
	hdrX265 := "log-level=0:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:" +
		"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400:pools=none:bframes=0"
	pq := []string{"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc"}
	// baseES encodes a near-lossless HDR10 HEVC stream (a remux's bitrate) of secs seconds.
	baseES := func(name, size string, secs int) string {
		out := w(name)
		ff(append([]string{"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=s=%s:r=24:d=%d,noise=alls=3:allf=u", size, secs),
			"-c:v", "libx265", "-preset", "ultrafast", "-crf", "2", "-x265-params", hdrX265, "-pix_fmt", "yuv420p10le"},
			append(pq, "-f", "hevc", out)...)...)
		return out
	}
	gen81 := func(name string, n int) string {
		cfg, rpu := w(name+".json"), w(name+".bin")
		must(t, os.WriteFile(cfg, []byte(fmt.Sprintf(`{"cm_version":"V40","profile":"8.1","length":%d,"level6":{"max_display_mastering_luminance":1000,"min_display_mastering_luminance":1,"max_content_light_level":1000,"max_frame_average_light_level":400}}`, n)), 0o644))
		tool("dovi_tool", "generate", "-j", cfg, "-o", rpu)
		return rpu
	}
	inject := func(es, rpu, name string) string {
		out := w(name)
		tool("dovi_tool", "inject-rpu", "-i", es, "--rpu-in", rpu, "-o", out)
		return out
	}
	// toLibrary wraps a raw stream as a library film with an English stereo track. dovi
	// writes the container's Dolby Vision record, as a real release has.
	toLibrary := func(es, title string, dovi bool) string {
		mp4 := es + ".mp4"
		args := []string{"-r", "24", "-i", es, "-c", "copy"}
		if dovi {
			args = append(args, "-bsf:v", "dovi_rpu=compression=none", "-strict", "unofficial")
		}
		ff(append(args, "-tag:v", "hvc1", mp4)...)
		path := filepath.Join(lib, title+" (2020).mkv")
		ff("-i", mp4, "-f", "lavfi", "-i", "sine=f=440:d=20", "-map", "0:v", "-map", "1:a", "-c:v", "copy", "-c:a", "aac",
			"-metadata:s:a:0", "language=eng", path)
		return path
	}
	addMovie := func(id int, title, path string) {
		_, err := db.Exec(`INSERT INTO movies (id, tmdb_id, title, monitored, has_file, movie_file_path) VALUES (?, ?, ?, 1, 1, ?)`, id, 1000+id, title, path)
		must(t, err)
		must(t, s.IndexMovie(ctx, int64(id)))
	}
	rpuSummaryOf := func(path string) rpuSummary {
		t.Helper()
		rpu := path + ".check.rpu"
		defer os.Remove(rpu)
		if err := s.extractRPU(ctx, path, 0, rpu, "", 0); err != nil {
			t.Fatalf("reading %s's RPU: %v", filepath.Base(path), err)
		}
		sum, err := s.rpuInfo(ctx, rpu)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}

	// 1. Profile 8.1 over HDR10.
	base := baseES("base.hevc", "640x360", 20)
	p8 := toLibrary(inject(base, gen81("p8", frames), "p8.hevc"), "DV Eight", true)
	if mi := mustProbe(t, p8); mi.HDR != "Dolby Vision" || mi.DVProfile != 8 || mi.DVCompat != 1 || mi.DVBase != "HDR10" {
		t.Fatalf("P8 source probed as %s profile %d compat %d base %s", mi.HDR, mi.DVProfile, mi.DVCompat, mi.DVBase)
	}
	addMovie(1, "DV Eight", p8)

	// 2. Profile 7 FEL: the HDR10 base, a quarter-size enhancement layer carrying the RPU,
	//    muxed into one stream as a disc's is.
	el := w("el.hevc")
	ff(append([]string{"-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=20", "-c:v", "libx265", "-preset", "ultrafast", "-crf", "30",
		"-x265-params", "log-level=0:pools=none:bframes=0", "-pix_fmt", "yuv420p10le"}, "-f", "hevc", el)...)
	written := func(name string, fel, extBlocks bool, n int) string {
		rpus := make([][]byte, n)
		for i := range rpus {
			rpus[i] = syntheticRPU(fel, extBlocks, i == 0)
		}
		path := w(name)
		must(t, os.WriteFile(path, rpuFile(rpus), 0o644))
		return path
	}
	rpu7 := written("p7.bin", true, true, frames)
	if sum, err := s.rpuInfo(ctx, rpu7); err != nil || sum.profile != 7 || sum.frames != frames {
		t.Fatalf("the synthetic profile 7 RPU reads as %+v (%v)", sum, err)
	}
	p7es := w("p7.hevc")
	tool("dovi_tool", "mux", "--bl", base, "--el", inject(el, rpu7, "el.rpu.hevc"), "-o", p7es)
	p7 := toLibrary(p7es, "DV Seven", false)
	if mi := mustProbe(t, p7); mi.HDR != "Dolby Vision" || mi.DVBase != "HDR10" {
		t.Fatalf("P7 source probed as %s base %s", mi.HDR, mi.DVBase)
	}
	if sum := rpuSummaryOf(p7); sum.profile != 7 {
		t.Fatalf("the P7 source's RPU reads as profile %d", sum.profile)
	}
	addMovie(2, "DV Seven", p7)

	// 3. Profile 8.1 over an HDR10+ base.
	var scenes []string
	for i := 0; i < frames; i++ {
		scenes = append(scenes, fmt.Sprintf(`{"LuminanceParameters":{"AverageRGB":1000,"LuminanceDistributions":{"DistributionIndex":[1,5,10,25,50,75,90,95,99],"DistributionValues":[10,50,100,500,1000,2000,4000,6000,9000]},"MaxScl":[9000,9000,9000]},"NumberOfWindows":1,"TargetedSystemDisplayMaximumLuminance":0,"SceneFrameIndex":%d,"SceneId":0,"SequenceFrameIndex":%d}`, i, i))
	}
	h10json := w("h10p.json")
	must(t, os.WriteFile(h10json, []byte(`{"JSONInfo":{"HDR10plusProfile":"A","Version":"1.0"},"SceneInfo":[`+strings.Join(scenes, ",")+`],"SceneInfoSummary":{"SceneFirstFrameIndex":[0],"SceneFrameNumbers":[480]},"ToolInfo":{"Tool":"hdr10plus_tool","Version":"1.7.2"}}`), 0o644))
	h10es := w("h10p.hevc")
	tool("hdr10plus_tool", "inject", "-i", baseES("base2.hevc", "640x360", 20), "-j", h10json, "-o", h10es)
	both := toLibrary(inject(h10es, gen81("both", frames), "both.hevc"), "DV Plus", true)
	if mi := mustProbe(t, both); mi.HDR != "Dolby Vision" || mi.DVBase != "HDR10+" {
		t.Fatalf("DV+HDR10+ source probed as %s base %s", mi.HDR, mi.DVBase)
	}
	addMovie(3, "DV Plus", both)

	// 4. Metadata on only the first half of the frames: two encodes joined, one with RPUs.
	first := inject(baseES("half1.hevc", "640x360", 10), gen81("half", frames/2), "half1.inj.hevc")
	a, err := os.ReadFile(first)
	must(t, err)
	b, err := os.ReadFile(baseES("half2.hevc", "640x360", 10))
	must(t, err)
	halfES := w("half.hevc")
	must(t, os.WriteFile(halfES, append(a, b...), 0o644))
	half := toLibrary(halfES, "DV Half", false)
	halfInfo, err := os.Stat(half)
	must(t, err)
	addMovie(4, "DV Half", half)

	// 5. Bare profile 8.1 RPUs with no extension blocks (out of spec — every real grade has
	//    L1). ffmpeg's rewrite in the remux mangles these; the output gate must notice.
	bare := toLibrary(inject(baseES("bare.hevc", "640x360", 20), written("bare.bin", false, false, frames), "bare.inj.hevc"), "DV Bare", false)
	bareInfo, err := os.Stat(bare)
	must(t, err)
	addMovie(5, "DV Bare", bare)

	keep := DVKeep
	if _, err := s.UpdateSettings(ctx, SettingsPatch{DolbyVision: &keep}); err != nil {
		t.Fatal(err)
	}
	set(t, s, map[string]string{keyAuto: "true"})
	go s.Run(ctx)

	waitDone := func(id int64) Job {
		t.Helper()
		deadline := time.Now().Add(10 * time.Minute)
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
		t.Fatalf("movie %d didn't finish", id)
		return Job{}
	}
	kept := func(id int64, path, wantBase string) {
		t.Helper()
		j := waitDone(id)
		if j.State != StateDone {
			for _, l := range s.Logs() {
				t.Log(l.Msg)
			}
			t.Fatalf("movie %d: %s — %s", id, j.State, j.Note)
		}
		out := mustProbe(t, path)
		if out.VideoCodec != "hevc" || out.HDR != "Dolby Vision" || out.DVProfile != 8 || out.DVCompat != 1 || out.DVBase != wantBase ||
			out.HDR10 == nil || out.HDR10.MaxCLL != "1000,400" || out.AudioTracks != 1 {
			t.Errorf("movie %d: want HEVC Dolby Vision 8.1 over %s with its mastering data and audio, got %s %s profile %d compat %d base %s meta %+v audio %d",
				id, wantBase, out.VideoCodec, out.HDR, out.DVProfile, out.DVCompat, out.DVBase, out.HDR10, out.AudioTracks)
		}
		if sum := rpuSummaryOf(path); sum.profile != 8 || sum.frames != frames {
			t.Errorf("movie %d: the output's RPU is profile %d with %d frames, want 8 with %d", id, sum.profile, sum.frames, frames)
		}
		t.Logf("movie %d (%s): %s — %s → %s, SSIM %.4f; output %s profile %d.%d over %s",
			id, j.Title, j.Encoder, humanBytes(j.SrcBytes), humanBytes(j.OutBytes), j.SSIM, out.HDR, out.DVProfile, out.DVCompat, out.DVBase)
	}

	kept(1, p8, "HDR10")
	kept(2, p7, "HDR10")
	kept(3, both, "HDR10+")

	j := waitDone(4)
	kind := ""
	_ = db.QueryRow(`SELECT kind FROM convert_skips WHERE item_key = ?`, movieKey(4)).Scan(&kind)
	if j.State != StateSkipped || kind != SkipHDRUnsupported || !strings.Contains(j.Note, "doesn't line up") {
		t.Fatalf("half-covered film: %s (%s) — %s, want an hdr_unsupported skip for the mismatch", j.State, kind, j.Note)
	}
	if fi, err := os.Stat(half); err != nil || fi.Size() != halfInfo.Size() || !fi.ModTime().Equal(halfInfo.ModTime()) {
		t.Fatalf("the half-covered original was touched: %v", err)
	}
	t.Logf("movie 4 (DV Half): %s — %s", j.State, j.Note)

	// The bare RPUs: never swapped in with metadata that doesn't read back. Should a later
	// ffmpeg write them faithfully, the conversion is simply good.
	j = waitDone(5)
	switch j.State {
	case StateDone:
		if sum := rpuSummaryOf(bare); sum.profile != 8 || sum.frames != frames {
			t.Fatalf("bare RPUs swapped in, reading as profile %d with %d frames", sum.profile, sum.frames)
		}
		t.Logf("movie 5 (DV Bare): converted — this ffmpeg writes bare RPUs faithfully")
	case StateSkipped:
		if fi, err := os.Stat(bare); err != nil || fi.Size() != bareInfo.Size() || !fi.ModTime().Equal(bareInfo.ModTime()) {
			t.Fatalf("the bare-RPU original was touched: %v", err)
		}
		t.Logf("movie 5 (DV Bare): %s — %s", j.State, j.Note)
	default:
		t.Fatalf("bare RPUs: %s — %s", j.State, j.Note)
	}

	var conversions []string
	for _, l := range s.Logs() {
		if strings.Contains(l.Msg, "Dolby Vision") {
			conversions = append(conversions, l.Msg)
		}
	}
	t.Logf("Dolby Vision log lines:\n  %s", strings.Join(conversions, "\n  "))
	if !strings.Contains(strings.Join(conversions, "\n"), "DV Seven: Dolby Vision profile 7 — converting its metadata to profile 8.1") {
		t.Error("the profile 7 film should say it was converted to 8.1")
	}
}
