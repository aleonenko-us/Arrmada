// Package stage is the one place music files are written (MUS-33): house tags go into a
// staging copy on the library's own drive, are checked, and only then does the file land
// in the library, with one move per file. A seeding download is never written: its file
// is copied into staging and the copy is tagged.
//
// Layout: <music root>/.arrmada-staging/<job id>/ holds manifest.json and files/, the
// staged copies. The folder is hidden (scanners skip dot-folders, library.SkipScanDir),
// carries .plexignore/.ignore (library.PrepareHiddenDir), and sits on the same drive as
// the library, so the final move is a link and an unlink, never a copy into place.
//
// Every step is written to the manifest before the next one starts, so a crash at any
// point leaves a job that Recover can finish or roll back on the next start: copies that
// never reached the library are deleted (they're Arrmada's own; the source is untouched),
// a file whose old version is already in the recycle bin is moved into place (it is the
// newest copy of that track), and a file already in place is recorded. A staged file is
// deleted only when it is a copy; the job folder goes only once every file is settled.
//
// The same manifest journals a rename (Kind "rename"): no staging, one no-clobber move
// per file, and recovery settles each file at its old or new name.
//
// Only this package may write tags (an archtest forbids audiotag.Write anywhere else).
package stage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DirName is the staging folder inside a library root.
const DirName = ".arrmada-staging"

const (
	manifestName = "manifest.json"
	filesDir     = "files"
)

// Kinds of job.
const (
	KindImport = "import" // a download's files tagged into the library
	KindRetag  = "retag"  // library files re-tagged in place (original to the bin)
	KindRename = "rename" // library files moved to new names; nothing staged
)

// State is how far one file has got. The order is the order of the steps.
type State string

const (
	// Planned: nothing done yet (for an import, a copy may be half-written in files/).
	Planned State = "planned"
	// Staged: the copy in files/ is complete.
	Staged State = "staged"
	// Verified: the copy is tagged and checked (tags read back, audio unchanged).
	Verified State = "verified"
	// Placing: the copy is moving to a free final path (nothing to replace there). Where
	// the file is afterwards says whether the move happened.
	Placing State = "placing"
	// Arriving: the copy is being linked into the album folder under a temporary name
	// (Item.Temp), beside the file it replaces, which is still in place.
	Arriving State = "arriving"
	// Binning: the file it replaces is being moved to the recycle bin; the new one is
	// ready beside it under its temporary name. Whether the old file is still at the
	// final path says whether the bin took it.
	Binning State = "binning"
	// Binned: the library file it replaces is in the recycle bin. From here the new file
	// is only ever moved into place — or, if that can't happen, the old one comes back.
	Binned State = "binned"
	// Placed: the file is at its final path.
	Placed State = "placed"
	// Recorded: the database knows; nothing left to do.
	Recorded State = "recorded"
	// Discarded: dropped (a staged copy deleted, a rename not done); the source is as it was.
	Discarded State = "discarded"
)

// terminal reports whether a file needs nothing more.
func (s State) terminal() bool { return s == Recorded || s == Discarded }

// Item is one file in a job.
type Item struct {
	// Source is where the file's content comes from: a download's file (import), the
	// library file itself (retag) or a file's current name (rename). Never written.
	Source string `json:"source"`
	// Staged is the copy in the job's files/ folder ("" for a rename).
	Staged string `json:"staged,omitempty"`
	// Final is where the file goes in the library.
	Final string `json:"final"`
	// Replace is a library file the new one takes the place of (often Final itself): it
	// goes to the recycle bin first, and a bin that can't take it refuses the file.
	Replace string `json:"replace,omitempty"`
	// Retire is an old file of the same track under another name (an .mp3 a .flac
	// replaces): to the bin once the new one is in place, best effort.
	Retire string `json:"retire,omitempty"`
	// Temp is the new file's temporary name beside Final while it replaces a file there.
	Temp string `json:"temp,omitempty"`
	// Binned is where Replace went in the bin.
	Binned string `json:"binned,omitempty"`
	State  State  `json:"state"`
	// Ref and Meta are the caller's: which track the file is, and what to record for it,
	// so recovery can record a file a crash left placed but unrecorded.
	Ref  int64           `json:"ref,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
	// Note says why a file was discarded or held, for the log.
	Note string `json:"note,omitempty"`
}

// Manifest is a job's record on disk.
type Manifest struct {
	Version int       `json:"version"`
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Created time.Time `json:"created"`
	// AlbumID and Hash say what the job is for (the album, and the download for an import).
	AlbumID int64  `json:"album_id,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Release string `json:"release,omitempty"`
	Items   []Item `json:"items"`
}

// reJobID is what a job folder's name looks like; Recover touches nothing else.
var reJobID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[0-9a-f]{8}$`)

// writeManifest saves m atomically: a temporary file, synced, renamed over the old one,
// so a crash leaves either the old manifest or the new one, never half of one.
func writeManifest(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(dir, manifestName))
	}
	if err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("stage: saving the manifest: %w", err)
	}
	syncDir(dir)
	return nil
}

// readManifest loads a job's manifest.
func readManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("stage: manifest %s: %w", dir, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("stage: manifest %s: version %d isn't one this build knows", dir, m.Version)
	}
	return &m, nil
}

// syncDir persists renames in dir, best effort (not every platform can sync a folder).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// ErrNotInJob guards every delete: only a file inside the job's own folder may go.
var ErrNotInJob = errors.New("stage: refusing to delete a file outside the staging job")

// removeOwn deletes a staged copy, after checking it is inside the job folder.
func removeOwn(jobDir, path string) error {
	if path == "" {
		return nil
	}
	rel, err := filepath.Rel(jobDir, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrNotInJob
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
