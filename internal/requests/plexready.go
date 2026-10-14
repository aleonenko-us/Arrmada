package requests

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
)

// 'Ready' means watchable on a media server. With Plex or Jellyfin set up (or both), a
// movie or series request whose files are complete isn't announced at once: the server
// still has to scan them in, and a family member opening it straight away would find
// nothing. The request is stamped on_disk_at instead, reads 'Adding to Plex…' ('…to
// Jellyfin…', or '…to your library…' with both), and its notice goes out as soon as
// either server's library index shows the title — "ready to watch on Plex", with a Watch
// link per server that has it — or, if none shows it, after plexGrace with wording that
// says it may take a few more minutes. Books are never on a media server and never wait.
//
// Everything that decides 'ready' (the import's outbox row, the ready sweep, and the
// server check that runs every two minutes and after each index rebuild) goes through the
// same gate, notifyReady. The wait lives in the request row, so a restart neither loses it
// nor restarts the clock, and the inbox's unique reference still tells each person once.

const (
	// plexGrace is how long a complete request waits for a server before it is announced
	// anyway. It fires from the server check whether or not an index ever builds.
	plexGrace = 30 * time.Minute
	// plexSettle: an index can only say a show is on the server, not which episodes, so
	// for a show the server already had (an earlier season) it counts as there only in an
	// index read at least this long after the files landed — by then Arrmada's partial
	// scan has gone out and the index has been read again (~90 s after the scan).
	plexSettle = 2 * time.Minute
	// plexRelook is how often a waiting request asks for the indexes to be read again
	// when the last read didn't have it (the server's own watcher, a slow scan).
	plexRelook = 5 * time.Minute
)

// PlexIDs are the outside ids a title is looked up by on the media servers. Zero / "" =
// unknown.
type PlexIDs struct {
	TMDB int
	TVDB int
	IMDB string
	// Folder is the title's library folder as Arrmada sees it: Jellyfin finds a title it
	// matched no id for (each file of a multi-version movie folder) by it. Plex ignores it.
	Folder string
}

// Sighting is one media server that has a title: its Watch link, and when that server's
// index was read (a show only counts in an index read after its files landed).
type Sighting struct {
	Link         mediaserver.WatchLink
	IndexBuiltAt time.Time
}

// PlexLocator says whether the media servers have a title, from their library indexes in
// memory (main adapts the Registry: Plex, then Jellyfin). nil-safe: without one, 'ready'
// goes out on import.
type PlexLocator interface {
	// Configured reports whether any media server is set up — whether 'ready' waits.
	Configured(ctx context.Context) bool
	// Servers names the set-up servers, in order: what 'Adding to …' and a late notice
	// name.
	Servers(ctx context.Context) []mediaserver.Kind
	// Find looks the title up ("movie" or "series") on every set-up server and answers a
	// sighting per server that has it, in order: none while no index has it. It never
	// calls a server.
	Find(ctx context.Context, media string, ids PlexIDs) []Sighting
	// IndexBuiltAt is the oldest index among the set-up servers (zero = one isn't read
	// yet): from then on every server's answer is current.
	IndexBuiltAt() time.Time
	// Refresh asks every server's index to be read again after the delay (bursts coalesce).
	Refresh(after time.Duration)
}

// SetPlexLocator makes 'ready' wait for the media servers (optional).
func (s *Service) SetPlexLocator(p PlexLocator) { s.plex = p }

// SetHomeServer says which media server each person watches on: their Watch links and
// the push's Watch action lead with it. nil = Plex first for everyone.
func (s *Service) SetHomeServer(fn func(ctx context.Context, userID int64) mediaserver.Kind) {
	s.homeServer = fn
}

// homeOf is the server uid's Watch links lead with.
func (s *Service) homeOf(ctx context.Context, uid int64) mediaserver.Kind {
	if s.homeServer == nil {
		return mediaserver.Plex
	}
	return s.homeServer(ctx, uid)
}

// plexWaiter is the server check's own state: kick wakes RunPlexChecks after an index
// rebuild, and mu keeps the timer's check and a kicked one from overlapping.
type plexWaiter struct {
	once sync.Once
	kick chan struct{}
	mu   sync.Mutex
}

