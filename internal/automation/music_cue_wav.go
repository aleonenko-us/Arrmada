package automation

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// wavSlack is how much a WAV file may hold past the audio its header declares (trailing
// tag chunks) before the header reads as wrong.
const wavSlack = 16 << 20

// wavConsistent checks that a WAV image's header describes the file: the audio it declares
// is all there, and not much more is. A plain RIFF WAV can't describe more than 4 GB; one
// written past that by a careless tool has a header (and a probed length) that lies, so
// its cue points can't be trusted against it. RF64 WAVs are read through their ds64 chunk.
// Anything that isn't a WAV passes.
func wavConsistent(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[8:12]) != "WAVE" {
		return nil
	}
	rf64 := string(hdr[:4]) == "RF64"
	if !rf64 && string(hdr[:4]) != "RIFF" {
		return nil
	}
	var dataSize64 uint64
	off := int64(12)
	for i := 0; i < 64; i++ { // chunks before the audio are few
		var ch [8]byte
		if _, err := f.ReadAt(ch[:], off); err != nil {
			return fmt.Errorf("the WAV header has no audio chunk")
		}
		id, n := string(ch[:4]), int64(binary.LittleEndian.Uint32(ch[4:]))
		body := off + 8
		switch id {
		case "ds64":
			var ds [16]byte
			if _, err := f.ReadAt(ds[:], body); err == nil {
				dataSize64 = binary.LittleEndian.Uint64(ds[8:16])
			}
		case "data":
			declared := n
			if rf64 && n == 0xFFFFFFFF {
				declared = int64(dataSize64)
			}
			have := size - body
			switch {
			case !rf64 && size > 0xFFFFFFFF:
				return fmt.Errorf("a WAV over 4 GB whose header can't describe it (the length it reports is wrong)")
			case declared > have:
				return fmt.Errorf("the WAV header says %d bytes of audio, but the file holds %d — it's cut short", declared, have)
			case have-declared > wavSlack:
				return fmt.Errorf("the WAV header says %d bytes of audio, but the file holds %d — the header is wrong", declared, have)
			}
			return nil
		}
		off = body + n + n%2 // chunks are padded to an even size
	}
	return fmt.Errorf("the WAV header has no audio chunk")
}
