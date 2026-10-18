package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tristenlammi/arrmada/internal/diskspace"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// A full downloads volume is the worst kind of failure here: qBittorrent errors every
// torrent at once, imports fail, and on a shared cache pool it takes everything else
// on that pool down with it. The guard stops downloads before the disk gets there.
//
// It pauses only what it needs to and remembers exactly which torrents it paused, so
// resuming never touches one you paused by hand. That list is persisted, so a restart
// mid-hold doesn't strand your queue.
const (
	KeyDiskGuard      = "downloads_disk_guard"
	KeyDiskGuardPause = "downloads_disk_guard_pause_pct"
	KeyDiskGuardResum = "downloads_disk_guard_resume_pct"
	// keyDiskGuardHeld is internal bookkeeping, not a preference — the hashes the
	// guard paused and therefore owns.
	keyDiskGuardHeld = "downloads_disk_guard_held"
	// keyDiskGuardHeldDirs is bookkeeping too: per held hash, the downloads folder whose
	// volume it was held for, when there's more than one (JSON object).
	keyDiskGuardHeldDirs = "downloads_disk_guard_held_dirs"
	// KeyDiskGuardMeasure is "Measure a different drive": a path whose filesystem the guard
	// reads instead of the Downloads folder's. Empty (the default) measures the Downloads
	// folder. It exists for Unraid, where the downloads folder sits in a user share
	// (/mnt/user, shfs) whose statfs is the merged array, while torrents land on the cache
	// pool first: mapping the pool read-only into the container (/mnt/cache → /cache) and
	// measuring that is the only way to see the drive that actually fills up. Downloads
	// still go to the downloads folder; only the measurement moves.
	KeyDiskGuardMeasure = "downloads_disk_guard_measure_path"
)

// Defaults. On by default: an unguarded full disk is a failure nobody opts into
// deliberately, and 85/80 leaves room to notice without pausing over normal churn.
const (
	DefaultDiskGuard      = true
	DefaultDiskGuardPause = 85
	DefaultDiskGuardResum = 80
)

// DiskGuard watches one volume and holds downloads while it's too full.
type DiskGuard struct {
	svc      *Service
	settings *settings.Service
	log      *slog.Logger
	dir      string // the volume to watch — where the download client writes
	// dirFn is the live downloads folder (Settings → Library), read on every pass so a
	// changed folder is watched from the next minute; nil = dir.
	dirFn func() string
	// dirsFn lists every downloads folder (SetDirsFunc); nil = the one above only.
	dirsFn func() []string
	// recorded is, per held hash, the downloads folder whose volume it was held for (per
	// volume only; persisted beside the held set), and carry the held hashes a pass leaves
	// alone that every save must keep. Both are set at the start of each Check (under mu).
	recorded map[string]string
	carry    []string
	// unlisted counts, per held hash, the passes in a row the queue didn't list it (a
	// client that's down): per volume, such a download is released after the same grace
	// as a deleted one once its volume has drained. Memory only, under mu.
	unlisted map[string]int
	// usage measures the volume. diskspace.Of in production; a test swaps it to put the
	// guard on either side of its thresholds without filling a real disk.
	usage func(path string) (diskspace.Usage, bool)
	// device is the filesystem id of a path (diskspace.Device in production), used to tell
	// which downloads folders share the Downloads folder's filesystem and so take the
	// "measure a different drive" reading. A test swaps it.
	device func(path string) (uint64, bool)
	// measureLost is whether the last pass found the "measure a different drive" path
	// unreadable, so the warning is logged once when it goes and once when it's back, not
	// every minute. Under mu.
	measureLost bool

	// mu serialises Check against itself. The scheduler won't overlap runs, but a
	// manual trigger from the API can land alongside one, and two passes racing
	// would double-pause and lose track of what's held.
	mu sync.Mutex
	// missed counts, per held hash, the passes in a row it was absent from a complete
	// queue (under mu). Memory only: a restart starts the count over, which only ever
	// keeps a hash longer.
	missed map[string]int
}

// NewDiskGuard wires a guard over the volume at dir.
func NewDiskGuard(svc *Service, set *settings.Service, log *slog.Logger, dir string) *DiskGuard {
	return &DiskGuard{svc: svc, settings: set, log: log, dir: dir, usage: diskspace.Of, device: diskspace.Device}
}

