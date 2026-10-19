package stage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Splitting real images (MUS-35): synthetic lavfi audio only, made here. The proof is
// independent of the code under test: each piece, decoded, must be exactly its slice of
// the decoded image, byte for byte. ARRMADA_FFMPEG_IT=1 only.

// itPCM decodes a file to raw PCM (s16le, or s32le for deeper audio).
func itPCM(t *testing.T, path string, bits int) []byte {
	t.Helper()
	f := "s16le"
	if bits > 16 {
		f = "s32le"
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0", "-c:a", "pcm_"+f, "-f", f, "-").Output()
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return out
}

func itProbe(t *testing.T, path, entries string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", entries, "-of", "default=nw=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return string(out)
}

func TestSplitRealImages(t *testing.T) {
	requireIT(t)
	for _, c := range []struct {
		name, ext string
		rate      int
		bits      int
		codec     []string
	}{
		{"flac 16-bit", ".flac", 44100, 16, []string{"-sample_fmt", "s16", "-c:a", "flac"}},
		{"flac 24-bit 96k", ".flac", 96000, 24, []string{"-sample_fmt", "s32", "-bits_per_raw_sample", "24", "-c:a", "flac"}},
		{"wav", ".wav", 44100, 16, []string{"-c:a", "pcm_s16le"}},
		{"wav 24-bit", ".wav", 48000, 24, []string{"-c:a", "pcm_s24le"}},
		{"wavpack", ".wv", 44100, 16, []string{"-sample_fmt", "s16p", "-c:a", "wavpack"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			img := filepath.Join(w.dl, "Album", "Album"+c.ext)
			if err := os.MkdirAll(filepath.Dir(img), 0o755); err != nil {
				t.Fatal(err)
			}
			// 9.5 seconds of stereo noise.
			args := []string{"-f", "lavfi", "-i", "anoisesrc=d=9.5:c=pink:a=0.4:r=" + strconv.Itoa(c.rate), "-ac", "2"}
			itFFmpeg(t, append(append(args, c.codec...), img)...)
			before := snap(t, img)
			// Cue points 00:03:12 and 00:06:40 (frames), in samples at this rate.
			frame := int64(c.rate / 75)
			cuts := []int64{(3*75 + 12) * frame, (6*75 + 40) * frame}
			plan := SplitPlan{Image: img, Cuts: cuts, TotalSamples: int64(9.5 * float64(c.rate)), Rate: c.rate, Channels: 2, Bits: c.bits,
				Tags: []map[string]string{{"title": "One", "track": "1"}, {"title": "Two", "track": "2"}, {"title": "Three", "track": "3"}}}
			job, res, err := Split(context.Background(), w.root, []SplitPlan{plan}, w.deps)
			if err != nil {
				t.Fatal(err)
			}
			pieces := res[0].Files
			if len(pieces) != 3 {
				t.Fatalf("pieces: %v", pieces)
			}
			whole := itPCM(t, img, c.bits)
			width := int64(2 * 2)
			if c.bits > 16 {
				width = 2 * 4
			}
			bounds := []int64{0, cuts[0], cuts[1], int64(len(whole)) / width}
			var joined []byte
			for k, p := range pieces {
				got := itPCM(t, p, c.bits)
				want := whole[bounds[k]*width : bounds[k+1]*width]
				if !bytes.Equal(got, want) {
					t.Errorf("piece %d: %d bytes, not its %d-byte slice of the image", k+1, len(got), len(want))
				}
				if res[0].Samples[k] != bounds[k+1]-bounds[k] {
					t.Errorf("piece %d reported %d samples, want %d", k+1, res[0].Samples[k], bounds[k+1]-bounds[k])
				}
				joined = append(joined, got...)
				facts := itProbe(t, p, "stream=codec_name,sample_rate,bits_per_raw_sample:format_tags=title")
				for _, want := range []string{"codec_name=flac", "sample_rate=" + strconv.Itoa(c.rate),
					"bits_per_raw_sample=" + strconv.Itoa(c.bits), "TAG:title=" + []string{"One", "Two", "Three"}[k]} {
					if !strings.Contains(facts, want) {
						t.Errorf("piece %d: no %q in\n%s", k+1, want, facts)
					}
				}
			}
			if !bytes.Equal(joined, whole) {
				t.Error("the pieces joined aren't the image")
			}
			before.same(t, img)
			// Swapped pieces don't pass the check.
			h := sha256.New()
			if _, _, err := decodePCM(context.Background(), plan, img, h); err != nil {
				t.Fatal(err)
			}
			swapped := &SplitResult{Samples: []int64{res[0].Samples[1], res[0].Samples[0], res[0].Samples[2]}}
			if err := verifyPieces(context.Background(), plan, []string{pieces[1], pieces[0], pieces[2]}, h.Sum(nil), swapped); !errors.Is(err, ErrSplit) {
				t.Errorf("swapped pieces passed: %v", err)
			}
			t.Logf("peak ffmpeg memory: %d KB", res[0].PeakRSSKB)
			if err := job.Abandon("test over"); err != nil {
				t.Fatal(err)
			}
			if jobs := w.stagingJobs(); len(jobs) != 0 {
				t.Errorf("staging left: %v", jobs)
			}
		})
	}
}

// No room for the pieces: nothing is split, nothing is left behind.
func TestSplitRefusesWithoutSpace(t *testing.T) {
	requireIT(t)
	w := newWorld(t)
	img := filepath.Join(w.dl, "Album.flac")
	itFFmpeg(t, "-f", "lavfi", "-i", "anoisesrc=d=3:r=44100", "-ac", "2", "-c:a", "flac", img)
	w.deps.FreeBytes = func(string) (uint64, bool) { return 1 << 20, true }
	_, _, err := Split(context.Background(), w.root, []SplitPlan{{Image: img, Cuts: []int64{44100}, TotalSamples: 3 * 44100, Rate: 44100, Channels: 2, Bits: 16}}, w.deps)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
	if jobs := w.stagingJobs(); len(jobs) != 0 {
		t.Errorf("staging left: %v", jobs)
	}
}

// A FLAC image whose samples don't match the MD5 in its own header is damaged: not split.
// And a cue running past the image's end is caught as the stream ends early.
func TestSplitRefusesBadImages(t *testing.T) {
	requireIT(t)
	w := newWorld(t)
	img := filepath.Join(w.dl, "Album.flac")
	itFFmpeg(t, "-f", "lavfi", "-i", "anoisesrc=d=4:r=44100", "-ac", "2", "-sample_fmt", "s16", "-c:a", "flac", img)
	plan := SplitPlan{Image: img, Cuts: []int64{44100}, Rate: 44100, Channels: 2, Bits: 16}
	if _, _, err := Split(context.Background(), w.root, []SplitPlan{plan}, w.deps); err != nil {
		t.Fatalf("the good image: %v", err)
	}
	// An encoder that fails (here: a layout it can't parse) is a failed split, for Review —
	// not mistaken for a shutdown because the split stopped its own decoder.
	broken := plan
	broken.Layout = "no-such-layout"
	if _, _, err := Split(context.Background(), w.root, []SplitPlan{broken}, w.deps); !errors.Is(err, ErrSplit) {
		t.Errorf("a failing encoder: %v", err)
	}
	past := plan
	past.Cuts = []int64{44100, 10 * 44100}
	if _, _, err := Split(context.Background(), w.root, []SplitPlan{past}, w.deps); !errors.Is(err, ErrSplit) || !strings.Contains(err.Error(), "ends before") {
		t.Errorf("a cut past the end: %v", err)
	}
	b, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	b[8+18] ^= 0xff // STREAMINFO's MD5 starts 18 bytes into the block
	if err := os.WriteFile(img, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Split(context.Background(), w.root, []SplitPlan{plan}, w.deps); !errors.Is(err, ErrSplit) || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a damaged image: %v", err)
	}
	if jobs := w.stagingJobs(); len(jobs) != 1 { // the good split's, still the caller's
		t.Errorf("staging: %v", jobs)
	}
}

// The split streams: however long the image, no ffmpeg process of it holds more than a
// few buffers beyond what ffmpeg needs just to start (measured on the spot: decoding the
// same image to nothing). The filter-graph split this replaced held most of the decoded
// disc: about 500 MB for a CD. Linux reports the peak; elsewhere this only checks the
// split works.
func TestSplitMemoryStaysBounded(t *testing.T) {
	requireIT(t)
	for _, c := range []struct {
		name    string
		seconds int
		rate    int
		bits    int
		codec   []string
	}{
		{"20-minute CD", 1200, 44100, 16, []string{"-sample_fmt", "s16", "-c:a", "flac", "-compression_level", "0"}},
		{"5-minute 24/192", 300, 192000, 24, []string{"-sample_fmt", "s32", "-bits_per_raw_sample", "24", "-c:a", "flac", "-compression_level", "0"}},
	} {
		t.Run(c.name, func(t *testing.T) { splitForMemory(t, c.seconds, c.rate, c.bits, c.codec) })
	}
}

// The reviewer's sizes, for the record (slow: ARRMADA_SPLIT_BIG=1).
func TestSplitMemoryBigImages(t *testing.T) {
	requireIT(t)
	if os.Getenv("ARRMADA_SPLIT_BIG") != "1" {
		t.Skip("set ARRMADA_SPLIT_BIG=1 to split a 74-minute CD and a 35-minute 24/192 image")
	}
	for _, c := range []struct {
		name    string
		seconds int
		rate    int
		bits    int
		codec   []string
	}{
		{"74-minute CD", 4440, 44100, 16, []string{"-sample_fmt", "s16", "-c:a", "flac", "-compression_level", "0"}},
		{"35-minute 24/192", 2100, 192000, 24, []string{"-sample_fmt", "s32", "-bits_per_raw_sample", "24", "-c:a", "flac", "-compression_level", "0"}},
	} {
		t.Run(c.name, func(t *testing.T) { splitForMemory(t, c.seconds, c.rate, c.bits, c.codec) })
	}
}

// splitForMemory makes a noise image of the given length, splits it into 12 pieces and
// checks the split's peak memory against ffmpeg's own baseline for that image.
func splitForMemory(t *testing.T, seconds, rate, bits int, codec []string) {
	t.Helper()
	w := newWorld(t)
	img := filepath.Join(w.dl, "Image.flac")
	args := append([]string{"-f", "lavfi", "-i", "anoisesrc=d=" + strconv.Itoa(seconds) + ":c=pink:a=0.3:r=" + strconv.Itoa(rate), "-ac", "2"}, codec...)
	itFFmpeg(t, append(args, img)...)
	base := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-i", img, "-f", "null", "-")
	if out, err := base.CombinedOutput(); err != nil {
		t.Fatalf("baseline: %v\n%s", err, out)
	}
	baseline := maxRSSKB(base.ProcessState)
	var cuts []int64
	for k := 1; k < 12; k++ {
		cuts = append(cuts, int64(k*seconds/12)*int64(rate))
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	job, res, err := Split(context.Background(), w.root, []SplitPlan{{Image: img, Cuts: cuts, Rate: rate, Channels: 2, Bits: bits}}, w.deps)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	_ = job.Abandon("test over")
	peak := res[0].PeakRSSKB
	t.Logf("peak ffmpeg memory %d MB (ffmpeg decoding it to nothing: %d MB); Go: %d MB allocated in all, heap %d MB after",
		peak>>10, baseline>>10, (after.TotalAlloc-before.TotalAlloc)>>20, after.HeapInuse>>20)
	if peak > baseline+(48<<10) {
		t.Errorf("peak ffmpeg memory %d MB against a %d MB baseline: the split isn't streaming", peak>>10, baseline>>10)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 256<<20 {
		t.Errorf("the split allocated %d MB in Go: it isn't streaming", grew>>20)
	}
}
