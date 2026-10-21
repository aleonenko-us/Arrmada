package insights

import (
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// The Apple TV case: 4K Dolby Vision converted to 720p on the home network, and DTS-HD
// sound it can't play. The reasons name both, and the health line says it keeps up.
func TestEnrichExplainsTranscode(t *testing.T) {
	s := &Service{}
	p := plex.Session{Type: "movie", Title: "The Nightmare Before Christmas", State: "playing", Local: true, Location: "lan",
		Transcoding: true, VideoDecision: "transcode", AudioDecision: "transcode", SubDecision: "copy",
		HWDecode: true, HWEncode: true, HWRequested: true, Throttled: true, BufferedSec: 400, Bandwidth: 3794,
		SrcResolution: "4k", SrcVideoCodec: "hevc", SrcAudioCodec: "dca", StreamResolution: "720p", StreamVideoCodec: "hevc",
		StreamAudioCodec: "opus", StreamAudioChannels: 6, SrcVideoTitle: "4K DoVi/HDR10 (HEVC Main 10)",
		SrcAudioTitle: "English (DTS-HD MA 7.1)", SubTitle: "English (SRT)",
		DeviceModel: "Apple TV 6,2", Platform: "tvOS", PlatformVersion: "26.6", Product: "Plex for Apple TV", AppVersion: "2026.20.0"}
	st := s.enrich(p)
	if len(st.Reasons) != 2 || !strings.HasPrefix(st.Reasons[0], "The app's quality is set below the original: 4K → 720p, about 3.8 Mb/s") ||
		st.Reasons[1] != "The player can't play DTS-HD MA 7.1 — converting the sound to OPUS 5.1" {
		t.Errorf("reasons = %q", st.Reasons)
	}
	if st.Video.Src != "4K DoVi/HDR10 (HEVC Main 10)" || st.Video.Stream != "HEVC 720p" || st.Audio.Stream != "OPUS 5.1" || st.Subtitles.Src != "English (SRT)" || st.Subtitles.Stream != "" {
		t.Errorf("details = %+v %+v %+v", st.Video, st.Audio, st.Subtitles)
	}
	if st.Device != "Apple TV 6,2 · tvOS 26.6 · Plex for Apple TV 2026.20.0" {
		t.Errorf("device = %q", st.Device)
	}
	if st.Health.Level != "warn" || st.Health.Text != "Converting the video — keeping up, 6 min ready ahead" {
		t.Errorf("health = %+v", st.Health)
	}

	// Remote and lowered: the remote setting or the connection, not the home one.
	p.Local, p.Location = false, "wan"
	if r := s.enrich(p).Reasons; !strings.HasPrefix(r[0], "Quality lowered for the remote connection") {
		t.Errorf("remote reasons = %q", r)
	}
	// A transcode that can't keep up is the worst news, ahead of anything else.
	p.Throttled, p.TranscodeSpeed = false, 0.7
	if h := s.enrich(p).Health; h.Level != "bad" || !strings.Contains(h.Text, "0.7×") {
		t.Errorf("slow health = %+v", h)
	}
	// Direct play needs no reasons and costs the server nothing.
	if d := s.enrich(plex.Session{Type: "movie", State: "playing", SrcVideoCodec: "hevc", SrcResolution: "4k"}); d.Reasons != nil || d.Health.Level != "good" || d.Video.Src != "HEVC 4K" {
		t.Errorf("direct = %+v", d)
	}
	// Burned-in subtitles say so on the subtitle row and in the reasons.
	b := s.enrich(plex.Session{Type: "movie", Transcoding: true, VideoDecision: "transcode", AudioDecision: "copy", SubDecision: "burn", SubTitle: "English (PGS)"})
	if b.Subtitles.Stream != "burned in" || !strings.HasPrefix(b.Reasons[len(b.Reasons)-1], "Burning in subtitles (English (PGS))") {
		t.Errorf("burn = %+v %q", b.Subtitles, b.Reasons)
	}
}

// Jellyfin's own reason codes come first, in plain English, without the guesses.
func TestJellyfinReasons(t *testing.T) {
	p := plex.Session{Transcoding: true, VideoDecision: "copy", AudioDecision: "transcode", SrcAudioCodec: "truehd", StreamAudioCodec: "aac", StreamAudioChannels: 2,
		TranscodeReasons: []string{"AudioCodecNotSupported", "AudioProfileNotSupported", "SomethingNew"}}
	r := transcodeReasons(p)
	if len(r) != 2 || r[0] != "The player can't play this sound format" || r[1] != "Converting audio (TRUEHD → AAC stereo)" {
		t.Errorf("reasons = %q", r)
	}
}
