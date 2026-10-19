package audiotag

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	mp4 "github.com/abema/go-mp4"
)

// AudioProperties describes the audio stream. Values are 0/"" when unknown.
type AudioProperties struct {
	Format     string // container: "flac", "mp3", "mp4", "ogg"
	Codec      string // "flac", "mp3", "aac", "alac", "vorbis", "opus"
	Duration   time.Duration
	SampleRate int // Hz
	Channels   int
	BitDepth   int // lossless only
	Bitrate    int // average, kbit/s
}

// kbps is the average bitrate of n bytes over d.
func kbps(n int64, d time.Duration) int {
	if d <= 0 || n <= 0 {
		return 0
	}
	return int(float64(n) * 8 / d.Seconds() / 1000)
}

// mp4Properties reads duration and format of the first sound track.
func mp4Properties(f io.ReadSeeker) (AudioProperties, error) {
	type trak struct {
		handler   string
		timescale uint32
		duration  uint64
		stsd      []byte
	}
	var (
		traks []*trak
		mdat  int64
	)
	raw := func(h *mp4.ReadHandle) ([]byte, error) {
		var buf bytes.Buffer
		_, err := h.ReadData(&buf)
		return buf.Bytes(), err
	}
	_, err := mp4.ReadBoxStructure(f, func(h *mp4.ReadHandle) (interface{}, error) {
		p := pathString(h.Path)
		switch p {
		case "mdat":
			mdat += int64(h.BoxInfo.Size - h.BoxInfo.HeaderSize)
			return nil, nil
		case "moov", "moov/trak/mdia", "moov/trak/mdia/minf", "moov/trak/mdia/minf/stbl":
			return h.Expand()
		case "moov/trak":
			traks = append(traks, &trak{})
			return h.Expand()
		case "moov/trak/mdia/hdlr", "moov/trak/mdia/mdhd", "moov/trak/mdia/minf/stbl/stsd":
		default:
			return nil, nil
		}
		cur := traks[len(traks)-1]
		b, err := raw(h)
		if err != nil {
			return nil, err
		}
		switch {
		case p == "moov/trak/mdia/hdlr" && len(b) >= 12:
			cur.handler = string(b[8:12])
		case p == "moov/trak/mdia/mdhd" && len(b) >= 20 && b[0] == 0:
			cur.timescale, cur.duration = binary.BigEndian.Uint32(b[12:16]), uint64(binary.BigEndian.Uint32(b[16:20]))
		case p == "moov/trak/mdia/mdhd" && len(b) >= 32 && b[0] == 1:
			cur.timescale, cur.duration = binary.BigEndian.Uint32(b[20:24]), binary.BigEndian.Uint64(b[24:32])
		case p == "moov/trak/mdia/minf/stbl/stsd":
			cur.stsd = b
		}
		return nil, nil
	})
	if err != nil {
		return AudioProperties{}, fmt.Errorf("mp4: %w", err)
	}
	for _, t := range traks {
		if t.handler != "soun" || t.timescale == 0 || len(t.stsd) < 44 {
			continue
		}
		// stsd: version/flags and entry count, then the first AudioSampleEntry:
		// size, type, 6 reserved, data ref index, version, revision, vendor,
		// channel count, sample size, compression id, packet size, 16.16 rate.
		e := t.stsd[8:]
		p := AudioProperties{
			Format:     "mp4",
			Codec:      string(e[4:8]),
			Channels:   int(binary.BigEndian.Uint16(e[24:26])),
			SampleRate: int(binary.BigEndian.Uint32(e[32:36]) >> 16),
			Duration:   time.Duration(float64(t.duration) / float64(t.timescale) * float64(time.Second)),
		}
		switch p.Codec {
		case "mp4a":
			p.Codec = "aac"
		case "alac":
			// The ALAC cookie holds the real bit depth and rate (the 16.16 field
			// can't hold 96 kHz): after version/flags come frame length (4),
			// compatible version, bit depth, pb, mb, kb, channels, max run (2),
			// max frame bytes (4), average bitrate (4), sample rate (4).
			if i := bytes.Index(e[36:], []byte("alac")); i >= 0 {
				if c := e[36+i+4:]; len(c) >= 28 {
					p.BitDepth = int(c[9])
					p.Channels = int(c[13])
					p.SampleRate = int(binary.BigEndian.Uint32(c[24:28]))
				}
			}
		}
		if p.SampleRate == 0 {
			p.SampleRate = int(t.timescale) // audio tracks normally tick at the sample rate
		}
		p.Bitrate = kbps(mdat, p.Duration)
		return p, nil
	}
	return AudioProperties{}, fmt.Errorf("%w: no audio track", ErrUnsupported)
}

var (
	mp3BitratesV1 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3BitratesV2 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3Rates      = map[byte][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}
)

// mp3Properties reads the first MPEG Layer III frame header and, when present,
// the Xing/Info or VBRI header for the exact frame count; otherwise the stream
// is treated as CBR.
func mp3Properties(f *os.File) (AudioProperties, error) {
	start := int64(0)
	hdr := make([]byte, 10)
	if _, err := f.ReadAt(hdr, 0); err == nil && string(hdr[:3]) == "ID3" {
		start = 10 + int64(synchsafe(hdr[6:10]))
		if hdr[5]&0x10 != 0 {
			start += 10 // footer
		}
	}
	end, err := mp3TrailerStart(f)
	if err != nil {
		return AudioProperties{}, err
	}
	buf := make([]byte, 64<<10)
	n, _ := f.ReadAt(buf, start)
	buf = buf[:n]
	for i := 0; i+4 <= len(buf); i++ {
		h := buf[i : i+4]
		ver, layer := h[1]>>3&3, h[1]>>1&3
		brIdx, srIdx, mode := h[2]>>4, h[2]>>2&3, h[3]>>6
		if h[0] != 0xFF || h[1]&0xE0 != 0xE0 || ver == 1 || layer != 1 || brIdx == 0 || brIdx == 15 || srIdx == 3 {
			continue
		}
		p := AudioProperties{Format: "mp3", Codec: "mp3", SampleRate: mp3Rates[ver][srIdx], Channels: 2}
		if mode == 3 {
			p.Channels = 1
		}
		spf, side, rate := 1152, 32, mp3BitratesV1[brIdx]
		if ver != 3 {
			spf, side, rate = 576, 17, mp3BitratesV2[brIdx]
		}
		if p.Channels == 1 { // mono side info is shorter
			side = 9
			if ver == 3 {
				side = 17
			}
		}
		var frames uint32
		if x := buf[min(i+4+side, len(buf)):]; len(x) >= 12 && (string(x[:4]) == "Xing" || string(x[:4]) == "Info") &&
			binary.BigEndian.Uint32(x[4:8])&1 != 0 {
			frames = binary.BigEndian.Uint32(x[8:12])
		} else if v := buf[min(i+36, len(buf)):]; len(v) >= 18 && string(v[:4]) == "VBRI" {
			frames = binary.BigEndian.Uint32(v[14:18])
		}
		audio := end - start - int64(i)
		if frames > 0 {
			p.Duration = time.Duration(float64(frames) * float64(spf) / float64(p.SampleRate) * float64(time.Second))
			p.Bitrate = kbps(audio, p.Duration)
		} else {
			p.Bitrate = rate
			p.Duration = time.Duration(float64(audio) * 8 / float64(rate*1000) * float64(time.Second))
		}
		return p, nil
	}
	return AudioProperties{}, fmt.Errorf("%w: no MPEG audio frame found", ErrUnsupported)
}