// SetDirFunc makes the watched folder live (see dirFn). Call it at startup, before the
// first Check.
func (g *DiskGuard) SetDirFunc(fn func() string) { g.dirFn = fn }

// SetDirsFunc makes the guard watch every downloads folder: the Downloads folder first,
// then each library folder's own downloads folder (GitHub #11), read on every pass. With
// more than one, each volume is judged on its own and only the downloads saving onto a
// full one are held. Call it at startup, before the first Check.
func (g *DiskGuard) SetDirsFunc(fn func() []string) { g.dirsFn = fn }

// path is the folder being watched now (the Downloads folder).
func (g *DiskGuard) path() string {
	if g.dirFn != nil {
		return strings.TrimSpace(g.dirFn())
	}
	return g.dir
}

// paths is every folder being watched now, the Downloads folder first, each once.
func (g *DiskGuard) paths() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p = strings.TrimSpace(p); p != "" && !seen[filepath.Clean(p)] {
			seen[filepath.Clean(p)] = true
			out = append(out, p)
		}
	}
	add(g.path())
	if g.dirsFn != nil {
		for _, p := range g.dirsFn() {
			add(p)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// measureOverride is the saved "measure a different drive" path, or "" for none.
func (g *DiskGuard) measureOverride(ctx context.Context) string {
	return strings.TrimSpace(g.settings.Get(ctx, KeyDiskGuardMeasure, ""))
}

// measurement is where one downloads folder's fullness is read from.
type measurement struct {
	path     string // the path whose filesystem is read
	override bool   // path is the "measure a different drive" setting
}

// measurer decides, for each downloads folder, where its fullness is read from.
//
// The rule with several downloads folders (GitHub #11): the override stands in for the
// Downloads folder and for every other downloads folder on the same filesystem as it (the
// same st_dev). On Unraid every /mnt/user share is one shfs filesystem, so all the
// downloads folders inside user shares are measured on the pool, which is where their
// torrents land first. A downloads folder on a filesystem of its own (a disk mounted
// directly) keeps its own measurement: the override says nothing about that disk. When a
// folder's filesystem id can't be read (off Linux, or a folder that doesn't exist), only
// the Downloads folder itself takes the override.
type measurer struct {
	over    string
	main    string
	mainDev uint64
	mainOK  bool
	device  func(string) (uint64, bool)
}

func (g *DiskGuard) measurer(ctx context.Context, main string) measurer {
	m := measurer{over: g.measureOverride(ctx), main: filepath.Clean(main), device: g.device}
	if m.over != "" && main != "" && g.device != nil {
		m.mainDev, m.mainOK = g.device(main)
	}
	return m
}

// of is where the downloads folder d is measured.
func (m measurer) of(d string) measurement {
	if m.over == "" {
		return measurement{path: d}
	}
	if filepath.Clean(d) == m.main {
		return measurement{path: m.over, override: true}
	}
	if m.mainOK && d != "" {
		if dev, ok := m.device(d); ok && dev == m.mainDev {
			return measurement{path: m.over, override: true}
		}
	}
	return measurement{path: d}
}

// noteMeasure logs, once per change, that the "measure a different drive" path has become
// unreadable or readable again. measured is "" when no such path is set: nothing is logged
// then, a cleared setting isn't news. Called under mu.
func (g *DiskGuard) noteMeasure(measured string, lost bool) {
	if measured == "" {
		g.measureLost = false
		return
	}
	if lost == g.measureLost {
		return
	}
	g.measureLost = lost
	if lost {
		g.log.Warn("disk guard: can't read the drive it's set to measure — pausing and resuming nothing until it can; check the path is still mapped into the container",
			"path", measured)
		return
	}
	g.log.Info("disk guard: the drive it measures can be read again", "path", measured)
}

// CheckMeasurePath checks a path for "measure a different drive" before it's saved, and
// returns it cleaned. Empty is fine: measure the Downloads folder. The error is written for
// the person who typed the path.
func (g *DiskGuard) CheckMeasurePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	// The server is Linux, so a full path starts with "/" whatever machine runs the tests.
	if !strings.HasPrefix(p, "/") {
		return "", errors.New("enter a full path starting with /, like /cache")
	}
	p = path.Clean(p)
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s doesn't exist inside Arrmada's container — add it as a path in the container's settings first", p)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s isn't a folder", p)
	}
	if u, ok := g.usage(p); !ok || u.TotalBytes == 0 {
		return "", fmt.Errorf("can't read how full the drive at %s is", p)
	}
	// Measuring the downloads folder's own filesystem through another path changes nothing,
	// and would hide that the setting isn't doing what it was set for.
	if dl := g.path(); dl != "" && g.device != nil {
		a, okA := g.device(p)
		b, okB := g.device(dl)
		if okA && okB && a == b {
			return "", fmt.Errorf("%s is on the same drive as the downloads folder (%s) — that's already what the guard measures", p, dl)
		}
	}
	return p, nil
}

