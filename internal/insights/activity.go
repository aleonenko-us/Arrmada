package insights

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/geoip"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plex"
)

// Stream is one live playback session, enriched for the Activity view + deep-dive.
type Stream struct {
	Server      string         `json:"server"` // plex | jellyfin
	SessionKey  string         `json:"session_key"`
	User        string         `json:"user"`
	UserID      string         `json:"user_id"` // the Plex account id the People page is under
	Title       string         `json:"title"`
	Subtitle    string         `json:"subtitle"` // "Show · S1 · E2" or the year
	Type        string         `json:"type"`
	Thumb       string         `json:"thumb"`           // proxied poster URL (the show's, for an episode)
	Still       string         `json:"still,omitempty"` // an episode's own landscape still
	Art         string         `json:"art,omitempty"`   // background art
	ProgressPct int            `json:"progress_pct"`
	OffsetMS    int64          `json:"offset_ms"`
	DurationMS  int64          `json:"duration_ms"`
	State       string         `json:"state"` // playing | paused | buffering
	Player      string         `json:"player"`
	Platform    string         `json:"platform"`
	Product     string         `json:"product"`
	Decision    string         `json:"decision"` // direct_play | direct_stream | transcode
	Bandwidth   int64          `json:"bandwidth_kbps"`
	Location    string         `json:"location"` // lan | wan
	IP          string         `json:"ip"`
	Geo         geoip.Location `json:"geo"`

	// Deep-dive.
	Video       Detail `json:"video"`
	Audio       Detail `json:"audio"`
	Container   Detail `json:"container"`
	HWTranscode bool   `json:"hw_transcode"` // the GPU is doing some of the work (decode or encode)
	HWDecode    bool   `json:"hw_decode"`
	HWEncode    bool   `json:"hw_encode"`
	HWRequested bool   `json:"hw_requested"` // asked for; without HWEncode it means Plex fell back to CPU
	// HWTitle names the hardware: Plex's ("Intel (QuickSync)"), or for Jellyfin the
	// configured acceleration ("vaapi") with HWTranscode set and the three above false —
	// Jellyfin doesn't say which part the GPU did, or whether it took the stream at all.
	HWTitle   string   `json:"hw_title,omitempty"`
	Throttled bool     `json:"throttled"`
	Reasons   []string `json:"reasons"` // why it's transcoding (empty for direct play)

	// Subtitles is the subtitle on screen ("English (SRT)"), with "burned in" or
	// "converted" as its stream when the server does that work; empty when none is on.
	Subtitles Detail `json:"subtitles"`
	// Device is the player's hardware and app beyond its name: "Apple TV 6,2 · tvOS 26.6 ·
	// Plex for Apple TV 2026.20.0".
	Device  string `json:"device,omitempty"`
	Relayed bool   `json:"relayed,omitempty"` // through Plex's relay: slow and capped
	Secure  bool   `json:"secure,omitempty"`
	// Speed is the transcode's throughput against real time (0 = not reported, or resting
	// while it's ahead); BufferedSecs how far the converted stream runs past the playhead.
	Speed        float64 `json:"speed,omitempty"`
	BufferedSecs int     `json:"buffered_secs,omitempty"`
	// Health is one line on how the playback is going, for the person checking on it.
	Health Health `json:"health"`
}

// Health says how a stream is doing: good, warn (it plays, at a cost) or bad (it stalls,
// or will).
type Health struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Detail is a source→stream comparison line for the deep-dive.
type Detail struct {
	Src    string `json:"src"`
	Stream string `json:"stream,omitempty"`
}

// Activity holds the current streams plus aggregate bandwidth.
type Activity struct {
	Streams   []Stream  `json:"streams"`
	Bandwidth Bandwidth `json:"bandwidth"`
	GeoActive bool      `json:"geo_active"` // full city resolution available
	// Errors says which connected server didn't answer (by server), when another did: its
	// streams are missing from the list.
	Errors map[string]string `json:"errors,omitempty"`
}

