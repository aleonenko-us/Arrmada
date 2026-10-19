package audiotag

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig reads a cover's size for the FLAC picture block
	_ "image/png"
	"io"
	"os"

	"github.com/bogem/id3v2/v2"
	flac "github.com/go-flac/go-flac"
)

// Embedded cover art. The house style embeds the album's front cover in every
// track (FLAC PICTURE block or Ogg METADATA_BLOCK_PICTURE, ID3 APIC, MP4 covr),
// picture type 3 ("Cover (front)"). Only the front cover is the house's: other
// pictures a file carries (a back cover, a booklet page) are kept.

// Picture is an image to embed.
type Picture struct {
	Data []byte
	// MIME is "image/jpeg" or "image/png"; empty is read from Data.
	MIME string
}

// MaxCoverBytes bounds an embedded cover. CAA's 500 px front is far smaller; a
// bigger image is refused rather than copied into every track of an album.
const MaxCoverBytes = 8 << 20

// pictureFront is the ID3/FLAC picture type of a front cover.
const pictureFront = 3

// ErrNoCover is returned by ReadFrontCover for a file with no front cover.
var ErrNoCover = errors.New("audiotag: no front cover")

// pictureMIME checks p and returns its MIME type: JPEG or PNG only, by content.
func pictureMIME(p *Picture) (string, error) {
	if p == nil || len(p.Data) == 0 {
		return "", errors.New("audiotag: empty cover image")
	}
	if len(p.Data) > MaxCoverBytes {
		return "", fmt.Errorf("audiotag: cover image is %d bytes, over the %d limit", len(p.Data), MaxCoverBytes)
	}
	var sniffed string
	switch {
	case bytes.HasPrefix(p.Data, []byte{0xFF, 0xD8, 0xFF}):
		sniffed = "image/jpeg"
	case bytes.HasPrefix(p.Data, []byte("\x89PNG\r\n\x1a\n")):
		sniffed = "image/png"
	default:
		return "", errors.New("audiotag: cover image is neither JPEG nor PNG")
	}
	if p.MIME != "" && p.MIME != sniffed {
		return "", fmt.Errorf("audiotag: cover says %s but holds %s", p.MIME, sniffed)
	}
	return sniffed, nil
}

// ReadFrontCover returns the front cover embedded in the file at path, using
// DefaultBackend. ErrNoCover when there is none.
func ReadFrontCover(path string) ([]byte, error) { return New(DefaultBackend).ReadFrontCover(path) }

// ReadFrontCover returns the file's embedded front cover (ErrNoCover when it has
// none). A file whose only pictures aren't marked as the front gets ErrNoCover;
// MP4 has no picture types, so its first covr image counts.
func (tg *Tagger) ReadFrontCover(path string) ([]byte, error) {
	f, ft, err := tg.open(path)
	if err != nil {
		return nil, fmt.Errorf("audiotag: read cover %s: %w", path, err)
	}
	defer f.Close()
	var img []byte
	switch {
	case tg.backendFor(ft) == TagLib:
		img, err = readCoverTagLib(path)
	case ft == formatFLAC:
		img, err = readFLACCover(f)
	case ft == formatMP3:
		img, err = readMP3Cover(f)
	case ft == formatMP4:
		img, err = readMP4Cover(f)
	}
	if err != nil {
		return nil, fmt.Errorf("audiotag: read cover %s: %w", path, err)
	}
	if len(img) == 0 {
		return nil, ErrNoCover
	}
	return img, nil
}

// flacPictureBlock builds a FLAC PICTURE metadata block (also the payload of an
// Ogg METADATA_BLOCK_PICTURE comment) for a front cover.
func flacPictureBlock(data []byte, mime string) *flac.MetaDataBlock {
	var w, h, depth uint32
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		w, h = uint32(cfg.Width), uint32(cfg.Height)
		depth = 24
	}
	var b bytes.Buffer
	put := func(v uint32) { _ = binary.Write(&b, binary.BigEndian, v) }
	put(pictureFront)
	put(uint32(len(mime)))
	b.WriteString(mime)
	put(0) // no description
	put(w)
	put(h)
	put(depth)
	put(0) // not an indexed-colour image
	put(uint32(len(data)))
	b.Write(data)
	return &flac.MetaDataBlock{Type: flac.Picture, Data: b.Bytes()}
}

