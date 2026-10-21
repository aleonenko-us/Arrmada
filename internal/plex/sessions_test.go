package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// sessionsFrom serves a /status/sessions fixture and reads it through the real client.
//
// The fixtures are modelled on Plex's TranscodeSession fields. They are not yet captured
// from the owner's server: replace them with a real capture of one hardware transcode and
// one forced CPU transcode when one is taken (the field types are undocumented).
func sessionsFrom(t *testing.T, fixture string) []Session {
	t.Helper()
	body, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	out, err := New(srv.URL, "test-token").Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 session, got %d", len(out))
	}
	return out
}

// The HW flags come from what Plex reports it is using, not from transcodeHwRequested: a
// CPU fallback with hardware switched on must not read as a hardware transcode.
func TestFlattenHWFields(t *testing.T) {
	hw := sessionsFrom(t, "sessions_hw.json")[0]
	if !hw.HWRequested || !hw.HWDecode || !hw.HWEncode || !hw.HWFullPipeline || !hw.TranscodeHW {
		t.Errorf("hardware transcode: %+v", hw)
	}
	if hw.HWTitle != "Intel (QuickSync)" {
		t.Errorf("hw title = %q", hw.HWTitle)
	}

	cpu := sessionsFrom(t, "sessions_cpu_fallback.json")[0]
	if !cpu.HWRequested || cpu.HWDecode || cpu.HWEncode || cpu.HWFullPipeline || cpu.TranscodeHW || cpu.HWTitle != "" {
		t.Errorf("cpu fallback: requested=%v dec=%v enc=%v full=%v hw=%v title=%q",
			cpu.HWRequested, cpu.HWDecode, cpu.HWEncode, cpu.HWFullPipeline, cpu.TranscodeHW, cpu.HWTitle)
	}
	if !cpu.Transcoding || cpu.StreamWidth != 1280 || cpu.SubDecision != "burn" {
		t.Errorf("the rest of the transcode still reads: %+v", cpu)
	}

	direct := sessionsFrom(t, "sessions_direct.json")[0]
	if direct.Transcoding || direct.HWRequested || direct.TranscodeHW {
		t.Errorf("direct play: %+v", direct)
	}
}

// The source video's bit depth and profile come from the part's video stream (streamType
// 1), whichever order the streams are listed in and however the numbers are encoded.
func TestFlattenSourceBitDepth(t *testing.T) {
	s := sessionsFrom(t, "sessions_hevc10.json")[0]
	if s.SrcVideoBitDepth != 10 || s.SrcVideoProfile != "main 10" || s.SrcVideoCodec != "hevc" {
		t.Errorf("source video = %d-bit %q %q", s.SrcVideoBitDepth, s.SrcVideoProfile, s.SrcVideoCodec)
	}
	// No streams listed: unknown, not 8-bit.
	if d := sessionsFrom(t, "sessions_direct.json")[0]; d.SrcVideoBitDepth != 0 || d.SrcVideoProfile != "" {
		t.Errorf("no streams: %d %q", d.SrcVideoBitDepth, d.SrcVideoProfile)
	}
}

func TestFlexStr(t *testing.T) {
	for in, want := range map[string]bool{
		`"qsv"`: true, `"vaapi"`: true, `true`: true, `1`: true, `"1"`: true,
		`false`: false, `null`: false, `""`: false, `"0"`: false, `0`: false, `{}`: false,
	} {
		var f flexStr
		if err := json.Unmarshal([]byte(in), &f); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if f.on() != want {
			t.Errorf("%s: on = %v, want %v", in, f.on(), want)
		}
	}
}

