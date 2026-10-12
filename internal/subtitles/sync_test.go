package subtitles

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// synthSpeech lays out utterances (0.8-4 s, gaps 0.3-3 s) over dur, deterministically
// from seed — a stand-in for a film's dialogue.
func synthSpeech(seed int64, dur time.Duration) []cue {
	rng := rand.New(rand.NewSource(seed))
	var out []cue
	at := 2 * time.Second
	for {
		at += time.Duration(300+rng.Intn(2700)) * time.Millisecond
		end := at + time.Duration(800+rng.Intn(3200))*time.Millisecond
		if end > dur {
			return out
		}
		out = append(out, cue{start: at, end: end, text: "line"})
		at = end
	}
}

// subtitleFor turns the true utterances into a plausible subtitle: a little timing
// jitter on every edge and some lines never subtitled (shouts, background voices).
func subtitleFor(speech []cue, seed int64) []cue {
	rng := rand.New(rand.NewSource(seed))
	var out []cue
	for _, c := range speech {
		if rng.Intn(10) == 0 {
			continue
		}
		j := func() time.Duration { return time.Duration(rng.Intn(81)-40) * time.Millisecond }
		out = append(out, cue{start: c.start + j(), end: c.end + j(), text: c.text})
	}
	return out
}

func envOf(cues []cue, dur time.Duration) []float32 {
	return cueEnvelope(cues, int(dur/syncFrame))
}

func TestAlignOffset(t *testing.T) {
	dur := 30 * time.Minute
	speech := synthSpeech(1, dur)
	for _, offMs := range []int{2350, -1500, 0, 47000} {
		sub := shiftCues(subtitleFor(speech, 7), offMs, 1)
		res := align(envOf(speech, dur), cueEnvelope(sub, 0))
		t.Logf("offset %d ms: %+v", offMs, res)
		if d := res.OffsetMs - offMs; d < -20 || d > 20 {
			t.Errorf("offset %d ms: got %d ms", offMs, res.OffsetMs)
		}
		if res.Scale != 1 {
			t.Errorf("offset %d ms: scale %v, want 1", offMs, res.Scale)
		}
		if res.Confidence < 80 {
			t.Errorf("offset %d ms: confidence %d, want ≥ 80 (%+v)", offMs, res.Confidence, res)
		}
		if res.Coverage < 0.9 {
			t.Errorf("offset %d ms: coverage %v", offMs, res.Coverage)
		}
		if res.NeedsFix(300) != (offMs > 300 || offMs < -300) {
			t.Errorf("offset %d ms: NeedsFix(300) = %v", offMs, res.NeedsFix(300))
		}
	}
}

// A subtitle timed for the 25 fps release on a 23.976 fps file runs 4% fast; the checker
// finds the stretch and the corrected cues land on the speech across the whole film.
func TestAlignDrift(t *testing.T) {
	dur := 60 * time.Minute
	speech := synthSpeech(3, dur)
	truth := subtitleFor(speech, 9)
	ntsc := 24000.0 / 1001
	sub := shiftCues(truth, 1200, ntsc/25) // PAL-timed, and a second late on top
	res := align(envOf(speech, dur), cueEnvelope(sub, 0))
	if math.Abs(res.Scale-25/ntsc) > 0.0005 {
		t.Fatalf("scale %v, want ≈ %.4f (%+v)", res.Scale, 25/ntsc, res)
	}
	if res.Confidence < 70 {
		t.Errorf("confidence %d (%+v)", res.Confidence, res)
	}
	fixed := res.Fix(sub)
	if len(fixed) != len(truth) {
		t.Fatalf("fixed %d cues, want %d", len(fixed), len(truth))
	}
	for i := range fixed {
		if d := fixed[i].start - truth[i].start; d > 100*time.Millisecond || d < -100*time.Millisecond {
			t.Fatalf("cue %d at %v lands %v off", i, truth[i].start, d)
		}
	}
}

// A drift no frame-rate ratio explains (a 0.2% stretch, 11 s over the film) is caught by
// the windows.
func TestAlignOddDrift(t *testing.T) {
	dur := 90 * time.Minute
	speech := synthSpeech(5, dur)
	truth := subtitleFor(speech, 11)
	sub := shiftCues(truth, 400, 1/1.002)
	res := align(envOf(speech, dur), cueEnvelope(sub, 0))
	fixed := res.Fix(sub)
	worst := time.Duration(0)
	for i := range fixed {
		d := fixed[i].start - truth[i].start
		if d < 0 {
			d = -d
		}
		worst = max(worst, d)
	}
	if worst > 150*time.Millisecond {
		t.Errorf("worst cue %v off after fix (%+v)", worst, res)
	}
}

