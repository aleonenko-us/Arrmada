package audiotag

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

var backends = []Backend{Permissive, TagLib}

// houseFixture fills every house field, with two or more values in every
// multi-value field and non-ASCII and slash-containing values throughout.
func houseFixture() Fields {
	return Fields{
		Title:        "Jóga (Live at Ásbyrgi)",
		Artist:       "Björk & Sigur Rós",
		Artists:      []string{"Björk", "Sigur Rós"},
		Album:        "Ágætis byrjun / Homogenic",
		AlbumArtist:  "AC/DC & Simon & Garfunkel",
		AlbumArtists: []string{"AC/DC", "Simon & Garfunkel", "Garfunkel"},
		TrackNumber:  3,
		TrackTotal:   12,
		DiscNumber:   1,
		DiscTotal:    2,
		Date:         "1999-06-12",
		OriginalDate: "1997-09-22",
		Genres:       []string{"Post-Rock", "Hard Rock", "Électronique"},
		Compilation:  true,

		MusicBrainzRecordingID:    "0c4f8a52-6c4d-4f27-9c9e-5b0c1a7d2f11",
		MusicBrainzReleaseTrackID: "7d1e5b88-2f4a-4a3c-8a8e-1f2b3c4d5e6f",
		MusicBrainzReleaseID:      "c2a5c1c2-1b9c-4d6e-9e3a-2b8f7a6d5c4b",
		MusicBrainzReleaseGroupID: "f1e2d3c4-b5a6-4978-8a9b-0c1d2e3f4a5b",
		MusicBrainzArtistIDs:      []string{"87c5dedd-371d-4a53-9f7f-80522fb7f3cb", "f6f2326f-6b25-4170-b89d-e235b25508e8"},
		MusicBrainzAlbumArtistIDs: []string{"66c662b6-6e2f-4930-8610-912e24c63ed1", "5d02f264-e225-41ff-83f7-d9b1f0b1874a"},

		ReleaseType:    []string{"album", "live"},
		ReleaseStatus:  "official",
		ReleaseCountry: "IS",
		Label:          "Smekkleysa / Fat Cat",
		CatalogNumber:  "SM 75CD",
		Barcode:        "5099749476224",
		ISRC:           "ISIS19900001",
		Media:          "CD",

		ReplayGainTrackGain: "-6.48 dB",
		ReplayGainTrackPeak: "0.988553",
		ReplayGainAlbumGain: "-7.10 dB",
		ReplayGainAlbumPeak: "1.000000",
	}
}

// diffTags reports every key that differs, so a failure names the broken
// mapping rather than dumping two maps.
func diffTags(t *testing.T, got, want Tags) {
	t.Helper()
	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if !reflect.DeepEqual(got[k], want[k]) {
			t.Errorf("%s: got %q, want %q", k, got[k], want[k])
		}
	}
}

func TestFixtureCoversEveryHouseKey(t *testing.T) {
	tags := houseFixture().Tags()
	for _, k := range houseKeys {
		if len(tags[k]) == 0 {
			t.Errorf("fixture has no %s", k)
		}
	}
	if len(tags) != len(houseKeys) {
		t.Errorf("fixture has %d keys, houseKeys has %d", len(tags), len(houseKeys))
	}
	if !reflect.DeepEqual(FieldsOf(tags), houseFixture()) {
		t.Error("Fields -> Tags -> Fields is not lossless")
	}
	// Canonical Tags pass through both normalisers unchanged.
	clean, err := prepareWrite(tags)
	if err != nil {
		t.Fatal(err)
	}
	diffTags(t, clean, tags)
	diffTags(t, normalizeRead(tags), tags)
}

