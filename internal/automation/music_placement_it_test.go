package automation

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/music"
)

// The house-tag import on real files (MUS-33): synthetic audio made by ffmpeg (lavfi
// noise, never real media), imported through the whole path with the real tag library and
// the real decoded-audio check. Runs only with ARRMADA_FFMPEG_IT=1, in an image with
// ffmpeg (arrmada-tests-goffmpeg).

func requireFFmpegIT(t *testing.T) {
	t.Helper()
	if os.Getenv("ARRMADA_FFMPEG_IT") != "1" {
		t.Skip("set ARRMADA_FFMPEG_IT=1 to run the real-file music import tests")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not on PATH but ARRMADA_FFMPEG_IT=1", bin)
		}
	}
}

// makeTrack encodes 12 s of noise (big enough for the import's 256 KB floor) with tags
// of its own that the house tags must replace (title) or keep (genre).
func makeTrack(t *testing.T, path string, codec ...string) {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "anoisesrc=d=12:c=pink:r=44100:a=0.3", "-ac", "2"}
	args = append(args, codec...)
	args = append(args, "-metadata", "title=Old Title", "-metadata", "genre=Shoegaze", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for x := 0; x < 16; x++ {
		for y := 0; y < 16; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: 80, B: uint8(y * 16), A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type jpegCovers struct{ img []byte }

func (j jpegCovers) Front(context.Context, string, string, int) ([]byte, error) { return j.img, nil }

const (
	itRG      = "6e3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"
	itRelease = "7e3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"
	itRec1    = "8e3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"
	itRec2    = "9e3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"
	itArtistA = "ae3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"
	itArtistB = "be3d2f1a-0b9c-4d8e-a7f6-5e4d3c2b1a09"
)

func TestHouseTagImportOnRealFiles(t *testing.T) {
	requireFFmpegIT(t)
	for _, f := range []struct {
		name, ext string
		codec     []string
	}{
		{"flac", ".flac", []string{"-c:a", "flac"}},
		{"mp3", ".mp3", []string{"-c:a", "libmp3lame", "-b:a", "320k"}},
		{"m4a", ".m4a", []string{"-c:a", "aac", "-b:a", "256k"}},
		{"ogg", ".ogg", []string{"-c:a", "libvorbis", "-q:a", "8"}},
	} {
		t.Run(f.name, func(t *testing.T) { houseImportOne(t, f.ext, f.codec) })
	}
}

func houseImportOne(t *testing.T, ext string, codec []string) {
	h, _ := musicImportHarness(t)
	cover := testJPEG(t)
	h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs {
		return MusicPlacementPrefs{WriteTags: true}
	}, jpegCovers{img: cover}, MusicAudioHash)

	// A Various Artists compilation whose second track features two artists.
	a := h.addArtist(t, "Various Artists", "", []string{"Sounds of the Test"}, nil, "1999-04-01")
	al := h.album(t, a.ID, "Sounds of the Test")
	if err := h.svc.EnsureTracks(h.ctx, al); err != nil {
		t.Fatal(err)
	}
	al = h.album(t, a.ID, "Sounds of the Test")
	db := h.st.DB()
	repo := music.NewRepo(db)
	pa, err := repo.CreateArtist(h.ctx, music.Artist{MBID: itArtistA, Name: "Ann Example"})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := repo.CreateArtist(h.ctx, music.Artist{MBID: itArtistB, Name: "Bo Sample"})
	if err != nil {
		t.Fatal(err)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	sqlExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(h.ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sqlExec(`UPDATE artists SET mbid = ? WHERE id = ?`, music.VariousArtistsMBID, a.ID)
	sqlExec(`UPDATE albums SET mbid = ?, year = 1999 WHERE id = ?`, itRG, al.ID)
	sqlExec(`UPDATE music_releases SET mbid = ?, status = 'Official', country = 'GB', release_date = '2001-02-03',
	        label = 'Test Label', catalog_number = 'TL-001', barcode = '0123456789012' WHERE id = ?`, itRelease, al.TargetEditionID)
	sqlExec(`UPDATE music_recordings SET mbid = ? WHERE id = ?`, itRec1, tracks[0].RecordingID)
	sqlExec(`UPDATE music_recordings SET mbid = ? WHERE id = ?`, itRec2, tracks[1].RecordingID)
	sqlExec(`INSERT INTO music_artist_credits (display, credit_key) VALUES ('Ann Example & Bo Sample', ?)`, itArtistA+"| & |"+itArtistB)
	var credit int64
	if err := db.QueryRowContext(h.ctx, `SELECT id FROM music_artist_credits WHERE credit_key = ?`, itArtistA+"| & |"+itArtistB).Scan(&credit); err != nil {
		t.Fatal(err)
	}
	sqlExec(`INSERT INTO music_artist_credit_names (credit_id, position, artist_id, credited_name, join_phrase) VALUES (?, 0, ?, 'Ann Example', ' & '), (?, 1, ?, 'Bo Sample', '')`,
		credit, pa.ID, credit, pb.ID)
	sqlExec(`UPDATE music_release_tracks SET artist_credit_id = ? WHERE id = ?`, credit, tracks[1].ID)
	if err := repo.AddExternalID(h.ctx, music.ExternalID{EntityType: "recording", EntityID: tracks[1].RecordingID, Source: "isrc", Value: "GBTST9900002"}); err != nil {
		t.Fatal(err)
	}

	dl := filepath.Join(t.TempDir(), "VA - Sounds of the Test (1999)")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	src1, src2 := filepath.Join(dl, "01 - One"+ext), filepath.Join(dl, "02 - Two"+ext)
	makeTrack(t, src1, codec...)
	makeTrack(t, src2, codec...)
	if err := os.Link(src1, filepath.Join(t.TempDir(), "seeding"+ext)); err != nil {
		t.Fatal(err)
	}
	before := stateOf(t, src1)
	srcMD5, err := MusicAudioHash(h.ctx, src2)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	res := h.c.importAlbumContent(h.ctx, a, al, dl, "VA - Sounds of the Test (1999) [MIX]")
	if res.placed != 2 || res.tagFailed != 0 {
		t.Fatalf("import = %+v", res)
	}
	if now := stateOf(t, src1); !bytes.Equal(now.content, before.content) || !now.mtime.Equal(before.mtime) || now.links != before.links {
		t.Errorf("the seeding file changed: links %d -> %d, mtime %v -> %v, same bytes %v",
			before.links, now.links, before.mtime, now.mtime, bytes.Equal(now.content, before.content))
	}
	tracks, _ = h.svc.Tracks(h.ctx, al.ID)
	two := tracks[1]
	if !two.HasFile || two.TagState != music.TagStateHouse {
		t.Fatalf("track Two: %+v", two)
	}
	if !strings.HasSuffix(filepath.ToSlash(two.FilePath), "Various Artists/Sounds of the Test (1999)/02 - Ann Example & Bo Sample - Two"+ext) {
		t.Errorf("compilation track named %s", two.FilePath)
	}
	got, err := audiotag.Read(two.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		audiotag.KeyTitle:            {"Two"},
		audiotag.KeyArtist:           {"Ann Example & Bo Sample"},
		audiotag.KeyArtists:          {"Ann Example", "Bo Sample"},
		audiotag.KeyAlbum:            {"Sounds of the Test"},
		audiotag.KeyAlbumArtist:      {"Various Artists"},
		audiotag.KeyAlbumArtists:     {"Various Artists"},
		audiotag.KeyTrackNumber:      {"2"},
		audiotag.KeyTrackTotal:       {"2"},
		audiotag.KeyDiscNumber:       {"1"},
		audiotag.KeyDiscTotal:        {"1"},
		audiotag.KeyDate:             {"2001-02-03"},
		audiotag.KeyOriginalDate:     {"1999-04-01"},
		audiotag.KeyCompilation:      {"1"},
		audiotag.KeyReleaseType:      {"album"},
		audiotag.KeyReleaseStatus:    {"official"},
		audiotag.KeyReleaseCountry:   {"GB"},
		audiotag.KeyLabel:            {"Test Label"},
		audiotag.KeyCatalogNumber:    {"TL-001"},
		audiotag.KeyBarcode:          {"0123456789012"},
		audiotag.KeyISRC:             {"GBTST9900002"},
		audiotag.KeyMBRecordingID:    {itRec2},
		audiotag.KeyMBReleaseID:      {itRelease},
		audiotag.KeyMBReleaseGroupID: {itRG},
		audiotag.KeyMBArtistID:       {itArtistA, itArtistB},
		audiotag.KeyMBAlbumArtistID:  {music.VariousArtistsMBID},
		audiotag.KeyGenre:            {"Shoegaze"}, // no source of Arrmada's: kept as found
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if img, err := audiotag.ReadFrontCover(two.FilePath); err != nil || !bytes.Equal(img, cover) {
		t.Errorf("embedded cover: %d bytes, %v", len(img), err)
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(two.FilePath), "cover.jpg")); err != nil || !bytes.Equal(b, cover) {
		t.Errorf("cover.jpg: %v", err)
	}
	if md5, err := MusicAudioHash(h.ctx, two.FilePath); err != nil || md5 != srcMD5 {
		t.Errorf("decoded audio changed: %s -> %s (%v)", srcMD5, md5, err)
	}
	// An independent reader agrees.
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags:stream_tags", "-of", "default=nw=1", two.FilePath).CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "title=two") {
		t.Errorf("ffprobe: %v\n%s", err, out)
	}
	var found string
	_ = db.QueryRowContext(h.ctx, `SELECT tags_json FROM music_track_files WHERE path = ?`, two.FilePath).Scan(&found)
	if !strings.Contains(found, "Old Title") {
		t.Errorf("tags as found weren't kept: %q", found)
	}
}
