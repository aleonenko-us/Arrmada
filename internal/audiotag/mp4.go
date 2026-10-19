package audiotag

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	mp4 "github.com/abema/go-mp4"
)

// MP4 metadata lives in moov/udta/meta/ilst. Each ilst item is an atom named for
// the field (e.g. "\xa9nam" for the title) holding one "data" child per value.
// Fields without a standard atom use freeform "----" atoms, identified by a
// "mean" (always com.apple.iTunes here) and a "name".
const itunesMean = "com.apple.iTunes"

// Atoms the house set owns besides mp4Fields. "gnre" is the old numeric genre;
// it is removed whenever genres are written so it can't contradict "\xa9gen".
const (
	atomTrack       = "trkn"
	atomDisc        = "disk"
	atomCompilation = "cpil"
	atomOldGenre    = "gnre"
)

// iTunes "well-known" data types.
const (
	dataImplicit = 0  // binary, meaning given by the atom (trkn, disk)
	dataUTF8     = 1  // text
	dataInt      = 21 // big-endian signed integer (cpil)
)

type ilstItem struct {
	typ        string
	raw        []byte // the whole item, written back verbatim when we don't own it
	mean, name string // freeform items only
	data       []ilstData
}

type ilstData struct {
	kind  uint32 // well-known type (low 24 bits of the type indicator)
	value []byte
}

func readMP4(f *os.File) (map[string][]string, error) {
	info, err := inspectMP4(f)
	if err != nil {
		return nil, err
	}
	items, err := parseIlst(info.ilst)
	if err != nil {
		return nil, err
	}
	return ilstFields(items), nil
}

// ilstFields extracts the house fields from parsed ilst items.
func ilstFields(items []ilstItem) map[string][]string {
	m := map[string][]string{}
	for _, it := range items {
		switch it.typ {
		case atomTrack, atomDisc:
			if v := it.firstData(dataImplicit); len(v) >= 6 {
				pair := fmt.Sprintf("%d/%d", binary.BigEndian.Uint16(v[2:4]), binary.BigEndian.Uint16(v[4:6]))
				if it.typ == atomTrack {
					m[KeyTrackNumber] = []string{pair}
				} else {
					m[KeyDiscNumber] = []string{pair}
				}
			}
			continue
		case atomCompilation:
			if v := it.firstData(dataInt); len(v) > 0 && v[len(v)-1] != 0 {
				m[KeyCompilation] = []string{"1"}
			}
			continue
		}
		fd, ok := mp4FieldFor(it)
		if !ok {
			continue
		}
		for _, d := range it.data {
			if d.kind == dataUTF8 && len(d.value) > 0 {
				m[fd.key] = append(m[fd.key], string(d.value))
			}
		}
	}
	return m
}

func mp4FieldFor(it ilstItem) (mp4Field, bool) {
	for _, fd := range mp4Fields {
		if fd.atom != it.typ {
			continue
		}
		if fd.atom != "----" || (it.mean == itunesMean && strings.EqualFold(fd.name, it.name)) {
			return fd, true
		}
	}
	return mp4Field{}, false
}

func (it ilstItem) firstData(kind uint32) []byte {
	for _, d := range it.data {
		if d.kind == kind {
			return d.value
		}
	}
	return nil
}

func (it ilstItem) isHouse() bool {
	switch it.typ {
	case atomTrack, atomDisc, atomCompilation, atomOldGenre:
		return true
	}
	_, ok := mp4FieldFor(it)
	return ok
}

