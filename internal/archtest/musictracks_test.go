package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tracksSQL matches a statement that reads or writes the old music tracks table, frozen by
// migration 0339 (MUS-28/29). sqlVerb keeps prose ("imported 3 tracks from ...") out.
var tracksSQL = regexp.MustCompile(`(?i)\b(FROM|INTO|JOIN|UPDATE|TABLE)\s+["` + "`" + `\[]?tracks\b`)

// TestOldTracksTableFrozen: the music model moved off the old tracks table (MUS-28 copied
// it into editions, release tracks and track files; MUS-29 cut every reader and writer
// over). It stays as the migration's source and an audit trail, so nothing may read or
// write it any more, tests included: a write there would be lost silently, and a read
// would show the library as it was on the day of the upgrade. Migrations are .sql files;
// internal/store's own tests build pre-migration databases and are the one exception.
func TestOldTracksTableFrozen(t *testing.T) {
	root := repoRoot(t)
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
			if !strings.HasSuffix(p, ".go") || strings.HasPrefix(rel, "internal/store/") || strings.HasPrefix(rel, "internal/archtest/") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if perr != nil {
				t.Errorf("parse %s: %v", rel, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !tracksSQL.MatchString(lit.Value) || !sqlVerb.MatchString(lit.Value) {
					return true
				}
				t.Errorf("%s:%d: SQL on the old music tracks table — it is frozen since MUS-28; use the music model (music_release_tracks, music_track_files) through internal/music",
					rel, fset.Position(lit.Pos()).Line)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The rule must catch the shapes the old code used, and leave prose and the new tables alone.
func TestOldTracksTableFrozenPattern(t *testing.T) {
	for q, want := range map[string]bool{
		"SELECT file_path FROM tracks WHERE has_file = 1":              true,
		"UPDATE tracks SET monitored = ? WHERE album_id = ?":           true,
		"INSERT INTO tracks (album_id, title) VALUES (?, ?)":           true,
		"SELECT * FROM albums al LEFT JOIN tracks t ON t.album_id = 1": true,
		"DELETE FROM tracks WHERE id = 1":                              true,
		"SELECT id FROM music_release_tracks WHERE medium_id = ?":      false,
		"UPDATE music_release_tracks SET monitored = ?":                false,
		"Imported 3 tracks from the download":                          false,
	} {
		if got := tracksSQL.MatchString(q) && sqlVerb.MatchString(q); got != want {
			t.Errorf("%q: flagged=%v, want %v", q, got, want)
		}
	}
}
