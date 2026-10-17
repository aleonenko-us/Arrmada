package libroots

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/tristenlammi/arrmada/internal/config"
)

// Settings keys for the folders chosen in the app (first-run setup, Settings → Library).
// Each falls back to the environment's folder (config.*Dir) while unset or blank.
const (
	KeyMovies     = "lib_movies_dir"
	KeyTV         = "lib_tv_dir"
	KeyEbooks     = "lib_ebooks_dir"
	KeyAudiobooks = "lib_audiobooks_dir"
	KeyMusic      = "lib_music_dir"
	KeyDownloads  = "lib_downloads_dir"
	// KeyFoldersConfirmed is set once the Movies or TV folder has been saved or confirmed
	// in the app — including confirming the defaults unchanged, which writes no folder.
	KeyFoldersConfirmed = "lib_folders_confirmed"
)

// Getter reads one setting (settings.Service.Get).
type Getter func(ctx context.Context, key, def string) string

// Roots resolves each library folder and the downloads folder from the saved settings
// every time it's asked, so a folder changed in the app reaches the importer, the
// download client's save path and the disk guard on their next use, with no restart.
//
// There is no cache on purpose: a setting read is one map lookup in memory, folders are
// resolved a handful of times per import or minute, and a cache would only add a window
// in which two parts of the app disagree about where the library is.
type Roots struct {
	get    Getter
	cfg    config.Config
	extras atomic.Pointer[ExtrasFunc]
}

// Extra is one additional Movies or TV folder (internal/libfolders keeps them). The
// folder in Settings → Library stays the default; these sit beside it.
type Extra struct {
	Media string // MediaMovie or MediaSeries
	ID    int64
	Path  string
	Label string
}

// The two libraries that can have more than one folder.
const (
	MediaMovie  = "movie"
	MediaSeries = "series"
)

// ExtrasFunc lists the extra folders as they are now.
type ExtrasFunc func(ctx context.Context) []Extra

// WithExtras makes Libraries, All, MovieFolders and TVFolders include the extra Movies and
// TV folders fn lists, read on every use like the rest. It returns r for chaining. With
// none set (or none added) everything reads exactly as it did before extra folders.
func (r *Roots) WithExtras(fn ExtrasFunc) *Roots {
	if r != nil && fn != nil {
		r.extras.Store(&fn)
	}
	return r
}

// extraList is the extra folders now (nil without a source).
func (r *Roots) extraList(ctx context.Context) []Extra {
	if r == nil {
		return nil
	}
	fn := r.extras.Load()
	if fn == nil || *fn == nil {
		return nil
	}
	return (*fn)(ctx)
}

// New builds the resolver. cfg must be the environment's config as loaded (never one
// already overlaid with saved folders): its folders are the defaults a blank setting
// falls back to.
func New(get Getter, cfg config.Config) *Roots { return &Roots{get: get, cfg: cfg} }

// Root is one named folder ("movies", "downloads", …) and where it resolves to now. An
// extra Movies or TV folder carries its library's Name and its own Label.
type Root struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
}

// resolve is the saved folder, or def when nothing (or only whitespace) is saved.
// settings.Get hands back a stored "" as-is, so the blank check can't be left to it.
func (r *Roots) resolve(ctx context.Context, key, def string) string {
	if r == nil {
		return strings.TrimSpace(def)
	}
	if r.get != nil {
		if v := strings.TrimSpace(r.get(ctx, key, "")); v != "" {
			return v
		}
	}
	return strings.TrimSpace(def)
}

