package automation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

type namingFunc func() library.MusicNaming

func (f namingFunc) MusicNaming() library.MusicNaming { return f() }

// MUS-34: a new template is a preview first — nothing moves until it's applied — and the
// apply moves the confirmed files with their extras and lyrics, never over anything.
func TestMusicRenamePreviewThenApply(t *testing.T) {
	h, _ := musicImportHarness(t)
	withHouseTagsAndExtras(h)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	_ = h.svc.EnsureTracks(h.ctx, al)
	dl, _ := houseDownload(t, "Radiohead - OK Computer (1997) [FLAC]")
	if res := h.c.importAlbumContent(h.ctx, a, h.album(t, a.ID, "OK Computer"), dl, "r"); res.placed != 2 {
		t.Fatalf("import %+v", res)
	}
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	oldDir := filepath.Dir(tracks[0].FilePath)

	h.c.imp.SetMusicNaming(namingFunc(func() library.MusicNaming {
		return library.MusicNaming{Folder: "{Album Artist}/{Album Title} [{Album Type}]", Track: "{Track:000}. {Track Title}", Compilation: library.DefaultMusicCompilationTrack}
	}))
	preview, err := h.c.MusicRenamePreview(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tracks[0].FilePath); err != nil {
		t.Fatal("the preview moved a file")
	}
	var trackMoves, extraMoves int
	for _, it := range preview {
		if it.Conflict != "" {
			t.Errorf("unexpected conflict: %+v", it)
		}
		if it.TrackID > 0 {
			trackMoves++
		} else {
			extraMoves++
		}
	}
	// Two tracks; the lyrics, booklet, folder.jpg, cover.jpg, rip.log and the scan.
	if trackMoves != 2 || extraMoves != 6 {
		t.Fatalf("preview: %d track moves, %d extras: %+v", trackMoves, extraMoves, preview)
	}
	// Confirm all but track Two; Two stays put.
	var only []MusicRenameItem
	for _, it := range preview {
		if !strings.Contains(it.From, "Two") {
			only = append(only, it)
		}
	}
	res, err := h.c.MusicRename(h.ctx, al.ID, only)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != len(only) {
		t.Fatalf("moved %d of %d: %+v", res.Moved, len(only), res)
	}
	tracks, _ = h.svc.Tracks(h.ctx, al.ID)
	newDir := filepath.Join(h.c.imp.MusicDir(), "Radiohead", "OK Computer [Album]")
	for _, tr := range tracks {
		switch tr.Title {
		case "One":
			if tr.FilePath != filepath.Join(newDir, "001. One.flac") {
				t.Errorf("One at %s", tr.FilePath)
			}
		case "Two":
			if filepath.Dir(tr.FilePath) != oldDir {
				t.Errorf("Two moved without being confirmed: %s", tr.FilePath)
			}
		}
		if _, err := os.Stat(tr.FilePath); err != nil {
			t.Errorf("%s isn't where the database says", tr.FilePath)
		}
	}
	for _, name := range []string{"001. One.lrc", "booklet.pdf", "folder.jpg", "cover.jpg", "rip.log", "Scans/back.jpg"} {
		if _, err := os.Stat(filepath.Join(newDir, name)); err != nil {
			t.Errorf("%s didn't move with the album", name)
		}
	}
	// Two can still be renamed, and then the old folder goes.
	if res, err := h.c.MusicRename(h.ctx, al.ID, nil); err != nil || res.Moved != 1 {
		t.Fatalf("second rename: %+v %v", res, err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("the emptied album folder is still there: %v", err)
	}
}

// A move whose target is taken is a conflict: previewed as one, and skipped on apply.
func TestMusicRenameNeverOverwrites(t *testing.T) {
	h, _ := musicImportHarness(t)
	a := h.addArtist(t, "Radiohead", "", []string{"OK Computer"}, nil, "1997-05-21")
	al := h.album(t, a.ID, "OK Computer")
	if res := h.importDownload(t, a, al, "Radiohead - OK Computer (1997) [FLAC]", ".flac", 1); res.placed != 2 {
		t.Fatalf("import %+v", res)
	}
	h.c.imp.SetMusicNaming(namingFunc(func() library.MusicNaming {
		return library.MusicNaming{Folder: library.DefaultMusicFolder, Track: "{Track} {Track Title}", Compilation: library.DefaultMusicCompilationTrack}
	}))
	tracks, _ := h.svc.Tracks(h.ctx, al.ID)
	squatter := filepath.Join(filepath.Dir(tracks[0].FilePath), "01 One.flac")
	if err := os.WriteFile(squatter, []byte("someone's"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, _ := h.c.MusicRenamePlan(h.ctx, al.ID)
	conflicts := 0
	for _, it := range plan {
		if it.Conflict != "" {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("plan %+v", plan)
	}
	res, err := h.c.MusicRename(h.ctx, al.ID, plan)
	if err != nil || res.Moved != 1 || len(res.Skipped) != 1 {
		t.Fatalf("apply %+v %v", res, err)
	}
	if b, _ := os.ReadFile(squatter); string(b) != "someone's" {
		t.Error("the file at the target was overwritten")
	}
}

func TestMusicHoldUnpreviewed(t *testing.T) {
	plan := []MusicRenameItem{{From: "a", To: "A"}, {From: "b", To: "B2"}, {From: "c", To: "C"}}
	only := []MusicRenameItem{{From: "a", To: "A"}, {From: "b", To: "B"}, {From: "gone", To: "G"}}
	keep, skipped := musicHoldUnpreviewed(plan, only)
	if len(keep) != 1 || keep[0].From != "a" {
		t.Errorf("keep %+v", keep)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped %+v", skipped)
	}
}

func TestMusicRenameToken(t *testing.T) {
	a := []MusicRenameItem{{AlbumID: 1, From: "x", To: "y"}, {AlbumID: 2, From: "p", To: "q"}}
	b := []MusicRenameItem{a[1], a[0]}
	if MusicRenameToken(a) != MusicRenameToken(b) {
		t.Error("the token depends on order")
	}
	if MusicRenameToken(a) == MusicRenameToken(a[:1]) {
		t.Error("the token ignores a move")
	}
}
