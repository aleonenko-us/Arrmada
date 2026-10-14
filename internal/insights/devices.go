package insights

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Which devices play which formats as they are, from the plays Insights recorded — for
// Convert's "Your devices" panel. Convert can write HEVC 10-bit or AV1, and a player that
// can't decode those makes Plex transcode every film it converted; the plays already say
// which players direct-played (or direct-streamed) each format and which had the video
// transcoded.
//
// What this exposes is per device (product, platform and the player's own name — what the
// History tab already shows staff), never who watched what on it.

// Device codec states.
const (
	CodecPlays      = "yes"     // direct-played (or direct-streamed) at least once
	CodecTranscodes = "no"      // only ever had its video transcoded
	CodecUnseen     = "unknown" // never played that format
)

// CodecUse is what one device did with one format.
type CodecUse struct {
	State      string `json:"state"`      // CodecPlays | CodecTranscodes | CodecUnseen
	Direct     int    `json:"direct"`     // plays whose video went through as it was
	Transcoded int    `json:"transcoded"` // plays whose video Plex had to transcode
}

// DeviceSupport is one device's record over the window.
type DeviceSupport struct {
	Server   string   `json:"server"`  // plex | jellyfin: the same TV runs each server's app
	Product  string   `json:"product"` // "Plex for Roku"
	Platform string   `json:"platform"`
	Player   string   `json:"player"` // the device's own name ("Bedroom Roku")
	LastSeen int64    `json:"last_seen"`
	Plays    int      `json:"plays"`
	HEVC     CodecUse `json:"hevc"`
	HEVC10   CodecUse `json:"hevc10"`
	AV1      CodecUse `json:"av1"`
}

// DeviceCodecSupport groups the video plays since `since` by device and says, per format,
// whether the device played it as it was. HEVC counts 8-bit plays (and ones whose depth
// wasn't recorded) plus any 10-bit play that went through — a device that plays 10-bit
// plays 8-bit too. HEVC 10-bit counts only plays whose depth was recorded (since migration
// 0215), so older history shows it as unseen rather than guessed.
//
// A remote play whose video came down in resolution is left out: that is usually Plex
// fitting the stream to the connection, not the device refusing the format.
func (s *Service) DeviceCodecSupport(ctx context.Context, since time.Time) ([]DeviceSupport, error) {
	rows, err := s.repo.db.QueryContext(ctx, `
		SELECT server, product, platform, player, video_src, video_stream, location, video_src_bitdepth, video_src_profile, stopped_at
		  FROM stream_sessions
		 WHERE stopped_at >= ? AND media_type IN ('movie', 'episode')`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ server, product, platform, player string }
	byDevice := map[key]*DeviceSupport{}
	for rows.Next() {
		var k key
		var src, stream, location, profile string
		var depth int
		var stopped int64
		if err := rows.Scan(&k.server, &k.product, &k.platform, &k.player, &src, &stream, &location, &depth, &profile, &stopped); err != nil {
			return nil, err
		}
		d := byDevice[k]
		if d == nil {
			d = &DeviceSupport{Server: k.server, Product: k.product, Platform: k.platform, Player: k.player}
			byDevice[k] = d
		}
		d.Plays++
		d.LastSeen = max(d.LastSeen, stopped)
		codec, srcRes := splitLabel(src)
		transcoded := stream != ""
		if transcoded && location == "wan" {
			if _, streamRes := splitLabel(stream); resRank(streamRes) > 0 && resRank(streamRes) < resRank(srcRes) {
				continue // most likely fitted to the connection
			}
		}
		tenBit := depth >= 10 || strings.Contains(strings.ToLower(profile), "10")
		switch codec {
		case "HEVC", "H265":
			if tenBit {
				d.HEVC10.count(transcoded)
				if !transcoded {
					d.HEVC.count(false)
				}
			} else { // 8-bit, or not recorded: either way it says something about HEVC
				d.HEVC.count(transcoded)
			}
		case "AV1":
			d.AV1.count(transcoded)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DeviceSupport, 0, len(byDevice))
	for _, d := range byDevice {
		for _, c := range []*CodecUse{&d.HEVC, &d.HEVC10, &d.AV1} {
			c.settle()
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].Player < out[j].Player
	})
	return out, nil
}

func (c *CodecUse) count(transcoded bool) {
	if transcoded {
		c.Transcoded++
	} else {
		c.Direct++
	}
}

func (c *CodecUse) settle() {
	switch {
	case c.Direct > 0:
		c.State = CodecPlays
	case c.Transcoded > 0:
		c.State = CodecTranscodes
	default:
		c.State = CodecUnseen
	}
}

// splitLabel undoes mediaLabel: "HEVC 4K" → ("HEVC", "4K"); "—" → ("", "").
func splitLabel(label string) (codec, res string) {
	label = strings.TrimSpace(label)
	if label == "" || label == "—" {
		return "", ""
	}
	codec, res, _ = strings.Cut(label, " ")
	if resRank(codec) > 0 { // a resolution alone
		return "", codec
	}
	return strings.ToUpper(codec), res
}

// resRank orders resolution labels; 0 = not a resolution.
func resRank(res string) int {
	switch strings.ToUpper(strings.TrimSpace(res)) {
	case "SD", "480", "480P", "576", "576P":
		return 1
	case "720", "720P":
		return 2
	case "1080", "1080P":
		return 3
	case "1440", "1440P", "2K":
		return 4
	case "4K", "2160", "2160P":
		return 5
	}
	return 0
}
