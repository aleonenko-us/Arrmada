package subtitles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The per-file view (SUB-27): everything the drawer shows about one video's subtitles —
// the sidecars beside it with where each came from and its first lines, the embedded
// tracks, each kept language's state, orphans, backoff and the latest attempts — and the
// two things the owner can do to a sidecar directly: nudge its timing and send it to the
// recycle bin.
//
// Path safety is the point of this file. The client names a video by its ids and a
// sidecar by its bare file name; the video's path comes from the database, and the name
// must be one of the subtitles that pair with that video (or one of its orphans) as the
// folder is right now. Nothing the client sends is ever joined onto a path unchecked.

var (
	// ErrMediaNotFound: no movie or episode with those ids, or it has no file on disk.
	ErrMediaNotFound = errors.New("that movie or episode has no file on disk")
	// ErrBadMediaRef: the ids don't name a movie or an episode.
	ErrBadMediaRef = errors.New(`kind must be "movie" (with movie_id) or "episode" (with series_id, season and episode)`)
	// ErrNotSidecar: the name isn't a subtitle that belongs to this video right now.
	ErrNotSidecar = errors.New("that isn't one of this file's subtitles")
	// ErrNotShiftable: only SRT timing can be rewritten in place.
	ErrNotShiftable = errors.New("only .srt subtitles can be shifted")
	// ErrBadShift: an offset of zero, or more than ten minutes either way.
	ErrBadShift = errors.New("the shift must be between −10 and +10 minutes, and not zero")
	// ErrFileBusy: a subtitle job is working on this video; a change now could race it.
	ErrFileBusy = errors.New("a subtitle job is working on this file — try again when it finishes")
)

// maxShiftMs bounds one nudge: the drawer moves in half-seconds, and nothing real is ten
// minutes out.
const maxShiftMs = 10 * 60 * 1000

// maxPreviewBytes is how much of a sidecar the view reads for its cue count and preview.
// A real subtitle is well under a megabyte; this only guards against a mislabelled file.
const maxPreviewBytes = 4 << 20

// utf8BOM opens many uploaded SRTs; it isn't part of the first cue.
const utf8BOM = "\xef\xbb\xbf"

