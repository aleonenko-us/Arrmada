package movies

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/outbox"
)

// A rename is planned for every track first and shown to the person; only then does
// anything move. The plan is read again when it's applied, and a track whose file or
// proposed name changed in between is left alone. Nothing is ever moved onto an existing
// file: a target that's taken is a conflict, reported and skipped.

// What a planned track rename will do (RenameItem.Status).
const (
	RenameOK       = "ok"       // the file moves to Proposed
	RenameSame     = "same"     // already named right
	RenameConflict = "conflict" // something else has the new name; the file stays put
	RenameMissing  = "missing"  // the recorded file isn't on disk
)

// Why a track was left alone.
const (
	reasonTaken        = "a different file already exists there"
	reasonOtherTrack   = "another version of this movie has that name"
	reasonSameName     = "another version of this movie would get the same name"
	reasonNotOnDisk    = "the file isn't on disk"
	reasonChanged      = "changed since the preview"
	reasonAlreadyNamed = "already named correctly"
	reasonMovieGone    = "the movie is no longer in the library"
)

// RenameItem is one track's planned rename. Media says what kind of title it is
// ("movie"), so series can share the organize endpoints' shape later.
type RenameItem struct {
	Media     string `json:"media"`
	MovieID   int64  `json:"movie_id"`
	VersionID int64  `json:"version_id"` // 0 = the default track
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	Label     string `json:"label"`
	Current   string `json:"current"`
	Proposed  string `json:"proposed"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// RenameResult is what applying one planned item did.
type RenameResult struct {
	MovieID   int64  `json:"movie_id"`
	VersionID int64  `json:"version_id"`
	Label     string `json:"label"`
	From      string `json:"from"`
	To        string `json:"to"`
	Renamed   bool   `json:"renamed"`
	Reason    string `json:"reason,omitempty"` // why it was left alone
}

// libraryPlanThreshold: asking for more titles than this reads the whole library in two
// queries instead of two per title.
const libraryPlanThreshold = 50

// RenamePlan lists every track of the given movies (all of them when ids is empty) with
// where its file is and where the naming scheme puts it. It changes nothing: one stat
// per file, one per proposed name. Ids that aren't in the library are skipped.
func (s *Service) RenamePlan(ctx context.Context, ids []int64) ([]RenameItem, error) {
	var out []RenameItem
	if len(ids) == 0 || len(ids) > libraryPlanThreshold {
		byMovie, all, err := s.LibraryVersionRows(ctx)
		if err != nil {
			return nil, err
		}
		want := map[int64]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for _, m := range all {
			if len(ids) > 0 && !want[m.ID] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out = append(out, s.planMovie(m, byMovie[m.ID])...)
		}
		return out, nil
	}
	for _, id := range ids {
		m, versions, err := s.trackRows(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, s.planMovie(m, versions)...)
	}
	return out, nil
}

// trackRows reads a movie and all its tracks from the database. Unlike VersionRows it
// fails when the extra tracks can't be read: a plan missing a track could hand that
// track's name to another.
func (s *Service) trackRows(ctx context.Context, id int64) (Movie, []Version, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return Movie{}, nil, err
	}
	extras, err := s.repo.ListVersions(ctx, id)
	if err != nil {
		return Movie{}, nil, err
	}
	return m, append([]Version{defaultVersionRow(m)}, extras...), nil
}

// planMovie plans one movie's tracks. A file two tracks both record is planned once,
// under the first; applying it repoints both.
func (s *Service) planMovie(m Movie, versions []Version) []RenameItem {
	owned := map[string]int64{} // every file this movie's tracks record → its first track
	for _, v := range versions {
		if v.HasFile && v.FilePath != "" {
			if _, ok := owned[v.FilePath]; !ok {
				owned[v.FilePath] = v.ID
			}
		}
	}
	targets := s.trackTargets(m, versions)
	var items []RenameItem
	claimed := map[string]bool{}
	for _, v := range versions {
		if !v.HasFile || v.FilePath == "" || owned[v.FilePath] != v.ID {
			continue
		}
		it := RenameItem{Media: "movie", MovieID: m.ID, VersionID: v.ID, Title: m.Title, Year: m.Year,
			Label: v.Label, Current: v.FilePath, Proposed: targets[v.ID]}
		it.Status, it.Reason = s.judgeRename(it, owned, claimed)
		if it.Status == RenameOK {
			claimed[it.Proposed] = true
		}
		items = append(items, it)
	}
	return items
}

// judgeRename decides one item's status against the disk and the movie's other tracks.
func (s *Service) judgeRename(it RenameItem, owned map[string]int64, claimed map[string]bool) (string, string) {
	cur, err := s.statFile(it.Current)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RenameMissing, reasonNotOnDisk
		}
		return RenameMissing, "couldn't read the file: " + err.Error()
	}
	if it.Proposed == "" || it.Proposed == it.Current {
		return RenameSame, ""
	}
	if owner, ok := owned[it.Proposed]; ok && owner != it.VersionID {
		return RenameConflict, reasonOtherTrack
	}
	if claimed[it.Proposed] {
		return RenameConflict, reasonSameName
	}
	if fi, err := os.Lstat(it.Proposed); err == nil {
		// The same file under another name (a hardlink, or a case-only change on a
		// case-insensitive disk) isn't in the way: Move just drops the old name.
		if !os.SameFile(cur, fi) {
			return RenameConflict, reasonTaken
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return RenameConflict, "couldn't check the new name: " + err.Error()
	}
	return RenameOK, ""
}

// trackTargets is where each track's file belongs under the naming scheme, keyed by
// version id. Each name comes from the one per-track builder imports use (TrackTarget:
// the scheme, the track's edition as Plex's {edition-…}, the movie's ids, and its label
// only to keep clear of another track's file), so a rename lands every track exactly
// where an import would have put it. Tracks are named in order, default first, and each
// one's planned name is kept clear of by the tracks after it, as their files would be.
func (s *Service) trackTargets(m Movie, versions []Version) map[int64]string {
	out := map[int64]string{}
	var planned []string
	for _, v := range versions {
		if !v.HasFile || v.FilePath == "" {
			continue
		}
		t := s.TrackNaming(m, versions, v)
		t.Protected = append(t.Protected, planned...)
		target := s.imp.MovieTrackTarget(t, filepath.Base(v.FilePath), filepath.Ext(v.FilePath))
		out[v.ID] = target
		planned = append(planned, target)
	}
	return out
}

// PlanToken fingerprints the renames a plan would make, so applying it can tell whether
// the library changed since the preview it was asked from.
func PlanToken(items []RenameItem) string {
	var lines []string
	for _, it := range items {
		if it.Status == RenameOK {
			lines = append(lines, strings.Join([]string{strconv.FormatInt(it.MovieID, 10), strconv.FormatInt(it.VersionID, 10), it.Current, it.Proposed}, "\x00"))
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}

// ApplyRename carries out planned renames. Each movie's plan is read again first: an item
// whose file or proposed name changed since it was planned, or that is no longer a clean
// move, is left alone and reported. Files move without ever replacing another file
// (library.Importer.Move), take their subtitles along, and — when the folder changes —
// the movie.nfo and artwork too; the emptied old folder is removed. Every moved track's
// record changes in one transaction with its movie.changed row, so Convert, Subtitles and
// a Plex scan follow the new name.
func (s *Service) ApplyRename(ctx context.Context, items []RenameItem) ([]RenameResult, error) {
	var order []int64
	byMovie := map[int64][]RenameItem{}
	for _, it := range items {
		if _, ok := byMovie[it.MovieID]; !ok {
			order = append(order, it.MovieID)
		}
		byMovie[it.MovieID] = append(byMovie[it.MovieID], it)
	}
	var out []RenameResult
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res, err := s.applyMovieRename(ctx, id, byMovie[id])
		out = append(out, res...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Service) applyMovieRename(ctx context.Context, id int64, asked []RenameItem) ([]RenameResult, error) {
	skip := func(it RenameItem, reason string) RenameResult {
		return RenameResult{MovieID: it.MovieID, VersionID: it.VersionID, Label: it.Label, From: it.Current, To: it.Proposed, Reason: reason}
	}
	var out []RenameResult
	m, versions, err := s.trackRows(ctx, id)
	if errors.Is(err, ErrNotFound) {
		for _, it := range asked {
			out = append(out, skip(it, reasonMovieGone))
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	fresh := map[int64]RenameItem{}
	for _, it := range s.planMovie(m, versions) {
		fresh[it.VersionID] = it
	}
	var moves []RenameItem
	for _, it := range asked {
		now, ok := fresh[it.VersionID]
		switch {
		case !ok || now.Current != it.Current || now.Proposed != it.Proposed:
			out = append(out, skip(it, reasonChanged))
		case now.Status == RenameSame:
			out = append(out, skip(it, reasonAlreadyNamed))
		case now.Status != RenameOK:
			out = append(out, skip(it, now.Reason))
		default:
			moves = append(moves, now)
		}
	}

	moved := 0
	oldDirs := map[string]string{} // old folder → new folder, for folders this rename left
	for _, it := range moves {
		r, ok := s.moveTrackFile(ctx, m, versions, it)
		out = append(out, r)
		if ok {
			moved++
			if od, nd := filepath.Dir(it.Current), filepath.Dir(it.Proposed); od != nd {
				oldDirs[od] = nd
			}
		}
	}
	for od, nd := range oldDirs {
		s.leaveOldFolder(od, nd)
	}
	if moved > 0 {
		s.log.Info("movie renamed", "movie_id", id, "files", moved)
	}
	return out, nil
}

// moveTrackFile moves one planned file and repoints every track recording it. If the
// record can't be written the file is put back, so the record and the disk never
// disagree.
func (s *Service) moveTrackFile(ctx context.Context, m Movie, versions []Version, it RenameItem) (RenameResult, bool) {
	res := RenameResult{MovieID: m.ID, VersionID: it.VersionID, Label: it.Label, From: it.Current, To: it.Proposed}
	if err := s.imp.Move(it.Current, it.Proposed); err != nil {
		if errors.Is(err, library.ErrTargetExists) {
			res.Reason = reasonTaken
		} else {
			res.Reason = "couldn't move the file: " + err.Error()
		}
		return res, false
	}
	s.imp.MoveEpisodeSubs(it.Current, it.Proposed)

	// The tracks that follow this file, with their cached facts moved along (read before
	// the transaction: a stale cache means a probe, and that can take seconds).
	type follow struct {
		v     Version
		media string
		size  int64
	}
	var follows []follow
	for _, v := range versions {
		if !v.HasFile || v.FilePath != it.Current {
			continue
		}
		info := s.movedMedia(v.File, it.Proposed)
		size := v.SizeBytes
		if info != nil && !info.Missing {
			size = info.SizeBytes
		}
		follows = append(follows, follow{v, mediaJSONOf(info), size})
	}
	err := s.repo.inTx(ctx, func(tx *sql.Tx, r *Repo) error {
		for _, f := range follows {
			if f.v.IsDefault {
				if err := setDefaultFileIn(ctx, r, m.ID, it.Proposed, f.media); err != nil {
					return err
				}
			} else if err := r.SetVersionFile(ctx, f.v.ID, it.Proposed, f.size, f.media); err != nil {
				return err
			}
			detail := filepath.Base(it.Current) + " → " + filepath.Base(it.Proposed)
			if !f.v.IsDefault {
				detail += " (" + f.v.Label + ")"
			}
			if err := r.AddEvent(ctx, m.ID, "renamed", detail); err != nil {
				return err
			}
			if err := s.enqueue(ctx, tx, outbox.TopicMovieChanged,
				outbox.MovieChanged{MovieID: m.ID, VersionID: f.v.ID, Change: outbox.ChangeRenamed, OldPath: it.Current, Path: it.Proposed},
				movieKey(m.ID)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if berr := s.imp.Move(it.Proposed, it.Current); berr == nil {
			s.imp.MoveEpisodeSubs(it.Proposed, it.Current)
			res.Reason = "couldn't record the new name, so the file was put back: " + err.Error()
		} else {
			s.log.Error("movie rename: the record couldn't be written and the file couldn't be put back — the movie points at the old name",
				"movie_id", m.ID, "old", it.Current, "new", it.Proposed, "err", err, "move_back_err", berr)
			res.Reason = fmt.Sprintf("couldn't record the new name (%v); the file is at %s — refresh the movie to pick it up", err, it.Proposed)
		}
		return res, false
	}
	for _, f := range follows {
		s.publish("movie.renamed", map[string]any{"id": m.ID, "version_id": f.v.ID, "old_path": it.Current, "new_path": it.Proposed})
	}
	res.Renamed = true
	return res, true
}

// folderSidecars are the files a movie folder holds for the film as a whole — the
// metadata and artwork Arrmada (and Kodi/Jellyfin tools) write — which follow the film to
// its new folder. Anything else stays where it is.
var folderSidecars = []string{
	"movie.nfo", "poster.jpg", "fanart.jpg", "folder.jpg", "backdrop.jpg", "landscape.jpg",
	"banner.jpg", "clearlogo.png", "logo.png", "disc.png",
}

// leaveOldFolder finishes a rename that moved a film out of oldDir: once no video is left
// there, the film's metadata and artwork follow it (never replacing what newDir already
// has) and the emptied folder is removed. The library root itself is never removed.
func (s *Service) leaveOldFolder(oldDir, newDir string) {
	if root := s.libRoot(); root != "" && filepath.Clean(oldDir) == filepath.Clean(root) {
		return // a file that sat in the root: its neighbours aren't this film's
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && isVideoFile(e.Name()) {
			return // another track (or an untracked video) still lives here
		}
	}
	for _, name := range folderSidecars {
		from := filepath.Join(oldDir, name)
		if _, err := os.Lstat(from); err != nil {
			continue
		}
		if err := s.imp.Move(from, filepath.Join(newDir, name)); err != nil {
			s.log.Info("movie rename: left a folder file behind", "file", from, "err", err)
		}
	}
	s.imp.RemoveDirIfEmpty(oldDir)
}

// Rename applies every clean rename for one movie (the plan's "ok" items). It returns
// the first item it couldn't apply as an error; the rest still ran.
func (s *Service) Rename(ctx context.Context, id int64) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	plan, err := s.RenamePlan(ctx, []int64{id})
	if err != nil {
		return err
	}
	var todo []RenameItem
	for _, it := range plan {
		if it.Status == RenameOK {
			todo = append(todo, it)
		}
	}
	res, err := s.ApplyRename(ctx, todo)
	if err != nil {
		return err
	}
	for _, r := range res {
		if !r.Renamed {
			return fmt.Errorf("%s: %s", filepath.Base(r.From), r.Reason)
		}
	}
	return nil
}