// Bandwidth is the summed stream bandwidth, split by network location.
type Bandwidth struct {
	TotalKbps int64 `json:"total_kbps"`
	LANKbps   int64 `json:"lan_kbps"`
	WANKbps   int64 `json:"wan_kbps"`
}

// Activity returns the current live streams from the connected servers the filter allows
// (server: "" for both, "plex", "jellyfin"), Plex's first, enriched with geolocation, the
// stream decision, source→stream detail, and human "why it's transcoding" reasons. It asks
// the servers directly, so it works whether or not monitoring is on. One server failing
// leaves its streams out (Errors says so); it's an error only when none answered.
func (s *Service) Activity(ctx context.Context, server string) (Activity, error) {
	srcs := s.liveSources(ctx, server)
	if len(srcs) == 0 {
		return Activity{}, plex.ErrNotConfigured
	}
	out := Activity{Streams: []Stream{}, GeoActive: s.geo.Enabled()}
	var firstErr error
	for _, src := range srcs {
		sessions, err := src.Sessions(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if out.Errors == nil {
				out.Errors = map[string]string{}
			}
			out.Errors[string(src.Kind())] = err.Error()
			continue
		}
		for _, sess := range sessions {
			st := s.enrich(sess)
			st.UserID = sess.UserID
			if src.Kind() == mediaserver.Plex {
				st.UserID = s.knownUserID(ctx, sess.UserID)
			}
			out.Streams = append(out.Streams, st)
			out.Bandwidth.TotalKbps += st.Bandwidth
			if strings.EqualFold(sess.Location, "lan") || st.Geo.Local {
				out.Bandwidth.LANKbps += st.Bandwidth
			} else {
				out.Bandwidth.WANKbps += st.Bandwidth
			}
		}
	}
	if len(out.Errors) == len(srcs) {
		return Activity{}, firstErr
	}
	return out, nil
}

func (s *Service) enrich(p plex.Session) Stream {
	ip := p.PublicIP
	if ip == "" {
		ip = p.Address
	}
	st := Stream{
		Server: string(serverOf(p.Server)), SessionKey: p.SessionKey, User: p.UserName, Title: p.Title, Type: p.Type,
		Thumb: proxyImage(posterPath(p.Type, p.Thumb, p.ShowThumb)), Art: proxyImage(p.Art), OffsetMS: p.OffsetMS, DurationMS: p.DurationMS,
		State: p.State, Player: p.PlayerName, Platform: p.Platform, Product: p.Product,
		Decision: p.Decision(), Bandwidth: p.Bandwidth, Location: p.Location,
		IP: ip, Geo: s.geo.Lookup(ip),
		HWTranscode: p.TranscodeHW, HWDecode: p.HWDecode, HWEncode: p.HWEncode, HWRequested: p.HWRequested,
		HWTitle: p.HWTitle, Throttled: p.Throttled,
	}
	if p.DurationMS > 0 {
		st.ProgressPct = int(p.OffsetMS * 100 / p.DurationMS)
	}
	if p.Type == "episode" && p.ShowThumb != "" {
		st.Still = proxyImage(p.Thumb)
	}
	st.Subtitle = subtitleFor(p)
	st.Video = Detail{Src: firstNonEmpty(p.SrcVideoTitle, mediaLabel(p.SrcVideoCodec, p.SrcResolution))}
	st.Audio = Detail{Src: firstNonEmpty(p.SrcAudioTitle, strings.ToUpper(p.SrcAudioCodec))}
	st.Container = Detail{Src: strings.ToUpper(p.SrcContainer)}
	st.Subtitles = Detail{Src: p.SubTitle}
	if p.Transcoding {
		if p.VideoDecision == "transcode" {
			st.Video.Stream = mediaLabel(p.StreamVideoCodec, outRes(p))
		}
		if p.AudioDecision == "transcode" {
			st.Audio.Stream = audioLabel(p.StreamAudioCodec, p.StreamAudioChannels)
		}
		if p.TranscodeCont != "" {
			st.Container.Stream = strings.ToUpper(p.TranscodeCont)
		}
		if p.SubTitle != "" {
			switch p.SubDecision {
			case "burn":
				st.Subtitles.Stream = "burned in"
			case "transcode":
				st.Subtitles.Stream = "converted"
			}
		}
		st.Speed = p.TranscodeSpeed
		st.BufferedSecs = int(p.BufferedSec)
	}
	st.Device = joinNonEmpty(" · ", p.DeviceModel, joinNonEmpty(" ", p.Platform, p.PlatformVersion), joinNonEmpty(" ", p.Product, p.AppVersion))
	st.Relayed, st.Secure = p.Relayed, p.Secure
	st.Reasons = transcodeReasons(p)
	st.Health = health(p)
	return st
}

