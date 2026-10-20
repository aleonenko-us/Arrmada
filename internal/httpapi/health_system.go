package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/audioserver"
	"github.com/tristenlammi/arrmada/internal/connstatus"
	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/health"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// refreshGap is how often "Check now" may re-run every check; a refresh asked for sooner
// is answered from the results already held.
const refreshGap = 10 * time.Second

// handleSystemHealth reports operational health — whether the pieces needed to acquire
// and import media are present and working — plus free disk space. It's the "why isn't
// anything downloading?" panel. Every answer comes from the health registry's cache: the
// checks run in the background, so a hung qBittorrent or Plex can't slow this down.
// ?refresh=1 re-runs them first (at most once per refreshGap).
func (a *api) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	rep := health.Report{Status: "ok", Warnings: []health.Warning{}, Checks: []health.CheckResult{}}
	if reg := a.deps.Health; reg != nil {
		if r.URL.Query().Get("refresh") == "1" {
			// Detached from the request: a closed tab mustn't cut the checks short and
			// leave them half-recorded.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
			reg.Refresh(ctx, refreshGap)
			cancel()
		}
		rep = reg.Results()
	}
	// Free space is a statfs on one folder — cheap and local, so it's read live.
	var disk map[string]any
	if dl := a.roots().Downloads(r.Context()); dl != "" {
		if free, ok := diskspace.FreeGB(dl); ok {
			disk = map[string]any{"free_gb": fmt.Sprintf("%.1f", free), "path": dl}
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"status": rep.Status, "warnings": rep.Warnings, "checks": rep.Checks, "disk": disk,
	})
}

