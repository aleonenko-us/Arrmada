package movies

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/store"
)

// "Move to folder" for a film, against real folders: every layout is moved twice, once
// the way it was before (the whole folder moved whenever no other film had a file present
// in it — OwnFolder always yes) and once with MoveFolderCheck. The fix may only move
// less; where both move, they move the same files. Every path is relative to a temp
// folder holding Movies (the default Movies folder) and Dest (the folder moved to).

type moveOther struct {
	title, file string
	year        int
	missing     bool // recorded, but its file isn't on disk
}

type moveCase struct {
	name      string
	title     string // default Heat
	year      int    // default 1995
	files     []string
	links     [][2]string // link → target
	tracked   []string    // the film's recorded files (the first is the default track)
	others    []moveOther
	protected []string // downloads and other folders a delete never empties
	cross     bool     // Movies is on another drive than Dest: every rename out of it is EXDEV
	destTaken string   // a folder already in Dest
	was, want string   // "whole" or "refused": before the fix, and now
}

var moveCases = []moveCase{
	{name: "own folder", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/Heat (1995).en.srt", "Movies/Heat (1995)/poster.jpg",
		"Movies/Heat (1995)/movie.nfo", "Movies/Heat (1995)/extrafanart/1.jpg", "Movies/Heat (1995)/theme.mp3",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"}, was: "whole", want: "whole"},
	{name: "loose film in a franchise folder with untracked films and art", title: "Iron Man", year: 2008, files: []string{
		"Movies/Marvel/Iron Man (2008).mkv", "Movies/Marvel/Iron Man 2 (2010).mkv", "Movies/Marvel/Thor (2011).mkv",
		"Movies/Marvel/poster.jpg", "Movies/Marvel/folder.jpg",
	}, tracked: []string{"Movies/Marvel/Iron Man (2008).mkv"}, was: "whole", want: "refused"},
	{name: "two films in one folder", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/Ronin (1998).mkv",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"},
		others: []moveOther{{title: "Ronin", year: 1998, file: "Movies/Heat (1995)/Ronin (1998).mkv"}}, was: "refused", want: "refused"},
	{name: "another film recorded there, its file missing", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/Ronin (1998).nfo",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"},
		others: []moveOther{{title: "Ronin", year: 1998, file: "Movies/Heat (1995)/Ronin (1998).mkv", missing: true}}, was: "whole", want: "refused"},
	{name: "symlinked movie folder", files: []string{
		"Elsewhere/Heat (1995)/Heat (1995).mkv", "Elsewhere/Heat (1995)/poster.jpg",
	}, links: [][2]string{{"Movies/Heat (1995)", "Elsewhere/Heat (1995)"}},
		tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"}, was: "whole", want: "refused"},
	{name: "symlinked category", files: []string{
		"Elsewhere/Crime/Heat (1995)/Heat (1995).mkv", "Elsewhere/Crime/Heat (1995)/poster.jpg",
	}, links: [][2]string{{"Movies/Crime", "Elsewhere/Crime"}},
		tracked: []string{"Movies/Crime/Heat (1995)/Heat (1995).mkv"}, was: "whole", want: "refused"},
	{name: "protected folder (a downloads folder in Movies)", files: []string{
		"Movies/Incoming/Heat (1995).mkv", "Movies/Incoming/Ronin.1998.1080p.mkv", "Movies/Incoming/half.part",
	}, tracked: []string{"Movies/Incoming/Heat (1995).mkv"}, protected: []string{"Movies/Incoming"}, was: "whole", want: "refused"},
	{name: "film loose in the Movies folder", files: []string{
		"Movies/Heat (1995).mkv", "Movies/Ronin (1998).mkv",
	}, tracked: []string{"Movies/Heat (1995).mkv"}, was: "refused", want: "refused"},
	{name: "release-named folder with GRP.sfv", files: []string{
		"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv", "Movies/Heat.1995.1080p.BluRay-GRP/GRP.sfv",
	}, tracked: []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv"}, was: "whole", want: "refused"},
	{name: "release-named folder with its own nfo", files: []string{
		"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv", "Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.nfo",
	}, tracked: []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv"}, was: "whole", want: "whole"},
	{name: "unknown file type", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/notes.docx",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"}, was: "whole", want: "refused"},
	{name: "another video in a hidden folder", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/.stash/Ronin (1998).mkv",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"}, was: "whole", want: "refused"},
	{name: "versions in separate folders", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/poster.jpg", "Movies/Heat (1995) 4K/Heat (1995) - 4K.mkv",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995) 4K/Heat (1995) - 4K.mkv"}, was: "whole", want: "whole"},
	{name: "a version in a subfolder of the film's folder", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/4K/Heat (1995) - 4K.mkv",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/4K/Heat (1995) - 4K.mkv"}, was: "whole", want: "refused"},
	{name: "own folder, onto another drive", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv", "Movies/Heat (1995)/Heat (1995).en.srt", "Movies/Heat (1995)/poster.jpg",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"}, cross: true, was: "whole", want: "whole"},
	{name: "franchise folder, onto another drive", title: "Iron Man", year: 2008, files: []string{
		"Movies/Marvel/Iron Man (2008).mkv", "Movies/Marvel/Thor (2011).mkv", "Movies/Marvel/poster.jpg",
	}, tracked: []string{"Movies/Marvel/Iron Man (2008).mkv"}, cross: true, was: "whole", want: "refused"},
	{name: "destination name already exists", files: []string{
		"Movies/Heat (1995)/Heat (1995).mkv",
	}, tracked: []string{"Movies/Heat (1995)/Heat (1995).mkv"}, destTaken: "Heat (1995)", was: "refused", want: "refused"},
}

