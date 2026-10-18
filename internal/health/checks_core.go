package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/libroots"
)

// The core checks: what has to be in place for anything to download and import at all.
// Each is built over a narrow function rather than a service, so tests can feed it fakes
// and the HTTP layer decides where the values come from.

// IndexersCheck reports when no indexer is enabled. enabled returns how many are; an
// error reading them says nothing (a database hiccup isn't "no indexers").
func IndexersCheck(enabled func(ctx context.Context) (int, error)) Check {
	return Check{
		Key: "indexers", Name: "Indexers", Category: CategoryIndexers,
		Run: func(ctx context.Context) []Finding {
			n, err := enabled(ctx)
			if err != nil || n > 0 {
				return nil
			}
			return []Finding{{
				Key: "indexers.none", Level: LevelError, Fix: FixIndexers,
				Message: "No indexers are enabled — Arrmada can't search for releases.",
			}}
		},
	}
}

// TroubledIndexer is an enabled indexer that isn't answering, from the integration status
// tracker (see internal/connstatus). Until is set while background searches are leaving it
// alone after repeated failures; FailingSince is when its current run of failures began.
type TroubledIndexer struct {
	ID           int64
	Name         string
	Failures     int
	FailingSince time.Time
	Until        time.Time // zero when it isn't paused
	LastError    string    // already redacted
}

// indexerFailingFor is how long an indexer must keep failing before it's worth a line when
// it isn't paused: a public tracker that times out now and then is still working, and the
// backoff ladder pauses a properly broken one well before an hour is up.
const indexerFailingFor = time.Hour

// indexerLinesMax is how many indexers get a line each. Past it they collapse into one, so
// a Prowlarr outage (every synced row failing at once) isn't a wall of warnings.
const indexerLinesMax = 3

// IndexerStatusCheck names each enabled indexer that is paused after repeated failures, or
// has been failing for over an hour, with its error and when it's next tried, and links to
// its card. More than three collapse into one line. A single failure isn't reported, and
// neither is a row an outage line already covers (every indexer failing, Prowlarr down —
// IndexerOutageCheck): the caller leaves those out. states returns how many indexers are
// enabled and which of them are failing; an error says nothing.
func IndexerStatusCheck(states func(ctx context.Context) (enabled int, failing []TroubledIndexer, err error)) Check {
	return indexerStatusCheck(states, time.Now)
}

func indexerStatusCheck(states func(ctx context.Context) (int, []TroubledIndexer, error), now func() time.Time) Check {
	return Check{
		Key: "indexers.status", Name: "Indexer status", Category: CategoryIndexers,
		Run: func(ctx context.Context) []Finding {
			_, failing, err := states(ctx)
			if err != nil {
				return nil
			}
			at := now()
			var bad []TroubledIndexer
			for _, t := range failing {
				if at.Before(t.Until) || (!t.FailingSince.IsZero() && at.Sub(t.FailingSince) >= indexerFailingFor) {
					bad = append(bad, t)
				}
			}
			switch {
			case len(bad) == 0:
				return nil
			case len(bad) > indexerLinesMax:
				return []Finding{{
					Key: "indexers.failing", Level: LevelWarning, Fix: FixIndexerStatus,
					Message: fmt.Sprintf("%d indexers are failing: %s. Automatic searches skip the paused ones until their next try.", len(bad), indexerNames(bad)),
				}}
			}
			out := make([]Finding, 0, len(bad))
			for _, t := range bad {
				key := fmt.Sprintf("indexers.failing.%d", t.ID)
				msg := fmt.Sprintf("%s has been failing since %s", t.Name, clock(t.FailingSince, at))
				if at.Before(t.Until) {
					key = fmt.Sprintf("indexers.paused.%d", t.ID)
					msg = fmt.Sprintf("%s has failed %d times in a row and is paused until %s — automatic searches skip it until then", t.Name, t.Failures, clock(t.Until, at))
				}
				if t.LastError != "" {
					msg += ". Last error: " + t.LastError
				}
				out = append(out, Finding{Key: key, Level: LevelWarning, Message: msg, Fix: ConnectionFix(fmt.Sprintf("indexer-%d", t.ID), "Check "+t.Name)})
			}
			return out
		},
	}
}

