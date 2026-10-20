package embysvc

import (
	"context"
	"log/slog"

	"github.com/tristenlammi/arrmada/internal/emby"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plexscan"
)

// scanClient lets plexscan drive Emby: libraries are the virtual folders, a folder
// scan is a /Library/Media/Updated report, and a whole-library scan refreshes the
// library's item. Several folders go in one report (plexscan.PathBatcher): Emby waits
// until no report has come for its library monitor delay (60 s) before it scans, so one
// report per folder would only keep pushing the scan back.
type scanClient struct{ c *emby.Client }

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
	updates := make([]emby.MediaUpdate, 0, len(dirs))
	for _, d := range dirs {
		updates = append(updates, emby.MediaUpdate{Path: d, UpdateType: emby.UpdateModified})
	}
	return a.c.LibraryMediaUpdated(ctx, updates)
}

func (a scanClient) RefreshSection(ctx context.Context, libraryID string) error {
	return a.c.RefreshItem(ctx, libraryID, false)
}

// NewScanner builds the plexscan.Scanner that tells Emby which folders changed, with
// Emby's own switch and path mappings. Once Emby accepted a report, the index looks
// again after Emby's wait (ScanSettle; LibrarySettle for a whole library), and once
// more FollowUp later in case the scan itself was slow. Run its worker with Run.
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
		Server:     "Emby",
	})
	sc.OnRefreshed(func(_, _, path string) {
		settle := ScanSettle
		if path == "" {
			settle = LibrarySettle
		}
		s.IndexStale(settle)
		s.idx.markFollowUp(settle + FollowUp)
	})
	if s.pathMaps == nil {
		s.SetPathMaps(sc.PathMaps)
	}
	return sc
}