// leaves lists every file and link under top (folders aside), relative.
func leaves(t *testing.T, top string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(top, p)
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// runMoveCase builds the case and moves the film to Dest, with the check (checked) or
// as before. It returns what left its place (relative, sorted) and the move's error.
func runMoveCase(t *testing.T, c moveCase, checked bool) (moved []string, top string, moveErr error) {
	t.Helper()
	top = t.TempDir()
	abs := func(rel string) string { return filepath.Join(top, filepath.FromSlash(rel)) }
	for _, d := range []string{"Movies", "Dest"} {
		if err := os.MkdirAll(abs(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range c.files {
		writeFixture(t, abs(f))
	}
	for _, l := range c.links {
		if err := os.MkdirAll(filepath.Dir(abs(l[0])), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(abs(l[1]), abs(l[0])); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}
	if c.destTaken != "" {
		if err := os.MkdirAll(abs("Dest/"+c.destTaken), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	roots := libroots.New(nil, config.Config{MoviesDir: abs("Movies")})
	folders := libfolders.New(st.DB(), roots, t.TempDir())
	roots.WithExtras(folders.Extras)
	dest, err := folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaMovie, Path: abs("Dest"), Label: "Dest"})
	if err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	svc := NewService(st.DB(), nil, nil, abs("Movies"), binDir, eventbus.New(log), log)
	svc.SetRootFunc(func() string { return abs("Movies") })
	svc.SetFolders(folders)
	svc.SetProtectedDirs(func() []string {
		var out []string
		for _, p := range c.protected {
			out = append(out, abs(p))
		}
		return out
	})
	title, year := c.title, c.year
	if title == "" {
		title, year = "Heat", 1995
	}
	m, err := svc.repo.Create(ctx, Movie{TMDBID: 949, Title: title, Year: year, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range c.tracked {
		if i == 0 {
			if err := svc.repo.SetFile(ctx, m.ID, abs(p)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		v, err := svc.repo.CreateVersion(ctx, m.ID, Version{Label: fmt.Sprintf("Version %d", i), QualityProfile: "hd"})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.repo.SetVersionFile(ctx, v.ID, abs(p), 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	for i, o := range c.others {
		om, err := svc.repo.Create(ctx, Movie{TMDBID: 5000 + i, Title: o.title, Year: o.year, Monitored: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.repo.SetFile(ctx, om.ID, abs(o.file)); err != nil {
			t.Fatal(err)
		}
		if o.missing {
			if _, err := st.DB().Exec(`UPDATE movies SET has_file = 0 WHERE id = ?`, om.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	mover := &libfolders.Mover{DB: st.DB(), Folders: folders, Bin: library.SingleBin(binDir), Log: log,
		OwnFolder: func(context.Context, int64, string) string { return "" }}
	if checked {
		mover.OwnFolder = svc.MoveFolderCheck
	}
	if c.cross {
		mover.Rename = func(oldpath, newpath string) error {
			if !libfolders.StrictlyUnder(oldpath, abs("Dest")) {
				return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
			}
			return os.Rename(oldpath, newpath)
		}
	}
	before := leaves(t, top)
	_, moveErr = mover.Move(ctx, libfolders.MediaMovie, m.ID, dest.ID, nil)
	after := leaves(t, top)
	for p := range before {
		if !after[p] {
			moved = append(moved, p)
		}
	}
	sort.Strings(moved)
	return moved, top, moveErr
}

func moveOutcome(moved []string, err error) string {
	switch {
	case err != nil && len(moved) == 0:
		return "refused"
	case err != nil:
		return fmt.Sprintf("PARTIAL %d (%v)", len(moved), err)
	case len(moved) == 0:
		return "nothing"
	}
	return "whole"
}

func TestMoveLayoutMatrix(t *testing.T) {
	rows := []string{"| layout | before (main) | now | moved before | moved now |", "|---|---|---|---|---|"}
	for _, c := range moveCases {
		t.Run(c.name, func(t *testing.T) {
			was, _, wasErr := runMoveCase(t, c, false)
			now, top, nowErr := runMoveCase(t, c, true)
			wasOut, nowOut := moveOutcome(was, wasErr), moveOutcome(now, nowErr)
			rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s | %s |", c.name, wasOut, nowOut, strings.Join(was, ", "), strings.Join(now, ", ")))
			if wasOut != c.was {
				t.Errorf("before: %s (%v), the matrix says %s", wasOut, wasErr, c.was)
			}
			if nowOut != c.want {
				t.Errorf("now: %s (%v), want %s", nowOut, nowErr, c.want)
			}
			// Only ever less: nothing moves now that didn't before.
			wasSet := map[string]bool{}
			for _, p := range was {
				wasSet[p] = true
			}
			for _, p := range now {
				if !wasSet[p] {
					t.Errorf("%s moves now and didn't before", p)
				}
			}
			if c.want == "refused" {
				if !errors.Is(nowErr, libfolders.ErrBadPath) && !errors.Is(nowErr, libfolders.ErrBusy) {
					t.Errorf("refusal = %v, want a plain refusal", nowErr)
				}
				return
			}
			// A whole move takes exactly the film's own folders, and everything arrives.
			own := map[string]bool{}
			for _, p := range c.tracked {
				own[filepath.ToSlash(filepath.Dir(p))] = true
			}
			if len(now) != len(was) {
				t.Errorf("now moves %d, before %d", len(now), len(was))
			}
			for _, p := range now {
				if !own[filepath.ToSlash(filepath.Dir(p))] && !own[filepath.ToSlash(filepath.Dir(filepath.Dir(p)))] {
					t.Errorf("%s isn't in the film's own folder", p)
				}
				rel := strings.TrimPrefix(p, "Movies/")
				if _, err := os.Stat(filepath.Join(top, "Dest", filepath.FromSlash(rel))); err != nil {
					t.Errorf("%s didn't arrive: %v", p, err)
				}
			}
			if c.cross {
				// The originals were retired only after the copy, and their folder with them.
				for d := range own {
					if _, err := os.Lstat(filepath.Join(top, filepath.FromSlash(d))); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("the emptied original %s is still there (%v)", d, err)
					}
				}
			}
		})
	}
	t.Log("\n" + strings.Join(rows, "\n"))
}

// The check's reasons, one per rule, as the dialog shows them.
func TestMoveFolderCheckReasons(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		c          moveCase
	}{
		{"franchise", "its name isn't the movie's", moveCases[1]},
		{"recorded missing", "another film in your library has a file recorded in it (Ronin (1998).mkv)", moveCases[3]},
		{"link", "it's a link to another folder", moveCases[4]},
		{"category link", "it's reached through a link", moveCases[5]},
		{"protected", "it's a library or downloads folder", moveCases[6]},
		{"sfv", "it holds a file named for something else (GRP.sfv)", moveCases[8]},
		{"unknown", "it holds a file Arrmada doesn't recognise (notes.docx)", moveCases[10]},
		{"hidden video", "another video is inside it (.stash)", moveCases[11]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runMoveCase(t, tc.c, true)
			if err == nil || !strings.Contains(err.Error(), "("+tc.want+")") {
				t.Errorf("refusal = %v, want it to say %q", err, tc.want)
			}
		})
	}
}
