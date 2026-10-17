package libfolders

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/pathguard"
)

// A library folder's own downloads folder (GitHub #11). Someone whose Movies and TV
// libraries sit on different disks has, with one Downloads folder, one library that
// imports by copying: the file then takes its space twice while the torrent seeds. Each
// Movies or TV folder — the default ones included — can name a downloads folder on its
// own disk; torrent grabs for its titles save there and import as hardlinks. Without one
// the Downloads folder is used, as before.
//
// The default folders keep theirs in settings (libroots.KeyMoviesDownloads /
// KeyTVDownloads), like the default folders themselves; an extra folder in its row
// (library_folders.downloads_path, migration 0329).

// SettingWriter saves one setting (settings.Service.Set).
type SettingWriter func(ctx context.Context, key, value string) error

// SetSettingWriter lets the service save the default folders' downloads folders. Without
// it only extra folders can have one. Call it at startup.
func (s *Service) SetSettingWriter(fn SettingWriter) { s.set = fn }

// defaultDownloads is a library's default folder's own downloads folder ("" = none).
func (s *Service) defaultDownloads(ctx context.Context, media string) string {
	if s == nil || s.roots == nil {
		return ""
	}
	if media == MediaSeries {
		return s.roots.TVDownloads(ctx)
	}
	return s.roots.MoviesDownloads(ctx)
}

// ownDownloads is every library folder's own downloads folder, named for messages.
func (s *Service) ownDownloads(ctx context.Context) []namedPath {
	var out []namedPath
	for _, media := range []string{MediaMovie, MediaSeries} {
		if p := s.defaultDownloads(ctx, media); p != "" {
			out = append(out, namedPath{"the downloads folder of " + s.labelOf(Folder{Media: media, Default: true}), p})
		}
	}
	for _, f := range s.load(ctx) {
		if p := strings.TrimSpace(f.Downloads); p != "" {
			out = append(out, namedPath{"the downloads folder of " + s.labelOf(f), p})
		}
	}
	return out
}

// DownloadsResult is a saved downloads folder and, when it isn't on the same filesystem
// as its library folder, why that matters. The warning never blocks the save: a shared
// mount can hide two disks behind one filesystem id and the reverse, so the person who
// knows their disks decides.
type DownloadsResult struct {
	Folder  Folder `json:"folder"`
	Warning string `json:"warning,omitempty"`
}

// SetDownloads gives folder id of library media (0 is the default folder) a downloads
// folder of its own, or takes it away with "". A folder must be a full path that already
// exists (it's never created: a typo would fill the container's own disk), the app must
// be able to write to it, and it must keep clear of Arrmada's data folder and of every
// library folder — a download inside a library would be scanned as a library file, and a
// library inside a downloads folder would be deleted with a finished torrent.
//
// It may be the Downloads folder, sit inside it, or be shared by several library folders
// on the same disk: those only decide where torrents save.
func (s *Service) SetDownloads(ctx context.Context, media string, id int64, path string) (DownloadsResult, error) {
	if !ValidMedia(media) {
		return DownloadsResult{}, ErrBadMedia
	}
	path = strings.TrimSpace(path)
	if path != "" {
		path = filepath.Clean(path)
	}
	lib, ok := s.Get(ctx, media, id)
	if !ok {
		return DownloadsResult{}, ErrNotFound
	}
	if path != "" {
		if err := s.checkDownloads(ctx, path); err != nil {
			return DownloadsResult{}, err
		}
	}
	if id == DefaultID {
		if s.set == nil {
			return DownloadsResult{}, errors.New("the default folder's downloads folder can't be saved here")
		}
		key := libroots.KeyMoviesDownloads
		if media == MediaSeries {
			key = libroots.KeyTVDownloads
		}
		if err := s.set(ctx, key, path); err != nil {
			return DownloadsResult{}, err
		}
	} else {
		res, err := s.db.ExecContext(ctx, `UPDATE library_folders SET downloads_path = ? WHERE id = ? AND media = ?`, path, id, media)
		if err != nil {
			return DownloadsResult{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return DownloadsResult{}, ErrNotFound
		}
		if err := s.reload(ctx); err != nil {
			return DownloadsResult{}, err
		}
	}
	out := DownloadsResult{Warning: DownloadsWarning(path, lib.Path)}
	out.Folder, _ = s.Get(ctx, media, id)
	return out, nil
}

// checkDownloads vets a downloads folder for a library folder.
func (s *Service) checkDownloads(ctx context.Context, p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%w: use the full path, starting with / (for example /storage/disk2/downloads)", ErrBadPath)
	}
	if libroots.UnderDataDir(p, s.dataDir) {
		return fmt.Errorf("%w: %s is Arrmada's own data folder (or inside or above it) — downloads must live on their own mount", ErrBadPath, p)
	}
	// The overlap first: a folder inside a library is wrong whether or not it exists yet.
	for _, o := range s.libraryFolders(ctx) {
		if pathguard.Under(p, o.path) || pathguard.Under(o.path, p) {
			return fmt.Errorf("%w: %s overlaps the %s library folder (%s) — a downloads folder can't sit inside or around a library folder", ErrBadPath, p, o.name, o.path)
		}
	}
	fi, err := os.Stat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %s doesn't exist inside the container — create it, and make sure it's inside a folder mounted into the container (and into the download client's, at the same path)", ErrBadPath, p)
	case err != nil:
		return fmt.Errorf("%w: Arrmada can't open %s (%v)", ErrBadPath, p, err)
	case !fi.IsDir():
		return fmt.Errorf("%w: %s is a file, not a folder", ErrBadPath, p)
	}
	if err := libroots.ProbeWritable(p); err != nil {
		return fmt.Errorf("%w: Arrmada can't write to %s (check PUID/PGID and the share's permissions)", ErrBadPath, p)
	}
	return nil
}

// libraryFolders is every library folder (each library's default and the extras), for
// the downloads folder's overlap check.
func (s *Service) libraryFolders(ctx context.Context) []namedPath {
	var out []namedPath
	if s.roots != nil {
		for _, r := range []namedPath{
			{"Movies", s.roots.Movies(ctx)}, {"TV", s.roots.TV(ctx)}, {"Ebooks", s.roots.Ebooks(ctx)},
			{"Audiobooks", s.roots.Audiobooks(ctx)}, {"Music", s.roots.Music(ctx)},
		} {
			if strings.TrimSpace(r.path) != "" {
				out = append(out, r)
			}
		}
	}
	for _, f := range s.load(ctx) {
		out = append(out, namedPath{s.labelOf(f), f.Path})
	}
	return out
}

// sameDevice compares two folders' filesystem ids (a test swaps it: a temp dir can't be
// put on a second filesystem on demand).
var sameDevice = diskspace.SameDevice

// DownloadsWarning says why a downloads folder on another filesystem from its library
// folder defeats its purpose, or "" when they share one (or it can't be told). The
// filesystem ids are compared: two folders on one are the only ones a hardlink can join.
func DownloadsWarning(downloads, library string) string {
	if strings.TrimSpace(downloads) == "" || strings.TrimSpace(library) == "" {
		return ""
	}
	if same, ok := sameDevice(downloads, library); ok && !same {
		return fmt.Sprintf("%s isn't on the same filesystem as its library folder %s, so imports from it are copied, not hardlinked — pick a downloads folder on the library's disk", downloads, library)
	}
	return ""
}
