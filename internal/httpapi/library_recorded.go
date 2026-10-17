package httpapi

import (
	"context"
	"path/filepath"
	"strings"
)

// recordedFilesUnder counts the files Arrmada has on record inside folder, across every
// library: films and their versions, episodes, ebooks, audiobooks and their versions,
// and music tracks. A missing folder that held files is a share that isn't mounted (an
// Unraid array not started), not a folder that was never made — creating it would put
// the next imports on the container's own disk, under a path the real share later hides.
//
// The comparison is on the path text: the folder doesn't exist, so there's nothing to
// resolve. An error is returned rather than a guess, so callers can refuse (fail closed).
func (a *api) recordedFilesUnder(ctx context.Context, folder string) (int, error) {
	dir := filepath.Clean(folder)
	seen := map[string]bool{}
	add := func(p string) {
		if p = strings.TrimSpace(p); p == "" {
			return
		}
		p = filepath.Clean(p)
		if p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) {
			seen[p] = true
		}
	}
	if a.deps.Movies != nil {
		tracks, films, err := a.deps.Movies.LibraryVersionRows(ctx)
		if err != nil {
			return 0, err
		}
		for _, m := range films {
			if m.HasFile {
				add(m.MovieFilePath)
			}
		}
		for _, vs := range tracks {
			for _, v := range vs {
				if v.HasFile {
					add(v.FilePath)
				}
			}
		}
	}
	if a.deps.Series != nil {
		eps, err := a.deps.Series.LibraryEpisodeFiles(ctx)
		if err != nil {
			return 0, err
		}
		for _, e := range eps {
			add(e.Path)
		}
	}
	if a.deps.Books != nil {
		list, err := a.deps.Books.List(ctx)
		if err != nil {
			return 0, err
		}
		for _, b := range list {
			if b.Ebook != nil {
				add(b.Ebook.Path)
			}
			if b.Audiobook != nil {
				add(b.Audiobook.Path)
			}
			for _, v := range b.AudioVersions {
				if v.File != nil {
					add(v.File.Path)
				}
			}
		}
	}
	if a.deps.Music != nil {
		paths, err := a.deps.Music.FilePaths(ctx)
		if err != nil {
			return 0, err
		}
		for _, p := range paths {
			add(p)
		}
	}
	return len(seen), nil
}
