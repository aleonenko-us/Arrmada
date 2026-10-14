package insights

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tristenlammi/arrmada/internal/mediaserver"
	"github.com/tristenlammi/arrmada/internal/plex"
)

// Insights records plays from every connected media server the same way: each server has
// a sessionSource (where its live streams come from) and a tracker (the streams it is
// following and what was playing at its last poll), and its own poller goroutine runs
// runMonitor over them. Recorded rows say which server they came from (stream_sessions.server,
// migration 0310); every read can be narrowed to one server.

// sessionSource is where one server's live streams come from, flattened to the neutral
// session.
type sessionSource interface {
	Kind() mediaserver.Kind
	Sessions(ctx context.Context) ([]mediaserver.Session, error)
}

// tracker is one server's live state. live belongs to that server's poller goroutine;
// playing and playingAt are read from anywhere (Convert pauses while someone watches).
type tracker struct {
	kind      mediaserver.Kind
	live      map[string]*liveSession
	playing   atomic.Int32
	playingAt atomic.Int64
}

func newTracker(kind mediaserver.Kind) *tracker {
	return &tracker{kind: kind, live: map[string]*liveSession{}}
}

// watching: something was playing at the server's last poll, and that poll was recent.
func (t *tracker) watching(now time.Time) bool {
	if t == nil {
		return false
	}
	at := t.playingAt.Load()
	if at == 0 || now.Sub(time.Unix(at, 0)) > 3*time.Minute {
		return false
	}
	return t.playing.Load() > 0
}

// serverOf is the server a session or row belongs to: Plex when it doesn't say (rows and
// sessions from before Jellyfin, and Plex's own client, name none).
func serverOf(k mediaserver.Kind) mediaserver.Kind {
	if k == "" {
		return mediaserver.Plex
	}
	return k
}

// Server filter values for every read: "" is both servers.
const (
	ServerAll      = ""
	ServerPlex     = string(mediaserver.Plex)
	ServerJellyfin = string(mediaserver.Jellyfin)
)

// NormalizeServer reads a ?server= value: "plex" or "jellyfin", anything else (including
// "all") is both.
func NormalizeServer(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ServerPlex:
		return ServerPlex
	case ServerJellyfin:
		return ServerJellyfin
	}
	return ServerAll
}

// serverAnd is " AND <alias>server = '<server>'" for a filtered read, "" for both. The
// value is one of two constants, never what a request sent, so it can sit in the SQL.
func serverAnd(alias, server string) string {
	switch server {
	case ServerPlex:
		return " AND " + alias + "server = 'plex'"
	case ServerJellyfin:
		return " AND " + alias + "server = 'jellyfin'"
	}
	return ""
}

// serverWhere is serverAnd as a whole WHERE clause ("" for both).
func serverWhere(alias, server string) string {
	if c := serverAnd(alias, server); c != "" {
		return " WHERE" + strings.TrimPrefix(c, " AND")
	}
	return ""
}

// rowServer names a group of rows' server: the one they share, or "" when they come from
// both. It's a SQL aggregate over stream_sessions' server column.
const rowServer = `CASE WHEN MIN(server) = MAX(server) THEN MIN(server) ELSE '' END`

// plexSource is the Plex connection's /status/sessions.
type plexSource struct{ s *Service }

func (plexSource) Kind() mediaserver.Kind { return mediaserver.Plex }

func (p plexSource) Sessions(ctx context.Context) ([]plex.Session, error) {
	out, err := p.s.client(ctx).Sessions(ctx)
	for i := range out {
		out[i].Server = mediaserver.Plex
	}
	return out, err
}

// liveSources are the sources the Activity view asks: the connected servers the filter
// allows, Plex first.
func (s *Service) liveSources(ctx context.Context, server string) []sessionSource {
	var out []sessionSource
	if server != ServerJellyfin && s.configured(ctx) {
		out = append(out, plexSource{s})
	}
	if w := s.jfw.Load(); server != ServerPlex && w != nil && w.svc.Configured(ctx) {
		out = append(out, w.src)
	}
	return out
}
