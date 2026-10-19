package automation

import (
	"context"
	"path/filepath"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
)

// seriesPlacement is where show s's episodes go: the importer (its TV folder fixed to the
// folder the show's folder is in) and the show folder's name ("" = a new folder named by
// the scheme). It refuses — with a *series.ShowFolderError — when that isn't safe:
//
//   - the show has files, but none in a folder of its own (all in a folder named like a
//     season, or one other shows' files are in), so where it lives can't be told;
//   - the folder it would go to is named like a season folder, is a TV folder, or holds
//     another show's files.
//
// Every path that places episode files, and every rescan or rename that walks the show's
// folder, goes through this. Importing into a shared folder merged shows' season folders
// and let their episodes overwrite each other (#56); walking one let a rescan claim
// another show's files.
func (c *Coordinator) seriesPlacement(ctx context.Context, s series.Series) (*library.Importer, string, error) {
	info := c.series.ShowFolderOf(ctx, s.ID)
	if info.Unknown {
		return nil, "", &series.ShowFolderError{Title: s.Title, Why: info.Why}
	}
	imp := c.imp.InTV(c.series.ImportDirOf(ctx, s, info)).Using(ctx)
	folder := ""
	if info.Path != "" {
		folder = filepath.Base(info.Path)
	}
	if why := c.series.UnsafeImportFolder(ctx, s, imp.SeriesFolderPath(folder, s.Title, s.Year)); why != "" {
		return nil, "", &series.ShowFolderError{Title: s.Title, Why: why}
	}
	return imp, folder, nil
}