// The resolved folder for each library, and for downloads.
func (r *Roots) Movies(ctx context.Context) string {
	return r.resolve(ctx, KeyMovies, r.env().MoviesDir)
}
func (r *Roots) TV(ctx context.Context) string { return r.resolve(ctx, KeyTV, r.env().TVDir) }
func (r *Roots) Ebooks(ctx context.Context) string {
	return r.resolve(ctx, KeyEbooks, r.env().EbooksDir)
}
func (r *Roots) Audiobooks(ctx context.Context) string {
	return r.resolve(ctx, KeyAudiobooks, r.env().AudiobooksDir)
}
func (r *Roots) Music(ctx context.Context) string { return r.resolve(ctx, KeyMusic, r.env().MusicDir) }
func (r *Roots) Downloads(ctx context.Context) string {
	return r.resolve(ctx, KeyDownloads, r.env().DownloadsDir)
}

// env is the environment's config (zero for a nil resolver).
func (r *Roots) env() config.Config {
	if r == nil {
		return config.Config{}
	}
	return r.cfg
}

// Libraries lists the five library folders (movies, tv, ebooks, audiobooks, music), in
// that order, then any extra Movies and TV folders, skipping any that resolve to nothing.
// Downloads isn't a library.
func (r *Roots) Libraries(ctx context.Context) []Root {
	var out []Root
	for _, x := range []Root{
		{Name: "movies", Path: r.Movies(ctx)},
		{Name: "tv", Path: r.TV(ctx)},
		{Name: "ebooks", Path: r.Ebooks(ctx)},
		{Name: "audiobooks", Path: r.Audiobooks(ctx)},
		{Name: "music", Path: r.Music(ctx)},
	} {
		if x.Path != "" {
			out = append(out, x)
		}
	}
	for _, e := range r.extraList(ctx) {
		if p := strings.TrimSpace(e.Path); p != "" {
			out = append(out, Root{Name: LibraryName(e.Media), Path: p, Label: e.Label})
		}
	}
	return out
}

// LibraryName is the Root name of a library's folders ("movies" or "tv").
func LibraryName(media string) string {
	if media == MediaSeries {
		return "tv"
	}
	return "movies"
}

// MovieFolders is every Movies folder: the default first, then the extras in their order.
// Folders that resolve to nothing are left out.
func (r *Roots) MovieFolders(ctx context.Context) []string {
	return r.folders(ctx, MediaMovie, r.Movies(ctx))
}

// TVFolders is every TV folder: the default first, then the extras in their order.
func (r *Roots) TVFolders(ctx context.Context) []string {
	return r.folders(ctx, MediaSeries, r.TV(ctx))
}

// Extras is the extra Movies and TV folders as they are now (none without a source).
func (r *Roots) Extras(ctx context.Context) []Extra { return r.extraList(ctx) }

func (r *Roots) folders(ctx context.Context, media, def string) []string {
	var out []string
	if def != "" {
		out = append(out, def)
	}
	for _, e := range r.extraList(ctx) {
		if p := strings.TrimSpace(e.Path); e.Media == media && p != "" {
			out = append(out, p)
		}
	}
	return out
}

// All is Libraries plus downloads, last.
func (r *Roots) All(ctx context.Context) []Root {
	out := r.Libraries(ctx)
	if d := r.Downloads(ctx); d != "" {
		out = append(out, Root{Name: "downloads", Path: d})
	}
	return out
}

// Config is the environment's config with every folder replaced by the resolved one,
// for code that judges folders through a config.Config (the health panel's folder list,
// the startup log). Every other field is the environment's, untouched.
func (r *Roots) Config(ctx context.Context) config.Config {
	c := r.env()
	c.MoviesDir = r.Movies(ctx)
	c.TVDir = r.TV(ctx)
	c.EbooksDir = r.Ebooks(ctx)
	c.AudiobooksDir = r.Audiobooks(ctx)
	c.MusicDir = r.Music(ctx)
	c.DownloadsDir = r.Downloads(ctx)
	return c
}

// Func adapts one resolver method to the func() string the importer, the coordinator
// and the disk guard take, reading on a background context: each call is one in-memory
// settings read, and the callers have no request context of their own to hand it.
func Func(f func(ctx context.Context) string) func() string {
	return func() string { return f(context.Background()) }
}
