package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tristenlammi/arrmada/internal/buildinfo"
	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/health"
)

// statusDisk is one filesystem on System → Status: the first folder found on it, its
// space, and the other folders that turned out to share it (a whole Unraid array often
// holds every library, so it's shown once, not five times).
type statusDisk struct {
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	Measurable bool           `json:"measurable"`
	Total      uint64         `json:"total"`
	Free       uint64         `json:"free"`
	UsedPct    float64        `json:"used_pct"`
	SharedWith []statusFolder `json:"shared_with"`
}

type statusFolder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type statusBackup struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	File string    `json:"file"`
}

// dbFiles are the database's files on disk: the database and SQLite's write-ahead log and
// its index, which can hold a good share of recent writes between checkpoints.
var dbFiles = []string{"arrmada.db", "arrmada.db-wal", "arrmada.db-shm"}

// handleSystemStatus — GET /api/v1/system/status (admin): which build is running, how big
// the database is, when the last backup was taken, and how full each library's drive is.
// Paths are shown, so it's admin-only like the rest of System.
func (a *api) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dataDir := a.deps.Config.DataDir
	if a.deps.Store != nil {
		dataDir = a.deps.Store.DataDir()
	}
	var dbBytes int64
	for _, name := range dbFiles {
		if fi, err := os.Stat(filepath.Join(dataDir, name)); err == nil {
			dbBytes += fi.Size()
		}
	}

	var last *statusBackup
	if a.deps.Backups != nil {
		if list, err := a.deps.Backups.List(ctx); err == nil {
			for _, b := range list {
				if last == nil || b.CreatedAt.After(last.At) {
					last = &statusBackup{At: b.CreatedAt, Kind: string(b.Kind), File: b.Name}
				}
			}
		}
	}

	folders := health.LibraryFolders(a.pickedConfig(ctx), a.booksEnabled(ctx), a.musicEnabled(ctx))
	if dataDir != "" {
		folders = append(folders, health.Folder{Role: "data", Label: "Database & backups", Path: dataDir})
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"version":        buildinfo.Version,
		"commit":         buildinfo.Commit,
		"go":             runtime.Version(),
		"started_at":     a.start.UTC().Format(time.RFC3339),
		"uptime_seconds": int(time.Since(a.start).Seconds()),
		"data_dir":       dataDir,
		"db_bytes":       dbBytes,
		"last_backup":    last,
		"disks":          groupDisks(folders),
		// Whether a newer release is out (nil when no check is wired).
		"update": a.updateStatus(ctx),
	})
}

// groupDisks puts folders on one filesystem together, in the order given, using the same
// "same filesystem" test as the disk guard's shared-drive warning.
func groupDisks(folders []health.Folder) []statusDisk {
	out := []statusDisk{}
	for _, f := range folders {
		placed := false
		for i := range out {
			if same, ok := health.SameFilesystem(out[i].Path, f.Path); ok && same {
				out[i].SharedWith = append(out[i].SharedWith, statusFolder{Name: f.Label, Path: f.Path})
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		d := statusDisk{Name: f.Label, Path: f.Path, SharedWith: []statusFolder{}}
		if u, ok := diskspace.Of(f.Path); ok {
			d.Measurable, d.Total, d.Free, d.UsedPct = true, u.TotalBytes, u.FreeBytes, u.UsedPct
		}
		out = append(out, d)
	}
	return out
}
