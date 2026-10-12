package subtitles

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The ledger is the module's memory: where each sidecar came from (subtitle_files), what
// every attempt to make one did (subtitle_attempts), and which provider files must never
// be fetched again for a video (subtitle_blocklist). Redo, sync checks, backoff, the
// needs-attention board and the persistent queue all read and write through it.
//
// Writes are bookkeeping: a ledger error is logged by the caller and never fails a job.
// Reads that guard a download (Blocklisted) return their error so the caller can refuse
// to act rather than fetch a file it was told never to fetch again.

// Rung is the step of the source ladder an attempt was made on.
type Rung string

const (
	RungExtract       Rung = "extract"       // an embedded text track pulled out with ffmpeg
	RungOpenSubtitles Rung = "opensubtitles" // a provider search + download
	RungAI            Rung = "ai"            // local whisper
	RungUpgrade       Rung = "upgrade"       // a later, better replacement for an owned sidecar
	RungRepair        Rung = "repair"        // fixing a sidecar already there (forced, misnamed)
)

// Result is an attempt's typed outcome.
type Result string

const (
	ResultOK            Result = "ok"
	ResultEmpty         Result = "empty"          // the embedded track extracted to nothing
	ResultExtractFailed Result = "extract_failed" // ffmpeg couldn't pull the track out
	ResultNoMatch       Result = "no_match"       // the provider has nothing for this file
	ResultQuota         Result = "quota"          // the daily download allowance is spent
	ResultRateLimited   Result = "rate_limited"   // the provider asked us to slow down (seconds)
	ResultAuthFailed    Result = "auth_failed"    // the provider rejected the key or the login
	ResultProviderError Result = "provider_error" // any other provider failure
	ResultBelowScore    Result = "below_score"    // candidates exist, none matches the file well enough
	ResultOutOfSync     Result = "out_of_sync"    // the best candidate failed the sync check
	ResultModelMissing  Result = "model_missing"  // whisper has no model for this task
	ResultAIFailed      Result = "ai_failed"      // whisper ran and failed
	ResultNoAudio       Result = "no_audio"       // nothing to transcribe
	ResultImpossible    Result = "impossible"     // whisper can't make this language from this audio
	ResultOCRNeeded     Result = "ocr_needed"     // only an image track exists
	ResultCancelled     Result = "cancelled"      // the job was stopped part-way
)

// IsError reports an outcome that means something is broken and wants fixing — a
// rejected login, a provider or whisper failure, a missing model — as opposed to the
// subtitle simply not existing. A job whose every attempted language ends in one is failed.
func (r Result) IsError() bool {
	switch r {
	case ResultAuthFailed, ResultProviderError, ResultAIFailed, ResultModelMissing, ResultExtractFailed:
		return true
	}
	return false
}

