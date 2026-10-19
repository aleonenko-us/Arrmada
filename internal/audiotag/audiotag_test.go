package audiotag

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// houseFixture fills every house field, with two or more values in every
// multi-value field and non-ASCII and slash-containing values throughout.
func houseFixture() Tags {
	return Tags{
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

// diffTags reports every field that differs, by name, so a failure says which
// mapping is broken rather than dumping two structs.
func diffTags(t *testing.T, got, want Tags) {
	t.Helper()
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := 0; i < gv.NumField(); i++ {
		g, w := gv.Field(i).Interface(), wv.Field(i).Interface()
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s: got %#v, want %#v", gv.Type().Field(i).Name, g, w)
		}
	}
}

func TestFieldsRoundTrip(t *testing.T) {
	want := houseFixture()
	diffTags(t, tagsFromFields(want.fields()), want)

	// Every house key is produced by a full Tags, so houseKeys can't drift from
	// the conversion tables.
	m := want.fields()
	for _, k := range houseKeys {
		if len(m[k]) == 0 {
			t.Errorf("full fixture produced no %s", k)
		}
	}
	if len(m) != len(houseKeys) {
		t.Errorf("fields() has %d keys, houseKeys has %d", len(m), len(houseKeys))
	}
}

func TestEmptyFieldsAreAbsent(t *testing.T) {
	m := Tags{Artists: []string{"", "  "}, Title: " "}.fields()
	if len(m) != 0 {
		t.Fatalf("empty values produced fields: %v", m)
	}
}

func TestTagsFromFieldsLenient(t *testing.T) {
	got := tagsFromFields(fieldMap{
		keyTrackNumber: {"3/12"},
		keyDiscNumber:  {"02"},
		keyDiscTotal:   {"4"},
		keyCompilation: {"true"},
		keyTitle:       {"first", "second"},
	})
	if got.TrackNumber != 3 || got.TrackTotal != 12 || got.DiscNumber != 2 || got.DiscTotal != 4 {
		t.Errorf("numbers: %+v", got)
	}
	if !got.Compilation {
		t.Error("compilation not parsed")
	}
	if got.Title != "first" {
		t.Errorf("single-value field should take the first value, got %q", got.Title)
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

	want := houseFixture()
	payload := buildIlst(old, want.fields())
	items, err := parseIlst(payload)
	if err != nil {
		t.Fatal(err)
	}
	diffTags(t, tagsFromFields(ilstFields(items)), want)

	// Foreign items survive verbatim and first; stale house items are gone.
	if !bytes.HasPrefix(payload, append(append([]byte{}, foreign...), comment...)) {
		t.Error("foreign items not preserved verbatim at the front")
	}
	for _, it := range items {
		if it.typ == "gnre" {
			t.Error("old numeric genre survived")
		}
		if it.typ == "----" && it.name == "artists" {
			t.Error("stale lower-case ARTISTS freeform survived")
		}
	}
	// ARTISTS is one atom with two data children, not two atoms.
	n := 0
	for _, it := range items {
		if it.typ == "----" && it.name == "ARTISTS" {
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

func TestUnknownFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.flac")
	if err := os.WriteFile(p, []byte("just some text, not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Read: got %v, want ErrUnsupported", err)
	}
	if err := Write(p, houseFixture()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Write: got %v, want ErrUnsupported", err)
	}
}

// A write that fails part-way must leave the original byte-identical and no
// temporary file behind.
func TestFailedWriteLeavesOriginal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "broken.flac")
	// "fLaC" + a STREAMINFO header claiming 34 bytes, but only 10 follow.
	orig := append([]byte("fLaC\x80\x00\x00\x22"), make([]byte, 10)...)
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, houseFixture()); err == nil {
		t.Fatal("Write of a truncated file succeeded")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Error("original was modified")
	}
	assertOnlyFile(t, dir, "broken.flac")
}

func TestReplaceFileCleansUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := replaceFile(p, func(dst *os.File) error {
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
