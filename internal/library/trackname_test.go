package library

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// MOV-12: a movie track is named for its edition (Plex's {edition-…}), and an import for
// one track never lands on, recycles or overwrites a file another track owns. All on
// synthetic files in temp dirs.

func TestMovieNamingPlexEdition(t *testing.T) {
	lib := t.TempDir()
	const rel = "Blade.Runner.1982.2160p.BluRay.x265-GRP"
	dc := MovieTrack{Title: "Blade Runner", Year: 1982, Edition: "Director's Cut", Label: "Director's Cut"}

	cases := []struct {
		name, file string
		track      MovieTrack
		want       string
	}{
		// The default scheme names no edition: the track's goes in after the year.
		{"auto-appended", DefaultMovieFile, dc, "Blade Runner (1982) {edition-Director's Cut} - 2160p BluRay"},
		// A track with no edition keeps exactly the default name (no churn).
		{"no edition unchanged", DefaultMovieFile, MovieTrack{Title: "Blade Runner", Year: 1982}, "Blade Runner (1982) - 2160p BluRay"},
		{"plexedition token", "{title} ({year}) {plexedition} - {quality}", dc, "Blade Runner (1982) {edition-Director's Cut} - 2160p BluRay"},
		{"empty plexedition", "{title} ({year}) {plexedition} - {quality}", MovieTrack{Title: "Blade Runner", Year: 1982}, "Blade Runner (1982) - 2160p BluRay"},
		// Hand-written Plex group: filled, or dropped entirely when there's no edition.
		{"hand-written group", "{title} ({year}) {edition-{edition}} - {quality}", dc, "Blade Runner (1982) {edition-Director's Cut} - 2160p BluRay"},
		{"bare edition group", "{title} ({year}) {edition-{edition}} - {quality}", MovieTrack{Title: "Blade Runner", Year: 1982}, "Blade Runner (1982) - 2160p BluRay"},
		// A scheme that already says {edition} isn't given a second one.
		{"edition token", "{title} ({year}) {edition} - {quality}", dc, "Blade Runner (1982) Director's Cut - 2160p BluRay"},
		// No year in the scheme: appended at the end.
		{"no year", "{title} - {quality}", dc, "Blade Runner - 2160p BluRay {edition-Director's Cut}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			im := NewImporter(lib, quiet())
			im.SetNaming(fixedNaming{Naming{Folder: DefaultMovieFolder, File: tc.file}})
			got := im.MovieTrackTarget(tc.track, rel, ".mkv")
			want := filepath.Join(lib, "Blade Runner (1982)", tc.want+".mkv")
			if got != want {
				t.Errorf("\n got  %q\n want %q", got, want)
			}
		})
	}
}

// The release's own edition still fills {edition} for a track without one.
func TestMovieNamingReleaseEditionWhenTrackHasNone(t *testing.T) {
	im := NewImporter("/lib", quiet())
	im.SetNaming(fixedNaming{Naming{Folder: DefaultMovieFolder, File: "{title} ({year}) {edition-{edition}} - {quality}"}})
	got := im.MovieTrackTarget(MovieTrack{Title: "Aliens", Year: 1986}, "Aliens.1986.Extended.1080p.BluRay.x264-GRP", ".mkv")
	want := filepath.Join("/lib", "Aliens (1986)", "Aliens (1986) {edition-Extended} - 1080p BluRay.mkv")
	if got != want {
		t.Errorf("\n got  %q\n want %q", got, want)
	}
}