// health is the one-line verdict on a live stream, worst first: stalling, a transcode
// that can't keep up, the relay, then how much work the server is doing for it.
func health(p plex.Session) Health {
	switch {
	case p.State == "buffering":
		_, detail := p.BufferCause()
		return Health{"bad", "Buffering — " + detail}
	case p.Transcoding && !p.Throttled && p.TranscodeSpeed > 0 && p.TranscodeSpeed < 0.95:
		return Health{"bad", fmt.Sprintf("The server can't keep up — converting at %.1f× real time, so it will pause to buffer", p.TranscodeSpeed)}
	case p.Relayed:
		return Health{"warn", "Going through Plex Relay — slow and capped at low quality. Remote access isn't reaching the server directly"}
	case p.Transcoding && p.VideoDecision == "transcode":
		return Health{"warn", "Converting the video" + keepingUp(p)}
	case p.Transcoding && p.AudioDecision == "transcode":
		return Health{"good", "Original picture, converted sound" + keepingUp(p)}
	case p.Transcoding:
		return Health{"good", "Original video and audio, repackaged for the player" + keepingUp(p)}
	}
	return Health{"good", "Playing the original file — no work for the server"}
}

// keepingUp says how far ahead a transcode is, when the server reports it.
func keepingUp(p plex.Session) string {
	switch {
	case p.BufferedSec >= 60:
		return fmt.Sprintf(" — keeping up, %s ready ahead", fmtSpan(int(p.BufferedSec)))
	case p.TranscodeSpeed >= 1:
		return fmt.Sprintf(" — keeping up at %.1f× real time", p.TranscodeSpeed)
	case p.Throttled:
		return " — keeping up"
	}
	return ""
}

// fmtSpan is a short span: "45s", "7 min", "1 h 5 min".
func fmtSpan(secs int) string {
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%d min", secs/60)
	case secs%3600/60 == 0:
		return fmt.Sprintf("%d h", secs/3600)
	}
	return fmt.Sprintf("%d h %d min", secs/3600, secs%3600/60)
}

// outRes is the converted picture's resolution: its height, else the server's label.
func outRes(p plex.Session) string {
	if r := streamRes(p.StreamHeight); r != "" {
		return r
	}
	return normRes(p.StreamResolution)
}

// audioLabel is a codec with its channel layout: "OPUS 5.1", "AAC stereo".
func audioLabel(codec string, channels int) string {
	codec = strings.ToUpper(strings.TrimSpace(codec))
	switch {
	case channels == 1:
		return strings.TrimSpace(codec + " mono")
	case channels == 2:
		return strings.TrimSpace(codec + " stereo")
	case channels > 2:
		return strings.TrimSpace(fmt.Sprintf("%s %d.1", codec, channels-1))
	}
	return codec
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func joinNonEmpty(sep string, vals ...string) string {
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, sep)
}

// subtitleFor builds the secondary line: "Show · S1 · E3" for episodes, else the year.
func subtitleFor(p plex.Session) string {
	if p.Type == "episode" && p.ShowTitle != "" {
		return fmt.Sprintf("%s · S%d · E%d", p.ShowTitle, p.SeasonNum, p.Index)
	}
	if p.Year > 0 {
		return fmt.Sprintf("%d", p.Year)
	}
	return p.SeasonName
}

