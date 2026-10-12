package subtitles

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real-tool sync checks: synthetic media made with ffmpeg, put through the same
// extraction and checker the download rung uses. They need ffmpeg (the app image's), so
// they run only with ARRMADA_FFMPEG_IT set — never on the owner's media.

func needFFmpegIT(t *testing.T) {
	t.Helper()
	if os.Getenv("ARRMADA_FFMPEG_IT") == "" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 inside the app image to run real-tool tests")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
}

func ffmpegIT(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// checkIT extracts a video's audio the way a job does and checks an SRT against it.
func checkIT(t *testing.T, video string, srts map[string]string) map[string]struct {
	res    SyncResult
	judged bool
} {
	t.Helper()
	s := &Service{ffmpeg: "ffmpeg"}
	a := s.newJobAudio(video, nil)
	defer a.close()
	track, err := a.track(context.Background(), -1)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]struct {
		res    SyncResult
		judged bool
	}{}
	for name, srt := range srts {
		res, judged := track.CheckJudged([]byte(srt))
		out[name] = struct {
			res    SyncResult
			judged bool
		}{res, judged}
	}
	return out
}

// Tone bursts at known times, made by ffmpeg's lavfi sources and muxed into a video with
// AAC audio: an SRT of the bursts 3 s late is kept and shifted, one in sync is kept as
// is, and an unrelated one is refused.
func TestSyncToneBurstsIT(t *testing.T) {
	needFFmpegIT(t)
	dir := t.TempDir()
	dur := 5 * time.Minute
	bursts := synthSpeech(21, dur)
	var on []string
	for _, b := range bursts {
		on = append(on, fmt.Sprintf("between(t,%.3f,%.3f)", b.start.Seconds(), b.end.Seconds()))
	}
	expr := strings.Join(on, "+")
	video := filepath.Join(dir, "Tones (2020).mkv")
	ffmpegIT(t, "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=160x90:r=24000/1001:d=%d", int(dur.Seconds())),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=f=900:sample_rate=48000:d=%d,volume='0.5*(%s)':eval=frame", int(dur.Seconds()), expr),
		"-f", "lavfi", "-i", fmt.Sprintf("anoisesrc=c=pink:a=0.01:r=48000:d=%d", int(dur.Seconds())),
		"-filter_complex", "[1:a][2:a]amix=inputs=2:normalize=0[a]", "-map", "0:v", "-map", "[a]",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-ac", "2", video)
	got := checkIT(t, video, map[string]string{
		"late":      formatSRT(shiftCues(subtitleFor(bursts, 4), 3000, 1)),
		"in sync":   formatSRT(subtitleFor(bursts, 5)),
		"unrelated": formatSRT(subtitleFor(synthSpeech(404, dur), 6)),
	})
	for name, g := range got {
		t.Logf("tones %-9s %+v judged=%v", name, g.res, g.judged)
	}
	if v := judgeSync(got["late"].res, got["late"].judged, false); !v.keep || !v.shift || abs(got["late"].res.OffsetMs-3000) > 250 {
		t.Errorf("late: %+v → %+v", got["late"].res, v)
	}
	if v := judgeSync(got["in sync"].res, got["in sync"].judged, false); !v.keep || v.shift {
		t.Errorf("in sync: %+v → %+v", got["in sync"].res, v)
	}
	if v := judgeSync(got["unrelated"].res, got["unrelated"].judged, false); v.keep || !v.block {
		t.Errorf("unrelated: %+v → %+v", got["unrelated"].res, v)
	}
}

// --- speech-like calibration ---