// indexerNames lists the first few names: "TorrentLeech, 1337x, EZTV and 2 more".
func indexerNames(ts []TroubledIndexer) string {
	const show = 3
	names := make([]string, 0, show)
	for i, t := range ts {
		if i == show {
			break
		}
		names = append(names, t.Name)
	}
	list := strings.Join(names, ", ")
	if n := len(ts) - len(names); n > 0 {
		list += fmt.Sprintf(" and %d more", n)
	}
	return list
}

// LibraryState is what the library-folders check judges: the folders in use (only the
// modules that are on), every folder setting (for the data-folder test, which applies
// whether or not the module is on), and Arrmada's data folder.
type LibraryState struct {
	Folders []Folder
	All     []Folder
	DataDir string
	// Recorded counts the files Arrmada has on record inside a folder. It's asked only
	// about a folder that's missing, to tell a share that isn't mounted (it held files)
	// from one that was never made. nil: unknown.
	Recorded func(ctx context.Context, folder string) (int, error)
}

// folderCheckEvery paces the checks that touch the library shares. They run whether or
// not anyone is looking, and on Unraid a look at a share can spin up a sleeping array
// disk; a share going missing is still noticed within minutes.
const folderCheckEvery = 5 * time.Minute

// LibraryFoldersCheck checks each folder the user picked is there and writable, and that
// none sits inside the data folder. Write probes go through probes, which trusts a pass
// for a while, for the same sleeping-disk reason. The timeout is generous because a disk
// spinning up takes several seconds to answer.
func LibraryFoldersCheck(src func(ctx context.Context) LibraryState, probes *ProbeCache) Check {
	return Check{
		Key: "library", Name: "Library folders", Category: CategoryStorage, Interval: folderCheckEvery, Timeout: 20 * time.Second,
		Run: func(ctx context.Context) []Finding {
			st := src(ctx)
			var out []Finding
			// Media in or above the data folder mixes it with the database, backups and
			// logs. Saving such a folder is refused now, but an older save or the
			// environment can still carry one and the app keeps running on it — so it's red.
			underData := map[string]bool{}
			for _, f := range st.All {
				if f.Path != "" && libroots.UnderDataDir(f.Path, st.DataDir) && !underData[f.Path] {
					underData[f.Path] = true
					out = append(out, Finding{
						Key: "library." + f.Role, Level: LevelError, Fix: FixLibraryFolders,
						Message: fmt.Sprintf("The %s folder (%s) is inside (or contains) Arrmada's data folder — move it to its own mount.", f.Label, f.Path),
					})
				}
			}
			for _, f := range st.Folders {
				if ctx.Err() != nil {
					break
				}
				if underData[f.Path] {
					continue // already reported, and nothing is probed inside the data folder
				}
				probe := probes.ProbeFolder(f.Path)
				if !probe.Exists && probe.Err == nil {
					probe.Recorded = RecordedUnknown
					if st.Recorded != nil {
						if n, err := st.Recorded(ctx, f.Path); err == nil {
							probe.Recorded = n
						}
					}
				}
				if level, msg := FolderProblem(f, probe); msg != "" {
					out = append(out, Finding{Key: "library." + f.Role, Level: level, Message: msg, Fix: FixLibraryFolders})
				}
			}
			return out
		},
	}
}

// RecordedUnknown is FolderState.Recorded when the records couldn't be read.
const RecordedUnknown = -1