func TestAlignRejectsUnrelated(t *testing.T) {
	dur := 45 * time.Minute
	speech := synthSpeech(1, dur)
	for seed := int64(2); seed < 7; seed++ {
		other := subtitleFor(synthSpeech(seed*101, dur), seed)
		res := align(envOf(speech, dur), cueEnvelope(other, 0))
		t.Logf("seed %d: %+v", seed, res)
		if res.Confidence >= 30 {
			t.Errorf("seed %d: unrelated subtitle scored %d (%+v)", seed, res.Confidence, res)
		}
	}
}

func TestAlignTooFewCues(t *testing.T) {
	dur := 10 * time.Minute
	speech := synthSpeech(1, dur)
	res := align(envOf(speech, dur), cueEnvelope(speech[:5], 0))
	if res.Confidence != 0 || res.Scale != 1 || res.OffsetMs != 0 {
		t.Errorf("five cues judged: %+v", res)
	}
	if res := align(nil, cueEnvelope(speech, 0)); res.Confidence != 0 {
		t.Errorf("no audio judged: %+v", res)
	}
}

func TestShiftCuesRoundTrip(t *testing.T) {
	in := parseSRT(sampleSRT)
	if back := shiftCues(shiftCues(in, 2350, 1), -2350, 1); !reflect.DeepEqual(back, in) {
		t.Errorf("offset round trip: %+v, want %+v", back, in)
	}
	back := shiftCues(shiftCues(in, 0, 25.0/24), 0, 24.0/25)
	if len(back) != len(in) {
		t.Fatalf("%d cues after round trip, want %d", len(back), len(in))
	}
	for i := range in {
		if d := back[i].start - in[i].start; d < -2*time.Millisecond || d > 2*time.Millisecond {
			t.Errorf("cue %d: %v, want %v", i, back[i].start, in[i].start)
		}
	}
	// A cue pushed wholly before zero is dropped; one straddling zero starts at zero.
	early := shiftCues(in, -2600, 1)
	if len(early) != len(in)-1 || early[0].start != 0 || early[0].end != 400*time.Millisecond {
		t.Errorf("shift before zero: %+v", early)
	}
}

const sampleSRT = "1\r\n00:00:01,000 --> 00:00:02,500\r\n<i>First line</i>\r\nsecond line\r\n\r\n" +
	"2\r\n00:00:02,000 --> 00:00:03,000 X1:100 X2:200\r\nNext\r\n\r\n" +
	"3\r\n00:01:00,250 --> 00:01:02,000\r\nLast\r\n"

// shiftSRT moves the timings exactly as shiftCues would and leaves everything else —
// line breaks, CRLF, tags, numbering, position hints — byte for byte.
func TestShiftSRTKeepsLayout(t *testing.T) {
	got := shiftSRT(sampleSRT, 1000, 1)
	want := strings.NewReplacer(
		"00:00:01,000 --> 00:00:02,500", "00:00:02,000 --> 00:00:03,500",
		"00:00:02,000 --> 00:00:03,000", "00:00:03,000 --> 00:00:04,000",
		"00:01:00,250 --> 00:01:02,000", "00:01:01,250 --> 00:01:03,000",
	).Replace(sampleSRT)
	if got != want {
		t.Fatalf("shiftSRT:\n%q\nwant\n%q", got, want)
	}
	for _, sc := range []struct {
		ms    int
		scale float64
	}{{-3000, 1}, {700, 25.0 / 24}, {0, 24.0 / 25}} {
		a := parseSRT(shiftSRT(sampleSRT, sc.ms, sc.scale))
		b := shiftCues(parseSRT(sampleSRT), sc.ms, sc.scale)
		// shiftSRT clamps rather than drops; compare the cues that survive.
		a = a[len(a)-len(b):]
		if !reflect.DeepEqual(a, b) {
			t.Errorf("shift %d×%v: shiftSRT %+v, shiftCues %+v", sc.ms, sc.scale, a, b)
		}
	}
}

