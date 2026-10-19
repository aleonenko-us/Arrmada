package stage

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestParseExtras(t *testing.T) {
	got := ParseExtras(" .JPG, png;  .pdf\n.lrc . ")
	for _, ext := range []string{".jpg", ".png", ".pdf", ".lrc"} {
		if !got[ext] {
			t.Errorf("%s missing from %v", ext, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("got %v", got)
	}
	if def := ParseExtras(DefaultExtras); len(def) != 14 || !def[".accurip"] || !def[".m3u8"] {
		t.Errorf("default allowlist %v", def)
	}
}

// MUS-36: the allowlist picks the extras; subfolders are kept; a .lrc named like a track
// follows the track's new name; a downloaded cover is kept as folder.jpg; hidden folders
// and files off the list stay behind.
func TestPlanExtras(t *testing.T) {
	w := newWorld(t)
	base := filepath.Join(w.dl, "Artist - Album (2001) [FLAC]")
	for _, p := range []string{"01 - One.flac", "01 - One.lrc", "02 - Two.flac", "Two.lrc", "cover.jpg", "booklet.pdf",
		"rip.log", "Album.cue", "Scans/back.png", "Scans/cd.jpg", "notes.exe", ".hidden.jpg", ".Trash/x.jpg", "CD2/disc2.log"} {
		w.write(filepath.Join(base, p), "x:"+p)
	}
	album := filepath.Join(w.root, "Artist", "Album (2001)")
	placed := map[string]string{filepath.Join(base, "01 - One"): filepath.Join(album, "01 - One (Remastered)")}
	got := map[string]string{}
	for _, e := range PlanExtras(base, album, ParseExtras(DefaultExtras), placed) {
		rel, _ := filepath.Rel(base, e.From)
		to, _ := filepath.Rel(album, e.To)
		got[filepath.ToSlash(rel)] = filepath.ToSlash(to)
	}
	want := map[string]string{
		"01 - One.lrc":   "01 - One (Remastered).lrc",
		"Two.lrc":        "Two.lrc", // names no placed track: kept as it is
		"cover.jpg":      "folder.jpg",
		"booklet.pdf":    "booklet.pdf",
		"rip.log":        "rip.log",
		"Album.cue":      "Album.cue",
		"Scans/back.png": "Scans/back.png",
		"Scans/cd.jpg":   "Scans/cd.jpg",
		"CD2/disc2.log":  "CD2/disc2.log",
	}
	if len(got) != len(want) {
		t.Errorf("planned %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s -> %q, want %q", k, got[k], v)
		}
	}
}

// Nothing in the album folder is ever overwritten; extras are hardlinked.
func TestPlaceExtrasNeverOverwrites(t *testing.T) {
	w := newWorld(t)
	base := filepath.Join(w.dl, "Album")
	w.write(filepath.Join(base, "booklet.pdf"), "NEW BOOKLET")
	w.write(filepath.Join(base, "folder.jpg"), "NEW FOLDER")
	w.write(filepath.Join(base, "Scans", "a.jpg"), "SCAN")
	album := filepath.Join(w.root, "A", "Album")
	keep := w.write(filepath.Join(album, "booklet.pdf"), "MY BOOKLET")
	placed, skipped := PlaceExtras(PlanExtras(base, album, ParseExtras(DefaultExtras), nil), w.deps.Log)
	sort.Strings(placed)
	if len(placed) != 2 || skipped != 1 {
		t.Fatalf("placed %v, skipped %d", placed, skipped)
	}
	if b, _ := os.ReadFile(keep); string(b) != "MY BOOKLET" {
		t.Error("an existing extra was overwritten")
	}
	si, _ := os.Stat(filepath.Join(base, "Scans", "a.jpg"))
	di, err := os.Stat(filepath.Join(album, "Scans", "a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(si, di) {
		t.Log("extras were copied, not linked (no hard links on this drive)")
	}
	// Running it again changes nothing and overwrites nothing.
	placed, skipped = PlaceExtras(PlanExtras(base, album, ParseExtras(DefaultExtras), nil), w.deps.Log)
	if len(placed) != 0 || skipped != 3 {
		t.Errorf("second run placed %v, skipped %d", placed, skipped)
	}
}