func TestNormalizeRead(t *testing.T) {
	got := normalizeRead(map[string][]string{
		"tracknumber": {"3/12"},
		"DISCNUMBER":  {"02"},
		"TOTALDISCS":  {"4"},
		"Compilation": {"true"},
		"TITLE":       {"first", "second"},
		"GENRE":       {"Rock", " ", "Pop"},
		"COMMENT":     {"not a house key"},
		"ISRC":        {""},
	})
	want := Tags{
		KeyTrackNumber: {"3"}, KeyTrackTotal: {"12"},
		KeyDiscNumber: {"2"}, KeyDiscTotal: {"4"},
		KeyCompilation: {"1"},
		KeyTitle:       {"first"},
		KeyGenre:       {"Rock", "Pop"},
	}
	diffTags(t, got, want)

	// The canonical total wins over an alias, and a false compilation vanishes.
	got = normalizeRead(map[string][]string{"TRACKTOTAL": {"9"}, "TOTALTRACKS": {"8"}, "TRACKNUMBER": {"1"}, "COMPILATION": {"0"}})
	diffTags(t, got, Tags{KeyTrackNumber: {"1"}, KeyTrackTotal: {"9"}})
}

func TestPrepareWrite(t *testing.T) {
	got, err := prepareWrite(Tags{"title": {" x "}, "TRACKNUMBER": {"03"}, "COMPILATION": {"no"}, "GENRE": {"", "Rock"}})
	if err != nil {
		t.Fatal(err)
	}
	diffTags(t, got, Tags{KeyTitle: {"x"}, KeyTrackNumber: {"3"}, KeyGenre: {"Rock"}})

	for name, bad := range map[string]Tags{
		"unknown key":    {"MOOD": {"calm"}},
		"two titles":     {KeyTitle: {"a", "b"}},
		"bad number":     {KeyTrackNumber: {"3/12"}},
		"zero disc":      {KeyDiscNumber: {"0"}},
		"vertical tab":   {KeyArtists: {"a\vb"}},
		"embedded NUL":   {KeyArtists: {"a\x00b"}},
		"too big number": {KeyTrackTotal: {"70000"}},
	} {
		if _, err := prepareWrite(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIlstRoundTripAndPreservation(t *testing.T) {
	foreign := box("----", box("mean", u32(0), []byte(itunesMean)), box("name", u32(0), []byte("MOOD")), dataBox(dataUTF8, []byte("calm")))
	comment := box("\xa9cmt", dataBox(dataUTF8, []byte("keep me")))
	staleTitle := box("\xa9nam", dataBox(dataUTF8, []byte("old title")))
	staleArtists := box("----", box("mean", u32(0), []byte(itunesMean)), box("name", u32(0), []byte("artists")), dataBox(dataUTF8, []byte("old")))
	oldGenre := box("gnre", dataBox(dataImplicit, []byte{0, 18}))
	old, err := parseIlst(bytes.Join([][]byte{foreign, staleTitle, comment, staleArtists, oldGenre}, nil))
	if err != nil {
		t.Fatal(err)
	}

	want := houseFixture().Tags()
	payload := buildIlst(old, want)
	items, err := parseIlst(payload)
	if err != nil {
		t.Fatal(err)
	}
	diffTags(t, normalizeRead(ilstFields(items)), want)

	// Foreign items survive verbatim and first; stale house items are gone.
	if !bytes.HasPrefix(payload, append(append([]byte{}, foreign...), comment...)) {
		t.Error("foreign items not preserved verbatim at the front")
	}
	n := 0
	for _, it := range items {
		switch {
		case it.typ == "gnre":
			t.Error("old numeric genre survived")
		case it.typ == "----" && it.name == "artists":
			t.Error("stale lower-case ARTISTS freeform survived")
		case it.typ == "----" && it.name == "ARTISTS":
			// One atom with one data child per value, not one atom per value.
			n++
			if len(it.data) != 2 {
				t.Errorf("ARTISTS has %d data atoms, want 2", len(it.data))
			}
		}
	}
	if n != 1 {
		t.Errorf("%d ARTISTS atoms, want 1", n)
	}
}

func TestCheckID3Frames(t *testing.T) {
	frame := func(id string, flags byte, body string) []byte {
		b := append([]byte(id), 0, 0, 0, byte(len(body)), 0, flags)
		return append(b, body...)
	}
	ok := append(frame("TIT2", 0, "\x03hi"), 0, 0, 0, 0) // plus padding
	if err := checkID3Frames(ok, 4); err != nil {
		t.Errorf("plain frame refused: %v", err)
	}
	for _, flags := range []byte{0x01, 0x02, 0x04, 0x08, 0x40} {
		if err := checkID3Frames(frame("TIT2", flags, "\x03hi"), 4); !errors.Is(err, ErrUnsupported) {
			t.Errorf("v2.4 format flags %#x accepted", flags)
		}
	}
	if err := checkID3Frames(frame("TIT2", 0x80, "\x03hi"), 3); !errors.Is(err, ErrUnsupported) {
		t.Error("v2.3 compressed frame accepted")
	}
	overrun := frame("TIT2", 0, "\x03hi")
	overrun[7] = 50
	if err := checkID3Frames(overrun, 4); !errors.Is(err, ErrUnsupported) {
		t.Error("overrunning frame accepted")
	}
}

func TestMP3TrailerStart(t *testing.T) {
	audio := bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x00}, 64)
	ape := func() []byte {
		item := []byte("\x05\x00\x00\x00\x00\x00\x00\x00Title\x00hello")
		footer := make([]byte, 32)
		copy(footer, "APETAGEX")
		footer[8] = 0xD0 // version 2000
		footer[9] = 0x07
		size := len(item) + 32
		footer[12] = byte(size)
		footer[16] = 1 // one item
		return append(item, footer...)
	}()
	v1 := append([]byte("TAG"), make([]byte, 125)...)
	for name, tail := range map[string][]byte{
		"none":     nil,
		"id3v1":    v1,
		"ape":      ape,
		"ape+v1":   append(append([]byte{}, ape...), v1...),
		"lyrics+1": append(append([]byte("LYRICSBEGINhi"), []byte("000013LYRICS200")...), v1...),
	} {
		p := filepath.Join(t.TempDir(), "x.mp3")
		if err := os.WriteFile(p, append(append([]byte{}, audio...), tail...), 0o644); err != nil {
			t.Fatal(err)
		}
		f, _ := os.Open(p)
		end, err := mp3TrailerStart(f)
		f.Close()
		if err != nil || end != int64(len(audio)) {
			t.Errorf("%s: end %d, %v; want %d", name, end, err, len(audio))
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.flac")
	if err := os.WriteFile(p, []byte("just some text, not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, b := range backends {
		tg := New(b)
		if _, err := tg.Read(p); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s Read: got %v, want ErrUnsupported", b, err)
		}
		if err := tg.Write(p, houseFixture().Tags(), WriteOptions{}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s Write: got %v, want ErrUnsupported", b, err)
		}
	}
}

// A write that fails part-way must leave the original byte-identical and no
// temporary file behind.
func TestFailedWriteLeavesOriginal(t *testing.T) {
	for _, b := range backends {
		dir := t.TempDir()
		p := filepath.Join(dir, "broken.flac")
		// "fLaC" + a STREAMINFO header claiming 34 bytes, but only 10 follow.
		orig := append([]byte("fLaC\x80\x00\x00\x22"), make([]byte, 10)...)
		if err := os.WriteFile(p, orig, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := New(b).Write(p, houseFixture().Tags(), WriteOptions{}); err == nil {
			t.Errorf("%s: Write of a truncated file succeeded", b)
		}
		if got, _ := os.ReadFile(p); !bytes.Equal(got, orig) {
			t.Errorf("%s: original was modified", b)
		}
		assertOnlyFile(t, dir, "broken.flac")
	}
}

func TestReplaceFileCleansUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := replaceFile(p, func(dst *os.File) error {
		if filepath.Ext(dst.Name()) != ".mp3" {
			t.Errorf("temp file %q lost the extension", dst.Name())
		}
		_, _ = dst.WriteString("half")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "original" {
		t.Errorf("original changed to %q", b)
	}
	assertOnlyFile(t, dir, "a.mp3")

	if err := replaceFile(p, func(dst *os.File) error {
		_, err := dst.WriteString("new")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("content %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 && os.PathSeparator == '/' {
		t.Errorf("mode %v, want 0640 kept", fi.Mode().Perm())
	}
	assertOnlyFile(t, dir, "a.mp3")
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != name {
			t.Errorf("leftover file %q", e.Name())
		}
	}
}
