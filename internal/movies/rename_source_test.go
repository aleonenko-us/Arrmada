package movies

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// feature makes a fixture file big enough (sparse) for a scan to take it for a film rather
// than a sample.
func feature(t *testing.T, path string) {
	t.Helper()
	if err := os.Truncate(path, 200<<20); err != nil {
		t.Fatal(err)
	}
}

type schemeNaming struct{ n library.Naming }

func (s schemeNaming) Naming() library.Naming { return s.n }

// issue48Scheme is the scheme from issue #48: more tokens than the files were named with.
var issue48Scheme = library.Naming{Folder: "{title} ({year})", File: "{title} ({year}) {plexedition} [{quality}][{codec}]-{group}"}

// A film imported as "American Psycho (2000).mkv" keeps its release in source_release.
// When the scheme grows {quality}/{codec}/{group}, the plan fills them from that release
// rather than from the file's bare name, which made it "Nothing to rename".
func TestRenamePlanReadsSourceRelease(t *testing.T) {
	cases := []struct {
		name, title string
		year        int
		file        string
		release     string
		want        string
	}{
		// The release says UNRATED; editions belong to tracks, so it isn't written.
		{"american psycho", "American Psycho", 2000, "American Psycho (2000).mkv",
			"American.Psycho.2000.UNRATED.German.DTSHD.DL.2160p.UHD.BluRay.DV.HDR.x265-VECTOR",
			"American Psycho (2000) [2160p BluRay][x265]-VECTOR.mkv"},
		{"avatar", "Avatar", 2009, "Avatar (2009) - 2160p BluRay.mkv",
			"Avatar.2009.2160p.UHD.BluRay.x265.10bit.HDR.TrueHD.7.1.Atmos-TERMiNAL",
			"Avatar (2009) [2160p BluRay][x265]-TERMiNAL.mkv"},
		// A manual import with no release recorded: the file's name, as before.
		{"no release", "Heat", 1995, "Heat.1995.1080p.BluRay.x264-GRP.mkv", "",
			"Heat (1995) [1080p BluRay][x264]-GRP.mkv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
			ctx := context.Background()
			f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
			m, err := f.svc.repo.Create(ctx, Movie{TMDBID: 7, Title: tc.title, Year: tc.year, Monitored: true})
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.root, fmt.Sprintf("%s (%d)", tc.title, tc.year))
			file := writeBody(t, filepath.Join(dir, tc.file), "film")
			if err := f.svc.repo.SetFile(ctx, m.ID, file); err != nil {
				t.Fatal(err)
			}
			if tc.release != "" {
				if err := f.svc.repo.SetSourceRelease(ctx, m.ID, tc.release); err != nil {
					t.Fatal(err)
				}
			}
			items, err := f.svc.RenamePlan(ctx, []int64{m.ID})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 {
				t.Fatalf("plan = %+v, want one item", items)
			}
			it := items[0]
			if it.Status != RenameOK || it.Proposed != filepath.Join(dir, tc.want) {
				t.Fatalf("plan = %s %q (%s), want ok %q", it.Status, filepath.Base(it.Proposed), it.Reason, tc.want)
			}
			// It stays a preview: nothing moved.
			if readBody(t, file) != "film" {
				t.Fatal("planning must not move the file")
			}
			// Applying it lands the name, and the next plan finds nothing more to do.
			if err := f.svc.Rename(ctx, m.ID); err != nil {
				t.Fatal(err)
			}
			again, err := f.svc.RenamePlan(ctx, []int64{m.ID})
			if err != nil {
				t.Fatal(err)
			}
			if len(again) != 1 || again[0].Status != RenameSame {
				t.Fatalf("second plan = %+v, want the file already named", again)
			}
		})
	}
}