// GuardStatus is what the guard is currently doing, for the API and the health panel.
// With several downloads folders, Path and UsedPct are the fullest one's, and Volumes
// lists each.
type GuardStatus struct {
	Enabled    bool    `json:"enabled"`
	Measurable bool    `json:"measurable"` // false = this platform/path can't be measured
	Path       string  `json:"path"`       // the downloads folder
	UsedPct    float64 `json:"used_pct"`
	PausePct   int     `json:"pause_pct"`
	ResumePct  int     `json:"resume_pct"`
	Holding    int     `json:"holding"` // torrents the guard has paused
	// MeasurePath is the saved "measure a different drive" path ("" = none).
	MeasurePath string `json:"measure_path"`
	// MeasuredPath is the path whose drive UsedPct is read from: MeasurePath when Override,
	// else Path.
	MeasuredPath string `json:"measured_path"`
	Override     bool   `json:"override"`
	// MeasureUnreadable is a saved MeasurePath that can't be read now (its mount is gone):
	// the guard then pauses and resumes nothing for the folders it stands in for.
	MeasureUnreadable bool   `json:"measure_unreadable"`
	TotalBytes        uint64 `json:"total_bytes"`
	FreeBytes         uint64 `json:"free_bytes"`
	// Volumes is each downloads folder watched, when there's more than one.
	Volumes []GuardVolume `json:"volumes,omitempty"`
}

// GuardVolume is one watched downloads folder and how full its disk is.
type GuardVolume struct {
	Path         string  `json:"path"`
	Measurable   bool    `json:"measurable"`
	UsedPct      float64 `json:"used_pct"`
	MeasuredPath string  `json:"measured_path"`
	Override     bool    `json:"override"`
	TotalBytes   uint64  `json:"total_bytes"`
	FreeBytes    uint64  `json:"free_bytes"`
}

// Status reports the guard's current view without changing anything.
func (g *DiskGuard) Status(ctx context.Context) GuardStatus {
	pause, resume := g.thresholds(ctx)
	dirs := g.paths()
	mr := g.measurer(ctx, dirs[0])
	first := mr.of(dirs[0])
	st := GuardStatus{
		Enabled:      g.settings.GetBool(ctx, KeyDiskGuard, DefaultDiskGuard),
		Path:         dirs[0],
		PausePct:     pause,
		ResumePct:    resume,
		Holding:      len(g.held(ctx)),
		MeasurePath:  mr.over,
		MeasuredPath: first.path,
		Override:     first.override,
	}
	read := map[string]*diskspace.Usage{} // one statfs per measured path
	for _, d := range dirs {
		m := mr.of(d)
		v := GuardVolume{Path: d, MeasuredPath: m.path, Override: m.override}
		u, seen := read[m.path]
		if !seen {
			if got, ok := g.usage(m.path); ok {
				u = &got
			}
			read[m.path] = u
		}
		if u != nil {
			v.Measurable, v.UsedPct, v.TotalBytes, v.FreeBytes = true, u.UsedPct, u.TotalBytes, u.FreeBytes
			// The fullest volume is the one that decides whether anything is held.
			if !st.Measurable || u.UsedPct > st.UsedPct {
				st.Measurable, st.UsedPct, st.Path = true, u.UsedPct, d
				st.MeasuredPath, st.Override, st.TotalBytes, st.FreeBytes = m.path, m.override, u.TotalBytes, u.FreeBytes
			}
		} else if m.override {
			st.MeasureUnreadable = true
		}
		if len(dirs) > 1 {
			st.Volumes = append(st.Volumes, v)
		}
	}
	return st
}

// Held returns the hashes the guard has paused and owns, lowercased for comparison
// against a client's queue.
func (g *DiskGuard) Held(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, h := range g.held(ctx) {
		out[h] = true
	}
	return out
}

