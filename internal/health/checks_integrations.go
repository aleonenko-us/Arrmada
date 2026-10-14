package health

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// Checks on the services Arrmada talks to. Each honours its own pace: a client is asked
// every minute, Plex's identity at most every five when the poller isn't already talking
// to it, and TMDB every six hours unless its key changes. None of them puts a token or
// key in a message.

// DownloadClientsCheck asks each enabled client on its own whether it answers. Queue only
// fails when every client does, so a dead second client used to leave the panel green.
// states returns how many clients exist (enabled or not) and each enabled one's answer.
func DownloadClientsCheck(states func(ctx context.Context) (configured int, st []download.ClientState, err error)) Check {
	return Check{
		Key: "downloads", Name: "Download clients", Category: CategoryDownloads, Timeout: 10 * time.Second,
		Run: func(ctx context.Context) []Finding {
			n, st, err := states(ctx)
			switch {
			case err != nil:
				return nil // a database hiccup isn't a client problem
			case n == 0:
				return []Finding{{
					Key: "downloads.client.none", Level: LevelError, Fix: FixDownloadClients,
					Message: "No download client is configured — grabbed releases have nowhere to download.",
				}}
			case len(st) == 0:
				return []Finding{{
					Key: "downloads.client.none", Level: LevelError, Fix: FixDownloadClients,
					Message: "Every download client is switched off — grabbed releases have nowhere to download.",
				}}
			}
			down := 0
			for _, c := range st {
				if !c.OK {
					down++
				}
			}
			var out []Finding
			for _, c := range st {
				if c.OK {
					continue
				}
				f := Finding{Key: fmt.Sprintf("downloads.client.%d", c.ID), Fix: ConnectionFix(fmt.Sprintf("download_client-%d", c.ID), "Check "+c.Name)}
				if down == len(st) {
					// Nothing else can take the downloads, so nothing downloads at all.
					f.Level = LevelError
					f.Message = fmt.Sprintf("%s is unreachable, so nothing can download until it answers: %s", c.Name, c.Err)
				} else {
					f.Level = LevelWarning
					f.Message = fmt.Sprintf("%s is unreachable — its torrents aren't tracked and stall fail-over is paused until it answers or is removed: %s", c.Name, c.Err)
				}
				out = append(out, f)
			}
			return out
		},
	}
}

// UnseenDownloads is one client whose finished downloads Arrmada can't see on disk.
type UnseenDownloads struct {
	ClientID   int64
	ClientName string
	// Count is how many of the finished downloads looked at aren't there; Example is one.
	Count   int
	Example string
}

// DownloadPathsCheck warns about a download client whose finished downloads aren't where
// Arrmada looks for them (after the client's path mappings): they download fine and never
// import. unseen reads the shared queue snapshot and stats a few paths; it asks no client
// anything itself.
func DownloadPathsCheck(unseen func(ctx context.Context) ([]UnseenDownloads, error)) Check {
	return Check{
		Key: "downloads.paths", Name: "Download paths", Category: CategoryDownloads, Timeout: 10 * time.Second,
		Run: func(ctx context.Context) []Finding {
			list, err := unseen(ctx)
			if err != nil {
				return nil // no queue to judge; the client check reports an outage
			}
			var out []Finding
			for _, u := range list {
				what := "a finished download"
				if u.Count > 1 {
					what = fmt.Sprintf("%d finished downloads", u.Count)
				}
				out = append(out, Finding{
					Key:   fmt.Sprintf("downloads.paths.%d", u.ClientID),
					Level: LevelWarning,
					Fix:   ConnectionFix(fmt.Sprintf("download_client-%d", u.ClientID), "Map paths for "+u.ClientName),
					Message: fmt.Sprintf("%s has %s Arrmada can't see on disk (such as %s), so they won't import. Mount its download folder into Arrmada or add a path mapping, then Test it.",
						u.ClientName, what, u.Example),
				})
			}
			return out
		},
	}
}

// PlexState is a media server connection as the check sees it (see insights.PlexHealth and
// jellyfinsvc.Health): the Plex check and the Jellyfin check read the same shape.
type PlexState struct {
	Configured   bool
	Monitoring   bool
	LastOKAt     time.Time
	FailingSince time.Time
	LastErr      string
	Unauthorized bool
	Consecutive  int
}

