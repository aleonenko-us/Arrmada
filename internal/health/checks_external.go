package health

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Checks on what searching depends on beyond the indexers' own rows: every indexer
// failing at once, Prowlarr (its hourly sync, and the trackers behind it failing
// together), FlareSolverr for the Cloudflare-protected trackers, and the nightly backups.
// None of them asks a tracker anything: indexer health comes from the integration status
// tracker, and only FlareSolverr — a local container — is pinged.

// IndexerState is one enabled, searched indexer as the outage checks see it: how it has
// been answering, from the integration status tracker (internal/connstatus).
type IndexerState struct {
	ID       int64
	Name     string
	Kind     string
	Prowlarr bool // synced from Prowlarr: it fails when Prowlarr does
	// Consecutive failures, when the streak began, whether background searches are
	// leaving it alone (backing off) and its last error (already redacted).
	Failures     int
	FailingSince time.Time
	BackingOff   bool
	LastError    string
}

// Failing thresholds: a public tracker that times out now and then is still working, so
// an indexer counts as failing only once it is backing off, has failed three times in a
// row, or has been failing for half an hour.
const (
	failingAfter     = 3
	failingFor       = 30 * time.Minute
	prowlarrCollapse = 2 // failing Prowlarr rows with one error that read as "Prowlarr is down"
)

func (s IndexerState) failing(now time.Time) bool {
	if s.Failures <= 0 {
		return false
	}
	return s.BackingOff || s.Failures >= failingAfter || (!s.FailingSince.IsZero() && now.Sub(s.FailingSince) >= failingFor)
}

// IndexerOutage is what the outage checks make of the indexers' states. It's worked out
// by one function so the per-indexer lines (IndexerStatusCheck) can leave out what these
// checks already say in one line.
type IndexerOutage struct {
	AllFailing bool
	// Prowlarr are the failing Prowlarr rows sharing one error, when there are enough of
	// them to say "Prowlarr is unreachable" instead of naming each tracker. Empty when
	// AllFailing (the one error says it).
	Prowlarr    []IndexerState
	ProwlarrErr string
	failing     []IndexerState
}

// FindIndexerOutage works out the outage from the enabled indexers' states.
func FindIndexerOutage(states []IndexerState, now time.Time) IndexerOutage {
	var o IndexerOutage
	for _, s := range states {
		if s.failing(now) {
			o.failing = append(o.failing, s)
		}
	}
	if len(states) > 0 && len(o.failing) == len(states) {
		o.AllFailing = true
		return o
	}
	byErr := map[string][]IndexerState{}
	for _, s := range o.failing {
		if s.Prowlarr {
			byErr[s.LastError] = append(byErr[s.LastError], s)
		}
	}
	for e, group := range byErr {
		// The biggest group wins; a tie goes to the error that sorts first, so the line
		// doesn't flip between runs.
		if len(group) > len(o.Prowlarr) || (len(group) == len(o.Prowlarr) && e < o.ProwlarrErr) {
			o.Prowlarr, o.ProwlarrErr = group, e
		}
	}
	if len(o.Prowlarr) < prowlarrCollapse {
		o.Prowlarr, o.ProwlarrErr = nil, ""
	}
	return o
}