// Engaged reports whether the guard is actively holding downloads right now: it's on,
// the volume can be measured and is still above the resume point, and it owns
// something. While engaged, resuming a held torrent by hand would only fill the disk the
// guard is protecting — and the guard would pause it again on its next pass.
func (g *DiskGuard) Engaged(ctx context.Context) bool {
	st := g.Status(ctx)
	return st.Enabled && st.Measurable && st.UsedPct > float64(st.ResumePct) && st.Holding > 0
}

// thresholds reads the pause/resume points, repairing anything nonsensical.
//
// A resume point at or above the pause point would pause and resume on alternate
// passes forever, hammering the client — so it is forced below rather than trusted.
func (g *DiskGuard) thresholds(ctx context.Context) (pause, resume int) {
	pause = g.intSetting(ctx, KeyDiskGuardPause, DefaultDiskGuardPause)
	resume = g.intSetting(ctx, KeyDiskGuardResum, DefaultDiskGuardResum)
	if pause < 1 {
		pause = 1
	}
	if pause > 99 {
		pause = 99
	}
	if resume >= pause {
		resume = pause - 1
	}
	if resume < 0 {
		resume = 0
	}
	return pause, resume
}

func (g *DiskGuard) intSetting(ctx context.Context, key string, def int) int {
	n, err := strconv.Atoi(g.settings.Get(ctx, key, ""))
	if err != nil {
		return def
	}
	return n
}

// Check runs one pass: pause if the volume is too full, resume once it has drained
// back below the resume point. Safe to call on a timer.
func (g *DiskGuard) Check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	held := g.held(ctx)
	g.carry, g.recorded = nil, nil

	if !g.settings.GetBool(ctx, KeyDiskGuard, DefaultDiskGuard) {
		// Turning the guard off must not strand whatever it was holding.
		if len(held) > 0 {
			g.log.Info("disk guard disabled — releasing the downloads it was holding", "torrents", len(held))
			g.resume(ctx, held)
		}
		return nil
	}

	dirs := g.paths()
	pause, resume := g.thresholds(ctx)
	// What each held download was held for, and the ones held for a downloads folder no
	// longer watched (a library folder's own one, cleared or removed while it was full):
	// those are judged by their own volume, never released with the rest.
	g.carry, g.recorded = nil, g.heldDirs(ctx)
	mr := g.measurer(ctx, dirs[0])
	held = g.releaseStrays(ctx, held, dirs, resume, mr)
	if len(dirs) > 1 {
		g.checkVolumes(ctx, held, dirs, mr)
		return nil
	}
	dir := dirs[0]
	m := mr.of(dir)
	u, ok := g.usage(m.path)
	g.noteMeasure(mr.over, m.override && !ok)
	if !ok {
		// Can't measure: do nothing at all. Guessing here would either pause a
		// perfectly healthy queue or give false assurance. That goes for an unreadable
		// "measure a different drive" path too: falling back to the downloads folder
		// would read exactly the number that setting is there to avoid (the whole array,
		// on Unraid), so what's held stays held until the drive can be read again.
		return nil
	}

	switch {
	case u.UsedPct >= float64(pause):
		g.pauseActive(ctx, held, u.UsedPct, pause, m.path)
	case u.UsedPct <= float64(resume) && len(held) > 0:
		g.log.Info("disk guard: space recovered, resuming downloads",
			"used_pct", round1(u.UsedPct), "resume_at_pct", resume, "torrents", len(held), "path", m.path)
		g.resume(ctx, held)
	case len(held) > 0:
		// Between the two lines and still holding: nothing to pause or release, but drop
		// torrents deleted in the meantime so "holding N" stays true.
		items, whole, err := g.svc.QueueComplete(ctx)
		if err != nil {
			return nil
		}
		kept, missed := prunedHeld(held, items, whole, g.missed)
		g.missed = missed
		if len(kept) != len(held) {
			g.save(ctx, kept)
		}
	}
	return nil
}

// pruneAfterMisses is how many passes in a row a held hash must be missing from a
// complete queue before the guard forgets it.
const pruneAfterMisses = 2

