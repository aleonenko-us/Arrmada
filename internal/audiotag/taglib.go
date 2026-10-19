package audiotag

import (
	"fmt"
	"io"
	"os"

	"go.senan.xyz/taglib"
)

// The TagLib backend. go-taglib runs TagLib 2 (C++, compiled to WASM) inside
// wazero, so it is pure Go to build and cross-compile. The first call in a
// process compiles the module (cached on disk under os.TempDir()); later calls
// instantiate it per call, which makes it safe for concurrent use.
//
// TagLib edits files in place. To keep this package's guarantees (atomic,
// hardlink-breaking, original untouched on failure) the backend copies the file
// to the temporary file first and lets TagLib edit only that copy.

func readTagLib(path string) (map[string][]string, error) {
	tags, err := taglib.ReadTags(path)
	if err != nil {
		return nil, fmt.Errorf("taglib: %w", err)
	}
	return tags, nil
}

func writeTagLib(src *os.File, dst *os.File, ft format, t Tags, opts WriteOptions) error {
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if err := src.Close(); err != nil {
		return err
	}
	if ft == formatMP4 {
		t = applyGenreJoin(t, opts.MP4GenreSeparator)
	}
	// Without the Clear option go-taglib only touches the keys it is given, and
	// toTagLib gives every house key (empty ones erase), so foreign tags stay.
	if err := taglib.WriteTags(dst.Name(), toTagLib(ft, t), 0); err != nil {
		return fmt.Errorf("taglib: %w", err)
	}
	if ft == formatMP3 {
		// TagLib saves MP3s with "duplicate tags", which creates or updates an
		// ID3v1 tag (and keeps any APE tag). The house rule is ID3v2.4 only.
		if err := stripMP3Trailers(dst); err != nil {
			return err
		}
	}
	return nil
}

func propertiesTagLib(path string) (AudioProperties, error) {
	p, err := taglib.ReadProperties(path)
	if err != nil {
		return AudioProperties{}, fmt.Errorf("taglib: %w", err)
	}
	out := AudioProperties{
		Format:     p.Format,
		Codec:      p.InnerCodec,
		Duration:   p.Length,
		SampleRate: int(p.SampleRate),
		Channels:   int(p.Channels),
		BitDepth:   int(p.BitDepth),
		Bitrate:    int(p.BitRate),
	}
	if out.Format == "mpeg" {
		out.Format = "mp3"
	}
	if out.Codec == "" {
		out.Codec = out.Format // flac, mp3: the format is the codec
	}
	return out, nil
}
