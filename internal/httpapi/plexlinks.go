package httpapi

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/insights"
	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/requests"
)

// Watch links: a title's page on each media server that has it — app.plex.tv for Plex,
// the Jellyfin web app at its public address — found from memory (each server's library
// index), never by asking a server. A person sees their own server's link first. Links
// carry no credential and no internal address: Plex's open app.plex.tv, which asks them
// to sign in to Plex; Jellyfin's are built on the public address only.

// PlexLinker finds titles in the owner's Plex server from memory (insights' library
// index) and builds app.plex.tv links to them. *insights.Service is one.
type PlexLinker interface {
	WatchURL(ctx context.Context, media string, ids insights.ExternalIDs) string
	// PlexIndexReady is false while no lookup can succeed (Plex not set up, the index
	// not built yet), so callers skip reading library records for fallback ids.
	PlexIndexReady(ctx context.Context) bool
}

func (a *api) plexReady(ctx context.Context) bool {
	return a.deps.PlexLinks != nil && a.deps.PlexLinks.PlexIndexReady(ctx)
}

// linkSource is one server Watch links can come from right now.
type linkSource struct {
	kind mediaserver.Kind
	url  func(ctx context.Context, media string, ids insights.ExternalIDs) string
}

// linkSources is the servers whose index can answer now, in order (Plex, then Jellyfin):
// the media servers when they're wired, else the Plex linker alone. None means no title
// can have a link, and callers skip reading library records.
func (a *api) linkSources(ctx context.Context) []linkSource {
	if a.deps.MediaServers != nil {
		var out []linkSource
		for _, s := range a.deps.MediaServers.ConfiguredServers(ctx) {
			if s.IndexReady(ctx) {
				out = append(out, linkSource{kind: s.Kind(), url: s.WatchURL})
			}
		}
		return out
	}
	if a.plexReady(ctx) {
		return []linkSource{{kind: mediaserver.Plex, url: a.deps.PlexLinks.WatchURL}}
	}
	return nil
}

// lookupLinks asks each source for the title by ids alone: a URL per source, "" where it
// has none.
func lookupLinks(ctx context.Context, srcs []linkSource, media string, ids insights.ExternalIDs) []string {
	urls := make([]string, len(srcs))
	if (media != "movie" && media != "series") || (ids.TMDB <= 0 && ids.TVDB <= 0 && ids.IMDB == "" && ids.Folder == "") {
		return urls
	}
	for i, s := range srcs {
		urls[i] = s.url(ctx, media, ids)
	}
	return urls
}

// retryMissed asks again, with more ids, the sources that had nothing.
func retryMissed(ctx context.Context, srcs []linkSource, media string, more insights.ExternalIDs, urls []string) {
	for i := range srcs {
		if urls[i] == "" {
			urls[i] = lookupLinks(ctx, srcs[i:i+1], media, more)[0]
		}
	}
}

func anyMissed(urls []string) bool {
	for _, u := range urls {
		if u == "" {
			return true
		}
	}
	return false
}

// collectLinks is the links found, in the sources' order.
func collectLinks(srcs []linkSource, urls []string) []mediaserver.WatchLink {
	var out []mediaserver.WatchLink
	for i, s := range srcs {
		if urls[i] != "" {
			out = append(out, mediaserver.WatchLink{Server: s.kind, Label: s.kind.Label(), URL: urls[i]})
		}
	}
	return out
}

// linkFor is server k's URL among links ("" when it has none).
func linkFor(links []mediaserver.WatchLink, k mediaserver.Kind) string {
	for _, l := range links {
		if l.Server == k {
			return l.URL
		}
	}
	return ""
}