// registerHealthChecks adds the checks built from the API's own dependencies. Each reads
// what it needs when it runs, so a setting changed since boot (a folder picked, a module
// switched off) is what's judged.
func (a *api) registerHealthChecks(reg *health.Registry) {
	if a.deps.Indexers != nil {
		reg.Register(health.IndexersCheck(func(ctx context.Context) (int, error) {
			ix, err := a.deps.Indexers.List(ctx)
			n := 0
			for _, i := range ix {
				// A usenet indexer with no usenet client is never searched, so it
				// doesn't count as somewhere to search.
				if a.deps.Indexers.Searched(i) {
					n++
				}
			}
			return n, err
		}))
		// Indexers that keep failing, from the integration status tracker (the same state
		// as the dots on their cards). Nothing here asks a tracker anything.
		reg.Register(health.IndexerStatusCheck(func(ctx context.Context) (int, []health.TroubledIndexer, error) {
			ix, err := a.deps.Indexers.List(ctx)
			if err != nil {
				return 0, nil, err
			}
			now := time.Now()
			// Rows the outage lines below already cover (every indexer failing, or
			// Prowlarr's trackers failing together) aren't listed one by one as well.
			states, _ := a.indexerStates(ctx)
			outage := health.FindIndexerOutage(states, now)
			enabled := 0
			var failing []health.TroubledIndexer
			for _, i := range ix {
				if !a.deps.Indexers.Searched(i) {
					continue
				}
				enabled++
				st, _, ok := a.deps.Indexers.Status(i.ID)
				if ok && st.ConsecutiveFailures > 0 && !outage.Covers(i.ID) {
					failing = append(failing, health.TroubledIndexer{ID: i.ID, Name: i.Name, Failures: st.ConsecutiveFailures,
						FailingSince: st.FailingSince, Until: st.BackoffUntil, LastError: st.LastError})
				}
			}
			return enabled, failing, nil
		}))
		// One error when every indexer is failing, and one line for Prowlarr being down
		// (its trackers failing together, or its hourly sync failing) instead of twelve.
		reg.Register(health.IndexerOutageCheck(a.indexerStates))
		if a.deps.Settings != nil {
			reg.Register(health.ProwlarrCheck(a.prowlarrState))
		}
		// MusicBrainz refusing background lookups (Music serves its cache meanwhile).
		if a.deps.Settings != nil {
			reg.Register(health.MusicBrainzCheck(a.musicEnabled, metadata.MusicBrainzPaused))
		}
		// FlareSolverr, only while a tracker that needs it is enabled. Pinging the local
		// container is the one live call these checks make; it's capped at 3 seconds.
		reg.Register(health.FlareSolverrCheck(a.flareSolverrDependents,
			a.deps.FlareSolverr.Configured,
			func(ctx context.Context) error {
				// Its error goes into a staff-visible line; a URL saved as user:pass@host
				// would otherwise come back in it.
				if _, err := a.deps.FlareSolverr.Ping(ctx); err != nil {
					return errors.New(connstatus.Redact(err.Error()))
				}
				return nil
			}))
	}

	if a.deps.Downloads != nil {
		reg.Register(health.DownloadClientsCheck(func(ctx context.Context) (int, []download.ClientState, error) {
			all, err := a.deps.Downloads.List(ctx)
			if err != nil {
				return 0, nil, err
			}
			// The shared queue read's per-client health, not a poll of its own: the
			// Downloads page, the sweeps and this check all see the same answer.
			snap, err := a.deps.Downloads.Snapshot(ctx)
			if err != nil && len(snap.Health) == 0 {
				return 0, nil, err // the client list itself couldn't be read
			}
			return len(all), download.EnabledStates(snap.Health), nil
		}))
		reg.Register(health.DownloadPathsCheck(a.unseenDownloads))
		a.registerUsenetChecks(reg)
	}

	if a.deps.Insights != nil {
		reg.Register(health.PlexCheck(
			func(ctx context.Context) health.PlexState {
				h := a.deps.Insights.PollHealth(ctx)
				return health.PlexState{
					Configured: h.Configured, Monitoring: h.Monitoring, LastOKAt: h.LastOKAt,
					FailingSince: h.FailingSince, LastErr: h.LastErr, Unauthorized: h.Unauthorized, Consecutive: h.Consecutive,
				}
			},
			func(ctx context.Context) error { return a.deps.Insights.ProbeIdentity(ctx) }))
	}
	if a.deps.Jellyfin != nil {
		reg.Register(health.JellyfinCheck(
			func(ctx context.Context) health.PlexState {
				h := a.deps.Jellyfin.Health(ctx)
				return health.PlexState{
					Configured: h.Configured, Monitoring: h.Monitoring, LastOKAt: h.LastOKAt,
					FailingSince: h.FailingSince, LastErr: connstatus.Redact(h.LastErr), Unauthorized: h.Unauthorized, Consecutive: h.Consecutive,
				}
			},
			a.deps.Jellyfin.Probe))
	}

	if v, ok := a.deps.Discovery.(tmdbValidator); ok && a.deps.APIKeys != nil {
		reg.Register(health.TMDBCheck(a.deps.APIKeys.Func("tmdb"), func(ctx context.Context) error {
			err := tmdbValidate(ctx, v)
			// Its answer is the TMDB card's last check on Settings → Connections too.
			if ctx.Err() == nil && !errors.Is(err, health.ErrKeyMissing) {
				rec := err
				if errors.Is(err, health.ErrKeyRejected) {
					rec = errors.New("TMDB rejected the key")
				}
				a.deps.ConnStatus.Record(connKindAPIKey, "tmdb", connstatus.Outcome{Err: rec, Name: "TMDB"})
			}
			return err
		}))
	}

	// The probe cache keeps the check from writing a probe file into every library folder
	// each time it runs — on Unraid that can wake sleeping array disks. A folder that
	// passed is trusted for an hour; one that failed is re-checked each run.
	if a.deps.Settings != nil {
		reg.Register(health.LibraryFoldersCheck(a.libraryState, health.NewProbeCache(time.Hour)))
	}
	// The missing-file check's "looks unmounted" verdict (MOV-21): read from memory only.
	if a.deps.Movies != nil {
		reg.Register(health.MovieFilesCheck(a.deps.Movies.MissingSweepWarning))
	}
	// The music file check's verdict (MUS-15), likewise from memory.
	if a.deps.Automation != nil {
		reg.Register(health.MusicFilesCheck(a.deps.Automation.MusicFilesWarning))
	}

	// TV episode files outside their show's folder (#56): from the database only.
	if a.deps.Series != nil {
		reg.Register(health.MisplacedEpisodesCheck(func(ctx context.Context) ([]health.MisplacedShow, error) {
			shows, err := a.deps.Series.MisplacedShows(ctx)
			out := make([]health.MisplacedShow, 0, len(shows))
			for _, s := range shows {
				out = append(out, health.MisplacedShow{SeriesID: s.SeriesID, Title: s.Title, Files: s.Files, NoFolder: s.NoFolder})
			}
			return out, err
		}))
	}

	if a.deps.Recycle != nil {
		reg.Register(health.RecycleDriveCheck(func() []health.RecycleBinProblem {
			var out []health.RecycleBinProblem
			for _, p := range a.deps.Recycle.Problems() {
				out = append(out, health.RecycleBinProblem{Dir: p.Dir, Legacy: p.Legacy, OtherDrive: p.OtherDrive, LegacyFull: p.LegacyFull})
			}
			return out
		}))
	}

	if a.deps.DiskGuard != nil {
		reg.Register(health.DiskGuardCheck(a.diskGuardState))
	}

	// The downloads folders are read live: one picked since boot is the one filling up.
	// A library folder's own downloads folder on another disk is watched too.
	// With "measure a different drive" set, a folder it stands in for is checked on that
	// drive: on Unraid the downloads folder's own reading is the whole array.
	reg.Register(health.DownloadsFreeCheck(func() []health.Folder {
		ctx := a.runCtx()
		r := a.roots()
		dl := r.Downloads(ctx)
		folders := append([]health.Folder{{Role: "downloads", Label: "Downloads", Path: dl}}, health.DownloadFolders(dl, r.PerFolderDownloads(ctx))...)
		if g := a.deps.DiskGuard; g != nil {
			for i := range folders {
				if strings.TrimSpace(folders[i].Path) != "" {
					folders[i].Path = g.MeasuredPathFor(ctx, folders[i].Path)
				}
			}
		}
		return folders
	}))
	// A library folder's own downloads folder only helps on the library's filesystem.
	reg.Register(health.FolderDownloadsCheck(func(ctx context.Context) []libroots.FolderDownloads {
		return a.roots().PerFolderDownloads(ctx)
	}))

	if a.deps.AudioManager != nil && a.deps.Settings != nil {
		reg.Register(health.AudiobookServerCheck(func(ctx context.Context) (bool, bool, string) {
			if !a.deps.Settings.GetBool(ctx, audioserver.KeyEnabled, false) {
				return false, false, ""
			}
			running, lastErr := a.deps.AudioManager.Running()
			return true, running, lastErr
		}))
	}

	if a.deps.Backups != nil {
		reg.Register(health.BackupsCheck(func(ctx context.Context) health.BackupState {
			return health.BackupState{Enabled: a.deps.Backups.Enabled(ctx), Uptime: time.Since(a.start), Newest: a.deps.Backups.LastNightly()}
		}))
	}
}