// FolderProblem turns a folder probe into a health line, or "" when the folder is fine.
// A missing folder whose parent can be written is only a warning — it can still be made —
// but a missing parent means the share isn't mounted at all, and so does a missing folder
// Arrmada has files on record in.
//
// Imports never make a missing library folder themselves (a share that isn't mounted
// looks just like one that was never made, and recreating it would put the library on
// the container's own disk). So Create it is suggested only for a folder known to have
// held nothing; anywhere else the advice is to check the mount. The download client does
// make its own folder.
func FolderProblem(f Folder, st FolderState) (level, msg string) {
	switch {
	case !st.Exists && st.Err != nil:
		return LevelError, fmt.Sprintf("Arrmada can't look at the %s folder %s: %v.", f.Label, f.Path, st.Err)
	case !st.Exists && st.Recorded > 0:
		return LevelError, fmt.Sprintf("The %s folder %s is missing, but it held %d file%s Arrmada has on record — its share is probably not mounted. Check the mount (or the container's volume mapping); don't create the folder.", f.Label, f.Path, st.Recorded, plural(st.Recorded))
	case !st.Exists && st.ParentExists && st.ParentWritable && f.Role == "downloads":
		return LevelWarning, fmt.Sprintf("The %s folder %s doesn't exist yet. The download client makes it with the first download; if it should be an existing share, check the container's volume mapping.", f.Label, f.Path)
	case !st.Exists && st.ParentExists && st.ParentWritable && st.Recorded == RecordedUnknown:
		return LevelWarning, fmt.Sprintf("The %s folder %s doesn't exist, so nothing can be imported into it. If it should be an existing share, check that it's mounted (and the container's volume mapping); if it's new, create it in Settings → Library.", f.Label, f.Path)
	case !st.Exists && st.ParentExists && st.ParentWritable:
		return LevelWarning, fmt.Sprintf("The %s folder %s doesn't exist yet, so nothing can be imported into it. Press Create it beside it in Settings → Library; if it should be an existing share, check the container's volume mapping.", f.Label, f.Path)
	case !st.Exists && st.ParentExists:
		return LevelError, fmt.Sprintf("The %s folder %s doesn't exist, and Arrmada can't create it (check the container's volume mapping and the share's permissions).", f.Label, f.Path)
	case !st.Exists:
		return LevelError, fmt.Sprintf("The %s folder %s isn't there — the share isn't mounted.", f.Label, f.Path)
	case !st.IsDir:
		return LevelError, fmt.Sprintf("The %s folder %s is a file, not a folder.", f.Label, f.Path)
	case !st.Writable:
		return LevelError, fmt.Sprintf("Arrmada can't write to the %s folder %s (check PUID/PGID and the share's permissions).", f.Label, f.Path)
	}
	return "", ""
}

// RecycleBinProblem is one recycle bin the health panel should mention (the recycle bin
// service's Problems, without importing it).
type RecycleBinProblem struct {
	Dir        string
	Legacy     bool // the old shared bin from before bins lived on each library folder
	OtherDrive bool // on a different drive from the library it serves
	LegacyFull bool // the old shared bin, still holding deleted files
}

// RecycleDriveCheck reports the bins worth a line: a per-library bin on another drive from
// the library it serves turns every delete into a full copy, and the old shared bin still
// holding files only drains when someone empties it or it ages out. The legacy bin never
// gets the "every delete" line — only stray files go there now, so it would overstate it.
func RecycleDriveCheck(problems func() []RecycleBinProblem) Check {
	return Check{
		Key: "recycle.drive", Name: "Recycle bins", Category: CategoryStorage, Interval: folderCheckEvery, Timeout: 10 * time.Second,
		Run: func(ctx context.Context) []Finding {
			var out []Finding
			for _, p := range problems() {
				if p.OtherDrive && !p.Legacy {
					out = append(out, Finding{
						Key: "recycle.drive." + p.Dir, Level: LevelWarning, Fix: FixRecycleBin,
						Message: fmt.Sprintf("The recycle bin %s is on a different drive from your library, so every delete is a full copy, which is slow and fills that drive.", p.Dir),
					})
				}
				if p.LegacyFull {
					out = append(out, Finding{
						Key: "recycle.legacy", Level: LevelWarning, Fix: FixRecycleBin,
						Message: fmt.Sprintf("The old shared recycle bin (%s) still holds deleted files; new deletes no longer go there. Empty it under Recycle bin once you've checked what's in it.", p.Dir),
					})
				}
			}
			return out
		},
	}
}