func TestWriteShifted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Movie.en.srt")
	if err := os.WriteFile(path, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeShifted(path, -1000, 1); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "00:00:00,000 --> 00:00:01,500\r\n<i>First line</i>\r\nsecond line") {
		t.Errorf("not shifted:\n%s", raw)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestExtractWAVArgs(t *testing.T) {
	got := strings.Join(extractWAVArgs("/m/Movie.mkv", 1, "/tmp/a.wav"), " ")
	if got != "-y -hide_banner -i /m/Movie.mkv -map 0:a:1 -vn -ac 1 -ar 16000 -c:a pcm_s16le /tmp/a.wav" {
		t.Errorf("args: %s", got)
	}
	got = strings.Join(extractWAVArgs("/m/Movie.mkv", -1, "/tmp/a.wav"), " ")
	if got != "-y -hide_banner -i /m/Movie.mkv -vn -ac 1 -ar 16000 -c:a pcm_s16le /tmp/a.wav" {
		t.Errorf("default stream args: %s", got)
	}
}

// writeToneWAV writes 16 kHz mono PCM: quiet noise throughout, and a 1 kHz tone
// (inside the voice band) during each burst.
func writeToneWAV(t *testing.T, path string, dur time.Duration, bursts []cue) {
	t.Helper()
	const rate = 16000
	n := int(dur.Seconds() * rate)
	pcm := make([]byte, 2*n)
	rng := rand.New(rand.NewSource(42))
	bi := 0
	for i := 0; i < n; i++ {
		at := time.Duration(i) * time.Second / rate
		for bi < len(bursts) && bursts[bi].end <= at {
			bi++
		}
		v := float64(rng.Intn(200) - 100)
		if bi < len(bursts) && at >= bursts[bi].start {
			v += 8000 * math.Sin(2*math.Pi*1000*float64(i)/rate)
		}
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(v)))
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("LIST")
	binary.Write(&b, binary.LittleEndian, uint32(4)) // an extra chunk ffmpeg sometimes adds
	b.WriteString("INFO")
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// End to end from a WAV: the envelope finds the tone bursts, and an SRT of the bursts
// running a second late is measured as a second late.
func TestSpeechTrackFromWAV(t *testing.T) {
	dur := 4 * time.Minute
	bursts := synthSpeech(8, dur)
	wav := filepath.Join(t.TempDir(), "a.wav")
	writeToneWAV(t, wav, dur, bursts)

	env, err := speechEnvelope(wav)
	if err != nil {
		t.Fatal(err)
	}
	if want := int(dur / syncFrame); len(env) != want {
		t.Fatalf("%d frames, want %d", len(env), want)
	}
	for _, c := range bursts {
		mid := int((c.start + c.end) / 2 / syncFrame)
		if env[mid] != 1 {
			t.Errorf("burst at %v not marked as speech", c.start)
		}
		// Past the hangover (and the filter's ring-down) the frame is quiet again.
		after := int((c.end + 300*time.Millisecond) / syncFrame)
		next := c.end + 300*time.Millisecond
		quiet := true
		for _, d := range bursts {
			if d.start <= next+10*time.Millisecond && d.end > c.end {
				quiet = false
			}
		}
		if quiet && after < len(env) && env[after] != 0 {
			t.Errorf("frame 300 ms after the burst ending %v still speech", c.end)
		}
	}

	srt := formatSRT(shiftCues(bursts, 1000, 1))
	res, err := checkSRTSync(wav, []byte(srt))
	if err != nil {
		t.Fatal(err)
	}
	// The 200 ms hangover makes each speech run a little longer than its cue; the fit
	// centres the cue in it, so allow for that.
	if res.OffsetMs < 850 || res.OffsetMs > 1050 || res.Confidence < 70 {
		t.Errorf("tone WAV: %+v, want ≈ 1000 ms late, confident", res)
	}

	if _, err := speechEnvelope(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Error("missing WAV read without error")
	}
	bad := filepath.Join(t.TempDir(), "bad.wav")
	_ = os.WriteFile(bad, []byte("RIFF\x00\x00\x00\x00AVI LIST"), 0o644)
	if _, err := speechEnvelope(bad); err == nil {
		t.Error("non-WAV read without error")
	}
}

func BenchmarkAlign2h(b *testing.B) {
	dur := 2 * time.Hour
	speech := envOf(synthSpeech(1, dur), dur)
	sub := cueEnvelope(shiftCues(subtitleFor(synthSpeech(1, dur), 2), 3100, 1), 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		align(speech, sub)
	}
}
