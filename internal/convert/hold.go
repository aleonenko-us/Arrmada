package convert

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
)

// The originals hold. A converted original used to go to the shared recycle bin, whose size
// cap is enforced oldest-first every hour: a 60–110 GB 4K original pushed out everything
// older — the family's own deletions included — and was then purged itself, and with the
// converted file already at the original's path, Restore couldn't put it back anyway.
//
// Now the original moves to <library folder>/.arrmada-hold/<ledger id>/<its name>: on the
// same drive as the film (a rename, no copy), hidden from Plex (dot folder plus a
// .plexignore) and skipped by every scan (library.SkipScanDir). The ledger row records
// where it is and until when. Nothing in the hold is ever purged to make room: it leaves
// only when its undo window ends (ExpireHolds — the retention the owner set), when the
// owner releases it, or straight after the swap when the owner chose no undo at all.

// Settings (one key each). Read on every use, so a change applies to the next conversion.
const (
	keyHoldDays      = "convert_hold_days"   // days an original is kept so its conversion can be undone; 0 = no undo
	keyHoldMaxGB     = "convert_hold_max_gb" // how much the hold may keep, in GB, before conversions pause; 0 = no limit
	defaultHoldDays  = 14
	defaultHoldMaxGB = 500
	maxHoldDays      = 365
)

// The ledger's hold_state values.
const (
	HoldHeld     = "held"     // the original is in the hold and can be put back
	HoldReleased = "released" // its window ended (or you released it): the original is deleted
	HoldReverted = "reverted" // you put the original back
	HoldMissing  = "missing"  // the held file had gone when it was due to be released
)

// holdConfig is where holds may go: the library folders, read live, and the app's data
// folder, which media (and so a hold) must never be inside.
type holdConfig struct {
	roots   func() []libroots.Root
	dataDir string
}

// SetHoldRoots tells Convert which library folders exist (read on every use, so a folder
// changed in Settings applies at once) and where the app's data lives. Call it at startup,
// before Run. Until it's called nothing can be converted: there'd be nowhere to keep the
// original.
func (s *Service) SetHoldRoots(roots func() []libroots.Root, dataDir string) {
	s.holdCfg.Store(&holdConfig{roots: roots, dataDir: dataDir})
}

func (s *Service) holdDays(ctx context.Context) int {
	n, err := strconv.Atoi(strings.TrimSpace(s.settings.Get(ctx, keyHoldDays, strconv.Itoa(defaultHoldDays))))
	if err != nil || n < 0 {
		return defaultHoldDays
	}
	return n
}

// holdRootFor is the library folder whose hold takes src's original, or a reason (for
// Problems) why there is none.
func (s *Service) holdRootFor(src string) (root, why string) {
	cfg := s.holdCfg.Load()
	if cfg == nil || cfg.roots == nil {
		return "", "no library folders are set up, so there's nowhere to keep the original"
	}
	root = library.RootOf(cfg.roots(), src)
	if root == "" {
		return "", "the file isn't inside any of your library folders, so there's nowhere on its drive to keep the original"
	}
	if cfg.dataDir != "" && libroots.InDataDir(filepath.Join(root, library.HoldDirName), cfg.dataDir) {
		return "", "its library folder is inside the app's data folder, where media must never be kept"
	}
	return root, ""
}

// holdPathFor is where src's original goes in the hold for ledger row id
// (<root>/.arrmada-hold/<id>/<name>), creating the hold itself with its .plexignore. The
// row's own folder is made just before the original moves (makeHoldDir), so a swap
// abandoned earlier leaves nothing behind.
func (s *Service) holdPathFor(src string, id int64) (string, error) {
	root, why := s.holdRootFor(src)
	if why != "" {
		return "", errors.New(why)
	}
	top := filepath.Join(root, library.HoldDirName)
	if err := library.PrepareHiddenDir(top); err != nil {
		return "", err
	}
	return filepath.Join(top, strconv.FormatInt(id, 10), filepath.Base(src)), nil
}