// MediaRef names one video from the client: a movie, or one episode of a show.
type MediaRef struct {
	Kind     string `json:"kind"` // "movie" | "episode"
	MovieID  int64  `json:"movie_id,omitempty"`
	SeriesID int64  `json:"series_id,omitempty"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
}

// job is the ref as a Job, which carries the key and identity helpers the queue uses.
func (r MediaRef) job() *Job {
	return &Job{Kind: r.Kind, MovieID: r.MovieID, SeriesID: r.SeriesID, Season: r.Season, Episode: r.Episode}
}

func (r MediaRef) valid() bool {
	switch r.Kind {
	case "movie":
		return r.MovieID > 0
	case "episode":
		return r.SeriesID > 0 && r.Season >= 0 && r.Episode > 0
	}
	return false
}

// CuePreview is one cue of a sidecar's preview.
type CuePreview struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// SidecarView is one subtitle file paired with the video: what the folder says (name,
// language, size, first lines) joined with what the ledger knows (source and provenance).
// A file Arrmada never wrote or adopted has Owned false and source "unknown".
type SidecarView struct {
	Name           string       `json:"name"`
	Lang           string       `json:"lang"`              // "" = untagged (<base>.srt); a non-language tag keeps its spelling
	Variant        string       `json:"variant,omitempty"` // "" (full) | forced | sdh
	Ext            string       `json:"ext"`               // ".srt", ".ass", …
	Size           int64        `json:"size"`
	ModifiedAt     int64        `json:"modified_at"`
	CueCount       int          `json:"cue_count"`  // 0 for a format the preview can't read
	FirstCues      []CuePreview `json:"first_cues"` // up to three
	Shiftable      bool         `json:"shiftable"`  // SRT: the nudges can rewrite it
	Owned          bool         `json:"owned"`      // the ledger has a live row for it
	Source         string       `json:"source"`
	Provider       string       `json:"provider,omitempty"`
	ProviderFileID string       `json:"provider_file_id,omitempty"`
	Release        string       `json:"release,omitempty"`
	HashMatch      bool         `json:"hash_match,omitempty"`
	Score          *int         `json:"score,omitempty"`
	Model          string       `json:"model,omitempty"`
	SyncOffsetMs   *int         `json:"sync_offset_ms,omitempty"`
	SyncConfidence *int         `json:"sync_confidence,omitempty"` // the Health score; nil = not checked
	Locked         bool         `json:"locked,omitempty"`
	EmbeddedIndex  *int         `json:"embedded_index,omitempty"`
	CreatedAt      int64        `json:"created_at,omitempty"` // when the ledger recorded it
}

// FileView is GET /subtitles/file: one video's whole subtitle picture.
type FileView struct {
	MediaRef
	MediaKey  string        `json:"media_key"`
	Title     string        `json:"title"`
	VideoPath string        `json:"video_path"`
	Kept      []string      `json:"kept"`      // the kept languages, in order
	Languages []LangStatus  `json:"languages"` // each kept language's state (the wants)
	Audio     []AudioTrack  `json:"audio"`
	Embedded  []SubTrack    `json:"embedded"`
	Sidecars  []SidecarView `json:"sidecars"`
	Orphans   []Orphan      `json:"orphans"`
	Backoffs  []Backoff     `json:"backoffs"`
	Attempts  []Attempt     `json:"attempts"` // the latest per language, variant and rung
	Jobs      []Job         `json:"jobs"`     // this file's queued and running jobs
}

// resolveMedia finds a ref's video from the database (the job seam in tests).
func (s *Service) resolveMedia(ctx context.Context, r MediaRef) (fileRef, error) {
	if !r.valid() {
		return fileRef{}, ErrBadMediaRef
	}
	job := r.job()
	if s.resolve != nil {
		ref, ok := s.resolve(ctx, job)
		if !ok {
			return fileRef{}, ErrMediaNotFound
		}
		if ref.MediaKey == "" {
			ref.MediaKey = job.key()
		}
		return ref, nil
	}
	if (r.Kind == "movie" && s.movies == nil) || (r.Kind == "episode" && s.series == nil) {
		return fileRef{}, ErrMediaNotFound
	}
	if r.Kind == "movie" {
		if _, err := s.movies.Get(ctx, r.MovieID); errors.Is(err, movies.ErrNotFound) {
			return fileRef{}, ErrMediaNotFound
		} else if err != nil {
			return fileRef{}, err
		}
	} else if _, err := s.series.Get(ctx, r.SeriesID); errors.Is(err, series.ErrNotFound) {
		return fileRef{}, ErrMediaNotFound
	} else if err != nil {
		return fileRef{}, err
	}
	ref, ok := s.resolveFile(ctx, job)
	if !ok || ref.Path == "" {
		return fileRef{}, ErrMediaNotFound
	}
	return ref, nil
}

// fileTitle is how the view names the video: the job's title format.
func fileTitle(r MediaRef, ref fileRef) string {
	if r.Kind == "episode" {
		return fmt.Sprintf("%s - S%02dE%02d", ref.Title, r.Season, r.Episode)
	}
	return ref.Title
}

// FileView builds the drawer's picture of one video. The probe is the cached one; only a
// file never probed (or changed since) runs ffprobe.
func (s *Service) FileView(ctx context.Context, r MediaRef) (FileView, error) {
	ref, err := s.resolveMedia(ctx, r)
	if err != nil {
		return FileView{}, err
	}
	kept := s.languages(ctx)
	v := FileView{
		MediaRef: r, MediaKey: ref.MediaKey, Title: fileTitle(r, ref), VideoPath: ref.Path, Kept: kept,
		Audio: []AudioTrack{}, Sidecars: []SidecarView{}, Orphans: []Orphan{}, Backoffs: []Backoff{},
		Attempts: []Attempt{}, Jobs: []Job{},
	}

	// The kept languages' state, exactly as the Library row shows it.
	fs := FileSubs{Kind: r.Kind, MovieID: r.MovieID, SeriesID: r.SeriesID, Season: r.Season, Episode: r.Episode, Path: ref.Path}
	s.fillCoverage(ctx, &fs, kept, s.provider != nil && s.provider.CanDownload())
	bs, err := s.ledger.BackoffsFor(ctx, ref.MediaKey)
	if err != nil {
		s.log.Warn("subtitles: could not read backoff for the file view", "media_key", ref.MediaKey, "err", err)
	}
	applyBackoff(&fs, bs, time.Now())
	v.Languages, v.Embedded = fs.Languages, fs.Embedded
	if mi, err := s.probeFile(ctx, ref.Path); err == nil && mi != nil && mi.Audio != nil {
		v.Audio = mi.Audio
	}
	for _, b := range bs {
		v.Backoffs = append(v.Backoffs, b)
	}
	sort.Slice(v.Backoffs, func(i, j int) bool { return v.Backoffs[i].Lang < v.Backoffs[j].Lang })

	sc := scanSidecars(ref.Path, kept, kindOf(r.Kind))
	if sc.Orphans != nil {
		v.Orphans = sc.Orphans
	}
	owned, err := s.ledger.FilesFor(ctx, ref.MediaKey)
	if err != nil {
		s.log.Warn("subtitles: could not read sidecar sources for the file view", "media_key", ref.MediaKey, "err", err)
	}
	byPath := make(map[string]SubtitleFile, len(owned))
	for _, f := range owned {
		byPath[f.Path] = f
	}
	dir := filepath.Dir(ref.Path)
	for i, name := range sc.Names {
		p := filepath.Join(dir, name)
		sv := SidecarView{Name: name, Lang: sc.Variants[i].Lang, Variant: sc.Variants[i].Variant, Ext: strings.ToLower(filepath.Ext(name)), Source: SourceUnknown, FirstCues: []CuePreview{}}
		sv.Shiftable = sv.Ext == ".srt"
		if fi, err := os.Stat(p); err == nil {
			sv.Size, sv.ModifiedAt = fi.Size(), fi.ModTime().Unix()
		}
		sv.CueCount, sv.FirstCues = previewCues(p, 3)
		if f, ok := byPath[p]; ok {
			sv.Owned, sv.Source, sv.Provider, sv.ProviderFileID = true, f.Source, f.Provider, f.ProviderFileID
			sv.Release, sv.HashMatch, sv.Score, sv.Model = f.ReleaseName, f.HashMatch, f.Score, f.Model
			sv.SyncOffsetMs, sv.SyncConfidence, sv.Locked, sv.EmbeddedIndex, sv.CreatedAt = f.SyncOffsetMs, f.SyncConfidence, f.Locked, f.EmbeddedIndex, f.CreatedAt
		}
		v.Sidecars = append(v.Sidecars, sv)
	}

	// The newest attempt for each language, variant and rung: what the last run of each
	// rung said, which is what a "why is this missing?" question needs.
	atts, err := s.ledger.Attempts(ctx, AttemptQuery{MediaKey: ref.MediaKey, Limit: 200})
	if err != nil {
		s.log.Warn("subtitles: could not read attempts for the file view", "media_key", ref.MediaKey, "err", err)
	}
	seen := map[string]bool{}
	for _, a := range atts {
		k := a.Lang + "|" + a.Variant + "|" + string(a.Rung)
		if !seen[k] {
			seen[k] = true
			v.Attempts = append(v.Attempts, a)
		}
	}

	key := ref.MediaKey
	s.mu.Lock()
	for _, j := range s.jobs {
		if (j.State == StateQueued || j.State == StateRunning) && j.key() == key {
			v.Jobs = append(v.Jobs, *j)
		}
	}
	s.mu.Unlock()
	return v, nil
}

// cueTagRe strips styling from preview text: HTML-ish tags and ASS override blocks.
var cueTagRe = regexp.MustCompile(`<[^>]*>|\{\\[^}]*\}`)

// previewCues reads a sidecar's cue count and its first n cues. A file it can't read, or
// a format other than SRT/VTT timing, comes back as 0 and none.
func previewCues(path string, n int) (int, []CuePreview) {
	f, err := os.Open(path)
	if err != nil {
		return 0, []CuePreview{}
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxPreviewBytes))
	if err != nil {
		return 0, []CuePreview{}
	}
	cues := parseSRT(strings.TrimPrefix(string(raw), utf8BOM))
	out := make([]CuePreview, 0, n)
	for _, c := range cues {
		if len(out) == n {
			break
		}
		out = append(out, CuePreview{StartMs: c.start.Milliseconds(), EndMs: c.end.Milliseconds(), Text: strings.TrimSpace(cueTagRe.ReplaceAllString(c.text, ""))})
	}
	return len(cues), out
}

// sidecarTarget is a sidecar the client named, checked against the folder as it is now.
type sidecarTarget struct {
	ref     fileRef
	path    string
	lang    string
	variant string
	orphan  bool
}

// validSidecarName is the first gate: a bare file name with a subtitle extension. The
// second (pairing with this video) is in sidecarFor.
func validSidecarName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") ||
		filepath.Base(name) != name || strings.ContainsRune(name, 0) {
		return false
	}
	return subExts[strings.ToLower(filepath.Ext(name))]
}

// sidecarFor resolves a client-named subtitle of a video: it must be a bare name with a
// subtitle extension that pairs with the video (orphans too, when allowOrphan), as listed
// in the video's folder right now. Anything else is ErrNotSidecar.
func (s *Service) sidecarFor(ctx context.Context, r MediaRef, name string, allowOrphan bool) (sidecarTarget, error) {
	ref, err := s.resolveMedia(ctx, r)
	if err != nil {
		return sidecarTarget{}, err
	}
	if !validSidecarName(name) {
		return sidecarTarget{}, ErrNotSidecar
	}
	sc := scanSidecars(ref.Path, s.languages(ctx), kindOf(r.Kind))
	dir := filepath.Dir(ref.Path)
	for i, n := range sc.Names {
		if n == name {
			return sidecarTarget{ref: ref, path: filepath.Join(dir, n), lang: sc.Variants[i].Lang, variant: sc.Variants[i].Variant}, nil
		}
	}
	if allowOrphan {
		for _, o := range sc.Orphans {
			if o.Name == name {
				return sidecarTarget{ref: ref, path: filepath.Join(dir, o.Name), lang: o.Lang, variant: o.Variant, orphan: true}, nil
			}
		}
	}
	return sidecarTarget{}, ErrNotSidecar
}

// fileBusy reports whether a job is working on this video right now.
func (s *Service) fileBusy(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[key] != nil
}

// ShiftSidecar moves a paired SRT's timing by offsetMs (+ = later), rewriting it through a
// temp file renamed into place so a player never reads half of it. The ledger keeps the
// running total of manual shifts on the file's row (one with source "unknown" is created
// for a file Arrmada didn't write), and clears the last sync check's confidence: it
// measured the file as it was.
func (s *Service) ShiftSidecar(ctx context.Context, r MediaRef, name string, offsetMs int) (SidecarView, error) {
	if offsetMs == 0 || offsetMs > maxShiftMs || offsetMs < -maxShiftMs {
		return SidecarView{}, ErrBadShift
	}
	t, err := s.sidecarFor(ctx, r, name, false)
	if err != nil {
		return SidecarView{}, err
	}
	if strings.ToLower(filepath.Ext(name)) != ".srt" {
		return SidecarView{}, ErrNotShiftable
	}
	if s.fileBusy(t.ref.MediaKey) {
		return SidecarView{}, ErrFileBusy
	}
	if fi, err := os.Stat(t.path); err == nil && fi.Size() > maxPreviewBytes {
		return SidecarView{}, ErrNotShiftable // not a real subtitle; don't rewrite it
	}
	if err := writeShifted(t.path, offsetMs, 1); err != nil {
		return SidecarView{}, fmt.Errorf("shift %s: %w", name, err)
	}
	s.noteShift(ctx, r, t, offsetMs)
	s.sidecarChanged(r.Kind, t.ref.Path, t.path, SidecarShifted)
	s.event("info", fmt.Sprintf("%s: shifted %s by %+.1f s", fileTitle(r, t.ref), name, float64(offsetMs)/1000))
	sv := SidecarView{Name: name, Lang: t.lang, Variant: t.variant, Ext: ".srt", Shiftable: true}
	sv.CueCount, sv.FirstCues = previewCues(t.path, 3)
	return sv, nil
}

// noteShift records a manual shift in the ledger. Bookkeeping: the file has already been
// rewritten, so an error is logged, not returned.
func (s *Service) noteShift(ctx context.Context, r MediaRef, t sidecarTarget, offsetMs int) {
	ctx = context.WithoutCancel(ctx)
	ok, err := s.ledger.AddShift(ctx, t.path, offsetMs)
	if err == nil && !ok {
		lang := t.lang
		if lang == "" {
			if kept := s.languages(ctx); len(kept) > 0 {
				lang = kept[0] // a bare <base>.srt counts as the first kept language
			}
		}
		if l, known := langs.Lookup(lang); known {
			lang = l.Code
		}
		off := offsetMs
		_, err = s.ledger.RecordFile(ctx, SubtitleFile{
			MediaKey: t.ref.MediaKey, Kind: r.Kind, MovieID: r.MovieID, SeriesID: r.SeriesID, Season: r.Season, Episode: r.Episode,
			VideoPath: t.ref.Path, Path: t.path, Lang: lang, Variant: t.variant, Source: SourceUnknown, SyncOffsetMs: &off,
		})
	}
	if err != nil {
		s.log.Warn("subtitles: couldn't record a shift", "path", t.path, "err", err)
	}
}

// RemoveSidecar sends one of a video's subtitles (a paired one or an orphan) to the
// recycle bin, never deleting it outright: when the bin can't take it, the file stays and
// the error says why. An OpenSubtitles download that goes is blocklisted for this video,
// so the next sweep doesn't fetch the same file straight back.
func (s *Service) RemoveSidecar(ctx context.Context, r MediaRef, name string) error {
	t, err := s.sidecarFor(ctx, r, name, true)
	if err != nil {
		return err
	}
	if s.fileBusy(t.ref.MediaKey) {
		return ErrFileBusy
	}
	row, owned, err := s.ledger.LiveFile(ctx, t.path)
	if err != nil {
		s.log.Warn("subtitles: couldn't read a sidecar's source before removing it", "path", t.path, "err", err)
	}
	if err := s.retireSidecar(ctx, t.path); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if owned && row.ProviderFileID != "" && row.Provider != "" {
		if err := s.ledger.Block(ctx, BlockEntry{Provider: row.Provider, FileID: row.ProviderFileID, MediaKey: t.ref.MediaKey, Lang: row.Lang, Reason: BlockManual}); err != nil {
			s.log.Warn("subtitles: couldn't blocklist a removed download", "path", t.path, "err", err)
		}
	}
	s.sidecarChanged(r.Kind, t.ref.Path, t.path, SidecarRemoved)
	s.event("info", fmt.Sprintf("%s: moved %s to the recycle bin", fileTitle(r, t.ref), name))
	s.refreshSnapshot(ctx, r.job())
	return nil
}
