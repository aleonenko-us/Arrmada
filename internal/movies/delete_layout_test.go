package movies

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The layout matrix (GH #59): real folders in a temp dir, one movie deleted per case,
// and exactly which paths went. This file only uses what v0.3.2 already had, so the same
// cases run against the last release for the differential check
// (ARRMADA_LAYOUT_DUMP=<file> writes what each case removed). The expectations, the
// preview check and the comparison with the release live in delete_layout_check_test.go.

// layoutCase is one library layout and one delete in it. Paths are relative to the test's
// top folder; the Movies folder is "Movies" unless root says otherwise.
type layoutCase struct {
	name  string
	root  string      // the Movies folder (relative); "" = "Movies"
	files []string    // files to create
	links [][2]string // symlinks to create: {link, target}
	title string
	year  int
	// tracked are the movie's files: the default track's first, then extra versions'.
	tracked []string
	// bin: "" = on; "off" = deliberately off; "refuse:<name>" = on, but refuses <name>.
	bin string
	// action: "" = delete the movie with its files; "version" = delete the last extra
	// version; "file" = delete the default track's file.
	action string
	// want is exactly what the delete removes; own is the movie's own folder when the
	// case means it to be provably so ("" when it isn't), the one place a delete may
	// remove more than v0.3.2 did.
	want []string
	own  string
}

func layoutCases() []layoutCase {
	art := func(dir string, names ...string) []string {
		var out []string
		for _, n := range names {
			out = append(out, dir+"/"+n)
		}
		return out
	}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	const heat = "Movies/Heat (1995)"
	plainArt := art(heat, "movie.nfo", "poster.jpg", "fanart.jpg", "folder.jpg", "theme.mp3", "Heat (1995).txt", "extrafanart/fanart1.jpg")
	return []layoutCase{
		{
			name:    "plain movie folder with nfo and artwork",
			files:   join([]string{heat + "/Heat (1995).mkv", heat + "/Heat (1995).en.srt"}, plainArt),
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    join([]string{heat + "/Heat (1995).mkv", heat + "/Heat (1995).en.srt", heat, heat + "/extrafanart"}, plainArt),
			own:     heat,
		},
		{
			name: "franchise folder: loose film beside its own art and other films' folders",
			files: []string{
				"Movies/Marvel/Iron Man (2008).mkv", "Movies/Marvel/Iron Man (2008).nfo",
				"Movies/Marvel/folder.jpg", "Movies/Marvel/poster.jpg", "Movies/Marvel/fanart.jpg",
				"Movies/Marvel/Iron Man 2 (2010)/Iron Man 2 (2010).mkv", "Movies/Marvel/Iron Man 2 (2010)/poster.jpg",
				"Movies/Marvel/The Avengers (2012)/The Avengers (2012).mkv",
			},
			title:   "Iron Man",
			year:    2008,
			tracked: []string{"Movies/Marvel/Iron Man (2008).mkv"},
			want:    []string{"Movies/Marvel/Iron Man (2008).mkv"},
		},
		{
			name: "franchise folder holding only this film and its art (named for the franchise)",
			files: []string{
				"Movies/Marvel/Iron Man (2008).mkv", "Movies/Marvel/folder.jpg", "Movies/Marvel/poster.jpg",
			},
			title:   "Iron Man",
			year:    2008,
			tracked: []string{"Movies/Marvel/Iron Man (2008).mkv"},
			want:    []string{"Movies/Marvel/Iron Man (2008).mkv"},
		},
		{
			name:    "movie folder holding another movie's video",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/movie.nfo", heat + "/Ronin (1998).mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "nested collection: another film's folder inside this one's",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/Heat 2 (2026)/Heat 2 (2026).mkv", heat + "/Heat 2 (2026)/poster.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "movie file directly in the Movies folder",
			files:   []string{"Movies/Heat (1995).mkv", "Movies/Heat (1995).en.srt", "Movies/poster.jpg", "Movies/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Heat (1995).mkv"},
			want:    []string{"Movies/Heat (1995).mkv", "Movies/Heat (1995).en.srt"},
		},
		{
			name:    "only file in the Movies folder",
			files:   []string{"Movies/Heat (1995).mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Heat (1995).mkv"},
			want:    []string{"Movies/Heat (1995).mkv"},
		},
		{
			name:    "symlinked movie folder",
			files:   []string{"elsewhere/Heat (1995)/Heat (1995).mkv", "elsewhere/Heat (1995)/poster.jpg", "elsewhere/Heat (1995)/movie.nfo", "Movies/.keep"},
			links:   [][2]string{{heat, "elsewhere/Heat (1995)"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{"elsewhere/Heat (1995)/Heat (1995).mkv"},
		},
		{
			name:    "symlinked movie folder holding only the video",
			files:   []string{"elsewhere/Heat (1995)/Heat (1995).mkv", "Movies/.keep"},
			links:   [][2]string{{heat, "elsewhere/Heat (1995)"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{"elsewhere/Heat (1995)/Heat (1995).mkv"},
		},
		{
			name:    "symlinked category",
			files:   []string{"elsewhere/Crime/Heat (1995)/Heat (1995).mkv", "elsewhere/Crime/Heat (1995)/poster.jpg", "elsewhere/Crime/folder.jpg", "Movies/.keep"},
			links:   [][2]string{{"Movies/Crime", "elsewhere/Crime"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Crime/Heat (1995)/Heat (1995).mkv"},
			want:    []string{"elsewhere/Crime/Heat (1995)/Heat (1995).mkv"},
		},
		{
			name:    "symlinked category, movie folder holding only the video",
			files:   []string{"elsewhere/Crime/Heat (1995)/Heat (1995).mkv", "Movies/.keep"},
			links:   [][2]string{{"Movies/Crime", "elsewhere/Crime"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Crime/Heat (1995)/Heat (1995).mkv"},
			want:    []string{"elsewhere/Crime/Heat (1995)/Heat (1995).mkv", "elsewhere/Crime/Heat (1995)"},
		},
		{
			name:    "a sidecar that is a symlink",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/movie.nfo", "elsewhere/art/poster.jpg"},
			links:   [][2]string{{heat + "/poster.jpg", "elsewhere/art/poster.jpg"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "a symlinked subfolder",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/movie.nfo", "elsewhere/art/fanart1.jpg"},
			links:   [][2]string{{heat + "/extrafanart", "elsewhere/art"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "an unknown file type keeps the folder",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/movie.nfo", heat + "/notes.docx"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "a hidden folder with a video",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/.stash/Other (2001).mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "an extras folder with a trailer video",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/Trailers/Heat trailer.mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name:    "an extras folder with a folder inside",
			files:   []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/extrafanart/more/1.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    []string{heat + "/Heat (1995).mkv"},
		},
		{
			name: "two versions in one folder: delete the movie",
			files: []string{heat + "/Heat (1995) Bluray-1080p.mkv", heat + "/Heat (1995) Bluray-1080p.en.srt",
				heat + "/Heat (1995) Bluray-1080p.Directors.Cut.mkv", heat + "/Heat (1995) Bluray-1080p.Directors.Cut.en.srt",
				heat + "/poster.jpg", heat + "/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995) Bluray-1080p.mkv", heat + "/Heat (1995) Bluray-1080p.Directors.Cut.mkv"},
			want: []string{heat + "/Heat (1995) Bluray-1080p.mkv", heat + "/Heat (1995) Bluray-1080p.en.srt",
				heat + "/Heat (1995) Bluray-1080p.Directors.Cut.mkv", heat + "/Heat (1995) Bluray-1080p.Directors.Cut.en.srt",
				heat + "/poster.jpg", heat + "/movie.nfo", heat},
			own: heat,
		},
		{
			name: "two versions in one folder: delete one version",
			files: []string{heat + "/Heat (1995) Bluray-1080p.mkv", heat + "/Heat (1995) Bluray-1080p.en.srt",
				heat + "/Heat (1995) Bluray-1080p.Directors.Cut.mkv", heat + "/Heat (1995) Bluray-1080p.Directors.Cut.en.srt",
				heat + "/poster.jpg", heat + "/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995) Bluray-1080p.mkv", heat + "/Heat (1995) Bluray-1080p.Directors.Cut.mkv"},
			action:  "version",
			want:    []string{heat + "/Heat (1995) Bluray-1080p.Directors.Cut.mkv", heat + "/Heat (1995) Bluray-1080p.Directors.Cut.en.srt"},
		},
		{
			name:    "delete only the default file of a plain folder",
			files:   join([]string{heat + "/Heat (1995).mkv", heat + "/Heat (1995).en.srt"}, plainArt),
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			action:  "file",
			want:    []string{heat + "/Heat (1995).mkv", heat + "/Heat (1995).en.srt"},
		},
		{
			name:    "delete only the default file in a symlinked movie folder",
			files:   []string{"elsewhere/Heat (1995)/Heat (1995).mkv", "Movies/.keep"},
			links:   [][2]string{{heat, "elsewhere/Heat (1995)"}},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			action:  "file",
			want:    []string{"elsewhere/Heat (1995)/Heat (1995).mkv"},
		},
		{
			name: "two versions in separate folders",
			files: []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/movie.nfo",
				heat + " 2160p/Heat (1995) 2160p.mkv", heat + " 2160p/poster.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv", heat + " 2160p/Heat (1995) 2160p.mkv"},
			want: []string{heat + "/Heat (1995).mkv", heat + "/poster.jpg", heat + "/movie.nfo", heat,
				heat + " 2160p/Heat (1995) 2160p.mkv", heat + " 2160p/poster.jpg", heat + " 2160p"},
			own: heat + "|" + heat + " 2160p",
		},
		{
			name:    "recycle bin off",
			files:   join([]string{heat + "/Heat (1995).mkv", heat + "/Heat (1995).en.srt"}, plainArt),
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			bin:     "off",
			want:    []string{heat + "/Heat (1995).mkv", heat + "/Heat (1995).en.srt"},
		},
		{
			name:    "recycle bin off, folder holding only the video",
			files:   []string{heat + "/Heat (1995).mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			bin:     "off",
			want:    []string{heat + "/Heat (1995).mkv", heat},
		},
		{
			name:    "a sidecar the bin refuses",
			files:   join([]string{heat + "/Heat (1995).mkv"}, plainArt),
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			bin:     "refuse:fanart.jpg",
			want: join([]string{heat + "/Heat (1995).mkv", heat + "/extrafanart"},
				art(heat, "movie.nfo", "poster.jpg", "folder.jpg", "theme.mp3", "Heat (1995).txt", "extrafanart/fanart1.jpg")),
			own: heat,
		},
		{
			name:    "the Movies folder is named like the movie",
			root:    "Heat (1995)",
			files:   []string{"Heat (1995)/Heat (1995).mkv", "Heat (1995)/poster.jpg", "Heat (1995)/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Heat (1995)/Heat (1995).mkv"},
			want:    []string{"Heat (1995)/Heat (1995).mkv"},
		},
		{
			name:    "the Movies folder is named like the movie and holds only the video",
			root:    "Heat (1995)",
			files:   []string{"Heat (1995)/Heat (1995).mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Heat (1995)/Heat (1995).mkv"},
			want:    []string{"Heat (1995)/Heat (1995).mkv"},
		},
		{
			name:    "case differences in the folder and sidecar names",
			files:   []string{"Movies/HEAT (1995)/heat (1995).MKV", "Movies/HEAT (1995)/POSTER.JPG", "Movies/HEAT (1995)/Movie.NFO", "Movies/HEAT (1995)/Folder.Jpeg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/HEAT (1995)/heat (1995).MKV"},
			want: []string{"Movies/HEAT (1995)/heat (1995).MKV", "Movies/HEAT (1995)/POSTER.JPG", "Movies/HEAT (1995)/Movie.NFO",
				"Movies/HEAT (1995)/Folder.Jpeg", "Movies/HEAT (1995)"},
			own: "Movies/HEAT (1995)",
		},
		{
			name:    "the Movies folder spelled in another case",
			files:   []string{"movies/Heat (1995)/Heat (1995).mkv", "movies/Heat (1995)/poster.jpg", "Movies/.keep"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"movies/Heat (1995)/Heat (1995).mkv"},
			want:    []string{"movies/Heat (1995)/Heat (1995).mkv"},
		},
		{
			name:    "a file directly in the Movies folder spelled in another case",
			files:   []string{"movies/Heat (1995).mkv", "Movies/.keep"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"movies/Heat (1995).mkv"},
			want:    []string{"movies/Heat (1995).mkv"},
		},
		{
			name:    "folder named without the year",
			files:   []string{"Movies/Heat/Heat.mkv", "Movies/Heat/poster.jpg"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Heat/Heat.mkv"},
			want:    []string{"Movies/Heat/Heat.mkv"},
		},
		{
			name:    "folder named without the year, holding only the video",
			files:   []string{"Movies/Heat/Heat.mkv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Heat/Heat.mkv"},
			want:    []string{"Movies/Heat/Heat.mkv", "Movies/Heat"},
		},
		{
			name:    "a sequel's folder",
			files:   []string{"Movies/Iron Man 2 (2010)/Iron Man (2008).mkv", "Movies/Iron Man 2 (2010)/poster.jpg"},
			title:   "Iron Man",
			year:    2008,
			tracked: []string{"Movies/Iron Man 2 (2010)/Iron Man (2008).mkv"},
			want:    []string{"Movies/Iron Man 2 (2010)/Iron Man (2008).mkv"},
		},
		{
			name:    "release-named folder",
			files:   []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv", "Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.nfo", "Movies/Heat.1995.1080p.BluRay-GRP/GRP.sfv"},
			title:   "Heat",
			year:    1995,
			tracked: []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv"},
			want: []string{"Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.mkv", "Movies/Heat.1995.1080p.BluRay-GRP/Heat.1995.1080p.BluRay-GRP.nfo",
				"Movies/Heat.1995.1080p.BluRay-GRP/GRP.sfv", "Movies/Heat.1995.1080p.BluRay-GRP"},
			own: "Movies/Heat.1995.1080p.BluRay-GRP",
		},
		{
			name:    "tracked file missing from disk",
			files:   []string{heat + "/poster.jpg", heat + "/movie.nfo"},
			title:   "Heat",
			year:    1995,
			tracked: []string{heat + "/Heat (1995).mkv"},
			want:    nil,
		},
	}
}

// refusingBin is a recycle bin that takes every file but one name.
type refusingBin struct{ dir, refuse string }

func (b refusingBin) For(path string) (string, error) {
	if filepath.Base(path) == b.refuse {
		return "", errors.New("the bin refused it (test)")
	}
	return b.dir, nil
}

// layoutPreview and layoutCheck are set by delete_layout_check_test.go (absent before
// #59): what the delete dialog lists for the movie, and the case's assertions.
var (
	layoutPreview func(t *testing.T, s *Service, id int64) []string
	layoutCheck   func(t *testing.T, c layoutCase, removed, preview []string, deleteErr error)
	layoutReport  func(t *testing.T, cases []layoutCase, removed map[string][]string)
)

// snapshotTree lists every path under top (relative, slash-separated), links not followed.
func snapshotTree(t *testing.T, top string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != top {
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

// runLayoutCase builds the case's folders, deletes, and returns what went (relative,
// sorted), what the preview listed (nil before #59) and the delete's error.
func runLayoutCase(t *testing.T, c layoutCase) (removed, preview []string, deleteErr error) {
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
	root := c.root
	if root == "" {
		root = "Movies"
	}
	if err := os.MkdirAll(abs(root), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	binDir := filepath.Join(t.TempDir(), "bin")
	recycle := binDir
	if c.bin == "off" {
		recycle = ""
	}
	svc := NewService(st.DB(), nil, nil, abs(root), recycle, eventbus.New(log), log)
	if name, ok := strings.CutPrefix(c.bin, "refuse:"); ok {
		svc.SetBin(refusingBin{dir: binDir, refuse: name})
	}
	ctx := context.Background()
	m, err := svc.repo.Create(ctx, Movie{TMDBID: 949, Title: c.title, Year: c.year, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	var lastVersion int64
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
		lastVersion = v.ID
	}
	if layoutPreview != nil && c.action == "" {
		// The plan names paths as the library spells them; the snapshot sees where they
		// really are. Resolve every link above the last element, as Lstat does.
		realTop, err := filepath.EvalSymlinks(top)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range layoutPreview(t, svc, m.ID) {
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
	before := snapshotTree(t, top)
	switch c.action {
	case "version":
		deleteErr = svc.DeleteVersion(ctx, lastVersion)
	case "file":
		deleteErr = svc.DeleteFile(ctx, m.ID)
	default:
		deleteErr = svc.Delete(ctx, m.ID, true)
	}
	after := snapshotTree(t, top)
	for p := range before {
		if !after[p] {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	return removed, preview, deleteErr
}

// TestDeleteLayoutMatrix deletes a movie in each layout and records exactly what went.
func TestDeleteLayoutMatrix(t *testing.T) {
	cases := layoutCases()
	results := map[string][]string{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			removed, preview, err := runLayoutCase(t, c)
			if removed == nil {
				removed = []string{}
			}
			results[c.name] = removed
			if layoutCheck != nil {
				layoutCheck(t, c, removed, preview, err)
			}
		})
	}
	if out := os.Getenv("ARRMADA_LAYOUT_DUMP"); out != "" {
		b, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if layoutReport != nil {
		layoutReport(t, cases, results)
	}
}