func (w *plexWaiter) ch() chan struct{} {
	w.once.Do(func() { w.kick = make(chan struct{}, 1) })
	return w.kick
}

// plexVerdict is what the gate says about a complete request.
type plexVerdict int

const (
	plexOff  plexVerdict = iota // no media server (or a book): tell them now, as always
	plexHas                     // a server has it: "ready to watch on Plex"
	plexLate                    // no server showed it within the grace period: tell them anyway
	plexWait                    // not yet
)

// readyNote is what a 'ready' notice says about the media servers: a Watch link per
// server that has the title (in order) and the servers its words name — where it is
// (plexHas) or where it is still being added (plexLate).
type readyNote struct {
	links []mediaserver.WatchLink
	where []mediaserver.Kind
}

// plexGated reports whether 'ready' for this request waits for a media server.
func (s *Service) plexGated(ctx context.Context, req Request) bool {
	return (req.MediaType == "movie" || req.MediaType == "series") && s.plex != nil && s.plex.Configured(ctx)
}

// plexGate decides whether a complete movie or series request can be announced, stamping
// on_disk_at the first time it is asked. An error (the stamp couldn't be written) leaves
// it to the caller's retry.
func (s *Service) plexGate(ctx context.Context, req Request) (plexVerdict, readyNote, error) {
	if !s.plexGated(ctx, req) {
		return plexOff, readyNote{}, nil
	}
	since := req.onDiskAt
	first := false
	if since == 0 {
		now := s.clock().Unix()
		at, err := s.repo.MarkOnDisk(ctx, req.ID, now)
		if err != nil {
			return plexWait, readyNote{}, fmt.Errorf("record that request %d is waiting for the media server: %w", req.ID, err)
		}
		// A copy of the row read before another path stamped it finds that stamp instead.
		since, first = at, at == now
	}
	look := s.plexLook(ctx, req.MediaType, req.TMDBID)
	v, note := s.plexJudge(ctx, look, time.Unix(since, 0))
	switch v {
	case plexWait:
		if first {
			// Info, with the ids: a title that never matches waits the whole grace period,
			// and this is how a GUID mismatch gets diagnosed.
			s.log.Info("request-ready: waiting for the media server to show the title", "request", req.ID,
				"tmdb", look.ids.TMDB, "tvdb", look.ids.TVDB, "imdb", look.ids.IMDB, "on_a_server", len(look.seen) > 0)
			// Open pages swap 'Ready' for 'Adding to Plex…'.
			s.publishUpdated(req, req.Status, s.parties(ctx, req))
		}
	case plexLate:
		s.log.Warn("request-ready: no media server showed the title; telling the requester anyway", "request", req.ID,
			"tmdb", look.ids.TMDB, "tvdb", look.ids.TVDB, "imdb", look.ids.IMDB, "waited", s.clock().Sub(time.Unix(since, 0)).Round(time.Minute))
	}
	return v, note, nil
}

// plexLookup is one title's answer from the indexes.
type plexLookup struct {
	movie bool
	ids   PlexIDs
	seen  []Sighting
	built time.Time // the oldest index
}

// plexLook finds a title on the media servers: by its TMDB id, then — legacy-agent Plex
// libraries match on TVDB and IMDb ids only, and Jellyfin can find an unmatched movie by
// its folder — with what its library record knows.
func (s *Service) plexLook(ctx context.Context, media string, tmdbID int) plexLookup {
	l := plexLookup{movie: media == "movie", ids: PlexIDs{TMDB: tmdbID}, built: s.plex.IndexBuiltAt()}
	if l.seen = s.plex.Find(ctx, media, l.ids); len(l.seen) > 0 {
		return l
	}
	more := s.libraryIDs(ctx, media, tmdbID)
	if more.TVDB == 0 && more.IMDB == "" && more.Folder == "" {
		return l
	}
	l.ids = more
	l.seen = s.plex.Find(ctx, media, more)
	return l
}