// Outcome is what one rung of the ladder did for one wanted language in one run.
type Outcome struct {
	Lang    string `json:"lang"`
	Variant string `json:"variant,omitempty"` // the want: VariantFull ("") | VariantSDH | VariantForced
	Rung    Rung   `json:"rung"`
	Result  Result `json:"result"`
	Detail  string `json:"detail,omitempty"` // human wording for the job note, e.g. "no OpenSubtitles match"
	// Score and Reasons are the OpenSubtitles candidate's match score and its explanation
	// (scoring.go), for a download that was saved or refused. The attempt row's detail
	// carries them; the job note stays short.
	Score   int      `json:"score,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
}

// attemptDetail is what the ledger stores as an attempt's detail: the note's wording plus
// the scoring explanation, when there is one.
func (o Outcome) attemptDetail() string {
	if o.Score == 0 && len(o.Reasons) == 0 {
		return o.Detail
	}
	d := fmt.Sprintf("%s (score %d", o.Detail, o.Score)
	if len(o.Reasons) > 0 {
		d += ": " + strings.Join(o.Reasons, ", ")
	}
	return d + ")"
}

// Sidecar sources, for SubtitleFile.Source.
const (
	SourceEmbedded      = "embedded"
	SourceRelease       = "release"
	SourceOpenSubtitles = "opensubtitles"
	SourceAITranscribe  = "ai-transcribe"
	SourceAITranslate   = "ai-translate"
	SourceAdopted       = "adopted"
	SourceManual        = "manual"
	SourceUnknown       = "unknown"
)

// Blocklist reasons, for BlockEntry.Reason.
const (
	BlockRedo      = "redo"
	BlockOutOfSync = "out_of_sync"
	BlockManual    = "manual"
)

// AttemptRetention is how long subtitle_attempts keeps a row.
const AttemptRetention = 180 * 24 * time.Hour

// SubtitleFile is one sidecar's provenance: a row of subtitle_files. Variant uses the
// package's names (VariantFull is ""). Optional numbers are nil when unknown.
type SubtitleFile struct {
	ID             int64  `json:"id"`
	MediaKey       string `json:"media_key"` // Job.key(): "m:<id>" or "e:<series>:<season>:<episode>"
	Kind           string `json:"kind"`      // "movie" | "episode"
	MovieID        int64  `json:"movie_id,omitempty"`
	SeriesID       int64  `json:"series_id,omitempty"`
	Season         int    `json:"season,omitempty"`
	Episode        int    `json:"episode,omitempty"`
	VideoPath      string `json:"video_path"`
	Path           string `json:"path"`
	Lang           string `json:"lang"`
	Variant        string `json:"variant,omitempty"`
	Source         string `json:"source"`
	EmbeddedIndex  *int   `json:"embedded_index,omitempty"` // 0:s:N, for SourceEmbedded
	Provider       string `json:"provider,omitempty"`
	ProviderFileID string `json:"provider_file_id,omitempty"`
	ReleaseName    string `json:"release_name,omitempty"`
	HashMatch      bool   `json:"hash_match,omitempty"`
	Score          *int   `json:"score,omitempty"`
	Model          string `json:"model,omitempty"` // whisper model file, for the AI sources
	SyncOffsetMs   *int   `json:"sync_offset_ms,omitempty"`
	SyncConfidence *int   `json:"sync_confidence,omitempty"`
	Locked         bool   `json:"locked,omitempty"` // the owner chose it: never replaced automatically
	CreatedAt      int64  `json:"created_at"`
	SupersededAt   int64  `json:"superseded_at,omitempty"` // 0 = the live row for its path
}

// Attempt is one row of subtitle_attempts.
type Attempt struct {
	ID        int64  `json:"id"`
	RunID     string `json:"run_id"`
	MediaKey  string `json:"media_key"`
	VideoPath string `json:"video_path"`
	Lang      string `json:"lang"`
	Variant   string `json:"variant,omitempty"`
	Rung      Rung   `json:"rung"`
	Outcome   Result `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
	At        int64  `json:"at"`
}

// AttemptQuery pages subtitle_attempts newest first. Empty filters match everything;
// Before is an attempt id cursor (0 = from the newest); Limit defaults to 50, max 500.
type AttemptQuery struct {
	MediaKey string
	RunID    string
	Before   int64
	Limit    int
}

// BlockEntry is one row of subtitle_blocklist.
type BlockEntry struct {
	Provider string `json:"provider"`
	FileID   string `json:"file_id"`
	MediaKey string `json:"media_key"`
	Lang     string `json:"lang"`
	Reason   string `json:"reason"` // BlockRedo | BlockOutOfSync | BlockManual
	At       int64  `json:"at"`
}

// Ledger reads and writes the subtitle tables. A nil Ledger (or one without a DB) records
// nothing and reads empty, so code paths that run without a database still work.
type Ledger struct {
	db  *sql.DB
	now func() time.Time
}