// filmLayout lays out a film's dialogue: scenes of conversation (utterances of 0.6-4.5 s
// with 0.2-2.5 s between them) separated by 8-90 s without any.
func filmLayout(seed int64, dur time.Duration) []cue {
	rng := rand.New(rand.NewSource(seed))
	var out []cue
	at := time.Duration(5+rng.Intn(30)) * time.Second
	for at < dur {
		scene := at + time.Duration(25+rng.Intn(120))*time.Second
		for at < scene {
			end := at + time.Duration(600+rng.Intn(3900))*time.Millisecond
			if end > dur-2*time.Second {
				return out
			}
			out = append(out, cue{start: at, end: end, text: "line"})
			at = end + time.Duration(200+rng.Intn(2300))*time.Millisecond
		}
		at += time.Duration(8+rng.Intn(82)) * time.Second
	}
	return out
}

// subtitlesFor turns utterances into a subtitle the way people time them: the cue comes
// in around the first word and stays up a little after the last for reading, long lines
// are split into two cues, a few lines are never subtitled, and a few cues are signs or
// song lyrics with nobody speaking.
func subtitlesFor(utts []cue, seed int64, dur time.Duration) []cue {
	rng := rand.New(rand.NewSource(seed))
	ms := func(lo, hi int) time.Duration { return time.Duration(lo+rng.Intn(hi-lo+1)) * time.Millisecond }
	var out []cue
	for i, u := range utts {
		if rng.Intn(100) < 8 {
			continue
		}
		start := u.start + ms(-150, 100)
		end := u.end + ms(0, 700)
		if i+1 < len(utts) && end > utts[i+1].start-50*time.Millisecond {
			end = utts[i+1].start - 50*time.Millisecond
		}
		if end-start > 3500*time.Millisecond {
			mid := start + (end-start)/2
			out = append(out, cue{start: start, end: mid, text: "a"}, cue{start: mid + ms(0, 80), end: end, text: "b"})
		} else {
			out = append(out, cue{start: start, end: end, text: "line"})
		}
		if rng.Intn(100) < 3 { // a sign or a lyric somewhere with no speech
			at := time.Duration(rng.Int63n(int64(dur - 10*time.Second)))
			out = append(out, cue{start: at, end: at + ms(1000, 3000), text: "♪"})
		}
	}
	sortCues(out)
	return out
}

func sortCues(cs []cue) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].start < cs[j-1].start; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

// mix is how loud everything that isn't dialogue is.
type mix struct {
	name    string
	music   float64 // music bed amplitude relative to speech (≈ 0.5 peak)
	musicN  int     // music cues per 10 minutes
	effects int     // effect bursts per 10 minutes
	noise   float64 // room tone
}

