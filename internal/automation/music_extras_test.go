package automation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/music/stage"
)

// withHouseTagsAndExtras turns house tags and the default extras on for a harness.
func withHouseTagsAndExtras(h *musicHarness) *fakeCovers {
	covers := &fakeCovers{}
	h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs {
		return MusicPlacementPrefs{WriteTags: true, Extras: stage.ParseExtras(stage.DefaultExtras)}
	}, covers, houseHash)
	h.c.musicPlace.tagger = (&houseFake{}).tagger()
	return covers
}

// MUS-36: the download's booklet, scans, rip log and lyrics arrive beside the album, the
// lyrics renamed with their track; a downloaded cover is kept as folder.jpg; files off the
// allowlist stay behind; and nothing already in the album folder is overwritten.
func TestImportBringsExtras(t *testing.T) {
	for _, tagsOn := range []bool{true, false} {
		t.Run(map[bool]string{true: "tags on", false: "tags off"}[tagsOn], func(t *testing.T) {
			h, _ := musicImportHarness(t)
			withHouseTagsAndExtras(h)
			if !tagsOn {
				h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs {
					return MusicPlacementPrefs{Extras: stage.ParseExtras(stage.DefaultExtras)}
				}, nil, nil)
			}
			a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
			al := h.album(t, a.ID, "OK Computer")
			_ = h.svc.EnsureTracks(h.ctx, al)
			albumDir := filepath.Dir(h.c.imp.MusicTrackTarget(library.MusicName{AlbumArtist: "Radiohead", AlbumTitle: "OK Computer", Disc: 1, DiscCount: 1, Track: 1}, ".flac"))
			// A booklet already in the album folder is someone's: never overwritten.
			if err := os.MkdirAll(albumDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(albumDir, "booklet.pdf"), []byte("MINE"), 0o644); err != nil {
				t.Fatal(err)
			}
			dl, _ := houseDownload(t, "Radiohead - OK Computer (1997) [FLAC]")
			if res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), dl, "Radiohead - OK Computer (1997) [FLAC]"); res.placed != 2 {
				t.Fatalf("import %+v", res)
			}
			for name, want := range map[string]string{"booklet.pdf": "MINE", "01 - One.lrc": "[00:01]la", "folder.jpg": "DLCOVER", "rip.log": "EAC", "Scans/back.jpg": "SCAN"} {
				if b, err := os.ReadFile(filepath.Join(albumDir, name)); err != nil || string(b) != want {
					t.Errorf("extra %s: %q %v", name, b, err)
				}
			}
			if _, err := os.Stat(filepath.Join(albumDir, "x.exe")); err == nil {
				t.Error("a file off the allowlist came along")
			}
			// Extras are copies: a media server writing to one can't touch the seeding file.
			si, _ := os.Stat(filepath.Join(dl, "rip.log"))
			di, _ := os.Stat(filepath.Join(albumDir, "rip.log"))
			if os.SameFile(si, di) {
				t.Error("an extra shares its bytes with the seeding download")
			}
		})
	}
}

// A torrent saved without a folder of its own reports the shared category folder as its
// content: nothing in it is taken as this album's extras.
func TestExtrasNotFromASharedFolder(t *testing.T) {
	h, _ := musicImportHarness(t)
	withHouseTagsAndExtras(h)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	shared := filepath.Join(t.TempDir(), "music") // the category folder, holding everything
	blobTrack(t, shared, "01 - One.flac", 1)
	blobTrack(t, shared, "02 - Two.flac", 2)
	if err := os.WriteFile(filepath.Join(shared, "someone-elses-booklet.pdf"), []byte("NOT OURS"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), shared, "Radiohead - OK Computer (1997) [FLAC]"); res.placed != 2 {
		t.Fatalf("import %+v", res)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	if _, err := os.Stat(filepath.Join(filepath.Dir(tracks[0].FilePath), "someone-elses-booklet.pdf")); err == nil {
		t.Error("an extra came from the shared downloads folder")
	}
	if !ownedByDownload(filepath.Join("/dl", "Radiohead - Discography", "OK Computer (1997)"), "Radiohead - Discography") ||
		ownedByDownload("/dl/music", "Radiohead - OK Computer") {
		t.Error("ownedByDownload")
	}
}

// Extras only come from inside the download's own folder: a download whose files sit
// straight in a shared downloads folder brings none of its neighbours' files, and audio is
// never an extra even when the allowlist names it.
func TestExtrasStayInsideTheDownload(t *testing.T) {
	h, _ := musicImportHarness(t)
	h.c.SetMusicPlacement(func(context.Context) MusicPlacementPrefs {
		return MusicPlacementPrefs{Extras: stage.ParseExtras(stage.DefaultExtras + " .flac")}
	}, nil, nil)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	shared := t.TempDir() // the downloads folder: other torrents' files live here too
	blobTrack(t, shared, "01 - One.flac", 1)
	blobTrack(t, shared, "02 - Two.flac", 2)
	blobTrack(t, shared, "Someone Else - 07 - Track.flac", 3)
	if err := os.WriteFile(filepath.Join(shared, "another-download-cover.jpg"), []byte("NOT OURS"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One file at a time, as a single-file torrent arrives.
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	res := h.c.placeAlbumTracks(h.ctx, a, h.album(t, a.ID, "OK Computer"), tracks,
		map[int64]music.AudioFile{tracks[0].ID: {Path: filepath.Join(shared, "01 - One.flac"), SizeBytes: 300 << 10}},
		music.QualityFLAC, "r", filepath.Join(shared, "01 - One.flac"))
	if res.placed != 1 {
		t.Fatalf("import %+v", res)
	}
	tracks, _ = h.svc.Tracks(h.ctx, al.ID)
	names := albumFiles(t, filepath.Dir(tracks[0].FilePath))
	if len(names) != 1 {
		t.Errorf("album folder holds %v, want only the track", names)
	}
}

// Without an allowlist nothing but the audio comes along.
func TestImportWithoutExtras(t *testing.T) {
	h, _ := musicImportHarness(t)
	withHouseTags(h, &houseFake{})
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	dl, _ := houseDownload(t, "Radiohead - OK Computer (1997) [FLAC]")
	if res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), dl, "Radiohead - OK Computer (1997) [FLAC]"); res.placed != 2 {
		t.Fatalf("import %+v", res)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	if _, err := os.Stat(filepath.Join(filepath.Dir(tracks[0].FilePath), "booklet.pdf")); err == nil {
		t.Error("an extra came along with no allowlist")
	}
}