// indexerStates is every enabled, searched indexer with how it has been answering, for
// the outage and Prowlarr checks. Read from the integration status tracker: no indexer is
// asked anything.
func (a *api) indexerStates(ctx context.Context) ([]health.IndexerState, error) {
	ix, err := a.deps.Indexers.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []health.IndexerState
	for _, i := range ix {
		if !a.deps.Indexers.Searched(i) {
			continue
		}
		s := health.IndexerState{ID: i.ID, Name: i.Name, Kind: string(i.Kind), Prowlarr: i.ProwlarrID > 0}
		if st, _, ok := a.deps.Indexers.Status(i.ID); ok {
			s.Failures, s.FailingSince, s.LastError = st.ConsecutiveFailures, st.FailingSince, st.LastError
			s.BackingOff = now.Before(st.BackoffUntil)
		}
		out = append(out, s)
	}
	return out, nil
}

// prowlarrState is the Prowlarr connection for its health check: whether a sync has
// saved a URL and key, how the latest sync went, and the indexers.
func (a *api) prowlarrState(ctx context.Context) health.ProwlarrState {
	st := health.ProwlarrState{
		Synced: a.deps.Settings.Get(ctx, keyProwlarrURL, "") != "" && a.deps.Settings.Get(ctx, keyProwlarrKey, "") != "",
	}
	if ls, ok := indexer.ReadProwlarrLastSync(ctx, a.deps.Settings); ok {
		st.LastSyncAt, st.LastSyncErr, st.FailedRuns = ls.At, ls.Error, ls.FailedRuns
	}
	st.Indexers, _ = a.indexerStates(ctx)
	return st
}

