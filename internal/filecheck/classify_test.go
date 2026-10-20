package filecheck

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func codes(fs []Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return strings.Join(out, ",")
}

func TestClassify(t *testing.T) {
	loud := []float64{-3.2, -1.0, -6.5}
	home := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		in     Input
		p      Probe
		want   string
		detail string
	}{
		{"healthy", Input{Kind: KindMovie, RuntimeMin: 120}, Probe{AudioLangs: []string{"eng"}, DurationSec: 7150, Loudness: loud, Windows: 3}, "", ""},
		{"ffprobe failed", Input{}, Probe{Err: errors.New("invalid data")}, CodeUnreadable, "the file can't be read"},
		{"no audio stream", Input{}, Probe{DurationSec: 7000}, CodeNoAudio, "the file has no sound"},
		{"silent in every window", Input{}, Probe{AudioLangs: []string{"eng"}, DurationSec: 7000, Loudness: []float64{-91, -91, -200}, Windows: 3}, CodeSilentAudio, "the sound track is silent"},
		{"one loud window is not silent", Input{}, Probe{AudioLangs: []string{"eng"}, DurationSec: 7000, Loudness: []float64{-91, -12, -91}, Windows: 3}, "", ""},
		{"an unmeasured window is not silent", Input{}, Probe{AudioLangs: []string{"eng"}, DurationSec: 7000, Loudness: []float64{-91, -91}, Windows: 3}, "", ""},
		{"russian only", Input{OrigLanguage: "en", Wanted: []string{"en"}}, Probe{AudioLangs: []string{"rus"}, DurationSec: 7000, Loudness: loud, Windows: 3}, CodeWrongLanguage, "audio is in Russian only"},
		{"russian and ukrainian", Input{OrigLanguage: "en"}, Probe{AudioLangs: []string{"rus", "ukr"}, DurationSec: 7000}, CodeWrongLanguage, "audio is in Russian and Ukrainian only"},
		{"original language is fine", Input{OrigLanguage: "ja", Wanted: []string{"en"}}, Probe{AudioLangs: []string{"jpn"}, DurationSec: 7000}, "", ""},
		{"a wanted language among others", Input{OrigLanguage: "en", Wanted: []string{"de"}}, Probe{AudioLangs: []string{"rus", "ger"}, DurationSec: 7000}, "", ""},
		{"untagged audio is never wrong", Input{OrigLanguage: "en"}, Probe{AudioLangs: []string{"rus", "und"}, DurationSec: 7000}, "", ""},
		{"empty tag is never wrong", Input{OrigLanguage: "en"}, Probe{AudioLangs: []string{""}, DurationSec: 7000}, "", ""},
		{"cut short", Input{Kind: KindMovie, RuntimeMin: 120}, Probe{AudioLangs: []string{"eng"}, DurationSec: 40 * 60}, CodeUnreadable, "the file is only 40 minutes long but the film runs 120"},
		{"no length", Input{}, Probe{AudioLangs: []string{"eng"}}, CodeUnreadable, "the file has no readable length"},
		{"named a cam", Input{Release: "Film.2026.HDCAM.x264-GRP"}, Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, CodeSuspectedCam, "probably a cam — the release says so"},
		{"cam allowed by the profile", Input{Release: "Film.2026.HDCAM.x264-GRP", CamOK: true}, Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, "", ""},
		{"web-dl grabbed 3 weeks early", Input{Kind: KindMovie, Release: "Resident.Evil.2026.1080p.WEB-DL.x264-GRP", Home: home, GrabbedAt: home.Add(-21 * 24 * time.Hour)},
			Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, CodeSuspectedCam, "probably a cam — grabbed 3 weeks before it was out at home"},
		{"web-dl grabbed inside the tolerance", Input{Kind: KindMovie, Release: "Resident.Evil.2026.1080p.WEB-DL.x264-GRP", Home: home, GrabbedAt: home.Add(-2 * 24 * time.Hour)},
			Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, "", ""},
		{"filename stands in for the release", Input{Kind: KindMovie, Filename: "Resident Evil (2026) - WEBDL-1080p.mkv", Home: home, GrabbedAt: home.Add(-10 * 24 * time.Hour)},
			Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, CodeSuspectedCam, "probably a cam — grabbed 10 days before"},
		{"no home date, grabbed 33 days after cinemas", Input{Kind: KindMovie, Release: "Resident Evil 2026 1080p WEB DL AAC x264 rothebehap", Theatrical: home, GrabbedAt: home.Add(33 * 24 * time.Hour)},
			Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, CodeSuspectedCam, "probably a cam — grabbed while it was only in cinemas"},
		{"no home date, grabbed long after cinemas", Input{Kind: KindMovie, Release: "Resident.Evil.2026.1080p.WEB-DL.x264-GRP", Theatrical: home, GrabbedAt: home.Add(90 * 24 * time.Hour)},
			Probe{AudioLangs: []string{"eng"}, DurationSec: 7000}, "", ""},
		{"episodes aren't judged by home release", Input{Kind: KindEpisode, Release: "Show.S01E01.1080p.WEB-DL.x264-GRP", Home: home, GrabbedAt: home.Add(-30 * 24 * time.Hour)},
			Probe{AudioLangs: []string{"eng"}, DurationSec: 3000}, "", ""},
	}
	for _, tc := range cases {
		got := Classify(tc.in, tc.p)
		if codes(got) != tc.want {
			t.Errorf("%s: findings %q, want %q (%+v)", tc.name, codes(got), tc.want, got)
			continue
		}
		if tc.detail != "" && !strings.HasPrefix(got[0].Detail, tc.detail) {
			t.Errorf("%s: detail %q, want it to start %q", tc.name, got[0].Detail, tc.detail)
		}
	}
}