// Covers reports whether an indexer is already part of an outage line, so a per-indexer
// warning for it would only repeat it.
func (o IndexerOutage) Covers(id int64) bool {
	if o.AllFailing {
		for _, s := range o.failing {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	for _, s := range o.Prowlarr {
		if s.ID == id {
			return true
		}
	}
	return false
}

// IndexerOutageCheck reports one error when every enabled indexer is failing: nothing
// can be searched at all, and a page of "no results" would otherwise be all anyone sees.
// states returns the enabled, searched indexers; an error (or none at all, which the
// "no indexers" check already reports) says nothing.
func IndexerOutageCheck(states func(ctx context.Context) ([]IndexerState, error)) Check {
	return indexerOutageCheck(states, time.Now)
}

func indexerOutageCheck(states func(ctx context.Context) ([]IndexerState, error), now func() time.Time) Check {
	return Check{
		Key: "indexers.outage", Name: "Indexer outage", Category: CategoryIndexers,
		Run: func(ctx context.Context) []Finding {
			st, err := states(ctx)
			if err != nil {
				return nil
			}
			o := FindIndexerOutage(st, now())
			if !o.AllFailing {
				return nil
			}
			msg := "Every indexer is failing — nothing can be searched."
			if cause := sharedProwlarrError(o.failing); cause != "" {
				msg += " Prowlarr is unreachable: " + cause
			} else if s := latestFailure(o.failing); s.LastError != "" {
				msg += fmt.Sprintf(" Latest error (%s): %s", s.Name, s.LastError)
			}
			return []Finding{{Key: "indexers.all_failing", Level: LevelError, Message: msg, Fix: FixIndexerStatus}}
		},
	}
}

// sharedProwlarrError is the error every failing row shares when they all come from
// Prowlarr; "" otherwise.
func sharedProwlarrError(rows []IndexerState) string {
	if len(rows) < prowlarrCollapse {
		return ""
	}
	for _, s := range rows {
		if !s.Prowlarr || s.LastError != rows[0].LastError {
			return ""
		}
	}
	return rows[0].LastError
}

// latestFailure is the row whose failing streak began last (the freshest news).
func latestFailure(rows []IndexerState) IndexerState {
	sorted := append([]IndexerState(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].FailingSince.After(sorted[j].FailingSince) })
	return sorted[0]
}

// ProwlarrState is the Prowlarr connection as its check sees it.
type ProwlarrState struct {
	// Synced is true once a sync has saved a URL and key; before that there's nothing to
	// watch.
	Synced bool
	// The latest sync: when, its error ("" when it worked) and how many scheduled runs
	// in a row have failed.
	LastSyncAt  time.Time
	LastSyncErr string
	FailedRuns  int
	// Indexers are every enabled, searched indexer, Prowlarr's and others.
	Indexers []IndexerState
}

// prowlarrSyncFailedRuns is how many scheduled syncs in a row must fail before it's worth
// a warning: one is often Prowlarr restarting for an update.
const prowlarrSyncFailedRuns = 2

// ProwlarrCheck reports Prowlarr being down: its trackers failing together with one
// error collapse into one line ("Prowlarr is unreachable (12 indexers)") rather than
// twelve, and an hourly sync that keeps failing says that indexers added or switched off
// in Prowlarr aren't reaching Arrmada. Quiet when Prowlarr was never synced.
func ProwlarrCheck(state func(ctx context.Context) ProwlarrState) Check {
	return prowlarrCheck(state, time.Now)
}

func prowlarrCheck(state func(ctx context.Context) ProwlarrState, now func() time.Time) Check {
	return Check{
		Key: "prowlarr", Name: "Prowlarr", Category: CategoryIndexers,
		Run: func(ctx context.Context) []Finding {
			st := state(ctx)
			syncFailing := st.FailedRuns >= prowlarrSyncFailedRuns && st.LastSyncErr != ""
			o := FindIndexerOutage(st.Indexers, now())
			if len(o.Prowlarr) > 0 {
				msg := fmt.Sprintf("Prowlarr is unreachable (%d indexers)", len(o.Prowlarr))
				if o.ProwlarrErr != "" {
					msg += ": " + o.ProwlarrErr
				}
				if syncFailing {
					msg += ". The hourly sync is failing too."
				}
				return []Finding{{Key: "prowlarr.unreachable", Level: LevelWarning, Message: msg, Fix: FixProwlarr}}
			}
			if !st.Synced || !syncFailing {
				return nil
			}
			return []Finding{{
				Key: "prowlarr.sync", Level: LevelWarning, Fix: FixProwlarr,
				Message: fmt.Sprintf("The hourly Prowlarr sync has failed %d times in a row (last at %s): %s. Indexers added or switched off in Prowlarr aren't reaching Arrmada.",
					st.FailedRuns, clock(st.LastSyncAt, now()), st.LastSyncErr),
			}}
		},
	}
}

// FlareSolverr check pacing: a local container, pinged once a minute with a short limit,
// so a stopped one shows within about a minute and a hung one can't hold the check up.
const (
	flareCheckEvery   = 60 * time.Second
	flarePingTimeout  = 3 * time.Second
	flareCheckTimeout = flarePingTimeout + 2*time.Second
)

// FlareSolverrCheck warns when FlareSolverr is needed — an enabled TorrentLeech or 1337x
// indexer, which can't get past Cloudflare without it — and isn't set up or doesn't
// answer a ping. dependents names those indexers (none: the check is quiet and pings
// nothing); configured says whether a FlareSolverr URL is set; ping asks it.
func FlareSolverrCheck(dependents func(ctx context.Context) ([]string, error), configured func() bool, ping func(ctx context.Context) error) Check {
	return Check{
		Key: "flaresolverr", Name: "FlareSolverr", Category: CategoryIntegrations, Interval: flareCheckEvery, Timeout: flareCheckTimeout,
		Run: func(ctx context.Context) []Finding {
			names, err := dependents(ctx)
			if err != nil || len(names) == 0 {
				return nil
			}
			who := joinNames(names)
			if !configured() {
				return []Finding{{
					Key: "flaresolverr.down", Level: LevelWarning, Fix: FixFlareSolverr,
					Message: fmt.Sprintf("FlareSolverr isn't set up, so %s can't get past Cloudflare.", who),
				}}
			}
			// The ping gets a little less than the check's own limit, so a hung FlareSolverr
			// reads as "not answering" rather than as this check timing out.
			pctx, cancel := context.WithTimeout(ctx, flarePingTimeout)
			defer cancel()
			err = ping(pctx)
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return nil // shutting down: says nothing about FlareSolverr
			}
			msg := fmt.Sprintf("FlareSolverr isn't answering, so %s can't get past Cloudflare", who)
			if pctx.Err() != nil {
				msg += " (no reply in time)"
			} else {
				msg += ": " + err.Error()
			}
			return []Finding{{Key: "flaresolverr.down", Level: LevelWarning, Fix: FixFlareSolverr, Message: msg}}
		},
	}
}