// prunedHeld drops held hashes the client no longer has, and returns the updated
// missed-pass counts. Forgetting a hash wrongly is the costly mistake — that torrent
// then stays paused for good, since the guard only resumes what it holds — so it's
// cautious three ways. Only a complete queue (every client answered) counts; with one
// client down its torrents are just missing from the list. An empty queue doesn't
// count either: a qBittorrent that has just restarted answers with nothing (or a
// partial list) while it's still loading torrents. And a hash must be missing on two
// passes in a row, which outlasts that loading window.
func prunedHeld(held []string, items []Item, whole bool, missed map[string]int) ([]string, map[string]int) {
	if !whole || len(items) == 0 {
		return held, missed
	}
	present := make(map[string]bool, len(items))
	for _, it := range items {
		present[strings.ToLower(it.Hash)] = true
	}
	kept := make([]string, 0, len(held))
	next := map[string]int{}
	for _, h := range held {
		if present[h] {
			kept = append(kept, h)
			continue
		}
		if n := missed[h] + 1; n < pruneAfterMisses {
			next[h] = n
			kept = append(kept, h)
		}
	}
	return kept, next
}

// pauseActive pauses every torrent still pulling data, and adds them to the held set.
//
// Seeding torrents are deliberately left alone: they aren't writing anything, and
// pausing them would stall seed goals and put the private trackers' hit-and-run
// clocks at risk for a problem they aren't causing.
//
// A torrent the guard already holds that is downloading again was resumed behind its
// back — "Resume all", or by hand in qBittorrent. It is paused again: the volume is
// still over the line, and leaving it running is exactly how the cache pool filled
// with the guard switched on.
func (g *DiskGuard) pauseActive(ctx context.Context, held []string, usedPct float64, pausePct int, measured string) {
	items, whole, err := g.svc.QueueComplete(ctx)
	if err != nil {
		g.log.Warn("disk guard: could not read the queue", "err", err)
		return
	}
	kept, missed := prunedHeld(held, items, whole, g.missed)
	g.missed = missed
	changed := len(kept) != len(held)
	owned := map[string]bool{}
	for _, h := range kept {
		owned[h] = true
	}

	var newly []string
	repaused := 0
	for _, it := range items {
		if it.State != "downloading" {
			continue
		}
		h := strings.ToLower(it.Hash)
		if err := g.svc.Pause(ctx, it.Hash); err != nil {
			g.log.Warn("disk guard: could not pause a download", "name", it.Name, "err", err)
			continue
		}
		if owned[h] {
			repaused++
			continue
		}
		newly = append(newly, h)
		owned[h] = true
	}
	if repaused > 0 {
		g.log.Warn("disk guard: paused downloads again that were resumed while the volume is still too full",
			"used_pct", round1(usedPct), "pause_at_pct", pausePct, "repaused", repaused, "path", measured)
	}
	if len(newly) > 0 {
		// Loud on purpose: "nothing is downloading" is otherwise a mystery, and this is
		// the first place anyone will look for the reason.
		g.log.Warn("disk guard: paused downloads — the volume is too full",
			"used_pct", round1(usedPct), "pause_at_pct", pausePct,
			"paused", len(newly), "path", measured)
		changed = true
	}
	if changed {
		g.save(ctx, append(kept, newly...))
	}
}

// resume restarts the held torrents and clears the set. A hash that no longer exists
// (deleted while held) fails harmlessly and is dropped either way — keeping it would
// leave the guard holding a ghost forever.
func (g *DiskGuard) resume(ctx context.Context, held []string) {
	for _, h := range held {
		if err := g.svc.Resume(ctx, h); err != nil {
			g.log.Debug("disk guard: could not resume a download", "hash", h, "err", err)
		}
	}
	g.save(ctx, nil)
}