// Convert re-encoded the file: its cached probe says AV1 while the name and a release from
// before Convert restamped releases still say x265. The name follows the file.
func TestRenamePlanUsesConvertedCodec(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
	if err := f.svc.repo.SetSourceRelease(ctx, f.id, "Heat.1995.1080p.BluRay.x265-GRP"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(f.main)
	if err != nil {
		t.Fatal(err)
	}
	media, _ := json.Marshal(MovieFile{Path: f.main, Filename: filepath.Base(f.main), SizeBytes: fi.Size(), MtimeUnix: fi.ModTime().Unix(),
		Codec: "AV1", Resolution: "1080p", Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, f.id, string(media)); err != nil {
		t.Fatal(err)
	}
	def := planByVersion(t, f)[0]
	if want := "Heat (1995) [1080p BluRay][AV1]-GRP.mkv"; filepath.Base(def.Proposed) != want {
		t.Fatalf("proposed %q, want %q", filepath.Base(def.Proposed), want)
	}

	// A cached read of another path says nothing about this file: the release's codec stands.
	media, _ = json.Marshal(MovieFile{Path: filepath.Join(f.root, "elsewhere.mkv"), Codec: "AV1", Resolution: "1080p",
		Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, f.id, string(media)); err != nil {
		t.Fatal(err)
	}
	def = planByVersion(t, f)[0]
	if want := "Heat (1995) [1080p BluRay][x265]-GRP.mkv"; filepath.Base(def.Proposed) != want {
		t.Fatalf("proposed %q, want %q", filepath.Base(def.Proposed), want)
	}
}

// A file swapped in by hand and adopted by a rescan is another file: it must not inherit the
// old one's release (and then be renamed with the old file's group and source over the only
// record of what it is).
func TestRescanAdoptingAnotherFileDropsItsRelease(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
	m, err := f.svc.repo.Create(ctx, Movie{TMDBID: 9, Title: "Aliens", Year: 1986, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.root, "Aliens (1986)")
	old := writeBody(t, filepath.Join(dir, "Aliens (1986) [1080p WEB-DL][x264]-OLDGRP.mkv"), "old")
	if err := f.svc.repo.SetFile(ctx, m.ID, old); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.SetSourceRelease(ctx, m.ID, "Aliens.1986.1080p.WEB-DL.x264-OLDGRP"); err != nil {
		t.Fatal(err)
	}
	media, _ := json.Marshal(MovieFile{Path: old, SizeBytes: 3, Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, m.ID, string(media)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	newf := writeBody(t, filepath.Join(dir, "Aliens.1986.1080p.BluRay.REMUX.AVC-NEWGRP.mkv"), "a different film")
	feature(t, newf)

	m, _ = f.svc.repo.Get(ctx, m.ID)
	if !f.svc.rescan(ctx, &m) {
		t.Fatal("the rescan didn't adopt the new file")
	}
	m, _ = f.svc.repo.Get(ctx, m.ID)
	if m.MovieFilePath != newf || m.SourceRelease != "" {
		t.Fatalf("after rescan: %q / release %q, want the new file with no release", m.MovieFilePath, m.SourceRelease)
	}
	items, err := f.svc.RenamePlan(ctx, []int64{m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Aliens (1986) [1080p Remux][x264]-NEWGRP.mkv"; len(items) != 1 || filepath.Base(items[0].Proposed) != want {
		t.Fatalf("plan = %+v, want %q", items, want)
	}
}

// The same file renamed outside Arrmada (same size) is still the file the release
// describes: the rescan keeps the release.
func TestRescanAdoptingRenamedFileKeepsItsRelease(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	feature(t, f.main)
	fi, err := os.Stat(f.main)
	if err != nil {
		t.Fatal(err)
	}
	const rel = "Heat.1995.1080p.BluRay.x264-GRP"
	if err := f.svc.repo.SetSourceRelease(ctx, f.id, rel); err != nil {
		t.Fatal(err)
	}
	media, _ := json.Marshal(MovieFile{Path: f.main, SizeBytes: fi.Size(), MtimeUnix: fi.ModTime().Unix(), Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, f.id, string(media)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.ClearVersionFile(ctx, f.vid); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.extra); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(filepath.Dir(f.main), "heat renamed by hand.mkv")
	if err := os.Rename(f.main, renamed); err != nil {
		t.Fatal(err)
	}
	m, _ := f.svc.repo.Get(ctx, f.id)
	if !f.svc.rescan(ctx, &m) {
		t.Fatal("the rescan didn't adopt the renamed file")
	}
	m, _ = f.svc.repo.Get(ctx, f.id)
	if m.MovieFilePath != renamed || m.SourceRelease != rel {
		t.Fatalf("after rescan: %q / release %q, want the renamed file keeping %q", m.MovieFilePath, m.SourceRelease, rel)
	}
}

// A different file that happens to be the same size isn't the same file: without the old
// file's modification time matching too it's treated as another file, and the release goes.
func TestRescanAdoptingSameSizeOtherFileDropsItsRelease(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
	m, _ := f.svc.repo.Create(ctx, Movie{TMDBID: 9, Title: "Aliens", Year: 1986, Monitored: true})
	dir := filepath.Join(f.root, "Aliens (1986)")
	old := writeBody(t, filepath.Join(dir, "Aliens (1986) [1080p WEB-DL][x264]-OLDGRP.mkv"), "old")
	feature(t, old)
	if err := f.svc.repo.SetFile(ctx, m.ID, old); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.repo.SetSourceRelease(ctx, m.ID, "Aliens.1986.1080p.WEB-DL.x264-OLDGRP"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(old)
	media, _ := json.Marshal(MovieFile{Path: old, SizeBytes: 200 << 20, MtimeUnix: fi.ModTime().Unix() - 3600, Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, m.ID, string(media)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	newf := writeBody(t, filepath.Join(dir, "Aliens.mkv"), "a different film")
	feature(t, newf)
	m, _ = f.svc.repo.Get(ctx, m.ID)
	if !f.svc.rescan(ctx, &m) {
		t.Fatal("the rescan didn't adopt the new file")
	}
	m, _ = f.svc.repo.Get(ctx, m.ID)
	if m.SourceRelease != "" {
		t.Fatalf("a same-size other file kept release %q", m.SourceRelease)
	}
	items, err := f.svc.RenamePlan(ctx, []int64{m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || filepath.Base(items[0].Proposed) != "Aliens (1986).mkv" {
		t.Fatalf("plan = %+v, want the file named from its own (empty) name", items)
	}
}

// An import with no known release onto a new path keeps none of the old file's; the same
// path attached again keeps it.
func TestMarkImportedWithoutReleaseClearsTheOldOne(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	const rel = "Heat.1995.1080p.BluRay.x264-GRP"
	if err := f.svc.repo.SetSourceRelease(ctx, f.id, rel); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.MarkImportedTo(ctx, f.id, 0, f.main, "", true); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.svc.repo.Get(ctx, f.id); m.SourceRelease != rel {
		t.Fatalf("the same file attached again lost its release: %q", m.SourceRelease)
	}
	other := writeBody(t, filepath.Join(f.root, "Heat (1995)", "heat other.mkv"), "other film")
	if err := f.svc.MarkImportedTo(ctx, f.id, 0, other, "", true); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.svc.repo.Get(ctx, f.id); m.MovieFilePath != other || m.SourceRelease != "" {
		t.Fatalf("after importing another file: %q / release %q, want no release", m.MovieFilePath, m.SourceRelease)
	}
}

// Preview, apply, preview again: the second plan has nothing left to rename, whatever the
// scheme. (A {plexedition} folder scheme isn't here: its folder flip is older than this.)
func TestRenamePlanSettlesAfterApply(t *testing.T) {
	schemes := map[string]library.Naming{
		"default": {Folder: library.DefaultMovieFolder, File: library.DefaultMovieFile},
		"issue48": issue48Scheme,
		"radarr":  {Folder: "{title} ({year})", File: "{title} ({year}) {edition} {quality} {codec}-{group}"},
		"no dash": {Folder: "{title} ({year})", File: "{title} ({year}) - {quality} {codec} {group}"},
	}
	films := []struct {
		title, name, release string
		year                 int
	}{
		{"Aliens", "Aliens (1986).mkv", "Aliens.1986.EXTENDED.1080p.BluRay.x264-AMIABLE", 1986},
		{"Heat", "Heat (1995) - Bluray-1080p.mkv", "Heat.1995.1080p.BluRay.x265-x0r", 1995},
		{"Dune", "Dune (2021) {edition-IMAX} [2160p WEB-DL][x265]-FLUX.mkv", "Dune.2021.IMAX.2160p.WEB-DL.DDP5.1.Atmos.HDR.H.265-FLUX", 2021},
		{"Up", "Up (2009) - 720p HDTV-3L.mkv", "Up.2009.720p.HDTV.x264-3L", 2009},
		{"Tenet", "Tenet (2020) - 1080p WEB-DL.mkv", "Tenet.2020.1080p.WEB-DL.DDP5.1.H.264-playWEB", 2020},
		{"Alien", "Alien (1979).mkv", "Alien.1979.Directors.Cut.2160p.UHD.BluRay.REMUX.HDR.HEVC.DTS-HD.MA.5.1-FraMeSToR", 1979},
	}
	for label, scheme := range schemes {
		t.Run(label, func(t *testing.T) {
			f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
			ctx := context.Background()
			f.svc.imp.SetNaming(schemeNaming{scheme})
			var ids []int64
			for i, fl := range films {
				m, err := f.svc.repo.Create(ctx, Movie{TMDBID: 100 + i, Title: fl.title, Year: fl.year, Monitored: true})
				if err != nil {
					t.Fatal(err)
				}
				p := writeBody(t, filepath.Join(f.root, fmt.Sprintf("%s (%d)", fl.title, fl.year), fl.name), fl.title)
				if err := f.svc.repo.SetFile(ctx, m.ID, p); err != nil {
					t.Fatal(err)
				}
				if err := f.svc.repo.SetSourceRelease(ctx, m.ID, fl.release); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, m.ID)
			}
			items, err := f.svc.RenamePlan(ctx, ids)
			if err != nil {
				t.Fatal(err)
			}
			var ok []RenameItem
			for _, it := range items {
				b := strings.TrimSuffix(filepath.Base(it.Proposed), ".mkv")
				if strings.HasSuffix(b, "]-DL") || strings.HasSuffix(b, " -DL") || strings.HasSuffix(b, " DL") || strings.HasSuffix(b, "DL-DL") {
					t.Errorf("a WEB-DL tail became a group: %s", filepath.Base(it.Proposed))
				}
				if it.Status == RenameOK {
					ok = append(ok, it)
				}
			}
			if _, err := f.svc.ApplyRename(ctx, ok); err != nil {
				t.Fatal(err)
			}
			again, err := f.svc.RenamePlan(ctx, ids)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range again {
				if it.Status != RenameSame {
					t.Errorf("second plan: %s → %s (%s %s)", filepath.Base(it.Current), filepath.Base(it.Proposed), it.Status, it.Reason)
				}
			}
		})
	}
}

// Editions belong to tracks: a release's edition neither replaces the one a file's name
// carries nor moves the film into an edition folder.
func TestRenamePlanNeverTakesReleaseEdition(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
	m, _ := f.svc.repo.Create(ctx, Movie{TMDBID: 10, Title: "Blade Runner", Year: 1982, Monitored: true})
	dir := filepath.Join(f.root, "Blade Runner (1982)")
	file := writeBody(t, filepath.Join(dir, "Blade Runner (1982) {edition-Final Cut} [2160p BluRay][x265]-GRP.mkv"), "x")
	_ = f.svc.repo.SetFile(ctx, m.ID, file)
	_ = f.svc.repo.SetSourceRelease(ctx, m.ID, "Blade.Runner.1982.REMASTERED.2160p.UHD.BluRay.x265-GRP")
	items, err := f.svc.RenamePlan(ctx, []int64{m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != RenameSame {
		t.Fatalf("plan = %+v, want the Final Cut file left as it is", items)
	}

	f.svc.imp.SetNaming(schemeNaming{library.Naming{Folder: "{title} ({year}) {plexedition}", File: "{title} ({year}) - {quality}"}})
	ap, _ := f.svc.repo.Create(ctx, Movie{TMDBID: 11, Title: "American Psycho", Year: 2000, Monitored: true})
	apDir := filepath.Join(f.root, "American Psycho (2000)")
	apFile := writeBody(t, filepath.Join(apDir, "American Psycho (2000) - 2160p BluRay.mkv"), "x")
	_ = f.svc.repo.SetFile(ctx, ap.ID, apFile)
	_ = f.svc.repo.SetSourceRelease(ctx, ap.ID, "American.Psycho.2000.UNRATED.2160p.UHD.BluRay.x265-VECTOR")
	items, err = f.svc.RenamePlan(ctx, []int64{ap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != RenameSame {
		t.Fatalf("plan = %+v, want the film kept in its folder", items)
	}
}

// A cached probe of the file's path that no longer matches the file (another size) says
// nothing about the file there now.
func TestRenamePlanIgnoresStaleProbe(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
	if err := f.svc.repo.SetSourceRelease(ctx, f.id, "Heat.1995.1080p.BluRay.x265-GRP"); err != nil {
		t.Fatal(err)
	}
	media, _ := json.Marshal(MovieFile{Path: f.main, SizeBytes: 999, Codec: "AV1", Resolution: "1080p", Probed: true, MediaVersion: MediaVersion})
	if err := f.svc.repo.SetMediaInfo(ctx, f.id, string(media)); err != nil {
		t.Fatal(err)
	}
	def := planByVersion(t, f)[0]
	if want := "Heat (1995) [1080p BluRay][x265]-GRP.mkv"; filepath.Base(def.Proposed) != want {
		t.Fatalf("proposed %q, want %q", filepath.Base(def.Proposed), want)
	}
}

// An upgrade records the new file's release, and the next plan names the file from it,
// not from the release it replaced.
func TestRenamePlanAfterUpgradeUsesNewRelease(t *testing.T) {
	f := newDeleteFixture(t, filepath.Join(t.TempDir(), "bin"))
	ctx := context.Background()
	f.svc.imp.SetNaming(schemeNaming{issue48Scheme})
	if err := f.svc.repo.SetSourceRelease(ctx, f.id, "Heat.1995.720p.BluRay.x264-OLD"); err != nil {
		t.Fatal(err)
	}
	upgraded := writeBody(t, filepath.Join(f.root, "Heat (1995)", "Heat (1995) upgraded.mkv"), "better film")
	if err := f.svc.MarkImportedTo(ctx, f.id, 0, upgraded, "Heat.1995.2160p.UHD.BluRay.x265-NEW", true); err != nil {
		t.Fatal(err)
	}
	def := planByVersion(t, f)[0]
	if def.Current != upgraded {
		t.Fatalf("current = %q, want the upgraded file", def.Current)
	}
	if want := "Heat (1995) [2160p BluRay][x265]-NEW.mkv"; filepath.Base(def.Proposed) != want {
		t.Fatalf("proposed %q, want %q", filepath.Base(def.Proposed), want)
	}
}
