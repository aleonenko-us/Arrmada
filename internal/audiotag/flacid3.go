package audiotag

import (
	"errors"
	"fmt"
	"io"
	"os"

	flac "github.com/go-flac/go-flac"
)

// Some taggers put an ID3v2 tag in front of a FLAC file. Players read a FLAC's Vorbis
// comments, never that ID3 tag, so such a file is a FLAC: the sniffer looks past the ID3
// tag for "fLaC", the FLAC code reads from there, and a write drops the ID3 tag so no
// stale copy of the tags sits in front of the real ones.

// id3PrefixLen is the size of an ID3v2 tag at the start of r (header, body and any
// footer), or 0 when r doesn't start with one.
func id3PrefixLen(r io.ReaderAt) int64 {
	hdr := make([]byte, 10)
	if _, err := r.ReadAt(hdr, 0); err != nil || string(hdr[:3]) != "ID3" {
		return 0
	}
	n := 10 + int64(synchsafe(hdr[6:10]))
	if hdr[5]&0x10 != 0 { // footer present (v2.4)
		n += 10
	}
	return n
}

// flacAfterID3 reports whether r is a FLAC stream behind an ID3v2 tag.
func flacAfterID3(r io.ReaderAt) bool {
	n := id3PrefixLen(r)
	if n == 0 {
		return false
	}
	magic := make([]byte, 4)
	_, err := r.ReadAt(magic, n)
	return err == nil && string(magic) == "fLaC"
}

// seekFLAC positions f at its "fLaC" marker and returns that offset (0 for an ordinary
// FLAC, the ID3 tag's size for one behind an ID3v2 tag).
func seekFLAC(f *os.File) (int64, error) {
	var off int64
	if flacAfterID3(f) {
		off = id3PrefixLen(f)
	}
	_, err := f.Seek(off, io.SeekStart)
	return off, err
}

// FLACStreamBlocks returns a FLAC file's STREAMINFO and SEEKTABLE blocks (type byte, then
// data, for each), which describe the audio (sample count, the decoded MD5, seek points)
// and which a tag write must carry over unchanged. ErrUnsupported for anything not FLAC.
func FLACStreamBlocks(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if ft, err := sniff(f); err != nil || ft != formatFLAC {
		return nil, fmt.Errorf("%w: not FLAC", ErrUnsupported)
	}
	if _, err := seekFLAC(f); err != nil {
		return nil, err
	}
	meta, err := flac.ParseMetadata(f)
	if err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}
	var out []byte
	for _, b := range meta.Meta {
		if b.Type == flac.StreamInfo || b.Type == flac.SeekTable {
			out = append(out, byte(b.Type))
			out = append(out, b.Data...)
		}
	}
	return out, nil
}

// AudioRange is where a file's encoded audio lies: the FLAC frames after the metadata
// blocks, or the MPEG frames between the ID3v2 tag and any trailing tags. The tag writer
// copies exactly these bytes, so comparing them proves a tag write left the audio alone
// without decoding it (a decoder's view of the stream can change with the tags around
// it). Formats whose tags are woven through the stream (MP4, Ogg) answer ErrUnsupported:
// compare their decoded audio instead.
func AudioRange(path string) (offset, length int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	ft, err := sniff(f)
	if err != nil {
		return 0, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	switch ft {
	case formatFLAC:
		off, err := seekFLAC(f)
		if err != nil {
			return 0, 0, err
		}
		meta, err := flac.ParseMetadata(f)
		if err != nil {
			return 0, 0, fmt.Errorf("flac: %w", err)
		}
		start := off + 4
		for _, b := range meta.Meta {
			start += 4 + int64(len(b.Data))
		}
		if start > fi.Size() {
			return 0, 0, errors.New("flac: metadata runs past the end of the file")
		}
		return start, fi.Size() - start, nil
	case formatMP3:
		_, tagEnd, err := parseID3(f)
		if err != nil {
			return 0, 0, err
		}
		end, err := mp3TrailerStart(f)
		if err != nil {
			return 0, 0, err
		}
		if end < tagEnd {
			return 0, 0, fmt.Errorf("%w: MP3 trailing tags overlap the ID3v2 tag", ErrUnsupported)
		}
		return tagEnd, end - tagEnd, nil
	}
	return 0, 0, fmt.Errorf("%w: no fixed audio range in this format", ErrUnsupported)
}