// NewLedger wraps the app database.
func NewLedger(db *sql.DB) *Ledger { return &Ledger{db: db, now: time.Now} }

func (l *Ledger) ok() bool { return l != nil && l.db != nil }

func (l *Ledger) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// NewRunID names one run of a job, so its attempts can be read back together. Job ids
// restart at 1 with the process; run ids don't repeat.
func NewRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%s", time.Now().Unix(), hex.EncodeToString(b[:]))
}

// dbVariant stores the full variant as 'full' (the package calls it "").
func dbVariant(v string) string {
	if v == VariantFull {
		return "full"
	}
	return v
}

func fromDBVariant(v string) string {
	if v == "full" {
		return VariantFull
	}
	return v
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIntPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RecordFile stores a sidecar's provenance and returns the row id. A live row for the
// same path is superseded (kept as history), so a path has at most one live row.
func (l *Ledger) RecordFile(ctx context.Context, f SubtitleFile) (int64, error) {
	if !l.ok() {
		return 0, nil
	}
	if f.Path == "" || f.MediaKey == "" || f.Lang == "" {
		return 0, errors.New("subtitle ledger: path, media key and language are required")
	}
	now := l.clock().Unix()
	if f.CreatedAt == 0 {
		f.CreatedAt = now
	}
	if f.Source == "" {
		f.Source = SourceUnknown
	}
	// Season 0 is a real season (specials) for an episode, so only movies store NULLs.
	var season, episode any
	if f.Kind == "episode" {
		season, episode = f.Season, f.Episode
	}
	var id int64
	err := store.WithTx(ctx, l.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE subtitle_files SET superseded_at = ? WHERE path = ? AND superseded_at IS NULL`, now, f.Path); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO subtitle_files
			(media_key, kind, movie_id, series_id, season, episode, video_path, path, lang, variant, source,
			 embedded_index, provider, provider_file_id, release_name, hash_match, score, model,
			 sync_offset_ms, sync_confidence, locked, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.MediaKey, f.Kind, nullID(f.MovieID), nullID(f.SeriesID), season, episode,
			f.VideoPath, f.Path, langs.Canonical(f.Lang), dbVariant(f.Variant), f.Source,
			nullIntPtr(f.EmbeddedIndex), nullStr(f.Provider), nullStr(f.ProviderFileID), nullStr(f.ReleaseName),
			boolInt(f.HashMatch), nullIntPtr(f.Score), nullStr(f.Model),
			nullIntPtr(f.SyncOffsetMs), nullIntPtr(f.SyncConfidence), boolInt(f.Locked), f.CreatedAt)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// SupersedeFile retires the live row for a path — the sidecar was deleted, binned or
// replaced by something Arrmada didn't write. Nothing to retire is not an error.
func (l *Ledger) SupersedeFile(ctx context.Context, path string) error {
	if !l.ok() {
		return nil
	}
	_, err := l.db.ExecContext(ctx, `UPDATE subtitle_files SET superseded_at = ? WHERE path = ? AND superseded_at IS NULL`, l.clock().Unix(), path)
	return err
}

// SetSync stores a sync check's result on a path's live row. Reports whether a live row
// was there to update.
func (l *Ledger) SetSync(ctx context.Context, path string, offsetMs, confidence int) (bool, error) {
	if !l.ok() {
		return false, nil
	}
	res, err := l.db.ExecContext(ctx, `UPDATE subtitle_files SET sync_offset_ms = ?, sync_confidence = ? WHERE path = ? AND superseded_at IS NULL`, offsetMs, confidence, path)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AddShift adds a manual timing shift to a path's live row: sync_offset_ms becomes the
// running total of shifts applied, and the last check's confidence is cleared, since it
// measured the file before the move. Reports whether a live row was there to update.
func (l *Ledger) AddShift(ctx context.Context, path string, shiftMs int) (bool, error) {
	if !l.ok() {
		return false, nil
	}
	res, err := l.db.ExecContext(ctx, `UPDATE subtitle_files SET sync_offset_ms = COALESCE(sync_offset_ms, 0) + ?, sync_confidence = NULL
		WHERE path = ? AND superseded_at IS NULL`, shiftMs, path)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

const fileCols = `id, media_key, kind, movie_id, series_id, season, episode, video_path, path, lang, variant, source,
	embedded_index, provider, provider_file_id, release_name, hash_match, score, model,
	sync_offset_ms, sync_confidence, locked, created_at, superseded_at`

func scanFile(sc interface{ Scan(...any) error }) (SubtitleFile, error) {
	var (
		f                                     SubtitleFile
		movieID, seriesID, season, episode    sql.NullInt64
		embIdx, score, syncOff, syncConf, sup sql.NullInt64
		provider, fileID, release, model      sql.NullString
		hash, locked                          int
		variant                               string
	)
	err := sc.Scan(&f.ID, &f.MediaKey, &f.Kind, &movieID, &seriesID, &season, &episode, &f.VideoPath, &f.Path,
		&f.Lang, &variant, &f.Source, &embIdx, &provider, &fileID, &release, &hash, &score, &model,
		&syncOff, &syncConf, &locked, &f.CreatedAt, &sup)
	if err != nil {
		return f, err
	}
	f.MovieID, f.SeriesID = movieID.Int64, seriesID.Int64
	f.Season, f.Episode = int(season.Int64), int(episode.Int64)
	f.Variant = fromDBVariant(variant)
	f.EmbeddedIndex, f.Score = intPtr(embIdx), intPtr(score)
	f.SyncOffsetMs, f.SyncConfidence = intPtr(syncOff), intPtr(syncConf)
	f.Provider, f.ProviderFileID, f.ReleaseName, f.Model = provider.String, fileID.String, release.String, model.String
	f.HashMatch, f.Locked = hash != 0, locked != 0
	f.SupersededAt = sup.Int64
	return f, nil
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// LiveFile returns the live provenance row for a sidecar path; ok is false when Arrmada
// doesn't own the file.
func (l *Ledger) LiveFile(ctx context.Context, path string) (SubtitleFile, bool, error) {
	if !l.ok() {
		return SubtitleFile{}, false, nil
	}
	f, err := scanFile(l.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM subtitle_files WHERE path = ? AND superseded_at IS NULL`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return SubtitleFile{}, false, nil
	}
	if err != nil {
		return SubtitleFile{}, false, err
	}
	return f, true, nil
}

// FilesFor lists the live sidecars owned for one media key, by language and variant.
func (l *Ledger) FilesFor(ctx context.Context, mediaKey string) ([]SubtitleFile, error) {
	if !l.ok() {
		return nil, nil
	}
	rows, err := l.db.QueryContext(ctx, `SELECT `+fileCols+` FROM subtitle_files
		WHERE media_key = ? AND superseded_at IS NULL ORDER BY lang, variant, id`, mediaKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleFile
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RecordAttempts stores one run's outcomes for a file, in one transaction: one row per
// (language, rung) the run tried.
func (l *Ledger) RecordAttempts(ctx context.Context, runID, mediaKey, videoPath string, outs []Outcome) error {
	if !l.ok() || len(outs) == 0 {
		return nil
	}
	at := l.clock().Unix()
	return store.WithTx(ctx, l.db, func(tx *sql.Tx) error {
		for _, o := range outs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO subtitle_attempts
				(run_id, media_key, video_path, lang, variant, rung, outcome, detail, at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				runID, mediaKey, videoPath, langs.Canonical(o.Lang), dbVariant(o.Variant), string(o.Rung), string(o.Result), nullStr(o.attemptDetail()), at); err != nil {
				return err
			}
		}
		return nil
	})
}

// Attempts pages the attempt history, newest first.
func (l *Ledger) Attempts(ctx context.Context, q AttemptQuery) ([]Attempt, error) {
	if !l.ok() {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	where := []string{"1=1"}
	var args []any
	if q.MediaKey != "" {
		where = append(where, "media_key = ?")
		args = append(args, q.MediaKey)
	}
	if q.RunID != "" {
		where = append(where, "run_id = ?")
		args = append(args, q.RunID)
	}
	if q.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, q.Before)
	}
	args = append(args, limit)
	rows, err := l.db.QueryContext(ctx, `SELECT id, run_id, media_key, video_path, lang, variant, rung, outcome, detail, at
		FROM subtitle_attempts WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var (
			a       Attempt
			variant string
			rung    string
			outcome string
			detail  sql.NullString
		)
		if err := rows.Scan(&a.ID, &a.RunID, &a.MediaKey, &a.VideoPath, &a.Lang, &variant, &rung, &outcome, &detail, &a.At); err != nil {
			return nil, err
		}
		a.Variant, a.Rung, a.Outcome, a.Detail = fromDBVariant(variant), Rung(rung), Result(outcome), detail.String
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneAttempts deletes attempt rows older than before; returns how many went.
func (l *Ledger) PruneAttempts(ctx context.Context, before time.Time) (int64, error) {
	if !l.ok() {
		return 0, nil
	}
	res, err := l.db.ExecContext(ctx, `DELETE FROM subtitle_attempts WHERE at < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Block puts a provider file on the blocklist for one video, so it is never downloaded
// for it again. Blocking the same file twice updates the reason.
func (l *Ledger) Block(ctx context.Context, e BlockEntry) error {
	if !l.ok() {
		return errors.New("subtitle ledger: no database")
	}
	if e.Provider == "" || e.FileID == "" || e.MediaKey == "" {
		return errors.New("subtitle ledger: provider, file id and media key are required")
	}
	if e.At == 0 {
		e.At = l.clock().Unix()
	}
	_, err := l.db.ExecContext(ctx, `INSERT INTO subtitle_blocklist (provider, file_id, media_key, lang, reason, at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, file_id, media_key) DO UPDATE SET lang = excluded.lang, reason = excluded.reason, at = excluded.at`,
		e.Provider, e.FileID, e.MediaKey, langs.Canonical(e.Lang), e.Reason, e.At)
	return err
}

// Unblock takes a provider file off one video's blocklist.
func (l *Ledger) Unblock(ctx context.Context, provider, fileID, mediaKey string) error {
	if !l.ok() {
		return nil
	}
	_, err := l.db.ExecContext(ctx, `DELETE FROM subtitle_blocklist WHERE provider = ? AND file_id = ? AND media_key = ?`, provider, fileID, mediaKey)
	return err
}

// Blocklisted returns the set of provider file ids blocked for one video — the set a
// candidate list is filtered against before anything is downloaded. On error the caller
// must not download (fail closed).
func (l *Ledger) Blocklisted(ctx context.Context, provider, mediaKey string) (map[string]bool, error) {
	out := map[string]bool{}
	if !l.ok() {
		return out, nil
	}
	rows, err := l.db.QueryContext(ctx, `SELECT file_id FROM subtitle_blocklist WHERE provider = ? AND media_key = ?`, provider, mediaKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// BlockedFor lists one video's blocklist rows, newest first (for the per-file view).
func (l *Ledger) BlockedFor(ctx context.Context, mediaKey string) ([]BlockEntry, error) {
	if !l.ok() {
		return nil, nil
	}
	rows, err := l.db.QueryContext(ctx, `SELECT provider, file_id, media_key, lang, reason, at FROM subtitle_blocklist
		WHERE media_key = ? ORDER BY at DESC`, mediaKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockEntry
	for rows.Next() {
		var e BlockEntry
		if err := rows.Scan(&e.Provider, &e.FileID, &e.MediaKey, &e.Lang, &e.Reason, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
