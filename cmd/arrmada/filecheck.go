package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tristenlammi/arrmada/internal/filecheck"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// fileCheckLibrary lists the library's video files for the file check, with what it
// judges them by: the release each came from, the runtime, the original language, a
// movie's home release and whether its profile accepts cams.
type fileCheckLibrary struct {
	movies  *movies.Service
	series  *series.Service
	quality *quality.Service
}

func (l fileCheckLibrary) Files(ctx context.Context) ([]filecheck.File, error) {
	versions, all, err := l.movies.LibraryVersionRows(ctx)
	if err != nil {
		return nil, err
	}
	cam := l.camOK()
	var out []filecheck.File
	for _, m := range all {
		out = append(out, l.movieFiles(ctx, m, versions[m.ID], cam)...)
	}
	eps, err := l.series.LibraryEpisodeFiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range eps {
		out = append(out, episodeFile(ctx, e, cam))
	}
	return out, nil
}

func (l fileCheckLibrary) MovieFiles(ctx context.Context, id int64) ([]filecheck.File, error) {
	m, err := l.movies.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	vs, err := l.movies.VersionRows(ctx, id)
	if err != nil {
		return nil, err
	}
	return l.movieFiles(ctx, m, vs, l.camOK()), nil
}

func (l fileCheckLibrary) SeriesFiles(ctx context.Context, id int64) ([]filecheck.File, error) {
	eps, err := l.series.LibraryEpisodeFiles(ctx)
	if err != nil {
		return nil, err
	}
	cam := l.camOK()
	var out []filecheck.File
	for _, e := range eps {
		if e.SeriesID == id {
			out = append(out, episodeFile(ctx, e, cam))
		}
	}
	return out, nil
}

// camOKFunc says whether a title's profile accepts cams (its AllowPreRelease), each
// profile resolved once per listing.
type camOKFunc func(ctx context.Context, ref, media string) bool

func (l fileCheckLibrary) camOK() camOKFunc {
	seen := map[string]bool{}
	return func(ctx context.Context, ref, media string) bool {
		k := media + "\x00" + ref
		if v, ok := seen[k]; ok {
			return v
		}
		p, _ := l.quality.Resolve(ctx, l.quality.Effective(ctx, ref, media))
		seen[k] = !p.RejectPreRelease
		return seen[k]
	}
}

func (l fileCheckLibrary) movieFiles(ctx context.Context, m movies.Movie, vs []movies.Version, cam camOKFunc) []filecheck.File {
	title := m.DisplayTitle
	if title == "" {
		title = m.Title
	}
	if m.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, m.Year)
	}
	var home time.Time
	lang := ""
	if m.Extra != nil {
		if m.Extra.DatesRead {
			home = movies.ReleaseDay(m.Extra.HomeReleaseDate)
		}
		lang = m.Extra.OriginalLanguage
	}
	var out []filecheck.File
	for _, v := range vs {
		if !v.HasFile || v.FilePath == "" {
			continue
		}
		out = append(out, filecheck.File{Kind: filecheck.KindMovie, MediaID: m.ID, VersionID: v.ID, OwnerID: m.ID,
			Title: title, Path: v.FilePath, Release: v.SourceRelease, RuntimeMin: m.Runtime, OrigLanguage: lang,
			Home: home, CamOK: cam(ctx, v.QualityProfile, quality.MediaMovie)})
	}
	return out
}

func episodeFile(ctx context.Context, e series.LibraryEpisodeFile, cam camOKFunc) filecheck.File {
	return filecheck.File{Kind: filecheck.KindEpisode, MediaID: e.EpisodeID, OwnerID: e.SeriesID, Season: e.Season, Episode: e.Episode,
		Title: fmt.Sprintf("%s S%02dE%02d", e.SeriesTitle, e.Season, e.Episode), Path: e.Path, Release: e.SourceRelease,
		RuntimeMin: e.RuntimeMin, OrigLanguage: e.OrigLanguage, CamOK: cam(ctx, e.SeriesProfile, quality.MediaSeries)}
}
