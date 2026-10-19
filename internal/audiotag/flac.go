package audiotag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-flac/flacvorbis"
	flac "github.com/go-flac/go-flac"
)

// maxFLACBlock is the largest metadata block FLAC can describe (24-bit length).
const maxFLACBlock = 1<<24 - 1

func readFLAC(f *os.File) (map[string][]string, error) {
	meta, err := flac.ParseMetadata(f)
	if err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}
	m := map[string][]string{}
	for _, block := range meta.Meta {
		if block.Type != flac.VorbisComment {
			continue
		}
		cmts, err := flacvorbis.ParseFromMetaDataBlock(*block)
		if err != nil {
			return nil, fmt.Errorf("flac: vorbis comment: %w", err)
		}
		for _, c := range cmts.Comments {
			k, v, ok := strings.Cut(c, "=")
			if !ok {
				continue // malformed comment: ignore it rather than fail the file
			}
			k = strings.ToUpper(k)
			m[k] = append(m[k], v)
		}
		break // a valid file has at most one comment block
	}
	return m, nil
}

// writeFLAC rewrites the metadata blocks and then copies the audio frames
// byte for byte from the original.
func writeFLAC(src *os.File, dst io.Writer, t Tags, _ WriteOptions) error {
	meta, err := flac.ParseMetadata(src)
	if err != nil {
		return fmt.Errorf("flac: %w", err)
	}
	// ParseMetadata reads exactly the header and blocks (src is unbuffered), but
	// compute the audio offset from the block sizes instead of trusting the
	// file position.
	audioStart := int64(4)
	for _, b := range meta.Meta {
		audioStart += 4 + int64(len(b.Data))
	}
	if len(meta.Meta) == 0 || meta.Meta[0].Type != flac.StreamInfo {
		return errors.New("flac: first metadata block is not STREAMINFO")
	}

	idx := -1
	cmts := flacvorbis.New()
	for i, b := range meta.Meta {
		if b.Type == flac.VorbisComment {
			idx = i
			if cmts, err = flacvorbis.ParseFromMetaDataBlock(*b); err != nil {
				return fmt.Errorf("flac: vorbis comment: %w", err)
			}
			break
		}
	}

	// Keep every comment the house set doesn't own, in its original order, then
	// append the house fields. Multi-value fields become repeated comments.
	kept := cmts.Comments[:0:0]
	for _, c := range cmts.Comments {
		k, _, _ := strings.Cut(c, "=")
		k = strings.ToUpper(k)
		if _, alias := readAliases[k]; !isHouseKey(k) && !alias {
			kept = append(kept, c)
		}
	}
	cmts.Comments = kept
	for _, key := range houseKeys {
		for _, v := range t[key] {
			if err := cmts.Add(key, v); err != nil {
				return fmt.Errorf("flac: %s: %w", key, err)
			}
		}
	}
	block := cmts.Marshal()
	if len(block.Data) > maxFLACBlock {
		return errors.New("flac: tags too large for a metadata block")
	}
	if idx >= 0 {
		meta.Meta[idx] = &block
	} else {
		// No comment block yet: put it straight after STREAMINFO, where
		// readers look first.
		meta.Meta = append(meta.Meta[:1], append([]*flac.MetaDataBlock{&block}, meta.Meta[1:]...)...)
	}

	// Marshal writes "fLaC", the blocks (fixing the last-block flag) and Frames,
	// which ParseMetadata left empty; the audio follows from the original file.
	if _, err := dst.Write(meta.Marshal()); err != nil {
		return err
	}
	if _, err := src.Seek(audioStart, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

// flacProperties decodes STREAMINFO.
func flacProperties(f *os.File) (AudioProperties, error) {
	meta, err := flac.ParseMetadata(f)
	if err != nil {
		return AudioProperties{}, fmt.Errorf("flac: %w", err)
	}
	if len(meta.Meta) == 0 || meta.Meta[0].Type != flac.StreamInfo || len(meta.Meta[0].Data) < 34 {
		return AudioProperties{}, errors.New("flac: no STREAMINFO")
	}
	si := meta.Meta[0].Data
	// Bytes 10..17: 20 bits sample rate, 3 bits channels-1, 5 bits bps-1,
	// 36 bits total samples.
	v := binary.BigEndian.Uint64(si[10:18])
	p := AudioProperties{
		Format:     "flac",
		Codec:      "flac",
		SampleRate: int(v >> 44),
		Channels:   int(v>>41&0x7) + 1,
		BitDepth:   int(v>>36&0x1F) + 1,
	}
	if samples := v & (1<<36 - 1); p.SampleRate > 0 {
		p.Duration = time.Duration(float64(samples) / float64(p.SampleRate) * float64(time.Second))
	}
	audioStart := int64(4)
	for _, b := range meta.Meta {
		audioStart += 4 + int64(len(b.Data))
	}
	if fi, err := f.Stat(); err == nil {
		p.Bitrate = kbps(fi.Size()-audioStart, p.Duration)
	}
	return p, nil
}