// DiskGuardState is the disk guard as the health check sees it. Holding counts only held
// torrents the client still has (a torrent deleted while held would otherwise keep
// promising a resume that can never happen).
type DiskGuardState struct {
	Enabled   bool
	UsedPct   float64
	PausePct  int
	ResumePct int
	Holding   int
	// MeasurePath is the "measure a different drive" path when the reading comes from it
	// (or should), and MeasureUnreadable says it can't be read now (its mount is gone).
	MeasurePath       string
	MeasureUnreadable bool
}

// DiskGuardCheck says plainly when the disk guard is holding downloads: it's the single
// most confusing reason for "nothing is downloading", because everything else looks fine.
func DiskGuardCheck(state func(ctx context.Context) (DiskGuardState, bool)) Check {
	return Check{
		Key: "disk.guard", Name: "Disk guard", Category: CategoryDownloads, Timeout: 10 * time.Second,
		Run: func(ctx context.Context) []Finding {
			g, ok := state(ctx)
			if !ok || !g.Enabled {
				return nil
			}
			// The drive it was told to measure is gone: it isn't guarding anything, and it
			// deliberately doesn't fall back to the downloads folder (the reading that
			// setting replaces), so say so rather than stay quiet.
			if g.MeasureUnreadable {
				msg := fmt.Sprintf("The disk guard can't read the drive it's set to measure (%s), so it isn't pausing or resuming downloads. "+
					"Check that the path is still mapped into Arrmada's container, or clear it in the disk guard settings.", g.MeasurePath)
				if g.Holding == 1 {
					msg += " 1 torrent it paused stays paused until it can read the drive again."
				} else if g.Holding > 1 {
					msg += fmt.Sprintf(" %d torrents it paused stay paused until it can read the drive again.", g.Holding)
				}
				return []Finding{{Key: "disk.guard", Level: LevelWarning, Fix: FixDiskGuard, Message: msg}}
			}
			if g.Holding <= 0 {
				return nil
			}
			volume := "the downloads volume"
			if g.MeasurePath != "" {
				volume = "the drive the disk guard measures (" + g.MeasurePath + ")"
			}
			return []Finding{{
				Key: "disk.guard", Level: LevelWarning, Fix: FixDiskGuard,
				Message: fmt.Sprintf(
					"Downloads are paused: %s is %.1f%% full (pause at %d%%). "+
						"%d torrent%s will resume automatically once it drops below %d%%.",
					volume, g.UsedPct, g.PausePct, g.Holding, plural(g.Holding), g.ResumePct),
			}}
		},
	}
}

// diskFreeCheck is DownloadsFreeCheck over the Downloads folder alone.
func diskFreeCheck(dir func() string, free func(string) (float64, bool)) Check {
	return downloadsFreeCheck(func() []Folder { return []Folder{{Role: "downloads", Label: "Downloads", Path: dir()}} }, free, nil)
}

// DownloadsFreeCheck warns when a downloads volume is nearly full: the Downloads folder's
// and, when library folders have downloads folders of their own (DownloadFolders), each
// of theirs on another filesystem — a torrent filling the TV disk is as stuck as one
// filling the main downloads drive.
func DownloadsFreeCheck(dirs func() []Folder) Check {
	return downloadsFreeCheck(dirs, diskspace.FreeGB, diskspace.SameDevice)
}