// Plex check pacing: how often Plex is asked directly when the poller isn't already
// talking to it, and how long it must stay unreachable before it's worth a warning (a
// Plex restart or update takes a few minutes and shouldn't light the panel up).
const (
	plexProbeEvery = 5 * time.Minute
	plexDownFor    = 10 * time.Minute
)

// PlexCheck reports a refused token at once and an unreachable Plex once it has been
// down for ten minutes. It's quiet when Plex isn't set up. probe asks Plex directly and
// records the answer in what state returns; it's used when monitoring is off, at most
// every five minutes, or when a person asked.
func PlexCheck(state func(ctx context.Context) PlexState, probe func(ctx context.Context) error) Check {
	return plexCheck(state, probe, time.Now)
}

func plexCheck(state func(ctx context.Context) PlexState, probe func(ctx context.Context) error, now func() time.Time) Check {
	return mediaServerCheck(mediaServerSpec{
		key: "plex.connection", name: "Plex connection", server: "Plex", fix: FixPlexConnection,
		refused: "Plex rejected Arrmada's token — reconnect Plex. Plex sign-in for family, recommendations and Insights stop working until you do.",
	}, state, probe, now)
}

// JellyfinCheck is PlexCheck for Jellyfin (state from jellyfinsvc's Health): a refused API
// key at once, an unreachable server after ten minutes, quiet when it isn't set up.
func JellyfinCheck(state func(ctx context.Context) PlexState, probe func(ctx context.Context) error) Check {
	return jellyfinCheck(state, probe, time.Now)
}

func jellyfinCheck(state func(ctx context.Context) PlexState, probe func(ctx context.Context) error, now func() time.Time) Check {
	return mediaServerCheck(mediaServerSpec{
		key: "jellyfin.connection", name: "Jellyfin connection", server: "Jellyfin", fix: FixJellyfinConnection,
		refused: "Jellyfin rejected Arrmada's API key — make a new one in Jellyfin (Dashboard → API Keys) and save it on the Jellyfin card. Watch links and library updates stop until you do.",
	}, state, probe, now)
}

// mediaServerSpec is what differs between the Plex and Jellyfin checks.
type mediaServerSpec struct {
	key, name, server string
	fix               *Fix
	refused           string // the message for a refused credential
}

func mediaServerCheck(spec mediaServerSpec, state func(ctx context.Context) PlexState, probe func(ctx context.Context) error, now func() time.Time) Check {
	var mu sync.Mutex
	var probedAt time.Time
	return Check{
		Key: spec.key, Name: spec.name, Category: CategoryIntegrations, Timeout: 20 * time.Second,
		Run: func(ctx context.Context) []Finding {
			st := state(ctx)
			if !st.Configured {
				return nil
			}
			// The poller already talks to Plex every few seconds while monitoring is on; a
			// person asking (Check now, Plex settings saved) gets a fresh answer either way.
			if !st.Monitoring || Forced(ctx) {
				mu.Lock()
				due := probedAt.IsZero() || now().Sub(probedAt) >= plexProbeEvery || Forced(ctx)
				if due {
					probedAt = now()
				}
				mu.Unlock()
				if due {
					_ = probe(ctx)
					st = state(ctx)
				}
			}
			switch {
			case st.Consecutive == 0:
				return nil
			case st.Unauthorized:
				return []Finding{{Key: spec.key, Level: LevelError, Fix: spec.fix, Message: spec.refused}}
			case st.FailingSince.IsZero() || now().Sub(st.FailingSince) < plexDownFor:
				return nil
			}
			since := st.LastOKAt
			if since.IsZero() {
				since = st.FailingSince
			}
			return []Finding{{
				Key: spec.key, Level: LevelWarning, Fix: spec.fix,
				Message: fmt.Sprintf("%s hasn't answered since %s: %s", spec.server, clock(since, now()), st.LastErr),
			}}
		},
	}
}

// clock is a time as people say it: "14:02" today, "3 Oct 14:02" before that.
func clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("2 Jan 15:04")
}