// libraryIDs is a title's outside ids and folder from its library record (just the TMDB
// id when it can't be read).
func (s *Service) libraryIDs(ctx context.Context, media string, tmdbID int) PlexIDs {
	ids := PlexIDs{TMDB: tmdbID}
	switch media {
	case "movie":
		if s.movies != nil {
			if m, err := s.movies.GetByTMDB(ctx, tmdbID); err == nil {
				ids.IMDB = m.IMDBID
				if m.MovieFilePath != "" {
					ids.Folder = filepath.Dir(m.MovieFilePath)
				}
			}
		}
	case "series":
		if s.series != nil {
			if sr, err := s.series.GetByTMDB(ctx, tmdbID); err == nil {
				ids.TVDB, ids.IMDB = sr.TVDBID, sr.IMDBID
				ids.Folder = s.series.FolderPath(ctx, sr.ID)
			}
		}
	}
	return ids
}

// counted is the sightings that count for files complete since settled: any server that
// has a movie; for a show, only servers whose index was read after settled (the index
// can't tell seasons apart, and the show may be there from an earlier season).
func (l plexLookup) counted(settled time.Time) []Sighting {
	var out []Sighting
	for _, sg := range l.seen {
		if l.movie || !sg.IndexBuiltAt.Before(settled) {
			out = append(out, sg)
		}
	}
	return out
}

// plexJudge is the rule, for files complete since since: ready as soon as any server has
// it (counted). Past plexGrace it goes out regardless. While it waits, it makes sure an
// index read is coming.
func (s *Service) plexJudge(ctx context.Context, l plexLookup, since time.Time) (plexVerdict, readyNote) {
	now := s.clock()
	settled := since.Add(plexSettle)
	if has := l.counted(settled); len(has) > 0 {
		var note readyNote
		for _, sg := range has {
			if sg.Link.URL != "" { // found, but no address to open it at
				note.links = append(note.links, sg.Link)
			}
			note.where = append(note.where, sg.Link.Server)
		}
		return plexHas, note
	}
	if now.Sub(since) >= plexGrace {
		return plexLate, readyNote{where: s.plex.Servers(ctx)}
	}
	switch {
	case l.built.Before(settled):
		// The next read must come after the scan Arrmada sent has had its time.
		s.plex.Refresh(max(settled.Sub(now), 0) + 5*time.Second)
	case now.Sub(l.built) >= plexRelook:
		s.plex.Refresh(0)
	}
	return plexWait, readyNote{}
}

// serverNames is how a notice names media servers: "Plex", "Plex and Jellyfin", or "your
// library" when it can't say.
func serverNames(kinds []mediaserver.Kind) string {
	var names []string
	for _, k := range kinds {
		names = append(names, k.Label())
	}
	if len(names) == 0 {
		return "your library"
	}
	return strings.Join(names, " and ")
}

// readyBody is a request's 'ready' wording: a book is ready to read; a movie or show is
// ready to watch — on the server that has it, or with a word of warning when none has
// shown it within the grace period.
func readyBody(req Request, v plexVerdict, note readyNote) string {
	if req.MediaType == "book" {
		return fmt.Sprintf("“%s” is ready to read.", req.Title)
	}
	return readyWords(requestedWhat(req), v, note)
}

// readyWords finishes "<what> is ready …" for a verdict.
func readyWords(what string, v plexVerdict, note readyNote) string {
	switch v {
	case plexHas:
		return what + " is ready to watch on " + serverNames(note.where) + "."
	case plexLate:
		return what + " is ready — it may take a few more minutes to show up in " + serverNames(note.where) + "."
	}
	return what + " is ready to watch."
}

// seasonGate is plexGate for the per-season notices of one request for several seasons,
// each season with its own wait (season_disk_at). The show is looked up once.
type seasonGate struct {
	s      *Service
	req    Request
	gated  bool
	stamps map[int]int64 // season → first seen complete, or seasonTold
	look   *plexLookup
}

func (s *Service) newSeasonGate(ctx context.Context, req Request) *seasonGate {
	return &seasonGate{s: s, req: req, gated: s.plexGated(ctx, req), stamps: decodeSeasonDisk(req.seasonDisk)}
}