// watchLinks is a title's Watch links (Plex, then Jellyfin), none when no server has it.
// The ids given (TMDB, a detail's IMDb id) find most titles; a server that missed is asked
// again with what the library record knows: the TVDB and IMDb ids legacy-agent Plex
// libraries match on, and the folder Jellyfin finds an unmatched movie by.
func (a *api) watchLinks(ctx context.Context, media string, ids insights.ExternalIDs) []mediaserver.WatchLink {
	srcs := a.linkSources(ctx)
	if len(srcs) == 0 {
		return nil
	}
	urls := lookupLinks(ctx, srcs, media, ids)
	if anyMissed(urls) {
		more := a.plexIDs(ctx, media, ids.TMDB)
		if (more.TVDB != 0 && more.TVDB != ids.TVDB) || (more.IMDB != "" && more.IMDB != ids.IMDB) || more.Folder != "" {
			if more.IMDB == "" {
				more.IMDB = ids.IMDB
			}
			if more.TVDB == 0 {
				more.TVDB = ids.TVDB
			}
			retryMissed(ctx, srcs, media, more, urls)
		}
	}
	return collectLinks(srcs, urls)
}

// homeServer is the server viewer watches on (Plex for nobody in particular).
func (a *api) homeServer(ctx context.Context, viewer *auth.User) mediaserver.Kind {
	if viewer == nil || a.deps.HomeServer == nil {
		return mediaserver.Plex
	}
	return a.deps.HomeServer(ctx, viewer.ID)
}

// leadWith puts viewer's own server's link first.
func (a *api) leadWith(ctx context.Context, links []mediaserver.WatchLink, viewer *auth.User) []mediaserver.WatchLink {
	if len(links) < 2 {
		return links
	}
	return mediaserver.LeadWith(links, a.homeServer(ctx, viewer))
}

// plexURL is the Watch on Plex link for a title, "" when there's none (Plex not set up,
// the title not in Plex yet, or no linker wired).
func (a *api) plexURL(ctx context.Context, media string, tmdbID int) string {
	return linkFor(a.watchLinks(ctx, media, insights.ExternalIDs{TMDB: tmdbID}), mediaserver.Plex)
}

// setRequestWatchLinks gives each delivered movie or show request its Watch links (the
// viewer's server first) and, for older pages, its Plex link. Run after Track, which sets
// the stage. Titles the TMDB id doesn't find everywhere are looked up again with their
// library ids and (movies) folder, read in one query per kind.
func (a *api) setRequestWatchLinks(ctx context.Context, list []requests.Request, viewer *auth.User) {
	srcs := a.linkSources(ctx)
	if len(srcs) == 0 {
		return
	}
	found := make([][]string, len(list))
	var missed []int
	missMovies, missSeries := []int{}, []int{}
	for i := range list {
		rq := &list[i]
		if rq.Tracking == nil || (rq.Tracking.Stage != requests.StageAvailable && rq.Tracking.Stage != requests.StagePartial) {
			continue
		}
		if found[i] = lookupLinks(ctx, srcs, rq.MediaType, insights.ExternalIDs{TMDB: rq.TMDBID}); !anyMissed(found[i]) {
			continue
		}
		switch rq.MediaType {
		case "movie":
			missMovies = append(missMovies, rq.TMDBID)
		case "series":
			missSeries = append(missSeries, rq.TMDBID)
		default:
			continue
		}
		missed = append(missed, i)
	}
	if len(missed) > 0 {
		ids := map[string]insights.ExternalIDs{}
		if len(missMovies) > 0 && a.deps.Movies != nil {
			if ms, err := a.deps.Movies.ByTMDBIDs(ctx, missMovies); err == nil {
				for _, m := range ms {
					ids["movie:"+strconv.Itoa(m.TMDBID)] = insights.ExternalIDs{TMDB: m.TMDBID, IMDB: m.IMDBID, Folder: movieFolder(m.MovieFilePath)}
				}
			}
		}
		if len(missSeries) > 0 && a.deps.Series != nil {
			// No folder here: it's one query per show, and Jellyfin matches shows by id.
			if ss, err := a.deps.Series.ByTMDBIDs(ctx, missSeries); err == nil {
				for _, s := range ss {
					ids["series:"+strconv.Itoa(s.TMDBID)] = insights.ExternalIDs{TMDB: s.TMDBID, TVDB: s.TVDBID, IMDB: s.IMDBID}
				}
			}
		}
		for _, i := range missed {
			rq := &list[i]
			if x, ok := ids[rq.MediaType+":"+strconv.Itoa(rq.TMDBID)]; ok && (x.TVDB > 0 || x.IMDB != "" || x.Folder != "") {
				retryMissed(ctx, srcs, rq.MediaType, x, found[i])
			}
		}
	}
	for i := range list {
		if found[i] == nil {
			continue
		}
		links := collectLinks(srcs, found[i])
		list[i].PlexURL = linkFor(links, mediaserver.Plex)
		list[i].WatchLinks = a.leadWith(ctx, links, viewer)
	}
}

