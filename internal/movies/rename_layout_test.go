package movies

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The rename layout matrix: real folders in a temp dir, one movie renamed per case (the
// preview's clean items applied, as the Rename dialog and Organize do), and exactly which
// paths left their place. This file only uses what main had before the rename fix
// (cf05a0de), so the same cases run against it for the differential check
// (ARRMADA_RENAME_LAYOUT_DUMP=<file> writes what each case took away). The expectations,
// the preview check and the comparison live in rename_layout_check_test.go.

// renameCase is one library layout and one rename in it. Paths are relative to the
// test's top folder; the Movies folder is "Movies".
type renameCase struct {
	name  string
	files []string    // files to create
	links [][2]string // symlinks to create: {link, target}
	title string
	year  int
	// tracked are the movie's files: the default track's first, then extra versions'.
	tracked []string
	// others are other films in the library with a recorded file (created only when it's
	// also in files: otherwise a missing file).
	others []layoutOther
	// protected are folders (besides Movies) a delete or rename never empties or removes.
	protected []string
	// gone is exactly what the rename takes away from where it was (files moved or
	// renamed, folders removed); own is the movie's own folder when the case means it to
	// be provably so ("" when it isn't), the one place the fix may take more than before.
	gone []string
	own  string
	// folder is the naming scheme's folder template ("" = the default).
	folder string
}

// renameOutcome is what a rename did: the paths that left their place and the ones that
// arrived (relative, sorted).
type renameOutcome struct {
	Gone    []string `json:"gone"`
	Arrived []string `json:"arrived"`
}

func renameCases() []renameCase {
	const old = "Movies/heat.1995.old"
	const vid = old + "/heat.1995.1080p.bluray.mkv"
	plain := []string{vid, old + "/heat.1995.1080p.bluray.en.srt", old + "/heat.1995.1080p.bluray.nfo",
		old + "/heat.1995.1080p.bluray-poster.jpg", old + "/movie.nfo", old + "/poster.jpg", old + "/fanart.jpg",
		old + "/folder.jpg", old + "/theme.mp3", old + "/Heat (1995).txt", old + "/extrafanart/fanart1.jpg"}
	with := func(base []string, more ...string) []string { return append(append([]string{}, base...), more...) }
	return []renameCase{
		{
			name:    "plain movie folder: every sidecar moves or is renamed",
			files:   plain,
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    with(plain, old, old+"/extrafanart"),
			own:     old,
		},
		{
			name: "franchise folder: loose film beside the franchise's art and other films' folders",
			files: []string{
				"Movies/Marvel/iron.man.2008.mkv", "Movies/Marvel/iron.man.2008.en.srt", "Movies/Marvel/iron.man.2008.nfo",
				"Movies/Marvel/folder.jpg", "Movies/Marvel/poster.jpg", "Movies/Marvel/fanart.jpg", "Movies/Marvel/movie.nfo",
				"Movies/Marvel/Iron Man 2 (2010)/Iron Man 2 (2010).mkv", "Movies/Marvel/Iron Man 2 (2010)/poster.jpg",
				"Movies/Marvel/The Avengers (2012)/The Avengers (2012).mkv",
			},
			title:   "Iron Man",
			year:    2008,
			tracked: []string{"Movies/Marvel/iron.man.2008.mkv"},
			others: []layoutOther{
				{title: "Iron Man 2", year: 2010, file: "Movies/Marvel/Iron Man 2 (2010)/Iron Man 2 (2010).mkv"},
				{title: "The Avengers", year: 2012, file: "Movies/Marvel/The Avengers (2012)/The Avengers (2012).mkv"},
			},
			gone: []string{"Movies/Marvel/iron.man.2008.mkv", "Movies/Marvel/iron.man.2008.en.srt"},
		},
		{
			name:    "franchise folder holding only this film and the franchise's art",
			files:   []string{"Movies/Marvel/iron.man.2008.mkv", "Movies/Marvel/folder.jpg", "Movies/Marvel/poster.jpg"},
			title:   "Iron Man",
			year:    2008,
			tracked: []string{"Movies/Marvel/iron.man.2008.mkv"},
			gone:    []string{"Movies/Marvel/iron.man.2008.mkv"},
		},
		{
			name:    "franchise folder holding only this film",
			files:   []string{"Movies/Marvel/iron.man.2008.mkv"},
			title:   "Iron Man",
			year:    2008,
			tracked: []string{"Movies/Marvel/iron.man.2008.mkv"},
			gone:    []string{"Movies/Marvel/iron.man.2008.mkv", "Movies/Marvel"},
		},
		{
			name:    "two films in one folder",
			files:   []string{"Movies/Crime/heat.1995.mkv", "Movies/Crime/Ronin (1998).mkv", "Movies/Crime/poster.jpg", "Movies/Crime/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Crime/heat.1995.mkv"},
			others:  []layoutOther{{title: "Ronin", year: 1998, file: "Movies/Crime/Ronin (1998).mkv"}},
			gone:    []string{"Movies/Crime/heat.1995.mkv"},
		},
		{
			name:    "two films in the movie's own-named folder, the other untracked",
			files:   []string{vid, old + "/Ronin (1998).mkv", old + "/poster.jpg", old + "/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{vid},
		},
		{
			name:    "another film recorded in the folder with its file missing",
			files:   []string{vid, old + "/poster.jpg", old + "/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			others:  []layoutOther{{title: "Ronin", year: 1998, file: old + "/Ronin (1998).mkv"}},
			gone:    []string{vid},
		},
		{
			name:    "a subtitle of another film's missing file that starts like the movie's",
			files:   []string{vid, old + "/heat.1995.1080p.bluray.en.srt", old + "/heat.1995.1080p.bluray.directors.cut.en.srt", old + "/poster.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			others:  []layoutOther{{title: "Heat Directors Cut", year: 1995, file: old + "/heat.1995.1080p.bluray.directors.cut.mkv"}},
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "symlinked movie folder",
			files:   []string{"elsewhere/heat.1995.old/heat.1995.1080p.bluray.mkv", "elsewhere/heat.1995.old/poster.jpg", "elsewhere/heat.1995.old/movie.nfo", "Movies/.keep"},
			links:   [][2]string{{old, "elsewhere/heat.1995.old"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{"elsewhere/heat.1995.old/heat.1995.1080p.bluray.mkv"},
		},
		{
			name:    "symlinked movie folder holding only the video",
			files:   []string{"elsewhere/heat.1995.old/heat.1995.1080p.bluray.mkv", "Movies/.keep"},
			links:   [][2]string{{old, "elsewhere/heat.1995.old"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{"elsewhere/heat.1995.old/heat.1995.1080p.bluray.mkv"},
		},
		{
			name:    "symlinked category",
			files:   []string{"elsewhere/Crime/heat.1995.old/heat.1995.1080p.bluray.mkv", "elsewhere/Crime/heat.1995.old/poster.jpg", "elsewhere/Crime/folder.jpg", "Movies/.keep"},
			links:   [][2]string{{"Movies/Crime", "elsewhere/Crime"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Crime/heat.1995.old/heat.1995.1080p.bluray.mkv"},
			gone:    []string{"elsewhere/Crime/heat.1995.old/heat.1995.1080p.bluray.mkv"},
		},
		{
			name:    "another folder links into the movie's folder",
			files:   plain,
			links:   [][2]string{{"Movies/HeatAlias", old}},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:      "a protected folder holding the film loose",
			files:     []string{"Movies/Incoming/heat.1995.1080p.bluray.mkv", "Movies/Incoming/poster.jpg", "Movies/Incoming/movie.nfo"},
			title:     "Heat",
			year:      1995,
			tracked:   []string{"Movies/Incoming/heat.1995.1080p.bluray.mkv"},
			protected: []string{"Movies/Incoming"},
			gone:      []string{"Movies/Incoming/heat.1995.1080p.bluray.mkv"},
		},
		{
			name:      "a protected folder holding only the film",
			files:     []string{"Movies/Incoming/heat.1995.1080p.bluray.mkv"},
			title:     "Heat",
			year:      1995,
			tracked:   []string{"Movies/Incoming/heat.1995.1080p.bluray.mkv"},
			protected: []string{"Movies/Incoming"},
			gone:      []string{"Movies/Incoming/heat.1995.1080p.bluray.mkv"},
		},
		{
			name:    "film loose in the Movies folder",
			files:   []string{"Movies/heat.1995.1080p.bluray.mkv", "Movies/heat.1995.1080p.bluray.en.srt", "Movies/poster.jpg", "Movies/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/heat.1995.1080p.bluray.mkv"},
			gone:    []string{"Movies/heat.1995.1080p.bluray.mkv", "Movies/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name: "release-named folder with the group's GRP.sfv",
			files: []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv", "Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.nfo",
				"Movies/Heat.1995.1080p.BluRay-GRP/GRP.sfv", "Movies/Heat.1995.1080p.BluRay-GRP/poster.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv"},
			gone:    []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv"},
		},
		{
			name:    "an unknown file type keeps the folder's files",
			files:   with(plain, old+"/notes.docx"),
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "a hidden folder with a video",
			files:   []string{vid, old + "/poster.jpg", old + "/movie.nfo", old + "/.stash/Other (2001).mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{vid},
		},
		{
			name:    "a sidecar that is a symlink",
			files:   []string{vid, old + "/movie.nfo", "elsewhere/art/poster.jpg"},
			links:   [][2]string{{old + "/poster.jpg", "elsewhere/art/poster.jpg"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{vid},
		},
		{
			name: "case differences in names",
			files: []string{"Movies/HEAT.1995.OLD/HEAT.1995.1080P.BLURAY.MKV", "Movies/HEAT.1995.OLD/HEAT.1995.1080P.BLURAY.EN.SRT",
				"Movies/HEAT.1995.OLD/POSTER.JPG", "Movies/HEAT.1995.OLD/Movie.NFO", "Movies/HEAT.1995.OLD/heat.1995.1080p.bluray-FANART.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/HEAT.1995.OLD/HEAT.1995.1080P.BLURAY.MKV"},
			gone: []string{"Movies/HEAT.1995.OLD/HEAT.1995.1080P.BLURAY.MKV", "Movies/HEAT.1995.OLD/HEAT.1995.1080P.BLURAY.EN.SRT",
				"Movies/HEAT.1995.OLD/POSTER.JPG", "Movies/HEAT.1995.OLD/Movie.NFO", "Movies/HEAT.1995.OLD/heat.1995.1080p.bluray-FANART.jpg", "Movies/HEAT.1995.OLD"},
			own: "Movies/HEAT.1995.OLD",
		},
		{
			name: "versions in separate folders",
			files: []string{"Movies/heat.1995.1080p/heat.1995.1080p.bluray.mkv", "Movies/heat.1995.1080p/poster.jpg", "Movies/heat.1995.1080p/movie.nfo",
				"Movies/heat.1995.2160p/heat.1995.2160p.uhd.bluray.mkv", "Movies/heat.1995.2160p/poster.jpg", "Movies/heat.1995.2160p/heat.1995.2160p.uhd.bluray.en.srt"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/heat.1995.1080p/heat.1995.1080p.bluray.mkv", "Movies/heat.1995.2160p/heat.1995.2160p.uhd.bluray.mkv"},
			gone: []string{"Movies/heat.1995.1080p/heat.1995.1080p.bluray.mkv", "Movies/heat.1995.1080p/poster.jpg", "Movies/heat.1995.1080p/movie.nfo", "Movies/heat.1995.1080p",
				"Movies/heat.1995.2160p/heat.1995.2160p.uhd.bluray.mkv", "Movies/heat.1995.2160p/heat.1995.2160p.uhd.bluray.en.srt"},
			own: "Movies/heat.1995.1080p|Movies/heat.1995.2160p",
		},
		{
			name:    "a {title} scheme sends the film into another film's recorded folder",
			files:   with(plain, "Movies/Heat/Heat - Bluray-1080p.mkv", "Movies/Heat/poster.jpg", "Movies/Heat/movie.nfo"),
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			others:  []layoutOther{{title: "Heat", year: 1986, file: "Movies/Heat/Heat - Bluray-1080p.mkv"}},
			folder:  "{title}",
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "a {title} scheme into another film's recorded folder whose file is missing",
			files:   with(plain, "Movies/Heat/movie.nfo"),
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			others:  []layoutOther{{title: "Heat", year: 1986, file: "Movies/Heat/Heat - Bluray-1080p.mkv"}},
			folder:  "{title}",
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "a {title} scheme into another film's recorded folder, missing from disk",
			files:   plain,
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			others:  []layoutOther{{title: "Heat", year: 1986, file: "Movies/Heat/Heat (1986).mkv"}},
			folder:  "{title}",
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "two films with the same title and year: the other's folder is the target",
			files:   with(plain, "Movies/Heat (1995)/Heat (1995) - Remux-2160p.mkv", "Movies/Heat (1995)/poster.jpg"),
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			others:  []layoutOther{{title: "Heat", year: 1995, file: "Movies/Heat (1995)/Heat (1995) - Remux-2160p.mkv"}},
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "the target folder exists and holds an untracked video",
			files:   with(plain, "Movies/Heat (1995)/sample.mkv"),
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    []string{vid, old + "/heat.1995.1080p.bluray.en.srt"},
		},
		{
			name:    "the target folder exists and holds no film",
			files:   with(plain, "Movies/Heat (1995)/.keep"),
			title:   "Heat",
			year:    1995,
			tracked: []string{vid},
			gone:    with(plain, old, old+"/extrafanart"),
			own:     old,
		},
		{
			name:    "another version of the movie is recorded in the folder, its file missing",
			files:   []string{vid, old + "/poster.jpg", old + "/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{vid, old + "/Heat (1995) Remux-2160p.mkv"},
			gone:    []string{vid},
		},
	}
}

// Hooks set by rename_layout_check_test.go (absent on main before the fix): what the
// preview says moves, the case's assertions and the comparison with the old behaviour.
var (
	renameLayoutPreview func(t *testing.T, items []RenameItem) []string
	renameLayoutCheck   func(t *testing.T, c renameCase, out renameOutcome, preview []string, items []RenameItem)
	renameLayoutReport  func(t *testing.T, cases []renameCase, outs map[string]renameOutcome)
)

// runRenameCase builds the case's folders, renames the movie, and returns what left its
// place (relative, sorted), what the preview listed (nil before the fix) and the plan.
func runRenameCase(t *testing.T, c renameCase) (out renameOutcome, preview []string, items []RenameItem) {
	t.Helper()
	top := t.TempDir()
	abs := func(rel string) string { return filepath.Join(top, filepath.FromSlash(rel)) }
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
	if err := os.MkdirAll(abs("Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(st.DB(), nil, nil, abs("Movies"), filepath.Join(t.TempDir(), "bin"), eventbus.New(log), log)
	if c.folder != "" {
		svc.imp.SetNaming(schemeNaming{library.Naming{Folder: c.folder, File: library.DefaultMovieFile}})
	}
	if len(c.protected) > 0 {
		var prot []string
		for _, p := range c.protected {
			prot = append(prot, abs(p))
		}
		svc.SetProtectedDirs(func() []string { return prot })
	}
	ctx := context.Background()
	m, err := svc.repo.Create(ctx, Movie{TMDBID: 949, Title: c.title, Year: c.year, Monitored: true})
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
		v, err := svc.repo.CreateVersion(ctx, m.ID, Version{Label: "Version " + string(rune('A'+i)), QualityProfile: "hd"})
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
	}
	plan, err := svc.RenamePlan(ctx, []int64{m.ID})
	if err != nil {
		t.Fatal(err)
	}
	var todo []RenameItem
	for _, it := range plan {
		if it.Status == RenameOK {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		t.Fatalf("nothing to rename: %+v", plan)
	}
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		t.Fatal(err)
	}
	if renameLayoutPreview != nil {
		// The plan names paths as the library spells them; the snapshot sees where they
		// really are. Resolve every link above the last element, as Lstat does.
		for _, p := range renameLayoutPreview(t, todo) {
			if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
				p = filepath.Join(dir, filepath.Base(p))
			}
			rel, err := filepath.Rel(realTop, p)
			if err != nil {
				t.Fatal(err)
			}
			preview = append(preview, filepath.ToSlash(rel))
		}
		sort.Strings(preview)
	}
	// Send the plan as the browser would: through JSON.
	b, err := json.Marshal(todo)
	if err != nil {
		t.Fatal(err)
	}
	var sent []RenameItem
	if err := json.Unmarshal(b, &sent); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, top)
	res, err := svc.ApplyRename(ctx, sent)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if !r.Renamed {
			t.Errorf("%s not renamed: %s", r.From, r.Reason)
		}
	}
	after := snapshotTree(t, top)
	for p := range before {
		if !after[p] {
			out.Gone = append(out.Gone, p)
		}
	}
	for p := range after {
		if !before[p] {
			out.Arrived = append(out.Arrived, p)
		}
	}
	sort.Strings(out.Gone)
	sort.Strings(out.Arrived)
	if out.Gone == nil {
		out.Gone = []string{}
	}
	if out.Arrived == nil {
		out.Arrived = []string{}
	}
	return out, preview, todo
}

// TestRenameLayoutMatrix renames a movie in each layout and records exactly what left.
func TestRenameLayoutMatrix(t *testing.T) {
	cases := renameCases()
	results := map[string]renameOutcome{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, preview, items := runRenameCase(t, c)
			results[c.name] = out
			if renameLayoutCheck != nil {
				renameLayoutCheck(t, c, out, preview, items)
			}
		})
	}
	if out := os.Getenv("ARRMADA_RENAME_LAYOUT_DUMP"); out != "" {
		b, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if renameLayoutReport != nil {
		renameLayoutReport(t, cases, results)
	}
}