// parseFLACPicture reads a PICTURE block's type and image data.
func parseFLACPicture(d []byte) (typ uint32, data []byte, ok bool) {
	r := bytes.NewReader(d)
	var mimeLen, descLen, dataLen uint32
	var skip [16]byte
	if binary.Read(r, binary.BigEndian, &typ) != nil || binary.Read(r, binary.BigEndian, &mimeLen) != nil {
		return 0, nil, false
	}
	if int64(mimeLen) > int64(r.Len()) {
		return 0, nil, false
	}
	if _, err := r.Seek(int64(mimeLen), io.SeekCurrent); err != nil {
		return 0, nil, false
	}
	if binary.Read(r, binary.BigEndian, &descLen) != nil || int64(descLen) > int64(r.Len()) {
		return 0, nil, false
	}
	if _, err := r.Seek(int64(descLen), io.SeekCurrent); err != nil {
		return 0, nil, false
	}
	if _, err := io.ReadFull(r, skip[:]); err != nil || binary.Read(r, binary.BigEndian, &dataLen) != nil {
		return 0, nil, false
	}
	if int64(dataLen) > int64(r.Len()) {
		return 0, nil, false
	}
	data = make([]byte, dataLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, nil, false
	}
	return typ, data, true
}

func readFLACCover(f *os.File) ([]byte, error) {
	if _, err := seekFLAC(f); err != nil {
		return nil, err
	}
	meta, err := flac.ParseMetadata(f)
	if err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}
	for _, b := range meta.Meta {
		if b.Type != flac.Picture {
			continue
		}
		if typ, data, ok := parseFLACPicture(b.Data); ok && typ == pictureFront {
			return data, nil
		}
	}
	return nil, nil
}

// setFLACCover replaces the front-cover PICTURE blocks in meta with one for p,
// keeping every other picture.
func setFLACCover(meta *flac.File, p *Picture) error {
	mime, err := pictureMIME(p)
	if err != nil {
		return err
	}
	kept := meta.Meta[:0:0]
	for _, b := range meta.Meta {
		if b.Type == flac.Picture {
			if typ, _, ok := parseFLACPicture(b.Data); ok && typ == pictureFront {
				continue
			}
		}
		kept = append(kept, b)
	}
	block := flacPictureBlock(p.Data, mime)
	if len(block.Data) > maxFLACBlock {
		return errors.New("flac: cover too large for a metadata block")
	}
	meta.Meta = append(kept, block)
	return nil
}

func readMP3Cover(f *os.File) ([]byte, error) {
	tag, _, err := parseID3(f)
	if err != nil {
		return nil, err
	}
	for _, fr := range tag.GetFrames("APIC") {
		if pf, ok := fr.(id3v2.PictureFrame); ok && pf.PictureType == pictureFront {
			return pf.Picture, nil
		}
	}
	return nil, nil
}

// setMP3Cover replaces the front-cover APIC frames with one for p.
func setMP3Cover(tag *id3v2.Tag, p *Picture) error {
	mime, err := pictureMIME(p)
	if err != nil {
		return err
	}
	// Copy before deleting: DeleteFrames hands the sequence back to a pool.
	old := append([]id3v2.Framer(nil), tag.GetFrames("APIC")...)
	tag.DeleteFrames("APIC")
	for _, fr := range old {
		if pf, ok := fr.(id3v2.PictureFrame); ok && pf.PictureType != pictureFront {
			tag.AddAttachedPicture(pf)
		}
	}
	tag.AddAttachedPicture(id3v2.PictureFrame{
		Encoding: id3v2.EncodingUTF8, MimeType: mime, PictureType: pictureFront, Picture: p.Data,
	})
	return nil
}

// MP4 cover data types.
const (
	dataJPEG = 13
	dataPNG  = 14
)

const atomCover = "covr"

func readMP4Cover(f *os.File) ([]byte, error) {
	info, err := inspectMP4(f)
	if err != nil {
		return nil, err
	}
	items, err := parseIlst(info.ilst)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.typ != atomCover {
			continue
		}
		for _, d := range it.data {
			if len(d.value) > 0 {
				return d.value, nil
			}
		}
	}
	return nil, nil
}

// mp4CoverItem builds a covr item holding p.
func mp4CoverItem(p *Picture) ([]byte, error) {
	mime, err := pictureMIME(p)
	if err != nil {
		return nil, err
	}
	kind := uint32(dataJPEG)
	if mime == "image/png" {
		kind = dataPNG
	}
	return box(atomCover, dataBox(kind, p.Data)), nil
}
