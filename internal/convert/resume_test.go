package convert

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The resumable pieced encode's orchestration (chunk.go), driven through process() with
// shell-script stand-ins for ffmpeg and ffprobe: what runs, in what order, with which
// arguments — and what doesn't run. The real encodes are TestResumablePiecedEncodeIT.

// resumeRig is a spaceRig whose ffmpeg writes its output file and logs its arguments, and
// whose ffprobe reports every piece whole. The film is two hours at 23.976 fps, its video a
// frame after its audio, cut into four 30-minute pieces.
type resumeRig struct {
	*spaceRig
	src   string
	mi    *MediaInfo
	calls string
}

const rigPieceFrames = 43157 // 1800 s at 24000/1001

func newResumeRig(t *testing.T) *resumeRig {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("uses shell-script stand-ins for ffmpeg and ffprobe")
	}
	r := &resumeRig{spaceRig: newSpaceRig(t)}
	r.src = r.addMovie(t, 1, "Film", 64)
	r.mi = film("h264", 1920, 1080, 35000, aud("ac3", "eng", 6))
	r.mi.FrameRateRat, r.mi.VideoStartSec = "24000/1001", 0.042
	stubProbe(t, r.mi)
	stubDisks(t, r.scratch, 1<<50, 1<<50)
	old := chunkSeconds
	chunkSeconds = 1800
	t.Cleanup(func() { chunkSeconds = old })

	bin := t.TempDir()
	r.calls = filepath.Join(bin, "calls")
	script := func(name, body string) string {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho \""+name+" $*\" >> "+r.calls+"\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	r.ffmpeg = script("ffmpeg", "for a; do last=$a; done\ncase \"$last\" in -|pipe:*) ;; *) printf x > \"$last\" ;; esac\nexit 0\n")
	r.ffprobe = script("ffprobe", "echo "+strconv.Itoa(rigPieceFrames)+"\n")
	set(t, r.Service, map[string]string{keyTenBit: TenBitAlways})
	return r
}

// settled is the plan a first run settled: HEVC at a quality the defaults wouldn't pick.
func (r *resumeRig) settled(t *testing.T) Plan {
	t.Helper()
	plan, _ := r.prefs(context.Background()).planFor(r.mi, r.src, "", nil)
	plan.VideoCodec, plan.Quality = "hevc", maxQualityCRF("hevc", r.mi)-3
	return plan
}