// ErrKeyMissing and ErrKeyRejected are what a TMDB validator returns for no key and a
// refused key; anything else means TMDB couldn't be asked.
var (
	ErrKeyMissing  = errors.New("no key")
	ErrKeyRejected = errors.New("key rejected")
)

// TMDB check pacing: a key is re-validated every six hours, sooner when it changes, and
// a check that couldn't reach TMDB tries again after a quarter of an hour.
const (
	tmdbRecheck      = 6 * time.Hour
	tmdbRetryNetwork = 15 * time.Minute
)

// TMDBCheck reports a missing or refused TMDB key. key returns the current key (only a
// fingerprint of it is kept, to notice a change); validate asks TMDB about it and returns
// nil, ErrKeyMissing, ErrKeyRejected or another error. The registry runs this every
// minute, but TMDB itself is asked only as often as the pacing above allows, unless a
// person asked (see Forced).
func TMDBCheck(key func() string, validate func(ctx context.Context) error) Check {
	return tmdbCheck(key, validate, time.Now)
}

func tmdbCheck(key func() string, validate func(ctx context.Context) error, now func() time.Time) Check {
	var (
		mu     sync.Mutex
		fp     [32]byte
		at     time.Time
		last   error
		asked  bool
		netErr bool
	)
	return Check{
		Key: "tmdb.key", Name: "TMDB key", Category: CategoryIntegrations, Timeout: 25 * time.Second,
		Run: func(ctx context.Context) []Finding {
			k := key()
			if k == "" {
				return findingsFor(ErrKeyMissing) // no need to ask TMDB about nothing
			}
			sum := sha256.Sum256([]byte(k))
			// Runs never overlap (the registry won't start one while another is in flight);
			// the lock is for the race detector's benefit as much as anything.
			mu.Lock()
			defer mu.Unlock()
			wait := tmdbRecheck
			if netErr {
				wait = tmdbRetryNetwork
			}
			if !asked || sum != fp || now().Sub(at) >= wait || Forced(ctx) {
				err := validate(ctx)
				if err != nil && ctx.Err() != nil {
					return findingsFor(last) // cut short: keep what was known
				}
				fp, at, last, asked = sum, now(), err, true
				netErr = err != nil && !errors.Is(err, ErrKeyRejected) && !errors.Is(err, ErrKeyMissing)
			}
			return findingsFor(last)
		},
	}
}

func findingsFor(err error) []Finding {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrKeyMissing):
		return []Finding{{Key: "tmdb.key", Level: LevelError, Fix: FixAPIKeys, Message: "No TMDB key is set — search, Discover and new titles won't work."}}
	case errors.Is(err, ErrKeyRejected):
		return []Finding{{Key: "tmdb.key", Level: LevelError, Fix: FixAPIKeys, Message: "TMDB rejected the API key — search, Discover and new titles won't work until it's replaced."}}
	}
	return []Finding{{Key: "tmdb.key", Level: LevelWarning, Fix: FixAPIKeys, Message: "Couldn't reach TMDB to check the API key: " + err.Error()}}
}

// TaskState is one scheduled task as the failing-tasks check sees it.
type TaskState struct {
	Name                string
	Label               string // how the UI names it; Name when empty
	LastError           string
	ConsecutiveFailures int
}

// tasksFailingAfter is how many failures in a row make a task worth a warning: one
// failure is often a blip (a client restarting), three is a pattern.
const tasksFailingAfter = 3

// TasksFailingCheck warns about every task that has failed three or more times in a row.
// It clears on the task's next success.
func TasksFailingCheck(snapshot func() []TaskState) Check {
	return Check{
		Key: "tasks", Name: "Scheduled tasks", Category: CategoryTasks,
		Run: func(ctx context.Context) []Finding {
			var out []Finding
			for _, t := range snapshot() {
				if t.ConsecutiveFailures < tasksFailingAfter {
					continue
				}
				label := t.Label
				if label == "" {
					label = t.Name
				}
				msg := fmt.Sprintf("“%s” has failed %d times in a row", label, t.ConsecutiveFailures)
				if t.LastError != "" {
					msg += ": " + t.LastError
				}
				out = append(out, Finding{Key: "tasks.failing." + t.Name, Level: LevelWarning, Message: msg, Fix: FixTasks})
			}
			return out
		},
	}
}