// Buffering during a CPU fallback blames the fallback, not the GPU; a GPU decode with a
// CPU encode is still a fallback (the encoder sets the pace).
func TestBufferCauseFallback(t *testing.T) {
	cases := []struct {
		name string
		s    Session
		want string
	}{
		{"hw encode slow", Session{Transcoding: true, TranscodeSpeed: 0.8, HWRequested: true, HWDecode: true, HWEncode: true}, "transcode"},
		{"requested, cpu used", Session{Transcoding: true, TranscodeSpeed: 0.7, HWRequested: true}, "transcode_fallback"},
		{"hw decode only", Session{Transcoding: true, TranscodeSpeed: 0.7, HWRequested: true, HWDecode: true}, "transcode_fallback"},
		{"hardware off", Session{Transcoding: true, TranscodeSpeed: 0.7}, "transcode_cpu"},
	}
	for _, c := range cases {
		if got, _ := c.s.BufferCause(); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	got, _ := sessionsFrom(t, "sessions_cpu_fallback.json")[0].BufferCause()
	if got != "transcode_fallback" {
		t.Errorf("captured cpu fallback buffering: got %q", got)
	}
}

// An episode's own thumb is its landscape still; the show's poster, rating key and
// background art come from the grandparent fields, and art falls back to the show's.
func TestFlattenGrandparentThumb(t *testing.T) {
	s := sessionsFrom(t, "sessions_episode.json")[0]
	if s.Thumb != "/library/metadata/8812/thumb/1700000001" {
		t.Errorf("thumb = %q, want the episode still", s.Thumb)
	}
	if s.ShowThumb != "/library/metadata/8800/thumb/1700000000" || s.ShowRatingKey != "8800" {
		t.Errorf("show poster/key = %q / %q", s.ShowThumb, s.ShowRatingKey)
	}
	if s.Art != "/library/metadata/8800/art/1700000000" {
		t.Errorf("art = %q, want the show's art when the episode has none", s.Art)
	}

	movie := sessionsFrom(t, "sessions_direct.json")[0]
	if movie.ShowThumb != "" || movie.ShowRatingKey != "" || movie.Art != "" {
		t.Errorf("a movie has no show fields: %+v", movie)
	}
}

// A real capture (an Apple TV on the home network, 4K Dolby Vision with DTS-HD audio,
// converted on the GPU): while transcoding, Media describes the output, so the source's
// resolution and codecs come from the stream names and the transcode session.
func TestFlattenTranscodeSource(t *testing.T) {
	s := sessionsFrom(t, "sessions_appletv.json")[0]
	if s.SrcResolution != "4k" || s.StreamResolution != "720p" {
		t.Errorf("resolution: source %q, sent %q", s.SrcResolution, s.StreamResolution)
	}
	if s.SrcVideoCodec != "hevc" || s.SrcAudioCodec != "dca" || s.StreamAudioCodec != "opus" || s.StreamAudioChannels != 6 {
		t.Errorf("codecs: %+v", s)
	}
	if s.SrcVideoTitle != "4K DoVi/HDR10 (HEVC Main 10)" || s.SrcAudioTitle != "English (DTS-HD MA 7.1)" || s.SubTitle != "English (SRT)" {
		t.Errorf("titles: %q, %q, %q", s.SrcVideoTitle, s.SrcAudioTitle, s.SubTitle)
	}
	if s.DeviceModel != "Apple TV 6,2" || s.PlatformVersion != "26.6" || s.SrcBitrate != 0 || s.BufferedSec < 60 {
		t.Errorf("device %q, os %q, source bitrate %d, buffered %v", s.DeviceModel, s.PlatformVersion, s.SrcBitrate, s.BufferedSec)
	}
}

func TestTitleResolution(t *testing.T) {
	for in, want := range map[string]string{"4K DoVi/HDR10 (HEVC Main 10)": "4k", "1080p (H.264)": "1080", "720p": "720", "1080i (MPEG2)": "1080", "SD (MPEG4)": "sd", "English (AAC)": "", "": ""} {
		if got := titleResolution(in); got != want {
			t.Errorf("titleResolution(%q) = %q, want %q", in, got, want)
		}
	}
}
