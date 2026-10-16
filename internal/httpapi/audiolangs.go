package httpapi

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The audio-language badges on the movie and series detail pages (issue #12): each file's
// audio languages from the library index Convert keeps for every library file — never a
// probe on a page load — and how they meet the profile's Audio language rules. Only the
// detail endpoints carry them; the list endpoints are unchanged.

// langProfiles resolves the profile a file runs under, once per ref per request.
type langProfiles struct {
	q     *quality.Service
	media string
	cache map[string]*quality.StoredProfile
}

func newLangProfiles(q *quality.Service, media string) *langProfiles {
	return &langProfiles{q: q, media: media, cache: map[string]*quality.StoredProfile{}}
}

// fit is the file's languages against its profile's rules, nil without rules.
func (p *langProfiles) fit(ctx context.Context, ref string, cur quality.CurrentFile) *langs.AudioFit {
	if p == nil || p.q == nil {
		return nil
	}
	sp, ok := p.cache[ref]
	if !ok {
		if got, err := p.q.GetStored(ctx, p.q.Effective(ctx, ref, p.media)); err == nil {
			sp = &got
		}
		p.cache[ref] = sp
	}
	if sp == nil {
		return nil
	}
	return sp.LanguageFit(cur)
}

// sameFile reports whether the index row is still the file the title records: a size the
// title knows that differs from the analysed one is a file replaced since.
func sameFile(fa convert.FileAudio, sizeBytes int64) bool {
	return sizeBytes <= 0 || fa.SizeBytes <= 0 || fa.SizeBytes == sizeBytes
}

// attachMovieAudio badges each of the movie's tracks with its file's audio languages.
func (a *api) attachMovieAudio(ctx context.Context, m *movies.Movie) {
	if a.deps.Convert == nil || len(m.Versions) == 0 {
		return
	}
	files, err := a.deps.Convert.AudioFor(ctx, "movie", m.ID)
	if err != nil || len(files) == 0 {
		return
	}
	orig := movieOrigLang(*m)
	profiles := newLangProfiles(a.deps.Quality, quality.MediaMovie)
	for i := range m.Versions {
		v := &m.Versions[i]
		fa, ok := files[v.FilePath]
		size := v.SizeBytes
		if v.File != nil && v.File.SizeBytes > 0 {
			size = v.File.SizeBytes
		}
		if !v.HasFile || v.FilePath == "" || !ok || !sameFile(fa, size) {
			continue
		}
		v.AudioLangs = langs.OrderAudio(fa.Badges, orig)
		facts := fa.Facts
		v.AudioLangFit = profiles.fit(ctx, v.QualityProfile, quality.CurrentFile{
			Release: automation.UpgradeBaseline(*m, *v), Facts: &facts, OrigLanguage: orig,
		})
	}
}

// attachSeriesAudio badges each episode file of the show with its audio languages.
func (a *api) attachSeriesAudio(ctx context.Context, s *series.Series) {
	if a.deps.Convert == nil {
		return
	}
	files, err := a.deps.Convert.AudioFor(ctx, "episode", s.ID)
	if err != nil || len(files) == 0 {
		return
	}
	orig := ""
	if s.Extra != nil {
		orig = s.Extra.OriginalLanguage
	}
	profiles := newLangProfiles(a.deps.Quality, quality.MediaSeries)
	for si := range s.Seasons {
		for ei := range s.Seasons[si].Episodes {
			e := &s.Seasons[si].Episodes[ei]
			fa, ok := files[e.FilePath]
			if !e.HasFile || e.FilePath == "" || !ok || !sameFile(fa, e.SizeBytes) {
				continue
			}
			e.AudioLangs = langs.OrderAudio(fa.Badges, orig)
			facts := fa.Facts
			e.AudioLangFit = profiles.fit(ctx, s.QualityProfile, quality.CurrentFile{
				Release: e.SourceRelease, Facts: &facts, OrigLanguage: orig,
			})
		}
	}
}