func downloadsFreeCheck(dirs func() []Folder, free func(string) (float64, bool), same func(a, b string) (bool, bool)) Check {
	return Check{
		Key: "disk.free", Name: "Free space", Category: CategoryStorage,
		Run: func(ctx context.Context) []Finding {
			var out []Finding
			var measured []string
			for _, f := range dirs() {
				if strings.TrimSpace(f.Path) == "" {
					continue
				}
				// One volume, one line: folders sharing a filesystem are the same space.
				dup := false
				for _, m := range measured {
					if same != nil {
						if s, ok := same(m, f.Path); ok && s {
							dup = true
							break
						}
					}
				}
				if dup {
					continue
				}
				gb, ok := free(f.Path)
				if !ok {
					continue
				}
				measured = append(measured, f.Path)
				key, where := "disk.free", "the downloads volume"
				if f.Role != "downloads" {
					key, where = "disk.free."+f.Role, fmt.Sprintf("the volume of %s (%s)", f.Label, f.Path)
				}
				switch {
				case gb < 2:
					out = append(out, Finding{Key: key, Level: LevelError, Message: fmt.Sprintf("Very low free disk space (%.1f GB) on %s.", gb, where)})
				case gb < 10:
					out = append(out, Finding{Key: key, Level: LevelWarning, Message: fmt.Sprintf("Low free disk space (%.1f GB) on %s.", gb, where)})
				}
			}
			return out
		},
	}
}

// FolderDownloadsCheck flags a library folder whose own downloads folder (GitHub #11)
// isn't on the same filesystem as the library folder: that's the whole point of one, and
// with the two apart every import is a copy again. A hint, not a fault — a shared mount
// can report one filesystem id for two disks and the reverse — so it's a warning. That
// the folder is there and writable is the library-folders check's job.
func FolderDownloadsCheck(src func(ctx context.Context) []libroots.FolderDownloads) Check {
	return folderDownloadsCheck(src, diskspace.SameDevice)
}

func folderDownloadsCheck(src func(ctx context.Context) []libroots.FolderDownloads, same func(a, b string) (bool, bool)) Check {
	return Check{
		Key: "library.downloads", Name: "Library downloads folders", Category: CategoryStorage, Interval: folderCheckEvery, Timeout: 20 * time.Second,
		Run: func(ctx context.Context) []Finding {
			var out []Finding
			for _, d := range src(ctx) {
				if strings.TrimSpace(d.Path) == "" || strings.TrimSpace(d.Library) == "" {
					continue
				}
				if s, ok := same(d.Path, d.Library); !ok || s {
					continue
				}
				key := "library.downloads." + libroots.LibraryName(d.Media)
				if d.ID != 0 {
					key = fmt.Sprintf("%s-%d", key, d.ID)
				}
				out = append(out, Finding{
					Key: key, Level: LevelWarning, Fix: FixMoreFolders,
					Message: fmt.Sprintf("The downloads folder of %s (%s) isn't on the same filesystem as the library folder (%s), so its imports are copied, not hardlinked. Pick a downloads folder on the library's disk.", d.LibraryLabel, d.Path, d.Library),
				})
			}
			return out
		},
	}
}

// AudiobookServerCheck reports an audiobook server that's switched on but couldn't start
// (usually its port is taken). state returns whether it's on, whether it's running and
// the last start error.
func AudiobookServerCheck(state func(ctx context.Context) (on, running bool, lastErr string)) Check {
	return Check{
		Key: "audiobooks.server", Name: "Audiobook server", Category: CategoryIntegrations,
		Run: func(ctx context.Context) []Finding {
			on, running, lastErr := state(ctx)
			if !on || running {
				return nil
			}
			msg := "The audiobook server is switched on but isn't running, so listening apps can't connect."
			if lastErr != "" {
				msg += " " + lastErr
			}
			return []Finding{{Key: "audiobooks.server", Level: LevelError, Message: msg, Fix: FixAudiobookServer}}
		},
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
