package jellyfinsvc

import (
	"context"
	"log/slog"

	"github.com/tristenlammi/arrmada/internal/jellyfin"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

// scanClient lets plexscan drive Jellyfin: libraries are the virtual folders, a folder
// scan is a /Library/Media/Updated report, and a whole-library scan refreshes the
// library's item. Several folders go in one report (plexscan.PathBatcher): Jellyfin waits
// until no report has come for its library monitor delay (60 s) before it scans, so one
// report per folder would only keep pushing the scan back.
type scanClient struct{ c *jellyfin.Client }

var (
	_ plexscan.Client      = scanClient{}
	_ plexscan.PathBatcher = scanClient{}
)

func (a scanClient) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	vfs, err := a.c.VirtualFolders(ctx)
	if err != nil {
		return nil, err
	}
	return libraries(vfs), nil
}

func (a scanClient) RefreshPath(ctx context.Context, _, dir string) error {
	return a.RefreshPaths(ctx, []string{dir})
}

func (a scanClient) RefreshPaths(ctx context.Context, dirs []string) error {
	updates := make([]jellyfin.MediaUpdate, 0, len(dirs))
	for _, d := range dirs {
		updates = append(updates, jellyfin.MediaUpdate{Path: d, UpdateType: jellyfin.UpdateModified})
	}
	return a.c.LibraryMediaUpdated(ctx, updates)
}

func (a scanClient) RefreshSection(ctx context.Context, libraryID string) error {
	return a.c.RefreshItem(ctx, libraryID, false)
}

// NewScanner builds the plexscan.Scanner that tells Jellyfin which folders changed, with
// Jellyfin's own switch and path mappings. Once Jellyfin accepted a report, the index looks
// again after Jellyfin's wait (ScanSettle; LibrarySettle for a whole library). Run its
// worker with Run.
func (s *Service) NewScanner(settings plexscan.Settings, roots func(ctx context.Context) map[string]string, log *slog.Logger) *plexscan.Scanner {
	sc := plexscan.New(plexscan.Options{
		Client: func(ctx context.Context) plexscan.Client {
			if !s.Configured(ctx) {
				return nil
			}
			return scanClient{s.Client(ctx)}
		},
		Configured: s.Configured,
		Settings:   settings,
		Roots:      roots,
		Log:        log,
		Keys:       ScanKeys,
		Server:     "Jellyfin",
	})
	sc.OnRefreshed(func(_, _, path string) {
		if path == "" {
			s.IndexStale(LibrarySettle)
			return
		}
		s.IndexStale(ScanSettle)
	})
	if s.pathMaps == nil {
		s.SetPathMaps(sc.PathMaps)
	}
	return sc
}
