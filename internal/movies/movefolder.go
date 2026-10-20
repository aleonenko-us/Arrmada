package movies

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
)

// MoveFolderCheck answers "Move to folder" (libfolders.Mover.OwnFolder): it returns ""
// when dir, a folder holding movie id's recorded files, is provably the movie's own, so
// the whole folder may move to another Movies folder, and otherwise why not.
//
// The rules are the movie delete's (deletefolder.go), applied to everything in the
// folder, because a move carries everything in it:
//   - it is a real folder, not a link and not a library or downloads folder (prunable);
//   - no other film has a file recorded in or under it, on disk or not (recordedInto);
//   - no link elsewhere points into it, which the move would leave dangling (aliasInto);
//   - it is named for the movie, inside a Movies folder, reached without links (ownFolder);
//   - everything in it besides the movie's own files and their subtitles is a known
//     sidecar named generically or for this movie, with no other video at any depth,
//     hidden folders included (leftoverSidecars).
//
// A folder that fails any rule doesn't move at all: Arrmada doesn't carve a film's files
// out of a shared folder (see libfolders.Mover.plan for why it refuses instead).
func (s *Service) MoveFolderCheck(ctx context.Context, id int64, dir string) string {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return "the film couldn't be read"
	}
	extras, err := s.repo.ListVersions(ctx, id)
	if err != nil {
		return "the film's versions couldn't be read"
	}
	files, _ := trackedFiles(m, extras)
	others, err := s.repo.OtherRecordedPaths(ctx, id)
	if err != nil {
		return "the library's other films couldn't be checked"
	}
	dir = filepath.Clean(dir)
	if !s.prunable(dir) {
		fi, err := os.Lstat(dir)
		switch {
		case err == nil && fi.Mode()&fs.ModeSymlink != 0:
			return "it's a link to another folder"
		case err == nil && fi.IsDir():
			return "it's a library or downloads folder"
		default:
			return "it couldn't be read"
		}
	}
	probe := newLinkProbe(others)
	if o := probe.recordedInto(dir); o != "" {
		rel, err := filepath.Rel(dir, o)
		if err != nil {
			rel = filepath.Base(o)
		}
		return "another film in your library has a file recorded in it (" + filepath.ToSlash(rel) + ")"
	}
	if link := s.aliasInto(probe, dir); link != "" {
		return "another folder links into it (" + filepath.Base(link) + ")"
	}
	if reason := s.ownFolder(m, dir); reason != "" {
		return reason
	}
	tracked, planned := map[string]bool{}, map[string]bool{}
	var bases []string
	for _, f := range files {
		c := filepath.Clean(f.path)
		tracked[c], planned[c] = true, true
		name := filepath.Base(f.path)
		bases = append(bases, strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))))
	}
	for _, f := range files {
		for _, sub := range library.Sidecars(f.path) {
			if !pairsWithOther(sub, f.path, others) {
				planned[filepath.Clean(sub)] = true
			}
		}
	}
	if _, _, reason := leftoverSidecars(dir, tracked, planned, leftoverNamedFor(m, bases)); reason != "" {
		return reason
	}
	return ""
}
