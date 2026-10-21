package plex

import (
	"context"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
)

// Session is one active playback stream, flattened (see mediaserver.Session). The type moved
// to internal/mediaserver so Jellyfin's sessions share it; Plex code keeps naming it here.
type Session = mediaserver.Session

// Sessions returns the server's current active streams.
func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var r struct {
		MediaContainer struct {
			Metadata []rawSession `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/status/sessions", &r); err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(r.MediaContainer.Metadata))
	for _, m := range r.MediaContainer.Metadata {
		out = append(out, m.flatten())
	}
	return out, nil
}

// rawSession mirrors the nested Plex JSON so we can flatten it.
type rawSession struct {
	SessionKey       string  `json:"sessionKey"`
	RatingKey        string  `json:"ratingKey"`
	Type             string  `json:"type"`
	Title            string  `json:"title"`
	GrandparentTitle string  `json:"grandparentTitle"`
	ParentTitle      string  `json:"parentTitle"`
	Index            flexInt `json:"index"`
	ParentIndex      flexInt `json:"parentIndex"`
	Year             flexInt `json:"year"`
	Thumb            string  `json:"thumb"`
	GrandparentThumb string  `json:"grandparentThumb"`
	GrandparentKey   string  `json:"grandparentRatingKey"`
	Art              string  `json:"art"`
	GrandparentArt   string  `json:"grandparentArt"`
	Duration         flexInt `json:"duration"`
	ViewOffset       flexInt `json:"viewOffset"`
	User             struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Thumb string `json:"thumb"`
	} `json:"User"`
	Player struct {
		Address             string `json:"address"`
		RemotePublicAddress string `json:"remotePublicAddress"`
		Device              string `json:"device"`
		Platform            string `json:"platform"`
		Product             string `json:"product"`
		Title               string `json:"title"`
		State               string `json:"state"`
		Local               bool   `json:"local"`
		Model               string `json:"model"`
		PlatformVersion     string `json:"platformVersion"`
		Version             string `json:"version"`
		Relayed             bool   `json:"relayed"`
		Secure              bool   `json:"secure"`
	} `json:"Player"`
	Session struct {
		Bandwidth flexInt `json:"bandwidth"`
		Location  string  `json:"location"`
	} `json:"Session"`
	TranscodeSession *struct {
		VideoDecision string  `json:"videoDecision"`
		AudioDecision string  `json:"audioDecision"`
		SubDecision   string  `json:"subtitleDecision"`
		Protocol      string  `json:"protocol"`
		Container     string  `json:"container"`
		Throttled     bool    `json:"throttled"`
		Speed         float64 `json:"speed"`
		// What hardware Plex asked for and what it is really using. Plex doesn't document
		// the types: decoding/encoding name the API ("qsv", "vaapi", "dxva2"), the others
		// are flags, and any of them may arrive as a string or a bool — or be missing when
		// the CPU does the work — so all of them are read tolerantly.
		HwRequested     flexStr `json:"transcodeHwRequested"`
		HwDecoding      flexStr `json:"transcodeHwDecoding"`
		HwDecodingTitle flexStr `json:"transcodeHwDecodingTitle"`
		HwEncoding      flexStr `json:"transcodeHwEncoding"`
		HwEncodingTitle flexStr `json:"transcodeHwEncodingTitle"`
		HwFullPipeline  flexStr `json:"transcodeHwFullPipeline"`
		VideoCodec      string  `json:"videoCodec"`
		AudioCodec      string  `json:"audioCodec"`
		Width           flexInt `json:"width"`
		Height          flexInt `json:"height"`
		AudioChannels   flexInt `json:"audioChannels"`
		// The source's codecs: while transcoding, Media below describes the output.
		SourceVideoCodec string `json:"sourceVideoCodec"`
		SourceAudioCodec string `json:"sourceAudioCodec"`
		// How far (seconds into the item) the converted stream reaches.
		MaxOffsetAvailable float64 `json:"maxOffsetAvailable"`
	} `json:"TranscodeSession"`
	Media []struct {
		VideoResolution string  `json:"videoResolution"`
		Bitrate         flexInt `json:"bitrate"`
		Container       string  `json:"container"`
		VideoCodec      string  `json:"videoCodec"`
		AudioCodec      string  `json:"audioCodec"`
		Part            []struct {
			Stream []rawStream `json:"Stream"`
		} `json:"Part"`
	} `json:"Media"`
}

// rawStream is one stream of a session's media part: streamType 1 is video, 2 audio, 3
// subtitles. displayTitle names the source stream even while it's being converted.
type rawStream struct {
	StreamType   flexInt `json:"streamType"`
	BitDepth     flexInt `json:"bitDepth"`
	Profile      string  `json:"profile"`
	DisplayTitle string  `json:"displayTitle"`
	Selected     bool    `json:"selected"`
}

func (m rawSession) flatten() Session {
	s := Session{
		SessionKey: m.SessionKey, RatingKey: m.RatingKey, Type: m.Type, Title: m.Title,
		ShowTitle: m.GrandparentTitle, SeasonName: m.ParentTitle,
		Index: int(m.Index), SeasonNum: int(m.ParentIndex), Year: int(m.Year), Thumb: m.Thumb,
		ShowThumb: m.GrandparentThumb, ShowRatingKey: m.GrandparentKey, Art: m.Art,
		DurationMS: int64(m.Duration), OffsetMS: int64(m.ViewOffset),
		UserID: m.User.ID, UserName: m.User.Title, UserThumb: m.User.Thumb,
		Address: m.Player.Address, PublicIP: m.Player.RemotePublicAddress,
		Device: m.Player.Device, Platform: m.Player.Platform, Product: m.Player.Product,
		PlayerName: m.Player.Title, State: m.Player.State, Local: m.Player.Local,
		Bandwidth: int64(m.Session.Bandwidth), Location: m.Session.Location,
		PlatformVersion: m.Player.PlatformVersion, AppVersion: m.Player.Version,
		Relayed: m.Player.Relayed, Secure: m.Player.Secure,
	}
	if m.Player.Model != "" {
		s.DeviceModel = strings.TrimSpace(m.Player.Device + " " + m.Player.Model)
	}
	if s.Art == "" {
		s.Art = m.GrandparentArt
	}
	if t := m.TranscodeSession; t != nil {
		s.Transcoding = true
		s.VideoDecision, s.AudioDecision, s.SubDecision = t.VideoDecision, t.AudioDecision, t.SubDecision
		s.HWRequested, s.HWDecode, s.HWEncode = t.HwRequested.on(), t.HwDecoding.on(), t.HwEncoding.on()
		s.HWFullPipeline = t.HwFullPipeline.on()
		s.TranscodeHW = s.HWDecode || s.HWEncode
		switch {
		case s.HWEncode && t.HwEncodingTitle.on():
			s.HWTitle = string(t.HwEncodingTitle)
		case s.HWDecode && t.HwDecodingTitle.on():
			s.HWTitle = string(t.HwDecodingTitle)
		}
		s.Throttled = t.Throttled
		s.TranscodeSpeed = t.Speed
		s.TranscodeProto, s.TranscodeCont = t.Protocol, t.Container
		s.StreamVideoCodec, s.StreamAudioCodec = t.VideoCodec, t.AudioCodec
		s.StreamWidth, s.StreamHeight = int(t.Width), int(t.Height)
		s.StreamAudioChannels = int(t.AudioChannels)
		if t.MaxOffsetAvailable > 0 {
			s.BufferedSec = max(0, t.MaxOffsetAvailable-float64(m.ViewOffset)/1000)
		}
	}
	if len(m.Media) > 0 {
		md := m.Media[0]
		s.SrcResolution, s.SrcVideoCodec, s.SrcAudioCodec = md.VideoResolution, md.VideoCodec, md.AudioCodec
		s.SrcContainer, s.SrcBitrate = md.Container, int64(md.Bitrate)
		if len(md.Part) > 0 {
			var firstAudio string
			for _, st := range md.Part[0].Stream {
				switch st.StreamType {
				case 1:
					if s.SrcVideoTitle == "" {
						s.SrcVideoBitDepth, s.SrcVideoProfile = int(st.BitDepth), st.Profile
						s.SrcVideoTitle = st.DisplayTitle
					}
				case 2:
					if firstAudio == "" {
						firstAudio = st.DisplayTitle
					}
					if st.Selected && s.SrcAudioTitle == "" {
						s.SrcAudioTitle = st.DisplayTitle
					}
				case 3:
					if st.Selected && s.SubTitle == "" {
						s.SubTitle = st.DisplayTitle
					}
				}
			}
			if s.SrcAudioTitle == "" {
				s.SrcAudioTitle = firstAudio
			}
		}
		// While transcoding, Media describes what's sent, not the file: the source's codecs
		// come from the transcode session and its resolution from the video stream's name.
		if m.TranscodeSession != nil {
			t := m.TranscodeSession
			if t.VideoDecision == "transcode" {
				s.StreamResolution = md.VideoResolution
				s.SrcResolution = titleResolution(s.SrcVideoTitle)
			}
			if t.SourceVideoCodec != "" {
				s.SrcVideoCodec = t.SourceVideoCodec
			}
			if t.SourceAudioCodec != "" {
				s.SrcAudioCodec = t.SourceAudioCodec
			}
			s.SrcBitrate = 0 // the output's, not the file's
		}
	}
	return s
}

// titleResolution reads the resolution a stream name starts with ("4K DoVi/HDR10 (HEVC
// Main 10)" → "4k", "1080p (H.264)" → "1080"), or "" when it doesn't say.
func titleResolution(title string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(title), " ")
	switch r := strings.ToLower(first); r {
	case "4k", "8k", "sd":
		return r
	default:
		if n := strings.TrimSuffix(r, "p"); n != r || strings.HasSuffix(r, "i") {
			n = strings.TrimSuffix(n, "i")
			if _, err := strconv.Atoi(n); err == nil {
				return n
			}
		}
	}
	return ""
}