// synthFilm writes 16 kHz mono PCM of speech-like sound for the utterances — a glottal
// pulse train at a wandering pitch through three formant resonators, syllable by
// syllable, with breathy fricatives — over a music bed and effect bursts that nobody
// subtitles.
func synthFilm(path string, dur time.Duration, utts []cue, m mix, seed int64) error {
	const rate = 16000
	n := int(dur.Seconds() * rate)
	buf := make([]float64, n) // speech first, scaled to a dialogue level, then the rest
	rng := rand.New(rand.NewSource(seed))

	vowels := [][3]float64{{730, 1090, 2440}, {270, 2290, 3010}, {530, 1840, 2480}, {660, 1720, 2410}, {300, 870, 2240}, {570, 840, 2410}, {440, 1020, 2240}}
	gains := [3]float64{1, 0.6, 0.3}
	for _, u := range utts {
		f0 := 90 + rng.Float64()*150
		pos := int(u.start.Seconds() * rate)
		end := int(u.end.Seconds() * rate)
		for pos < end {
			syl := int((0.11 + rng.Float64()*0.22) * rate)
			if pos+syl > end {
				syl = end - pos
			}
			v := vowels[rng.Intn(len(vowels))]
			res := [3]*biquad{newBandpass(rate, v[0], 6), newBandpass(rate, v[1], 9), newBandpass(rate, v[2], 12)}
			fric := rng.Intn(4) == 0
			hp := newBiquad(rate, 2500, true)
			pitch := f0 * (0.9 + rng.Float64()*0.2)
			phase := 0.0
			for i := 0; i < syl && pos+i < n; i++ {
				env := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(syl))
				phase += pitch * (1 + 0.03*math.Sin(float64(i)/400)) / rate
				src := 0.0
				if phase >= 1 {
					phase -= 1
					src = 1 // a glottal pulse
				}
				x := 0.0
				for k, r := range res {
					x += r.step(src) * gains[k]
				}
				if fric && i < syl/3 {
					x += 0.15 * hp.step(rng.Float64()*2-1)
				}
				buf[pos+i] += 0.35 * env * x
			}
			pos += syl + int(rng.Float64()*0.04*rate)
		}
	}
	// Dialogue at about -18 dBFS RMS while someone speaks.
	var ss float64
	var sn int
	for _, u := range utts {
		for i := int(u.start.Seconds() * rate); i < int(u.end.Seconds()*rate) && i < n; i++ {
			ss += buf[i] * buf[i]
			sn++
		}
	}
	if sn > 0 && ss > 0 {
		g := 0.12 / math.Sqrt(ss/float64(sn))
		for i := range buf {
			buf[i] *= g
		}
	}
	// Music: chords of a few saw-ish tones that swell and fade, 5-40 s long, anywhere.
	mins := dur.Minutes() / 10
	for c := 0; c < int(float64(m.musicN)*mins); c++ {
		start := rng.Intn(n)
		length := int((5 + rng.Float64()*35) * rate)
		root := 110 * math.Pow(2, float64(rng.Intn(24))/12)
		notes := []float64{root, root * 1.26, root * 1.5, root * 2}
		for i := 0; i < length && start+i < n; i++ {
			env := math.Min(1, math.Min(float64(i)/(2*rate), float64(length-i)/(2*rate)))
			x := 0.0
			for _, f := range notes {
				ph := math.Mod(f*float64(i)/rate, 1)
				x += (ph*2 - 1) * 0.25
			}
			buf[start+i] += m.music * env * x * (1 + 0.2*math.Sin(2*math.Pi*5*float64(i)/rate))
		}
	}
	// Effects: broadband bursts (a door, a crash, traffic).
	for e := 0; e < int(float64(m.effects)*mins); e++ {
		start := rng.Intn(n)
		length := int((0.15 + rng.Float64()*1.8) * rate)
		amp := 0.2 + rng.Float64()*0.5
		for i := 0; i < length && start+i < n; i++ {
			buf[start+i] += amp * math.Exp(-3*float64(i)/float64(length)) * (rng.Float64()*2 - 1)
		}
	}
	pcm := make([]byte, 2*n)
	for i, x := range buf {
		x += m.noise * (rng.Float64()*2 - 1)
		x = math.Max(-1, math.Min(1, x))
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(x*32000)))
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// newBandpass is an RBJ constant-peak band-pass (a formant).
func newBandpass(rate int, freq, q float64) *biquad {
	w := 2 * math.Pi * freq / float64(rate)
	alpha := math.Sin(w) / (2 * q)
	a0 := 1 + alpha
	return &biquad{b0: alpha / a0, b1: 0, b2: -alpha / a0, a1: -2 * math.Cos(w) / a0, a2: (1 - alpha) / a0}
}