// transcodeReasons explains, in plain English, why a stream is being transcoded. Jellyfin
// says why itself; Plex doesn't, so its reasons are read from what changed.
func transcodeReasons(p plex.Session) []string {
	if !p.Transcoding {
		return nil
	}
	var r []string
	for _, code := range p.TranscodeReasons {
		if why := jellyfinReason(code); why != "" && !slices.Contains(r, why) {
			r = append(r, why)
		}
	}
	srcVideo := firstNonEmpty(p.SrcVideoTitle, mediaLabel(p.SrcVideoCodec, p.SrcResolution))
	srcAudio := firstNonEmpty(audioName(p.SrcAudioTitle), strings.ToUpper(p.SrcAudioCodec))
	lowered := false
	if p.VideoDecision == "transcode" {
		from, to := resRank(p.SrcResolution), resRank(outRes(p))
		lowered = from > 0 && to > 0 && to < from
		out := mediaLabel(p.StreamVideoCodec, outRes(p))
		switch {
		case lowered && len(p.TranscodeReasons) == 0 && remote(p):
			r = append(r, fmt.Sprintf("Quality lowered for the remote connection: %s → %s, about %s Mb/s — the app's remote quality setting, or the connection", normRes(p.SrcResolution), outRes(p), mbps(p.Bandwidth)))
		case lowered && len(p.TranscodeReasons) == 0:
			r = append(r, fmt.Sprintf("The app's quality is set below the original: %s → %s, about %s Mb/s. On the home network, set its quality to Original (Maximum) to play the file as it is", normRes(p.SrcResolution), outRes(p), mbps(p.Bandwidth)))
		default:
			r = append(r, fmt.Sprintf("Converting video (%s → %s)", srcVideo, out))
		}
	}
	if p.AudioDecision == "transcode" {
		out := audioLabel(p.StreamAudioCodec, p.StreamAudioChannels)
		if len(p.TranscodeReasons) == 0 && (!lowered || losslessAudio(p.SrcAudioCodec)) {
			r = append(r, fmt.Sprintf("The player can't play %s — converting the sound to %s", srcAudio, out))
		} else {
			r = append(r, fmt.Sprintf("Converting audio (%s → %s)", srcAudio, out))
		}
	}
	switch p.SubDecision {
	case "burn":
		r = append(r, "Burning in subtitles"+paren(p.SubTitle)+" — the player can't show them itself, so the whole picture is converted")
	case "transcode":
		r = append(r, "Converting subtitles"+paren(p.SubTitle))
	}
	if len(r) == 0 { // both streams copied but still a TranscodeSession → container/protocol remux
		r = append(r, "Remuxing the container (video & audio copied)")
	}
	return r
}

func remote(p plex.Session) bool {
	return !p.Local && !strings.EqualFold(p.Location, "lan")
}

// losslessAudio: the studio formats most players can't decode, so they're converted
// whatever the quality setting.
func losslessAudio(codec string) bool {
	switch strings.ToLower(codec) {
	case "dca", "dts", "dts-hd", "truehd", "mlp", "flac", "pcm":
		return true
	}
	return false
}

