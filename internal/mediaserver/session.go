package mediaserver

import "fmt"

// Session is one active playback stream, flattened into the fields Insights cares about.
// It started as Plex's /status/sessions row (internal/plex still calls it plex.Session);
// a Jellyfin /Sessions row is mapped onto the same fields. The raw payloads are far larger;
// we keep what drives the Activity view, the recorder, and the stream deep-dive.
type Session struct {
	// Server is which media server the stream is on. Plex's client leaves it empty, which
	// means Plex: rows recorded before Jellyfin existed have none.
	Server Kind

	SessionKey string
	RatingKey  string
	Type       string // movie | episode | track
	Title      string
	ShowTitle  string // grandparentTitle
	SeasonName string // parentTitle
	Index      int    // episode number
	SeasonNum  int    // parentIndex
	Year       int
	Thumb      string // the item's own image: an episode's landscape still, a movie's poster
	DurationMS int64
	OffsetMS   int64

	// The show an episode belongs to: its portrait poster and rating key ("" for movies).
	ShowThumb     string
	ShowRatingKey string
	// Art is the background image (the item's art, else the show's).
	Art string

	UserID    string
	UserName  string
	UserThumb string

	Address    string // player LAN/local address
	PublicIP   string // remotePublicAddress (for geolocation)
	Device     string
	Platform   string
	Product    string
	PlayerName string
	State      string // playing | paused | buffering
	Local      bool

	Bandwidth int64  // kbps
	Location  string // lan | wan

	// Transcode (zero-value when direct playing).
	Transcoding   bool
	VideoDecision string // copy | transcode
	AudioDecision string // copy | transcode
	SubDecision   string // "", copy, transcode, burn
	// TranscodeHW is whether the GPU is doing any of the work (decode or encode). The HW*
	// fields say which part: Plex reports what it actually uses, which can differ from
	// what was asked for — with hardware transcoding switched on, Plex still falls back to
	// the CPU when the GPU can't take a stream, and only HWRequested stays true.
	TranscodeHW    bool
	HWRequested    bool
	HWDecode       bool
	HWEncode       bool
	HWFullPipeline bool   // decode, filters and encode all on the GPU
	HWTitle        string // Plex's name for the hardware in use ("Intel (QuickSync)"), encoder first
	Throttled      bool
	TranscodeSpeed float64 // transcode throughput vs realtime; < 1.0 = falling behind
	TranscodeProto string
	TranscodeCont  string

	// Source media specs (from Media[0]).
	SrcResolution string
	SrcVideoCodec string
	SrcAudioCodec string
	SrcContainer  string
	SrcBitrate    int64
	// The source video stream's bit depth (8, 10; 0 = not reported) and codec profile
	// ("main 10", "high"), from Media[0].Part[0]'s video Stream: Convert's devices panel
	// tells HEVC 10-bit from 8-bit with them.
	SrcVideoBitDepth int
	SrcVideoProfile  string

	// The server's names for the playing streams of the source file: "4K DoVi/HDR10 (HEVC
	// Main 10)", "English (DTS-HD MA 7.1)", "English (SRT)"; "" when not reported (no
	// subtitle when none is on).
	SrcVideoTitle string
	SrcAudioTitle string
	SubTitle      string

	// The player's hardware and app, beyond its name: "Apple TV 6,2", "tvOS 26.6", the
	// app's version. Relayed: Plex is carrying the stream through its relay (slow, capped).
	DeviceModel     string
	PlatformVersion string
	AppVersion      string
	Relayed         bool
	Secure          bool

	// Transcode target specs (from TranscodeSession; empty when direct playing).
	// StreamResolution is the converted picture's ("720p") when the height isn't reported.
	StreamVideoCodec    string
	StreamAudioCodec    string
	StreamAudioChannels int
	StreamWidth         int
	StreamHeight        int
	StreamResolution    string
	// BufferedSec is how far the converted stream runs ahead of the playhead (Plex; 0 =
	// not reported). TranscodeReasons are Jellyfin's own codes ("AudioCodecNotSupported").
	BufferedSec      float64
	TranscodeReasons []string
}

// BufferCause classifies the likely reason a stream stalled, from the transcode and
// network signals in the session. Plex reports no explicit reason, so this is a
// best-effort diagnosis returning (cause key, human-readable detail).
func (s Session) BufferCause() (cause, detail string) {
	// A transcoder that can't keep up (throughput < realtime) is the clearest, most
	// common culprit — and it tells you whether hardware transcoding is helping.
	// The encoder is what sets the pace, so it decides which story this is: a GPU encode
	// falling behind, a GPU Plex was asked to use but didn't, or a CPU-only setup.
	if s.Transcoding && s.TranscodeSpeed > 0 && s.TranscodeSpeed < 0.95 {
		switch {
		case s.HWEncode:
			return "transcode", fmt.Sprintf("hardware transcode falling behind (%.1f× realtime)", s.TranscodeSpeed)
		case s.HWRequested:
			return "transcode_fallback", fmt.Sprintf("Plex fell back to CPU — hardware transcoding was requested but not used (%.1f× realtime)", s.TranscodeSpeed)
		}
		return "transcode_cpu", fmt.Sprintf("CPU transcode too slow (%.1f× realtime) — hardware transcoding not in use", s.TranscodeSpeed)
	}
	// Remote direct play whose reserved bandwidth is under the source bitrate → network.
	if !s.Local && !s.Transcoding && s.Bandwidth > 0 && s.SrcBitrate > 0 && s.Bandwidth < s.SrcBitrate {
		return "bandwidth", fmt.Sprintf("remote direct play — ~%d kbps available vs %d kbps source", s.Bandwidth, s.SrcBitrate)
	}
	if !s.Local {
		return "bandwidth", "remote stream — likely limited by the client's connection"
	}
	if s.Transcoding {
		return "transcode", "transcoding — the server was momentarily busy"
	}
	return "unknown", "no clear cause — a brief source-read or network hiccup"
}

// Decision classifies the stream as direct_play / direct_stream / transcode.
func (s Session) Decision() string {
	if !s.Transcoding {
		return "direct_play"
	}
	if s.VideoDecision == "copy" && s.AudioDecision == "copy" {
		return "direct_stream"
	}
	return "transcode"
}