func TestParseStreams(t *testing.T) {
	out := `{"streams": [
	  {"codec_type": "video", "codec_name": "h264"},
	  {"codec_type": "audio", "codec_name": "eac3", "tags": {"language": "rus"}},
	  {"codec_type": "audio", "codec_name": "aac"},
	  {"codec_type": "subtitle", "tags": {"language": "eng"}}],
	  "format": {"duration": "7212.480000"}}`
	p, err := ParseStreams([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AudioLangs) != 2 || p.AudioLangs[0] != "rus" || p.AudioLangs[1] != "" || int(p.DurationSec) != 7212 {
		t.Fatalf("probe = %+v", p)
	}
	if _, err := ParseStreams([]byte(`{"streams": [], "format": {}}`)); err == nil {
		t.Error("a file with no streams parsed")
	}
	if _, err := ParseStreams([]byte(`not json`)); err == nil {
		t.Error("garbage parsed")
	}
}

func TestParseMaxVolume(t *testing.T) {
	out := `[Parsed_volumedetect_0 @ 0x55] n_samples: 1920000
[Parsed_volumedetect_0 @ 0x55] mean_volume: -27.3 dB
[Parsed_volumedetect_0 @ 0x55] max_volume: -4.1 dB
`
	if db, ok := ParseMaxVolume([]byte(out)); !ok || db != -4.1 {
		t.Errorf("max = %v, %v", db, ok)
	}
	if db, ok := ParseMaxVolume([]byte("max_volume: -inf dB")); !ok || db >= SilentBelowDB {
		t.Errorf("-inf = %v, %v", db, ok)
	}
	if db, ok := ParseMaxVolume([]byte("max_volume: 0.0 dB")); !ok || db != 0 {
		t.Errorf("0 dB = %v, %v", db, ok)
	}
	if _, ok := ParseMaxVolume([]byte("Output file is empty, nothing was encoded")); ok {
		t.Error("no measurement read as one")
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{
		24 * time.Hour: "1 day", 5 * 24 * time.Hour: "5 days", 21 * 24 * time.Hour: "3 weeks", 75 * 24 * time.Hour: "2 months",
	} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
}