// buildIlst returns the new ilst payload: every item we don't own, verbatim and
// in order, followed by the house fields from m.
func buildIlst(old []ilstItem, m Tags) []byte {
	var out bytes.Buffer
	for _, it := range old {
		if !it.isHouse() {
			out.Write(it.raw)
		}
	}
	for _, fd := range mp4Fields {
		vals := m[fd.key]
		if len(vals) == 0 {
			continue
		}
		var children [][]byte
		if fd.atom == "----" {
			children = append(children,
				box("mean", u32(0), []byte(itunesMean)),
				box("name", u32(0), []byte(fd.name)))
		}
		for _, v := range vals {
			children = append(children, dataBox(dataUTF8, []byte(v)))
		}
		out.Write(box(fd.atom, children...))
	}
	writePair := func(atom, nKey, tKey string, trailer bool) {
		n, t := itoa16(m.first(nKey)), itoa16(m.first(tKey))
		if n == 0 && t == 0 {
			return
		}
		v := make([]byte, 6, 8)
		binary.BigEndian.PutUint16(v[2:], n)
		binary.BigEndian.PutUint16(v[4:], t)
		if trailer {
			v = append(v, 0, 0) // trkn carries two extra reserved bytes; disk doesn't
		}
		out.Write(box(atom, dataBox(dataImplicit, v)))
	}
	writePair(atomTrack, KeyTrackNumber, KeyTrackTotal, true)
	writePair(atomDisc, KeyDiscNumber, KeyDiscTotal, false)
	if parseBool(m.first(KeyCompilation)) {
		out.Write(box(atomCompilation, dataBox(dataInt, []byte{1})))
	}
	return out.Bytes()
}

// parseIlst splits an ilst payload into items. Children other than mean, name
// and data are ignored (they stay in the item's raw bytes).
func parseIlst(b []byte) ([]ilstItem, error) {
	var items []ilstItem
	for len(b) >= 8 {
		size, typ, hdr, err := rawBoxHeader(b)
		if err != nil {
			return nil, fmt.Errorf("mp4: ilst: %w", err)
		}
		it := ilstItem{typ: typ, raw: b[:size]}
		for c := b[hdr:size]; len(c) >= 8; {
			csize, ctyp, chdr, err := rawBoxHeader(c)
			if err != nil {
				return nil, fmt.Errorf("mp4: ilst item %q: %w", typ, err)
			}
			p := c[chdr:csize]
			switch {
			case (ctyp == "mean" || ctyp == "name") && len(p) >= 4:
				if ctyp == "mean" {
					it.mean = string(p[4:])
				} else {
					it.name = string(p[4:])
				}
			case ctyp == "data" && len(p) >= 8:
				it.data = append(it.data, ilstData{kind: binary.BigEndian.Uint32(p) & 0xFFFFFF, value: p[8:]})
			}
			c = c[csize:]
		}
		items = append(items, it)
		b = b[size:]
	}
	return items, nil
}

// rawBoxHeader decodes the box header at the start of b.
func rawBoxHeader(b []byte) (size int, typ string, hdr int, err error) {
	if len(b) < 8 {
		return 0, "", 0, errors.New("short box header")
	}
	s, hdr := uint64(binary.BigEndian.Uint32(b)), 8
	switch s {
	case 0: // extends to the end of the parent
		s = uint64(len(b))
	case 1:
		if len(b) < 16 {
			return 0, "", 0, errors.New("short box header")
		}
		s, hdr = binary.BigEndian.Uint64(b[8:]), 16
	}
	if s < uint64(hdr) || s > uint64(len(b)) {
		return 0, "", 0, fmt.Errorf("bad box size %d", s)
	}
	return int(s), string(b[4:8]), hdr, nil
}

func box(typ string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	out := make([]byte, 8, n)
	binary.BigEndian.PutUint32(out, uint32(n))
	copy(out[4:], typ)
	for _, p := range payload {
		out = append(out, p...)
	}
	return out
}

func dataBox(kind uint32, v []byte) []byte {
	return box("data", u32(kind), u32(0), v) // type indicator, locale, value
}

func u32(v uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, v)
}

// mp4Layout is what inspectMP4 learns about a file.
type mp4Layout struct {
	moov    mp4.BoxInfo
	hasUdta bool // moov/udta
	hasMeta bool // moov/udta/meta
	hasIlst bool // moov/udta/meta/ilst
	ilst    []byte
}

func pathString(p mp4.BoxPath) string {
	parts := make([]string, len(p))
	for i, t := range p {
		parts[i] = string(t[:])
	}
	return strings.Join(parts, "/")
}