// movieFolder is a movie's folder from its file's path ("" without a file).
func movieFolder(file string) string {
	if file == "" {
		return ""
	}
	return filepath.Dir(file)
}

// plexIDs is a title's outside ids and folder from its library record (just the TMDB id
// when it isn't in the library).
func (a *api) plexIDs(ctx context.Context, media string, tmdbID int) insights.ExternalIDs {
	ids := insights.ExternalIDs{TMDB: tmdbID}
	if tmdbID <= 0 {
		return ids
	}
	switch media {
	case "movie":
		if a.deps.Movies != nil {
			if m, err := a.deps.Movies.GetByTMDB(ctx, tmdbID); err == nil {
				ids.IMDB, ids.Folder = m.IMDBID, movieFolder(m.MovieFilePath)
			}
		}
	case "series":
		if a.deps.Series != nil {
			if s, err := a.deps.Series.GetByTMDB(ctx, tmdbID); err == nil {
				ids.TVDB, ids.IMDB = s.TVDBID, s.IMDBID
				ids.Folder = a.deps.Series.FolderPath(ctx, s.ID)
			}
		}
	}
	return ids
}

// handlePlexLink answers GET /plex/link?media_type=movie|series&tmdb_id=N with {url}, or
// 204 when the title isn't in Plex (or Plex isn't set up). Kept for older pages: GET
// /media/links answers every server. Any signed-in person may ask: the link opens Plex's
// own page, which asks them to sign in to Plex. It never carries the token or the
// server's address.
func (a *api) handlePlexLink(w http.ResponseWriter, r *http.Request) {
	media := r.URL.Query().Get("media_type")
	tmdbID, err := strconv.Atoi(r.URL.Query().Get("tmdb_id"))
	if (media != "movie" && media != "series") || err != nil || tmdbID <= 0 {
		a.writeError(w, http.StatusBadRequest, "media_type (movie or series) and tmdb_id are required")
		return
	}
	u := a.plexURL(r.Context(), media, tmdbID)
	if u == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// handleMediaLinks answers GET /media/links?media=movie|series&tmdb=N with {links:[{server,
// label, url}]}: a Watch link per media server that has the title, the caller's own server
// first (none: an empty list). Any signed-in person may ask, as for /plex/link: each link
// opens the server's own app, which asks them to sign in there; none carries a key or an
// internal address.
func (a *api) handleMediaLinks(w http.ResponseWriter, r *http.Request) {
	media := r.URL.Query().Get("media")
	tmdbID, err := strconv.Atoi(r.URL.Query().Get("tmdb"))
	if (media != "movie" && media != "series") || err != nil || tmdbID <= 0 {
		a.writeError(w, http.StatusBadRequest, "media (movie or series) and tmdb are required")
		return
	}
	u, _ := userFrom(r)
	links := a.leadWith(r.Context(), a.watchLinks(r.Context(), media, insights.ExternalIDs{TMDB: tmdbID}), u)
	if links == nil {
		links = []mediaserver.WatchLink{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"links": links})
}