// flareSolverrDependents names the enabled indexers that can't get past Cloudflare
// without FlareSolverr: TorrentLeech and 1337x.
func (a *api) flareSolverrDependents(ctx context.Context) ([]string, error) {
	ix, err := a.deps.Indexers.List(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, i := range ix {
		if i.Enabled && (i.Kind == indexer.KindTorrentLeech || i.Kind == indexer.KindX1337) {
			names = append(names, i.Name)
		}
	}
	return names, nil
}

// recheckHealth re-runs the named checks in the background after something they judge was
// just changed (a key saved, a folder picked), so the panel catches up now rather than at
// the check's next turn.
func (a *api) recheckHealth(r *http.Request, keys ...string) {
	if a.deps.Health == nil {
		return
	}
	_, _, _ = a.submit(r, jobs.Spec{Kind: "health.check", Target: strings.Join(keys, ","), Timeout: time.Minute,
		Fn: errFn(func(ctx context.Context) error {
			for _, k := range keys {
				a.deps.Health.RunNow(ctx, k)
			}
			return nil
		})})
}

// tmdbValidator is the TMDB provider's key check (metadata.TMDB.Validate).
type tmdbValidator interface {
	Validate(ctx context.Context) error
}

// tmdbValidate maps TMDB's answer onto the health check's terms.
func tmdbValidate(ctx context.Context, v tmdbValidator) error {
	err := v.Validate(ctx)
	switch {
	case errors.Is(err, metadata.ErrInvalidKey):
		return health.ErrKeyRejected
	case errors.Is(err, metadata.ErrNotConfigured):
		return health.ErrKeyMissing
	}
	return err
}

// libraryState is the folders the user picked (saved in the app, not just the ones the
// app started with), for the library-folders check.
func (a *api) libraryState(ctx context.Context) health.LibraryState {
	return health.LibraryState{
		Folders:  a.healthFolders(ctx, a.booksEnabled(ctx), a.musicEnabled(ctx)),
		All:      a.healthFolders(ctx, true, true),
		DataDir:  a.deps.Config.DataDir,
		Recorded: a.recordedFilesUnder,
	}
}

// diskGuardState is the disk guard's view for the health check. While it's holding
// torrents the queue is read once, to count only the held ones the client still has.
func (a *api) diskGuardState(ctx context.Context) (health.DiskGuardState, bool) {
	g := a.deps.DiskGuard.Status(ctx)
	st := health.DiskGuardState{Enabled: g.Enabled, UsedPct: g.UsedPct, PausePct: g.PausePct, ResumePct: g.ResumePct, Holding: g.Holding,
		MeasureUnreadable: g.MeasureUnreadable, MeasureNotDisk: g.MeasureNotDisk,
		// With the override unusable, anything still measurable is a downloads folder on a
		// drive of its own.
		OtherVolumes: g.MeasureUnreadable && g.Measurable,
		MeasuredPath: g.MeasuredPath, ZFS: g.ZFS, FreeGB: gbOf(g.FreeBytes),
		PauseFreeGB: gbOf(g.PauseFreeBytes), ResumeFreeGB: gbOf(g.ResumeFreeBytes)}
	if g.Override || g.MeasureUnreadable {
		st.MeasurePath = g.MeasurePath
	}
	if g.Enabled && g.Holding > 0 && a.deps.Downloads != nil {
		if queue, err := a.deps.Downloads.Queue(ctx); err == nil {
			st.Holding = heldInQueue(a.deps.DiskGuard.Held(ctx), queue)
		}
	}
	return st, true
}

// heldInQueue counts the guard's held torrents that the client still has.
func heldInQueue(held map[string]bool, queue []download.Item) int {
	n := 0
	for _, it := range queue {
		if held[strings.ToLower(it.Hash)] {
			n++
		}
	}
	return n
}