// Two same-resolution tracks with no editions: the second gets its label, then a counter.
func TestMovieTrackTargetCollisionGetsLabel(t *testing.T) {
	lib := t.TempDir()
	im := NewImporter(lib, quiet())
	dir := filepath.Join(lib, "Dune (2021)")
	theirs := filepath.Join(dir, "Dune (2021) - 2160p BluRay.mkv")
	tr := MovieTrack{Title: "Dune", Year: 2021, Label: "HDR copy", Protected: []string{theirs}}
	got := im.MovieTrackTarget(tr, "Dune.2021.2160p.BluRay.x265-GRP", ".mkv")
	if want := filepath.Join(dir, "Dune (2021) - 2160p BluRay - HDR copy.mkv"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	tr.Protected = append(tr.Protected, got)
	got = im.MovieTrackTarget(tr, "Dune.2021.2160p.BluRay.x265-GRP", ".mkv")
	if want := filepath.Join(dir, "Dune (2021) - 2160p BluRay - HDR copy (2).mkv"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestImportAsNamedNeverRecyclesProtected(t *testing.T) {
	src, lib, bin := t.TempDir(), t.TempDir(), t.TempDir()
	im := NewImporter(lib, quiet())
	im.SetRecycleDir(bin)

	// The theatrical 2160p file the default track owns, under the default name.
	theatrical := filepath.Join(lib, "Dune (2021)", "Dune (2021) - 2160p BluRay.mkv")
	writeBytes(t, theatrical, patterned(4096, 't', 10, "THEATRICAL"))
	// A different 2160p download for a second track with no edition.
	dl := filepath.Join(src, "Dune.2021.2160p.BluRay.x265-OTHER.mkv")
	writeBytes(t, dl, patterned(4096, 'o', 10, "OTHER"))

	res, err := im.ImportAsNamed(MovieTrack{Title: "Dune", Year: 2021, Label: "Second", Protected: []string{theatrical}}, dl)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.TargetPath == theatrical {
		t.Fatal("imported onto the other track's file")
	}
	if want := filepath.Join(lib, "Dune (2021)", "Dune (2021) - 2160p BluRay - Second.mkv"); res.TargetPath != want {
		t.Fatalf("target %q, want %q", res.TargetPath, want)
	}
	if !bytes.Contains(readBytes(t, theatrical), []byte("THEATRICAL")) {
		t.Fatal("the protected file was changed")
	}
	if entries, _ := os.ReadDir(bin); len(entries) != 0 {
		t.Fatalf("something was recycled: %v", entries)
	}
	if !bytes.Contains(readBytes(t, res.TargetPath), []byte("OTHER")) {
		t.Fatal("the new file isn't the download")
	}
}

// The edition track: a Director's Cut download lands under its own Plex name next to the
// theatrical file, which stays where it is.
func TestImportAsNamedEditionBesideTheatrical(t *testing.T) {
	src, lib := t.TempDir(), t.TempDir()
	im := NewImporter(lib, quiet())
	im.SetRecycleDir(t.TempDir())
	theatrical := filepath.Join(lib, "Blade Runner (1982)", "Blade Runner (1982) - 2160p BluRay.mkv")
	writeBytes(t, theatrical, patterned(4096, 't', 10, "THEATRICAL"))
	dl := filepath.Join(src, "Blade.Runner.1982.Directors.Cut.2160p.BluRay.x265-GRP.mkv")
	writeBytes(t, dl, patterned(4096, 'd', 10, "DC"))

	res, err := im.ImportAsNamed(MovieTrack{Title: "Blade Runner", Year: 1982, Edition: "Director's Cut", Label: "Director's Cut", Protected: []string{theatrical}}, dl)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(lib, "Blade Runner (1982)", "Blade Runner (1982) {edition-Director's Cut} - 2160p BluRay.mkv")
	if res.TargetPath != want {
		t.Fatalf("target %q, want %q", res.TargetPath, want)
	}
	if !bytes.Contains(readBytes(t, theatrical), []byte("THEATRICAL")) {
		t.Fatal("the theatrical file was changed")
	}
}

// A hardlink of another track's file under this track's name is still that file.
func TestOwnedByOtherSeesHardlinks(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mkv")
	b := filepath.Join(dir, "b.mkv")
	writeBytes(t, a, []byte("x"))
	if err := os.Link(a, b); err != nil {
		t.Skipf("no hardlinks here: %v", err)
	}
	if !OwnedByOther(b, []string{a}) {
		t.Fatal("hardlink not seen as the protected file")
	}
	if OwnedByOther(filepath.Join(dir, "c.mkv"), []string{a}) {
		t.Fatal("a free path counted as owned")
	}
}

// The manager keeps the track the resolver decided on the import record, for the attach.
type trackResolver struct{ n MovieImportName }

func (r trackResolver) ResolveMovieImport(context.Context, string, string) (MovieImportName, bool) {
	return r.n, true
}

func TestProcessRecordsDecidedTrack(t *testing.T) {
	src, lib := t.TempDir(), t.TempDir()
	m := newTestManager(t, lib, nil)
	db := m.repo.db
	m.SetTitleResolver(trackResolver{MovieImportName{MovieID: 3, VersionID: 9,
		Track: MovieTrack{Title: "Heat", Year: 1995, Edition: "Director's Cut", Label: "DC"}}})
	dl := filepath.Join(src, "Heat.1995.1080p.BluRay.x264-GRP.mkv")
	writeBytes(t, dl, patterned(4096, 'h', 0, "HEAT"))
	if n := m.Process(context.Background(), []Candidate{{Hash: "h1", Name: "Heat.1995.1080p.BluRay.x264-GRP", ContentPath: dl}}); n != 1 {
		t.Fatalf("imported %d, want 1", n)
	}
	var movieID, versionID int64
	var target string
	if err := db.QueryRow(`SELECT movie_id, version_id, target_path FROM imports WHERE download_hash = 'h1'`).Scan(&movieID, &versionID, &target); err != nil {
		t.Fatal(err)
	}
	if movieID != 3 || versionID != 9 {
		t.Fatalf("recorded movie %d track %d, want 3 / 9", movieID, versionID)
	}
	if want := filepath.Join(lib, "Heat (1995)", "Heat (1995) {edition-Director's Cut} - 1080p BluRay.mkv"); target != want {
		t.Fatalf("target %q, want %q", target, want)
	}
}