// makeHoldDir creates a held file's own folder inside the hold.
func makeHoldDir(held string) error {
	if err := os.Mkdir(filepath.Dir(held), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// isHoldFile reports whether path has the shape of a held original
// (<…>/.arrmada-hold/<id>/<name>) — checked before anything in the hold is deleted, so a
// damaged ledger row can never point a delete at a library file.
func isHoldFile(path string, id int64) bool {
	dir := filepath.Dir(path)
	return filepath.Base(dir) == strconv.FormatInt(id, 10) && filepath.Base(filepath.Dir(dir)) == library.HoldDirName
}

// moveKept moves a file Convert is keeping (an original into the hold, or back out) without
// ever overwriting anything at to. On one filesystem it's a rename: instant, same inode.
// Across filesystems (a library folder spanning mounts) it's a copy to a temporary name,
// fsynced and renamed into place, and only then is from removed — at no point is the only
// copy of the file a partial one.
func (s *Service) moveKept(from, to string) error {
	if exists(to) {
		return fmt.Errorf("%s is already taken", to)
	}
	err := os.Rename(from, to)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	tmp := to + ".partial"
	started := time.Now()
	if err := copySynced(from, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if exists(to) {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s is already taken", to)
	}
	if err := os.Rename(tmp, to); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Remove(from); err != nil {
		_ = os.Remove(to) // from is still whole where it was
		return err
	}
	if s.log != nil {
		s.log.Warn("convert: slow hold — copied across filesystems instead of renaming",
			"from", from, "to", to, "size", humanBytes(fileSize(to)), "took", time.Since(started).Round(time.Second).String())
	}
	return nil
}

// copySynced copies from to a new file to and fsyncs it.
func copySynced(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// --- the startup self-check -----------------------------------------------------------

// holdStartup runs when the runner starts, after interrupted swaps are recovered: the
// self-check, and clearing bin_full skips an older version recorded (originals no longer go
// to the recycle bin, so nothing is waiting on it).
func (s *Service) holdStartup(ctx context.Context) {
	s.holdSelfCheck(ctx)
	if s.skips != nil && s.skips.clearKind(ctx, SkipBinFull) > 0 {
		s.invalidateLibraryCache()
	}
}

// HoldCheck is what the self-check found for one library folder: whether moving a file
// into its hold is an instant rename (same drive) or would be a full copy.
type HoldCheck struct {
	Root    string `json:"root"`
	Label   string `json:"label"` // "Movies", "TV", "Movies & TV"
	Instant bool   `json:"instant"`
	Err     string `json:"err,omitempty"`
}

type holdChecks struct {
	mu   sync.Mutex
	list []HoldCheck
}

// HoldChecks returns the latest self-check results (empty before the first run).
func (s *Service) HoldChecks() []HoldCheck {
	s.holdCheck.mu.Lock()
	defer s.holdCheck.mu.Unlock()
	return append([]HoldCheck{}, s.holdCheck.list...)
}

// holdSelfCheck proves, for each Movies and TV folder, that a file moves into its hold
// instantly: a 1 KB probe is written in the hold, renamed within the folder and compared
// before and after (same inode = a real rename, as on one Unraid share). No library file is
// touched. Run at startup; the result shows in Convert's settings.
func (s *Service) holdSelfCheck(ctx context.Context) []HoldCheck {
	cfg := s.holdCfg.Load()
	var out []HoldCheck
	if cfg != nil && cfg.roots != nil {
		labels := map[string][]string{}
		var order []string
		for _, r := range cfg.roots() {
			if (r.Name != "movies" && r.Name != "tv") || strings.TrimSpace(r.Path) == "" {
				continue
			}
			root, err := filepath.Abs(r.Path)
			if err != nil {
				continue
			}
			if _, seen := labels[root]; !seen {
				order = append(order, root)
			}
			labels[root] = append(labels[root], map[string]string{"movies": "Movies", "tv": "TV"}[r.Name])
		}
		for _, root := range order {
			if ctx.Err() != nil {
				break
			}
			c := probeHold(root, cfg.dataDir)
			c.Label = strings.Join(labels[root], " & ")
			out = append(out, c)
			if s.log != nil {
				switch {
				case c.Err != "":
					s.log.Warn("convert: originals hold self-check failed", "folder", root, "err", c.Err)
				case !c.Instant:
					s.log.Warn("convert: originals hold would copy rather than rename on this folder", "folder", root)
				default:
					s.log.Info("convert: originals hold self-check passed — instant moves", "folder", root)
				}
			}
		}
	}
	s.holdCheck.mu.Lock()
	s.holdCheck.list = out
	s.holdCheck.mu.Unlock()
	return out
}

func probeHold(root, dataDir string) HoldCheck {
	c := HoldCheck{Root: root}
	dir := filepath.Join(root, library.HoldDirName)
	if dataDir != "" && libroots.InDataDir(dir, dataDir) {
		c.Err = "this folder is inside the app's data folder — media must never be kept there"
		return c
	}
	if err := library.PrepareHiddenDir(dir); err != nil {
		c.Err = "couldn't make the hold folder: " + err.Error()
		return c
	}
	probe := filepath.Join(dir, ".selfcheck.probe")
	moved := filepath.Join(root, library.HoldDirName+".probe")
	defer os.Remove(probe)
	defer os.Remove(moved)
	if err := os.WriteFile(probe, make([]byte, 1024), 0o644); err != nil {
		c.Err = "couldn't write to the hold folder: " + err.Error()
		return c
	}
	before, err := os.Stat(probe)
	if err != nil {
		c.Err = err.Error()
		return c
	}
	if err := os.Rename(probe, moved); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			c.Err = "couldn't move a file within the folder: " + err.Error()
		}
		return c // EXDEV: not instant, but the copy fallback still works
	}
	after, err := os.Stat(moved)
	c.Instant = err == nil && os.SameFile(before, after)
	return c
}

// --- releasing --------------------------------------------------------------------------

// ErrNotHeld is returned when a ledger row's original isn't in the hold (any more), so it
// can't be released or put back.
var ErrNotHeld = errors.New("this conversion's original isn't in the hold")

// ErrSwapBusy is returned when the row's swap hasn't finished yet.
var ErrSwapBusy = errors.New("this conversion is still being swapped in — try again in a moment")

// ExpireHolds releases every held original whose undo window has ended — the hourly
// convert-hold-expire task. It is the only thing that deletes from the hold on its own, and
// it only follows the retention you set.
func (s *Service) ExpireHolds(ctx context.Context) error {
	if s.history == nil || s.history.db == nil {
		return nil
	}
	ids, err := s.history.dueHolds(ctx, time.Now().Unix())
	if err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.releaseHold(ctx, id, "its undo window ended"); err != nil && !errors.Is(err, ErrNotHeld) && !errors.Is(err, ErrSwapBusy) {
			s.log.Warn("convert: couldn't release a held original", "history_id", id, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ReleaseHold deletes one held original now (you chose to free its space) and returns the
// updated ledger row. The conversion can't be undone after this.
func (s *Service) ReleaseHold(ctx context.Context, id int64) (HistoryEntry, error) {
	return s.releaseHold(ctx, id, "you released it")
}

// releaseHold deletes a held original and marks its ledger row released, counting the
// space as saved only now. A held file already gone is marked missing and counts nothing.
func (s *Service) releaseHold(ctx context.Context, id int64, why string) (HistoryEntry, error) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	e, err := s.history.get(ctx, id)
	if err != nil {
		return e, err
	}
	if e.HoldState != HoldHeld || e.Outcome != OutcomeDone || e.HoldPath == "" {
		return e, ErrNotHeld
	}
	if busy, err := s.swapInFlight(ctx, id); err != nil || busy {
		if err == nil {
			err = ErrSwapBusy
		}
		return e, err
	}
	if !isHoldFile(e.HoldPath, id) {
		// Never delete on a path that doesn't look like ours.
		return e, fmt.Errorf("the recorded hold path %s isn't inside an originals hold — left alone", e.HoldPath)
	}
	state := HoldReleased
	switch err := os.Remove(e.HoldPath); {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		state = HoldMissing
	default:
		return e, err
	}
	// Anything else in the row's folder was moved aside with it (a stray file at the
	// converted file's name); it goes with the original.
	_ = os.RemoveAll(filepath.Dir(e.HoldPath))
	if err := s.history.endHold(ctx, id, state); err != nil {
		return e, err
	}
	if state == HoldReleased {
		if !e.ReclaimDeferred {
			s.addReclaimed(ctx, e.SrcSize-e.OutSize)
		}
		s.event("info", fmt.Sprintf("Released the original of %s (%s) — %s", e.Title, humanBytes(e.SrcSize), why))
	} else {
		s.event("warn", fmt.Sprintf("The held original of %s was already gone when it was due to be released", e.Title))
	}
	s.holdFreed(ctx)
	e.HoldState = state
	return e, nil
}

// holdFreed is told when the hold shrinks. CONV-09's budget hooks in here.
func (s *Service) holdFreed(ctx context.Context) {
	s.invalidateLibraryCache()
	s.wakeUp()
}

// swapInFlight reports whether a journalled swap still belongs to ledger row id.
func (s *Service) swapInFlight(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM convert_swaps WHERE history_id = ?`, id).Scan(&n)
	return n > 0, err
}

// --- the ledger's hold columns ----------------------------------------------------------

// setHold records where a row's original is held and until when ("" clears it).
func (h *historyStore) setHold(ctx context.Context, id int64, path string, until int64, state string) error {
	if h == nil || h.db == nil || id <= 0 {
		return ErrNoHistory
	}
	res, err := h.db.ExecContext(ctx,
		`UPDATE convert_history SET hold_path = ?, hold_until = ?, hold_state = ? WHERE id = ?`, path, until, state, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoHistory
	}
	return nil
}

// endHold marks a held row released or missing.
func (h *historyStore) endHold(ctx context.Context, id int64, state string) error {
	_, err := h.db.ExecContext(ctx,
		`UPDATE convert_history SET hold_state = ?, released_at = ? WHERE id = ? AND hold_state = ?`,
		state, time.Now().Unix(), id, HoldHeld)
	return err
}

// setOutcome rewrites a row's outcome after the fact — a swap the restart interrupted that
// startup recovery finished, or rolled back.
func (h *historyStore) setOutcome(ctx context.Context, id int64, outcome, note string) {
	if h == nil || h.db == nil || id <= 0 {
		return
	}
	_, _ = h.db.ExecContext(ctx,
		`UPDATE convert_history SET outcome = ?, note = ?, finished_at = CASE WHEN finished_at = 0 THEN ? ELSE finished_at END WHERE id = ?`,
		outcome, note, time.Now().Unix(), id)
}

// dueHolds lists the converted rows whose undo window has ended.
func (h *historyStore) dueHolds(ctx context.Context, now int64) ([]int64, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT id FROM convert_history WHERE hold_state = ? AND outcome = ? AND hold_until > 0 AND hold_until <= ? ORDER BY hold_until`,
		HoldHeld, OutcomeDone, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
