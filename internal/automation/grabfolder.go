package automation

import (
	"context"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Per-folder downloads (GitHub #11). A Movies or TV library folder can have a downloads
// folder of its own on the same disk, so a grab for a title in it downloads there and its
// import is a hardlink rather than a copy that doubles the space while the torrent seeds.
//
// The grab paths don't carry the title down to the client call, so the title's library
// folder rides on the context: whatever knows the movie or show (a sweep, an upgrade, a
// hand pick) marks it with forMovie/forSeries before it checks free space and grabs, and
// savePath reads it. A grab nothing marked — books, music, anything new — saves to the
// Downloads folder exactly as before.
//
// Torrents only: SABnzbd has no per-job folder, and its categories are how Arrmada tells
// a movie job from a TV one, so usenet jobs keep going to SABnzbd's own folders. A usenet
// download never seeds — its folder is deleted once it's imported — so a copy onto another
// disk costs time once, not the space twice for weeks.

type grabFolderKey struct{}

// grabFolder is the library folder a grab is for: its library and its folder's path (""
// is the library's default folder).
type grabFolder struct{ media, root string }

func withGrabFolder(ctx context.Context, media, root string) context.Context {
	return context.WithValue(ctx, grabFolderKey{}, grabFolder{media: media, root: root})
}

// SetFolderDownloadsFunc wires the lookup of a library folder's own downloads folder
// (libroots.Roots.OwnDownloads: "" when it has none). Read on every grab, so a folder set
// in Settings → Library applies from the next one. Call it at startup.
func (c *Coordinator) SetFolderDownloadsFunc(fn func(ctx context.Context, media, root string) string) {
	c.folderDownloadsFn = fn
}

// forMovie marks ctx as a grab for film m, in the folder its files live in (or the one it
// is assigned to) — the folder the import will put it in, so the two share a disk.
func (c *Coordinator) forMovie(ctx context.Context, m movies.Movie) context.Context {
	if c.movies == nil {
		return ctx
	}
	return withGrabFolder(ctx, libroots.MediaMovie, c.movies.RootFor(m))
}

// forMovieID is forMovie by id; a film that can't be read leaves ctx as it is (the grab
// then saves to the Downloads folder, as every grab did before).
func (c *Coordinator) forMovieID(ctx context.Context, id int64) context.Context {
	if c.movies == nil {
		return ctx
	}
	m, err := c.movies.Get(ctx, id)
	if err != nil {
		return ctx
	}
	return c.forMovie(ctx, m)
}

// forSeries marks ctx as a grab for show s, in the TV folder its episodes import into.
func (c *Coordinator) forSeries(ctx context.Context, s series.Series) context.Context {
	if c.series == nil {
		return ctx
	}
	return withGrabFolder(ctx, libroots.MediaSeries, c.series.RootFor(ctx, s))
}

// forSeriesID is forSeries by id.
func (c *Coordinator) forSeriesID(ctx context.Context, id int64) context.Context {
	if c.series == nil {
		return ctx
	}
	s, err := c.series.Get(ctx, id)
	if err != nil {
		return ctx
	}
	return c.forSeries(ctx, s)
}

// savePath is where a torrent grab made under ctx saves: its library folder's own
// downloads folder when it has one, else the Downloads folder.
func (c *Coordinator) savePath(ctx context.Context) string {
	if p := c.folderDownloads(ctx); p != "" {
		return p
	}
	return c.downloadsPath()
}

// usenetSavePath is the save path a usenet job is sent with: always the Downloads folder,
// whatever library folder the grab is for. SABnzbd files every job by category into its
// own completed folder (one per category, never one per job), and Arrmada's categories
// say movie or TV, not which folder — a category per downloads folder would be a new
// category every import sweep and queue reader had to learn, made in a SABnzbd that may
// be the user's own. Little is lost: a usenet job's folder is deleted once it's imported,
// so there's no seeding copy for a hardlink to save space on.
func (c *Coordinator) usenetSavePath(context.Context) string { return c.downloadsPath() }

// folderDownloads is the own downloads folder of the library folder ctx's grab is for, ""
// when there is none (or the grab isn't marked).
func (c *Coordinator) folderDownloads(ctx context.Context) string {
	g, ok := ctx.Value(grabFolderKey{}).(grabFolder)
	if !ok || c.folderDownloadsFn == nil {
		return ""
	}
	return strings.TrimSpace(c.folderDownloadsFn(ctx, g.media, g.root))
}