// interrupted writes the resume folder a restart left: the manifest for plan and the first
// two of four pieces, the first a sparse gigabyte so it counts in the space checks.
func (r *resumeRig) interrupted(t *testing.T, plan Plan) string {
	t.Helper()
	dir := resumeDir(r.scratch, movieKey(1), r.src, r.mi, cpuEncoder("hevc"), plan, "mkv")
	fi, err := os.Stat(r.src)
	if err != nil {
		t.Fatal(err)
	}
	resumeFixture(t, dir, &resumeManifest{Version: 1, Key: movieKey(1), Src: r.src, Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano(),
		Kind: "mkv", Codec: plan.VideoCodec, CRF: plan.Quality, Encoder: "libx265", PixFmt: outputPixFmt(r.mi, plan),
		Pieces: 4, Seconds: chunkSeconds, ActiveSec: 3600}, "chunk-0001.mkv", "chunk-0002.mkv")
	if err := os.Truncate(filepath.Join(dir, "chunk-0001.mkv"), 1<<30); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (r *resumeRig) callLines() []string {
	b, _ := os.ReadFile(r.calls)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (r *resumeRig) logged(sub string) bool {
	for _, l := range r.Logs() {
		if strings.Contains(l.Msg, sub) {
			return true
		}
	}
	return false
}

func argAfter(line, flag string) string {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == flag {
			return f[i+1]
		}
	}
	return ""
}

// An encode a restart interrupted carries on from its first missing piece with the plan it
// had settled: no format test, no test encodes, the same quality, frame-exact seeks, and a
// join that restores the video's start offset. The pieces already done count in the space
// check and the ledger's encoding time.
func TestResumeCarriesOnWithoutTests(t *testing.T) {
	r := newResumeRig(t)
	// The format test would run (AV1 allowed) and the film is long enough to rehearse.
	set(t, r.Service, map[string]string{keyResumable: "true", keyAllowAV1: "true"})
	plan := r.settled(t)
	dir := r.interrupted(t, plan)
	// One byte short of the whole pieced need: it only fits because a gigabyte is done.
	stubDisks(t, r.scratch, uint64(scratchNeeded(r.mi, plan, true)-1), 1<<50)

	job := r.run(t, 1, "Film")
	if kind, _, _ := r.skipOf(t, movieKey(1)); kind == SkipNoScratch {
		t.Fatalf("the finished pieces were counted against the scratch space: %s", job.Note)
	}
	for _, l := range r.callLines() {
		if strings.Contains(l, "trial-") {
			t.Errorf("a test encode ran on resume: %s", l)
		}
	}
	for _, msg := range []string{"Testing HEVC and AV1", "Test-encoding"} {
		if r.logged(msg) {
			t.Errorf("logged %q on resume — the tests were settled before the restart", msg)
		}
	}
	if !r.logged("resuming from piece 3 of 4") {
		t.Error("the log should say where the encode carries on")
	}
	if !r.logged("HEVC at CRF " + strconv.Itoa(plan.Quality)) {
		t.Error("the log should say it carries on with the settled plan")
	}

	fd := 1001.0 / 24000
	var pieces []string
	var join string
	for _, l := range r.callLines() {
		switch {
		case strings.HasPrefix(l, "ffmpeg ") && strings.Contains(l, ".part"):
			pieces = append(pieces, l)
		case strings.Contains(l, "-f concat"):
			join = l
		}
	}
	if len(pieces) != 2 || !strings.Contains(pieces[0], "chunk-0003.mkv.part") || !strings.Contains(pieces[1], "chunk-0004.mkv.part") {
		t.Fatalf("want pieces 3 and 4 encoded, got:\n%s", strings.Join(pieces, "\n"))
	}
	wantSS := strconv.FormatFloat(0.042+(float64(2*rigPieceFrames)-0.5)*fd, 'f', 6, 64)
	if got := argAfter(pieces[0], "-ss"); got != wantSS {
		t.Errorf("piece 3 seeks to %s, want half a frame before its first frame (%s)", got, wantSS)
	}
	if got := argAfter(pieces[0], "-frames:v"); got != strconv.Itoa(rigPieceFrames) {
		t.Errorf("piece 3 holds %s frames, want %d", got, rigPieceFrames)
	}
	if got := argAfter(pieces[1], "-frames:v"); got != "" {
		t.Errorf("the last piece runs to the end, but it was given %s frames", got)
	}
	for _, p := range pieces {
		if argAfter(p, "-fps_mode") != "passthrough" || !strings.HasPrefix(argAfter(p, "-vf"), "setpts=PTS-STARTPTS") {
			t.Errorf("a piece must pass every frame through once, its clock starting at its first frame: %s", p)
		}
	}
	if got := argAfter(pieces[0], "-crf"); got != strconv.Itoa(plan.Quality) {
		t.Errorf("piece 3 encoded at CRF %s, want the settled %d", got, plan.Quality)
	}
	if got := argAfter(join, "-itsoffset"); got != "0.042000" {
		t.Errorf("the join should restore the video's start offset: %q in %s", got, join)
	}
	// The fake output can't pass the quality check, so the job ends there — and with it the
	// pieces, which are no use to anything after an outcome.
	if job.State == StateDone {
		t.Fatalf("the stand-in output passed the quality check?")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the job is over: its pieces should be gone")
	}
	var secs float64
	if err := r.db.QueryRow(`SELECT encode_secs FROM convert_history WHERE item_key = ? ORDER BY id DESC LIMIT 1`, movieKey(1)).Scan(&secs); err != nil {
		t.Fatal(err)
	}
	if secs < 3600 {
		t.Errorf("ledger encode_secs = %.0f, want the hour spent before the restart included", secs)
	}
}

// Pieces of a file that has changed, or settled under settings that have, are discarded
// and the film is encoded from the start.
func TestResumeDiscardsStalePieces(t *testing.T) {
	for name, change := range map[string]func(*resumeRig){
		"file changed": func(r *resumeRig) {
			if err := os.WriteFile(r.src, []byte(strings.Repeat("y", 65)), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"pixel format changed": func(r *resumeRig) {
			set(t, r.Service, map[string]string{keyTenBit: TenBitMatchSource})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newResumeRig(t)
			set(t, r.Service, map[string]string{keyResumable: "true"})
			preflightMinDuration = 1e9 // the rehearsal isn't what this is about
			t.Cleanup(func() { preflightMinDuration = 20 * 60 })
			r.interrupted(t, r.settled(t))
			change(r)
			r.run(t, 1, "Film")
			if !r.logged("discarded 2 encoded pieces from before") {
				t.Error("the stale pieces should be discarded, and the log say so")
			}
			var first string
			for _, l := range r.callLines() {
				if strings.HasPrefix(l, "ffmpeg ") && strings.Contains(l, ".part") {
					first = l
					break
				}
			}
			if !strings.Contains(first, "chunk-0001.mkv.part") || argAfter(first, "-ss") != "" {
				t.Errorf("the encode should start again from frame one: %s", first)
			}
		})
	}
}

// With the setting off nothing changes: pieces left from when it was on are dropped, the
// film is encoded in one go with its size cap, and the space it needs is the usual figure.
func TestResumableOffEncodesInOneGo(t *testing.T) {
	r := newResumeRig(t)
	preflightMinDuration = 1e9
	t.Cleanup(func() { preflightMinDuration = 20 * 60 })
	r.interrupted(t, r.settled(t))
	job := r.run(t, 1, "Film")
	if r.resumableKeys(context.Background())[movieKey(1)] {
		t.Error("leftover pieces should be dropped with the setting off")
	}
	if r.record(job).resume != nil {
		t.Error("the job shouldn't run with resumable pieces")
	}
	encodes := 0
	for _, l := range r.callLines() {
		if strings.Contains(l, ".part") || strings.Contains(l, "-frames:v") || strings.Contains(l, "-f concat") {
			t.Errorf("a piece ran with the setting off: %s", l)
		}
		if strings.Contains(l, "convert-") && strings.Contains(l, "-progress") {
			encodes++
			if argAfter(l, "-fs") == "" {
				t.Errorf("the one-go encode should carry its size cap: %s", l)
			}
		}
	}
	if encodes == 0 {
		t.Error("the film should have been encoded in one go")
	}
	if got, want := r.jobScratchNeed(context.Background(), job, r.mi, r.settled(t), false, "cpu"), scratchNeeded(r.mi, r.settled(t), false); got != want {
		t.Errorf("scratch need %d, want the usual %d", got, want)
	}
}

// The Settings indicator's scratch figure counts a pieced encode's two copies, less the
// pieces already done.
func TestScratchNeedCountsPieces(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	s.scratchDir = t.TempDir()
	mi := film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8))
	mi.FrameRateRat = "24000/1001"
	indexMovie(t, s, 1, "Remux", mi)
	off, _ := s.ScratchNeed(ctx)

	set(t, s, map[string]string{keyResumable: "true"})
	s.invalidateLibraryCache()
	on, _ := s.ScratchNeed(ctx)
	if on <= off {
		t.Fatalf("pieced need %d should be more than the one-go %d", on, off)
	}
	dir := filepath.Join(s.scratchDir, "resume-a")
	resumeFixture(t, dir, &resumeManifest{Version: 1, Key: movieKey(1), Pieces: 4}, "chunk-0001.mkv")
	if err := os.Truncate(filepath.Join(dir, "chunk-0001.mkv"), 1<<30); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ScratchNeed(ctx); got != on-(1<<30) {
		t.Fatalf("with a gigabyte done the need is %d, want %d", got, on-(1<<30))
	}
	if done, total := s.resumePieces(ctx, movieKey(1)); done != 1 || total != 4 {
		t.Errorf("resumePieces = %d of %d", done, total)
	}
}