// judge is the verdict for complete season n. A season already announced isn't held
// again (a follower who joined since still hears, as before).
func (g *seasonGate) judge(ctx context.Context, n int) (plexVerdict, readyNote, error) {
	since, stamped := g.stamps[n]
	if !g.gated || since == seasonTold {
		return plexOff, readyNote{}, nil
	}
	if !stamped {
		st, err := g.s.repo.MarkSeasonOnDisk(ctx, g.req.ID, n, g.s.clock().Unix())
		if err != nil {
			return plexWait, readyNote{}, fmt.Errorf("record that season %d of request %d is waiting for the media server: %w", n, g.req.ID, err)
		}
		g.stamps = st
		if since = st[n]; since <= 0 {
			return plexOff, readyNote{}, nil // announced meanwhile
		}
	}
	if g.look == nil {
		l := g.s.plexLook(ctx, "series", g.req.TMDBID)
		g.look = &l
	}
	v, note := g.s.plexJudge(ctx, *g.look, time.Unix(since, 0))
	if v == plexLate {
		g.s.log.Warn("request-ready: no media server showed the season; telling the requester anyway", "request", g.req.ID, "season", n,
			"tmdb", g.look.ids.TMDB, "tvdb", g.look.ids.TVDB, "imdb", g.look.ids.IMDB)
	}
	return v, note, nil
}

// told marks season n announced, so the server check stops looking at it. A season that
// never waited (no media server) has nothing to mark.
func (g *seasonGate) told(ctx context.Context, n int) {
	if since, stamped := g.stamps[n]; !stamped || since == seasonTold {
		return
	}
	if err := g.s.repo.MarkSeasonTold(ctx, g.req.ID, n); err != nil {
		g.s.log.Warn("request-ready: couldn't record that a season was announced", "request", g.req.ID, "season", n, "err", err)
		return
	}
	g.stamps[n] = seasonTold
}

// KickPlexCheck asks for the server check to run now — main calls it after each index
// rebuild, on any server. It never blocks (the index workers call it).
func (s *Service) KickPlexCheck() {
	select {
	case s.plexWaiter.ch() <- struct{}{}:
	default:
	}
}

// RunPlexChecks runs the server check each time it is kicked, until ctx ends. The
// two-minute timer is the scheduler's (request-plex-check); this is the "an index just
// changed" path, so a title a server has just added is announced within seconds of its
// index seeing it.
func (s *Service) RunPlexChecks(ctx context.Context) {
	kick := s.plexWaiter.ch()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
			if err := s.CheckPlexWaiting(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("request-ready: media server check failed", "err", err)
			}
		}
	}
}

// CheckPlexWaiting looks again at every request waiting for a media server: each is
// announced once a server has it or the grace period is over, by the same path an import
// takes. It reads the indexes from memory and never calls a server, so it runs whether or
// not they answer — the grace fallback can't be held up by a server that is down. A
// request whose files went again before anyone was told starts its wait afresh when
// they're back.
func (s *Service) CheckPlexWaiting(ctx context.Context) error {
	s.plexWaiter.mu.Lock()
	defer s.plexWaiter.mu.Unlock()
	reqs, err := s.repo.ListPlexWaiting(ctx)
	if err != nil || len(reqs) == 0 {
		return err
	}
	return s.readyPass(ctx, reqs, true)
}

// clearGone forgets the waits of a request (and its seasons) that is no longer complete,
// during the server check.
func (s *Service) clearGone(ctx context.Context, lk *readyLookup, rq Request) {
	if rq.onDiskAt > 0 && !lk.ready(ctx, s, rq) {
		if err := s.repo.ClearOnDisk(ctx, rq.ID); err != nil {
			s.log.Warn("request-ready: couldn't reset a media server wait", "request", rq.ID, "err", err)
		}
	}
	if rq.MediaType != "series" || rq.seasonDisk == "" {
		return
	}
	prog, _ := lk.seasons(ctx, s, rq)
	for n, at := range decodeSeasonDisk(rq.seasonDisk) {
		if p, ok := prog[n]; at > 0 && (!ok || !seasonReady(p)) {
			if err := s.repo.ClearSeasonOnDisk(ctx, rq.ID, n); err != nil {
				s.log.Warn("request-ready: couldn't reset a season's media server wait", "request", rq.ID, "season", n, "err", err)
			}
		}
	}
}