// audioName is an audio stream's format without its language: "English (DTS-HD MA 7.1)"
// → "DTS-HD MA 7.1".
func audioName(title string) string {
	if i, j := strings.LastIndex(title, "("), strings.LastIndex(title, ")"); i >= 0 && j > i {
		return title[i+1 : j]
	}
	return title
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func mbps(kbps int64) string {
	return strconv.FormatFloat(float64(kbps)/1000, 'f', 1, 64)
}

// jellyfinReason puts Jellyfin's transcode reason codes in plain English.
func jellyfinReason(code string) string {
	switch code {
	case "ContainerNotSupported":
		return "The player can't open this file type"
	case "VideoCodecNotSupported", "VideoCodecTagNotSupported":
		return "The player can't play this video format"
	case "VideoProfileNotSupported", "VideoLevelNotSupported", "RefFramesNotSupported":
		return "The player can't handle this video's encoding settings"
	case "VideoResolutionNotSupported":
		return "The player can't show this resolution"
	case "VideoBitDepthNotSupported":
		return "The player can't play 10-bit video"
	case "VideoRangeTypeNotSupported":
		return "The player can't show this HDR format, so it's converted to SDR"
	case "VideoFramerateNotSupported":
		return "The player can't play this frame rate"
	case "InterlacedVideoNotSupported":
		return "The player can't play interlaced video"
	case "AnamorphicVideoNotSupported":
		return "The player can't play anamorphic video"
	case "AudioCodecNotSupported", "AudioProfileNotSupported":
		return "The player can't play this sound format"
	case "AudioChannelsNotSupported":
		return "The player can't play this many sound channels"
	case "AudioSampleRateNotSupported", "AudioBitDepthNotSupported":
		return "The player can't play this sound's sample rate or depth"
	case "AudioIsExternal", "SecondaryAudioNotSupported":
		return "The chosen sound track has to be mixed in by the server"
	case "SubtitleCodecNotSupported":
		return "The player can't show these subtitles, so they're burned into the picture"
	case "ContainerBitrateExceedsLimit", "VideoBitrateNotSupported", "AudioBitrateNotSupported":
		return "The file's bitrate is over the player's quality limit"
	case "DirectPlayError":
		return "Direct play failed on the player"
	case "UnknownVideoStreamInfo", "UnknownAudioStreamInfo":
		return "The server couldn't read the file's details"
	}
	return ""
}

func mediaLabel(codec, res string) string {
	codec = strings.ToUpper(strings.TrimSpace(codec))
	res = strings.TrimSpace(res)
	switch {
	case codec != "" && res != "":
		return codec + " " + normRes(res)
	case codec != "":
		return codec
	case res != "":
		return normRes(res)
	}
	return "—"
}

func normRes(res string) string {
	switch strings.ToLower(res) {
	case "4k", "2160":
		return "4K"
	case "1080":
		return "1080p"
	case "720":
		return "720p"
	case "480", "sd":
		return "SD"
	}
	return res
}

func streamRes(h int) string {
	switch {
	case h >= 2000:
		return "4K"
	case h >= 1000:
		return "1080p"
	case h >= 700:
		return "720p"
	case h > 0:
		return "SD"
	}
	return ""
}

// posterPath picks the portrait image for a play: an episode's own thumb is a landscape
// still, so its show's poster wins when there is one.
func posterPath(mediaType, thumb, showThumb string) string {
	if mediaType == "episode" && showThumb != "" {
		return showThumb
	}
	return thumb
}

// proxyImage turns a Plex image path (or a Jellyfin one, "jellyfin:/Items/…") into an
// Arrmada-proxied URL (the token or key stays server-side).
func proxyImage(path string) string {
	if path == "" {
		return ""
	}
	return "/api/v1/insights/image?path=" + url.QueryEscape(path)
}

// Image fetches a poster or art for the caller, keeping the server's credential
// server-side: a Plex image path, or a Jellyfin one in its strict form (IsJellyfinImage).
// The image proxy and alert posters both come through here.
func (s *Service) Image(ctx context.Context, path string) (*http.Response, error) {
	if strings.HasPrefix(path, jellyfinImagePrefix) {
		return s.jellyfinImage(ctx, path)
	}
	return s.client(ctx).Image(ctx, path)
}

// HistoryResult is a page of recorded plays with the total for pagination.
type HistoryResult struct {
	Rows  []HistoryEntry `json:"rows"`
	Total int            `json:"total"`
}

// watchedSecs is the Go twin of watchedExpr — the two must agree, or a row's watch time in
// the History list contradicts the totals on Users and Graphs, which are summed in SQL.
// Prefer what the source reported; fall back to wall time minus paused, which is only
// trustworthy for a live-tracked session where the poller saw the start AND the stop.
func watchedSecs(r HistoryRow) int64 {
	span := r.StoppedAt - r.StartedAt
	if span < 0 {
		span = 0
	}
	if r.WatchedMS > 0 {
		if w := r.WatchedMS / 1000; w < span {
			return w
		}
		return span // reported longer than the session existed — impossible, so cap it
	}
	if w := span - r.PausedMS/1000; w > 0 {
		return w
	}
	return 0
}

// HistoryEntry is a recorded play enriched for display (proxied thumb, geolocation, subtitle line).
type HistoryEntry struct {
	HistoryRow
	Subtitle    string         `json:"subtitle"`
	ThumbURL    string         `json:"thumb_url"`
	Geo         geoip.Location `json:"geo"`
	WatchedSecs int64          `json:"watched_secs"` // see watchedSecs
	ProgressPct int            `json:"progress_pct"`
	// Sessions is how many sittings a grouped row folds together (HistoryFilter.Group);
	// 0 on an ungrouped row.
	Sessions int `json:"sessions,omitempty"`
}

// History returns a page of recorded plays.
func (s *Service) History(ctx context.Context, f HistoryFilter) (HistoryResult, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	out := HistoryResult{Rows: []HistoryEntry{}}
	total, err := s.historyEach(ctx, f, func(e HistoryEntry) error { out.Rows = append(out.Rows, e); return nil })
	if err != nil {
		return HistoryResult{}, err
	}
	out.Total = total
	return out, nil
}

// HistoryExportMax caps one CSV export.
const HistoryExportMax = 50_000

// HistoryExport hands every play the filters match (newest first, at most
// HistoryExportMax) to fn, enriched as History lists them; f's paging is ignored.
func (s *Service) HistoryExport(ctx context.Context, f HistoryFilter, fn func(HistoryEntry) error) error {
	f.Limit, f.Offset = HistoryExportMax, 0
	_, err := s.historyEach(ctx, f, fn)
	return err
}

// historyEach runs one page of the history query, grouped or not, through toHistoryEntry.
func (s *Service) historyEach(ctx context.Context, f HistoryFilter, fn func(HistoryEntry) error) (int, error) {
	if f.Group {
		return s.repo.historyGrouped(ctx, f, func(g GroupedRow) error {
			e := s.toHistoryEntry(g.HistoryRow)
			// A viewing's watch time is its sittings' summed: its start-to-stop span also
			// covers the gaps between them.
			e.WatchedSecs, e.Sessions = g.WatchedSecs, g.Sessions
			return fn(e)
		})
	}
	return s.repo.historyEach(ctx, f, func(r HistoryRow) error { return fn(s.toHistoryEntry(r)) })
}

// toHistoryEntry enriches a recorded play for display. History and the Recently watched card
// both go through it, so a play's watch time and progress can't differ between the two — or
// from the Users and Graphs totals, which watchedSecs mirrors in SQL.
func (s *Service) toHistoryEntry(r HistoryRow) HistoryEntry {
	e := HistoryEntry{HistoryRow: r, ThumbURL: proxyImage(posterPath(r.MediaType, r.Thumb, r.GrandparentThumb)), Subtitle: historySubtitle(r), WatchedSecs: watchedSecs(r)}
	if s.geo != nil {
		e.Geo = s.geo.Lookup(r.IPAddress)
	}
	if r.DurationMS > 0 {
		pct := r.ViewOffsetMS * 100 / r.DurationMS
		e.ProgressPct = int(min(max(pct, 0), 100))
	}
	return e
}

func historySubtitle(r HistoryRow) string {
	if r.MediaType == "episode" && r.GrandparentTitle != "" {
		return fmt.Sprintf("%s · S%d · E%d", r.GrandparentTitle, r.ParentIndex, r.MediaIndex)
	}
	if r.Year > 0 {
		return fmt.Sprintf("%d", r.Year)
	}
	return r.ParentTitle
}