// inspectMP4 finds moov and the iTunes ilst. Fragmented files and QuickTime
// "mdta" metadata under udta are refused: tags in those need different handling.
func inspectMP4(f io.ReadSeeker) (*mp4Layout, error) {
	var l mp4Layout
	moovs := 0
	_, err := mp4.ReadBoxStructure(f, func(h *mp4.ReadHandle) (interface{}, error) {
		switch pathString(h.Path) {
		case "moov":
			moovs++
			l.moov = h.BoxInfo
			return h.Expand()
		case "moof", "moov/mvex":
			return nil, fmt.Errorf("%w: fragmented MP4", ErrUnsupported)
		case "moov/udta":
			l.hasUdta = true
			return h.Expand()
		case "moov/udta/meta":
			l.hasMeta = true
			return h.Expand()
		case "moov/udta/meta/hdlr":
			var buf bytes.Buffer
			if _, err := h.ReadData(&buf); err != nil {
				return nil, err
			}
			if p := buf.Bytes(); len(p) >= 12 && string(p[8:12]) != "mdir" {
				return nil, fmt.Errorf("%w: udta metadata handler %q", ErrUnsupported, p[8:12])
			}
		case "moov/udta/meta/keys":
			return nil, fmt.Errorf("%w: QuickTime mdta metadata", ErrUnsupported)
		case "moov/udta/meta/ilst":
			var buf bytes.Buffer
			if _, err := h.ReadData(&buf); err != nil {
				return nil, err
			}
			l.hasIlst = true
			l.ilst = buf.Bytes()
		}
		return nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("mp4: %w", err)
	}
	if moovs != 1 {
		return nil, fmt.Errorf("%w: %d moov boxes", ErrUnsupported, moovs)
	}
	return &l, nil
}

// writeMP4 rebuilds moov with the new ilst and copies everything else of the
// file unchanged. When moov sits before the media data (a "fast start" file)
// and changes size, every chunk offset in stco/co64 is shifted by the same
// amount so it still points at the same, unmoved audio bytes.
func writeMP4(src *os.File, dst io.Writer, m Tags, opts WriteOptions) error {
	m = applyGenreJoin(m, opts.MP4GenreSeparator)
	return rewriteMP4(src, dst, func(items []ilstItem) []byte {
		return buildIlst(items, m)
	})
}

// rewriteMP4 copies src to dst with the ilst payload replaced by what newIlst
// returns for the current items.
func rewriteMP4(src *os.File, dst io.Writer, newIlst func([]ilstItem) []byte) error {
	l, err := inspectMP4(src)
	if err != nil {
		return err
	}
	items, err := parseIlst(l.ilst)
	if err != nil {
		return err
	}
	ilst := newIlst(items)

	// First build with offsets untouched to learn the new size, then rebuild
	// with the shift if the size changed.
	moov, err := buildMoov(src, l, ilst, 0)
	if err != nil {
		return err
	}
	if delta := int64(len(moov)) - int64(l.moov.Size); delta != 0 {
		if moov, err = buildMoov(src, l, ilst, delta); err != nil {
			return err
		}
	}

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.CopyN(dst, src, int64(l.moov.Offset)); err != nil {
		return err
	}
	if _, err := dst.Write(moov); err != nil {
		return err
	}
	if _, err := src.Seek(int64(l.moov.Offset+l.moov.Size), io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

// buildMoov writes a new moov box into memory. Containers on the way to the
// ilst and to the chunk offset tables are re-emitted (go-mp4's Writer fixes
// their sizes); every other box is copied byte for byte.
func buildMoov(src io.ReadSeeker, l *mp4Layout, ilst []byte, delta int64) ([]byte, error) {
	var out memWriteSeeker
	w := mp4.NewWriter(&out)
	moovStart := l.moov.Offset
	// Don't create empty udta/meta/ilst boxes in a file that had none.
	create := len(ilst) > 0

	_, err := mp4.ReadBoxStructureFromInternal(src, &l.moov, func(h *mp4.ReadHandle) (interface{}, error) {
		p := pathString(h.Path)
		switch p {
		case "moov", "moov/trak", "moov/trak/mdia", "moov/trak/mdia/minf",
			"moov/trak/mdia/minf/stbl", "moov/udta", "moov/udta/meta":
			if _, err := w.StartBox(&h.BoxInfo); err != nil {
				return nil, err
			}
			// Some containers have fields before their children (meta's
			// version/flags, absent in QuickTime-style meta); copy them raw.
			_, n, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			if n > 0 {
				if err := copyRange(w, src, int64(h.BoxInfo.Offset+h.BoxInfo.HeaderSize), int64(n)); err != nil {
					return nil, err
				}
			}
			if _, err := h.Expand(); err != nil {
				return nil, err
			}
			switch {
			case p == "moov" && !l.hasUdta && create:
				_, err = w.Write(box("udta", newMeta(ilst)))
			case p == "moov/udta" && !l.hasMeta && create:
				_, err = w.Write(newMeta(ilst))
			case p == "moov/udta/meta" && !l.hasIlst && create:
				_, err = w.Write(box("ilst", ilst))
			}
			if err != nil {
				return nil, err
			}
			_, err = w.EndBox()
			return nil, err
		case "moov/udta/meta/ilst":
			_, err := w.Write(box("ilst", ilst))
			return nil, err
		case "moov/trak/mdia/minf/stbl/stco", "moov/trak/mdia/minf/stbl/co64":
			if delta == 0 {
				return nil, w.CopyBox(src, &h.BoxInfo)
			}
			b, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			if err := shiftChunkOffsets(b, moovStart, delta); err != nil {
				return nil, err
			}
			if _, err := w.StartBox(&h.BoxInfo); err != nil {
				return nil, err
			}
			if _, err := mp4.Marshal(w, b, h.BoxInfo.Context); err != nil {
				return nil, err
			}
			_, err = w.EndBox()
			return nil, err
		}
		return nil, w.CopyBox(src, &h.BoxInfo)
	})
	if err != nil {
		return nil, fmt.Errorf("mp4: rebuild moov: %w", err)
	}
	return out.buf, nil
}

// shiftChunkOffsets moves every chunk offset that points past moov's start
// (i.e. data stored after moov) by delta. Offsets before moov don't move.
func shiftChunkOffsets(b mp4.IBox, moovStart uint64, delta int64) error {
	shift := func(o uint64, limit uint64) (uint64, error) {
		if o <= moovStart {
			return o, nil
		}
		n := int64(o) + delta
		if n < 0 || uint64(n) > limit {
			return 0, fmt.Errorf("%w: chunk offset out of range after shift", ErrUnsupported)
		}
		return uint64(n), nil
	}
	switch t := b.(type) {
	case *mp4.Stco:
		for i, o := range t.ChunkOffset {
			n, err := shift(uint64(o), math.MaxUint32)
			if err != nil {
				return err
			}
			t.ChunkOffset[i] = uint32(n)
		}
	case *mp4.Co64:
		for i, o := range t.ChunkOffset {
			n, err := shift(o, math.MaxInt64)
			if err != nil {
				return err
			}
			t.ChunkOffset[i] = n
		}
	default:
		return fmt.Errorf("mp4: unexpected chunk offset box %T", b)
	}
	return nil
}

// newMeta builds an iTunes-style meta box (version/flags, an "mdir" handler,
// then the ilst) for files that have no metadata yet.
func newMeta(ilst []byte) []byte {
	hdlr := box("hdlr",
		u32(0),         // version + flags
		u32(0),         // pre_defined
		[]byte("mdir"), // handler type
		[]byte("appl"), // reserved; iTunes puts its vendor code here
		u32(0), u32(0), // reserved
		[]byte{0}) // empty name
	return box("meta", u32(0), hdlr, box("ilst", ilst))
}

// memWriteSeeker is the in-memory io.WriteSeeker go-mp4's Writer needs to patch
// box sizes after writing the children.
type memWriteSeeker struct {
	buf []byte
	pos int
}

func (m *memWriteSeeker) Write(p []byte) (int, error) {
	if end := m.pos + len(p); end > len(m.buf) {
		m.buf = append(m.buf, make([]byte, end-len(m.buf))...)
	}
	copy(m.buf[m.pos:], p)
	m.pos += len(p)
	return len(p), nil
}

func (m *memWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = int64(m.pos)
	case io.SeekEnd:
		base = int64(len(m.buf))
	default:
		return 0, errors.New("memWriteSeeker: bad whence")
	}
	n := base + offset
	if n < 0 || n > math.MaxInt32 {
		return 0, errors.New("memWriteSeeker: seek out of range")
	}
	m.pos = int(n)
	return n, nil
}