// joinNames is "TorrentLeech", "TorrentLeech and 1337x", "A, B and C".
func joinNames(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	return strings.Join(sorted[:len(sorted)-1], ", ") + " and " + sorted[len(sorted)-1]
}

// BackupState is the nightly backups as the check sees them.
type BackupState struct {
	Enabled bool
	Uptime  time.Duration // how long the app has been up (a fresh install hasn't had a night yet)
	Newest  time.Time     // the newest nightly backup; zero when there's none
}

// Backup age thresholds: two missed nights is a warning, a week is an error — by then a
// restore would lose a week of requests, history and settings.
const (
	backupStaleAfter = 48 * time.Hour
	backupErrorAfter = 7 * 24 * time.Hour
)

// BackupsCheck warns when nightly backups have stopped (usually a full disk on the data
// volume): the newest is over 48 hours old, or none was ever made once the app has been
// up a day; over a week old is an error. Quiet when nightly backups are switched off.
func BackupsCheck(state func(ctx context.Context) BackupState) Check {
	return backupsCheck(state, time.Now)
}

func backupsCheck(state func(ctx context.Context) BackupState, now func() time.Time) Check {
	return Check{
		Key: "backups", Name: "Database backups", Category: CategoryStorage,
		Run: func(ctx context.Context) []Finding {
			st := state(ctx)
			if !st.Enabled || st.Uptime < 24*time.Hour {
				return nil
			}
			const why = " — check the log for why (often a full disk)."
			if st.Newest.IsZero() {
				return []Finding{{Key: "backups.age", Level: LevelWarning, Fix: FixBackups, Message: "No nightly database backup has been made yet" + why}}
			}
			age := now().Sub(st.Newest)
			if age <= backupStaleAfter {
				return nil
			}
			level := LevelWarning
			if age > backupErrorAfter {
				level = LevelError
			}
			return []Finding{{Key: "backups.age", Level: level, Fix: FixBackups, Message: fmt.Sprintf("No database backup in %d days%s", int(age.Hours()/24), why)}}
		},
	}
}