// TestSyncCalibrationIT measures the checker's confidence on speech-like films with
// known offsets and frame-rate drifts, plus unrelated subtitles, under three mixes from
// clean to music-heavy, through a real AAC round trip and the job's own extraction. It
// logs every number (the thresholds in syncgate.go were set from them) and asserts the
// decisions that must hold.
func TestSyncCalibrationIT(t *testing.T) {
	needFFmpegIT(t)
	dir := t.TempDir()
	dur := 20 * time.Minute
	ntsc := 24000.0 / 1001
	mixes := []mix{
		{name: "clean", music: 0.05, musicN: 2, effects: 4, noise: 0.003},
		{name: "film", music: 0.15, musicN: 8, effects: 15, noise: 0.006},
		{name: "music-heavy", music: 0.3, musicN: 20, effects: 30, noise: 0.01},
		// Score louder than the dialogue nearly throughout, constant crowd/rain noise.
		{name: "wall-to-wall", music: 0.6, musicN: 45, effects: 60, noise: 0.05},
	}
	for mi, m := range mixes {
		utts := filmLayout(int64(100+mi), dur)
		wav := filepath.Join(dir, m.name+".wav")
		if err := synthFilm(wav, dur, utts, m, int64(7+mi)); err != nil {
			t.Fatal(err)
		}
		video := filepath.Join(dir, m.name+".mkv")
		ffmpegIT(t, "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=160x90:r=24000/1001:d=%d", int(dur.Seconds())),
			"-i", wav, "-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-shortest", video)
		sub := subtitlesFor(utts, int64(50+mi), dur)
		other := subtitlesFor(filmLayout(int64(900+mi), dur), int64(60+mi), dur)
		other2 := subtitlesFor(filmLayout(int64(1900+mi), dur), int64(70+mi), dur)
		half := len(sub) / 2
		cut := append(append([]cue{}, sub[:half]...), shiftCues(sub[half:], 6000, 1)...)
		// Sloppy timing: every cue edge off by up to ±0.6 s, as a hand-made subtitle can be.
		jr := rand.New(rand.NewSource(int64(80 + mi)))
		var sloppy []cue
		for _, c := range sub {
			j := func() time.Duration { return time.Duration(jr.Intn(1201)-600) * time.Millisecond }
			s, e := c.start+j(), c.end+j()
			if e > s {
				sloppy = append(sloppy, cue{start: s, end: e, text: c.text})
			}
		}
		cases := map[string]string{
			"sloppy +2s":      formatSRT(shiftCues(sloppy, 2000, 1)),
			"aligned":         formatSRT(sub),
			"+3.0s":           formatSRT(shiftCues(sub, 3000, 1)),
			"-1.5s":           formatSRT(shiftCues(sub, -1500, 1)),
			"+47s":            formatSRT(shiftCues(sub, 47000, 1)),
			"25fps+1.2s":      formatSRT(shiftCues(sub, 1200, ntsc/25)),
			"24fps":           formatSRT(shiftCues(sub, 0, ntsc/24)),
			"unrelated":       formatSRT(other),
			"unrelated2":      formatSRT(other2),
			"other cut (+6s)": formatSRT(cut),
		}
		want := map[string]int{"aligned": 0, "+3.0s": 3000, "-1.5s": -1500, "+47s": 47000}
		got := checkIT(t, video, cases)
		for _, name := range []string{"aligned", "+3.0s", "-1.5s", "+47s", "25fps+1.2s", "24fps", "sloppy +2s", "other cut (+6s)", "unrelated", "unrelated2"} {
			g := got[name]
			v := judgeSync(g.res, g.judged, false)
			verdict := "refuse"
			switch {
			case v.keep && v.shift:
				verdict = "keep+shift"
			case v.keep:
				verdict = "keep"
			}
			t.Logf("CALIBRATION %-11s %-16s offset=%6dms scale=%.4f confidence=%3d coverage=%.2f → %s",
				m.name, name, g.res.OffsetMs, g.res.Scale, g.res.Confidence, g.res.Coverage, verdict)
			if strings.HasPrefix(name, "unrelated") {
				if v.keep {
					t.Errorf("%s/%s: an unrelated subtitle was kept (%+v)", m.name, name, g.res)
				}
				continue
			}
			if off, ok := want[name]; ok && (m.name == "clean" || m.name == "film") {
				if !v.keep || abs(g.res.OffsetMs-off) > 300 {
					t.Errorf("%s/%s: %+v → %s", m.name, name, g.res, verdict)
				}
			}
		}
		if g := got["25fps+1.2s"]; (m.name == "clean" || m.name == "film") && math.Abs(g.res.Scale-25/ntsc) > 0.001 {
			t.Errorf("%s: PAL-timed subtitle measured scale %v", m.name, g.res.Scale)
		}
	}
}