// held returns the hashes the guard has paused, lowercased: clients and older saved
// sets don't agree on case, and a case mismatch would read a held torrent as unowned.
func (g *DiskGuard) held(ctx context.Context) []string {
	raw := g.settings.Get(ctx, keyDiskGuardHeld, "")
	if raw == "" {
		return nil
	}
	var stored []string
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil
	}
	out := make([]string, 0, len(stored))
	seen := map[string]bool{}
	for _, h := range stored {
		h = strings.ToLower(h)
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// save stores the held set: hashes plus whatever this pass carries (g.carry), and, for
// those that have one, the folder each was held for.
func (g *DiskGuard) save(ctx context.Context, hashes []string) {
	hashes = dedupe(append(append([]string(nil), hashes...), g.carry...))
	g.saveDirs(ctx, hashes)
	if len(hashes) == 0 {
		if err := g.settings.Set(ctx, keyDiskGuardHeld, ""); err != nil {
			g.log.Warn("disk guard: could not clear the held set", "err", err)
		}
		return
	}
	sort.Strings(hashes)
	b, err := json.Marshal(hashes)
	if err != nil {
		return
	}
	if err := g.settings.Set(ctx, keyDiskGuardHeld, string(b)); err != nil {
		// Worth a warning: if this doesn't stick, a restart forgets what it paused
		// and those torrents stay paused until someone notices.
		g.log.Warn("disk guard: could not record which downloads it paused", "err", err)
	}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// heldDirs reads, per held hash, the downloads folder it was held for ({} when none).
func (g *DiskGuard) heldDirs(ctx context.Context) map[string]string {
	out := map[string]string{}
	if raw := g.settings.Get(ctx, keyDiskGuardHeldDirs, ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// saveDirs stores g.recorded trimmed to the hashes still held.
func (g *DiskGuard) saveDirs(ctx context.Context, hashes []string) {
	keep := map[string]string{}
	for _, h := range hashes {
		if d := g.recorded[h]; d != "" {
			keep[h] = d
		}
	}
	g.recorded = keep
	val := ""
	if len(keep) > 0 {
		b, _ := json.Marshal(keep)
		val = string(b)
	}
	if val == g.settings.Get(ctx, keyDiskGuardHeldDirs, "") {
		return
	}
	if err := g.settings.Set(ctx, keyDiskGuardHeldDirs, val); err != nil {
		g.log.Warn("disk guard: could not record which disks it's holding downloads for", "err", err)
	}
}

// releaseStrays deals with held downloads recorded for a downloads folder that is no
// longer watched (a library folder's own one, cleared or removed while its disk was
// full): each stays held while that folder's volume is still above the resume line, and
// is released once it has drained — or can't be measured any more, since nothing would
// ever release it then. They're carried through the rest of the pass untouched; the rest
// of held is returned for the pass to judge as usual.
//
// A stray folder on the Downloads folder's filesystem is measured where that is (the
// "measure a different drive" path, when set), and an unreadable one of those keeps its
// downloads held: that path is expected to come back, unlike a folder that was removed.
func (g *DiskGuard) releaseStrays(ctx context.Context, held, dirs []string, resume int, mr measurer) []string {
	watched := map[string]bool{}
	for _, d := range dirs {
		watched[filepath.Clean(d)] = true
	}
	var rest []string
	released := 0
	for _, h := range held {
		d := g.recorded[h]
		if d == "" || watched[filepath.Clean(d)] {
			rest = append(rest, h)
			continue
		}
		m := mr.of(d)
		if u, ok := g.usage(m.path); (ok && u.UsedPct > float64(resume)) || (!ok && m.override) {
			g.carry = append(g.carry, h)
			continue
		}
		if err := g.svc.Resume(ctx, h); err != nil {
			g.log.Debug("disk guard: could not resume a download", "hash", h, "err", err)
		}
		released++
	}
	if released > 0 {
		g.log.Info("disk guard: resumed downloads held for a downloads folder no longer watched — its disk has room again",
			"torrents", released)
		g.save(ctx, rest)
	}
	return rest
}

// checkVolumes is Check with several downloads folders (a library folder's own downloads
// folder on another disk, GitHub #11). Each volume is judged on its own: a download is
// held while the volume it saves onto is over the pause line, and released once that
// volume — not the fullest one — has drained below the resume line. A download is placed
// by its content path; one in none of the folders counts as the Downloads folder's, the
// client's default. A volume that can't be measured holds nothing and releases nothing,
// as with one folder.
//
// A held download is judged by the folder it was held for (recorded), else by where its
// content is. One the queue doesn't list — its client is down — is released after the
// same grace a deleted one gets (pruneAfterMisses passes) once that folder's volume has
// drained, as the single-folder guard would; it isn't held for ever on a client's say.
//
// With "measure a different drive" set, each folder's volume is read where mr says (see
// measurer): the override for the folders on the Downloads folder's filesystem, their own
// for the rest. An unreadable override is an unmeasurable volume like any other — its
// folders hold nothing new and release nothing.
func (g *DiskGuard) checkVolumes(ctx context.Context, held, dirs []string, mr measurer) {
	pause, resume := g.thresholds(ctx)
	use := map[string]diskspace.Usage{}
	full := false
	lost := false
	for _, d := range dirs {
		m := mr.of(d)
		if u, ok := g.usage(m.path); ok {
			use[d] = u
			full = full || u.UsedPct >= float64(pause)
		} else if m.override {
			lost = true
		}
	}
	g.noteMeasure(mr.over, lost)
	if len(use) == 0 || (!full && len(held) == 0) {
		return
	}
	items, whole, err := g.svc.QueueComplete(ctx)
	if err != nil {
		if full {
			g.log.Warn("disk guard: could not read the queue", "err", err)
		}
		// Nothing can be paused, but a held download whose client stays down is still
		// released below once its volume has drained.
		items, whole = nil, false
	}
	if g.recorded == nil {
		g.recorded = map[string]string{}
	}
	kept, missed := prunedHeld(held, items, whole, g.missed)
	g.missed = missed
	owned := map[string]bool{}
	for _, h := range kept {
		owned[h] = true
	}
	byHash := map[string]Item{}
	for _, it := range items {
		byHash[strings.ToLower(it.Hash)] = it
	}
	volumeOf := func(it Item) (string, diskspace.Usage, bool) {
		d := folderOf(it.ContentPath, dirs)
		u, ok := use[d]
		return d, u, ok
	}

	// Hold what is still pulling data onto a full volume. Seeding torrents are left alone,
	// as with one folder: they write nothing.
	pausedNow := map[string]bool{}
	var newly []string
	for _, it := range items {
		if it.State != "downloading" {
			continue
		}
		d, u, ok := volumeOf(it)
		if !ok || u.UsedPct < float64(pause) {
			continue
		}
		h := strings.ToLower(it.Hash)
		if err := g.svc.Pause(ctx, it.Hash); err != nil {
			g.log.Warn("disk guard: could not pause a download", "name", it.Name, "err", err)
			continue
		}
		pausedNow[h] = true
		g.recorded[h] = d
		if owned[h] {
			g.log.Warn("disk guard: paused a download again that was resumed while its volume is still too full",
				"name", it.Name, "used_pct", round1(u.UsedPct), "pause_at_pct", pause, "path", d)
			continue
		}
		g.log.Warn("disk guard: paused a download — its volume is too full",
			"name", it.Name, "used_pct", round1(u.UsedPct), "pause_at_pct", pause, "path", d)
		newly = append(newly, h)
		owned[h] = true
	}

	// Release what was held on a volume that has drained: the folder it was held for when
	// that's known, else the one its content is in.
	drained := func(h string, it *Item) bool {
		d := g.recorded[h]
		if d == "" && it != nil {
			d, _, _ = volumeOf(*it)
		}
		if d == "" {
			d = dirs[0]
		}
		u, ok := use[d]
		return ok && u.UsedPct <= float64(resume)
	}
	unlisted := map[string]int{}
	var still []string
	released := 0
	for _, h := range append(kept, newly...) {
		if pausedNow[h] {
			still = append(still, h)
			continue
		}
		it, listed := byHash[h]
		if !listed {
			// A partial read or a client restarting: held until it's seen again, or for a
			// client that stays down, released after the grace once its volume drained.
			n := g.unlisted[h] + 1
			if n < pruneAfterMisses || !drained(h, nil) {
				unlisted[h] = n
				still = append(still, h)
				continue
			}
			if err := g.svc.Resume(ctx, h); err != nil {
				g.log.Debug("disk guard: could not resume a download its client didn't list", "hash", h, "err", err)
			}
			released++
			continue
		}
		if drained(h, &it) {
			if err := g.svc.Resume(ctx, it.Hash); err != nil {
				g.log.Debug("disk guard: could not resume a download", "hash", h, "err", err)
			}
			released++
			continue
		}
		still = append(still, h)
	}
	g.unlisted = unlisted
	if released > 0 {
		g.log.Info("disk guard: space recovered, resuming downloads", "resume_at_pct", resume, "torrents", released)
	}
	if len(pausedNow) > 0 || released > 0 || len(still) != len(held) {
		g.save(ctx, still)
	}
}

// folderOf is the watched folder a download's content path lies in (the longest that
// holds it), else the first: the Downloads folder, where the client saves by default.
func folderOf(content string, dirs []string) string {
	best := dirs[0]
	bestLen := -1
	if content == "" || !filepath.IsAbs(content) {
		return best
	}
	c := filepath.Clean(content)
	for _, d := range dirs {
		dc := filepath.Clean(d)
		if c == dc || strings.HasPrefix(c, strings.TrimSuffix(dc, string(filepath.Separator))+string(filepath.Separator)) {
			if len(dc) > bestLen {
				best, bestLen = d, len(dc)
			}
		}
	}
	return best
}
