package subtitles

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/langs"
)

// The needs-attention board: why the subtitles that are still missing are missing,
// grouped by the reason the last run gave (subtitle_backoff's primary_reason) plus the
// orphaned subtitles the sweep is leaving for the owner, each bucket with the one fix
// that clears it. Read from one backoff query and the last library pass — no file is
// probed.

// Fixes a bucket offers.
const (
	FixCreds         = "creds"          // the OpenSubtitles login was rejected: fix the credentials
	FixDownloadLarge = "download_large" // whisper needs a model (large-v3 to translate)
	FixRetry         = "retry"          // try again now, ignoring the wait
	FixTryAI         = "try_ai"         // no download fits: make it with the AI now
	FixAdopt         = "adopt"          // an orphaned subtitle: adopt it or replace it
)

// ReasonOrphaned is the bucket for languages covered only by an orphaned subtitle.
const ReasonOrphaned = "orphaned"

// attentionItemCap is how many items a bucket lists; Count covers the rest.
const attentionItemCap = 50

// AttentionItem is one file language in a bucket.
type AttentionItem struct {
	Title       string   `json:"title"`
	Kind        string   `json:"kind"` // "movie" | "episode"
	MovieID     int64    `json:"movie_id,omitempty"`
	SeriesID    int64    `json:"series_id,omitempty"`
	Season      int      `json:"season,omitempty"`
	Episode     int      `json:"episode,omitempty"`
	MediaKey    string   `json:"media_key"`
	Lang        string   `json:"lang"`
	Variant     string   `json:"variant,omitempty"`
	Detail      string   `json:"detail,omitempty"`
	At          int64    `json:"at,omitempty"`            // when the last run gave up (unix)
	NextRetryAt int64    `json:"next_retry_at,omitempty"` // when the sweep tries again (0 = an event or a button)
	Orphans     []string `json:"orphans,omitempty"`       // ReasonOrphaned: the files in the folder
}

// AttentionBucket is every file language missing for one reason.
type AttentionBucket struct {
	Reason  string          `json:"reason"` // a Result, or ReasonOrphaned
	Count   int             `json:"count"`
	Items   []AttentionItem `json:"items"`
	Fix     string          `json:"fix,omitempty"`
	ResetAt int64           `json:"reset_at,omitempty"` // quota: when the daily allowance resets
}

// AttentionBoard is GET /subtitles/attention.
type AttentionBoard struct {
	Buckets []AttentionBucket `json:"buckets"`
	Total   int               `json:"total"`
	// Sources counts the subtitles Arrmada knows the origin of, by source (embedded,
	// opensubtitles, ai-transcribe…); Untracked the movie subtitles it doesn't (there
	// before Arrmada, or put there by hand).
	Sources   map[string]int `json:"sources"`
	Untracked int            `json:"untracked"`
	Scanned   bool           `json:"scanned"` // a library pass has finished (orphans and titles come from it)
}

// fixFor is the one-click fix for a reason.
func fixFor(reason string) string {
	switch Result(reason) {
	case ResultAuthFailed:
		return FixCreds
	case ResultModelMissing:
		return FixDownloadLarge
	case ResultNoMatch, ResultBelowScore, ResultOutOfSync:
		return FixTryAI
	case ResultQuota, ResultRateLimited, ResultProviderError, ResultAIFailed, ResultNoAudio:
		return FixRetry
	}
	if reason == ReasonOrphaned {
		return FixAdopt
	}
	return "" // impossible, ocr_needed: nothing to press
}

// Attention builds the board.
func (s *Service) Attention(ctx context.Context) (AttentionBoard, error) {
	rows, err := s.ledger.LoadBackoffs(ctx)
	if err != nil {
		return AttentionBoard{}, err
	}
	s.snap.mu.Lock()
	movies := make(map[int64]FileSubs, len(s.snap.movies))
	for _, m := range s.snap.movies {
		movies[m.MovieID] = m
	}
	shows := make(map[int64]string, len(s.snap.groups))
	for _, g := range s.snap.groups {
		shows[g.SeriesID] = g.Title
	}
	scanned := !s.snap.at.IsZero()
	s.snap.mu.Unlock()

	byReason := map[string]*AttentionBucket{}
	add := func(reason string, it AttentionItem) {
		b := byReason[reason]
		if b == nil {
			b = &AttentionBucket{Reason: reason, Fix: fixFor(reason)}
			byReason[reason] = b
		}
		b.Count++
		b.Items = append(b.Items, it)
	}
	for _, r := range rows {
		it, ok := attentionItem(r, movies, shows)
		if !ok {
			continue // made since, or the file changed
		}
		add(string(r.PrimaryReason), it)
	}
	// Orphans: a language only an old-named subtitle covers. The sweep leaves it alone
	// until the owner adopts or replaces it.
	for _, m := range movies {
		for _, ls := range m.Languages {
			if ls.Have || !ls.Orphan {
				continue
			}
			it := AttentionItem{Title: titleYearOf(m), Kind: "movie", MovieID: m.MovieID, MediaKey: mediaKeyOf(&m), Lang: ls.Lang}
			for _, o := range m.Orphans {
				if o.Lang == "" || langMatches(o.Lang, ls.Lang) {
					it.Orphans = append(it.Orphans, o.Name)
				}
			}
			it.Detail = "only " + strings.Join(it.Orphans, ", ") + " covers it, and Plex won't pair it"
			add(ReasonOrphaned, it)
		}
	}

	board := AttentionBoard{Buckets: []AttentionBucket{}, Scanned: scanned}
	for _, b := range byReason {
		sort.SliceStable(b.Items, func(i, j int) bool {
			if b.Items[i].At != b.Items[j].At {
				return b.Items[i].At > b.Items[j].At
			}
			return b.Items[i].Title < b.Items[j].Title
		})
		if len(b.Items) > attentionItemCap {
			b.Items = b.Items[:attentionItemCap]
		}
		if b.Reason == string(ResultQuota) {
			if reset := s.quotaResetAt(); reset.After(time.Now()) {
				b.ResetAt = reset.Unix()
			}
		}
		board.Total += b.Count
		board.Buckets = append(board.Buckets, *b)
	}
	sort.SliceStable(board.Buckets, func(i, j int) bool {
		return bucketRank(board.Buckets[i].Reason) < bucketRank(board.Buckets[j].Reason)
	})
	board.Sources, err = s.ledger.SourceCounts(ctx)
	if err != nil {
		return AttentionBoard{}, err
	}
	for _, m := range movies {
		for _, ls := range m.Languages {
			if ls.Have && (ls.Health == nil || ls.Health.Source == "") {
				board.Untracked++
			}
		}
	}
	return board, nil
}

// bucketRank orders buckets: what the owner can fix first, orphans last.
func bucketRank(reason string) int {
	if r, ok := reasonRank[Result(reason)]; ok {
		return r
	}
	return 100
}

// attentionItem turns a backoff row into a board item, or ok=false when the language is
// there now or the row is about a file that has since been replaced.
func attentionItem(b Backoff, movies map[int64]FileSubs, shows map[int64]string) (AttentionItem, bool) {
	it := AttentionItem{MediaKey: b.MediaKey, Lang: b.Lang, Variant: b.Variant, Detail: b.Detail, At: b.UpdatedAt, NextRetryAt: b.NextRetryAt}
	ref, ok := parseMediaKey(b.MediaKey)
	if !ok {
		return it, false
	}
	it.Kind, it.MovieID, it.SeriesID, it.Season, it.Episode = ref.Kind, ref.MovieID, ref.SeriesID, ref.Season, ref.Episode
	if ref.Kind == "movie" {
		m, known := movies[ref.MovieID]
		if known {
			if m.Path != b.VideoPath {
				return it, false // upgraded since: the row is about the old file
			}
			for _, ls := range m.Languages {
				if ls.Have && langMatches(ls.Lang, b.Lang) {
					return it, false
				}
			}
			it.Title = titleYearOf(m)
		}
	} else {
		// Episodes aren't in the pass's flat list: one directory listing says whether it's
		// there now.
		for _, p := range scanSidecars(b.VideoPath, []string{b.Lang}, "episode").Present {
			if langMatches(p, b.Lang) {
				return it, false
			}
		}
		if show := shows[ref.SeriesID]; show != "" {
			it.Title = fmt.Sprintf("%s - S%02dE%02d", show, ref.Season, ref.Episode)
		}
	}
	if it.Title == "" {
		base := filepath.Base(b.VideoPath)
		it.Title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return it, true
}

func titleYearOf(m FileSubs) string {
	if m.Year > 0 {
		return fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	return m.Title
}

// parseMediaKey reads a Job.key() media key back into a catalog reference.
func parseMediaKey(k string) (MediaRef, bool) {
	parts := strings.Split(k, ":")
	num := func(s string) (int64, bool) {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil && n >= 0
	}
	switch {
	case len(parts) == 2 && parts[0] == "m":
		id, ok := num(parts[1])
		return MediaRef{Kind: "movie", MovieID: id}, ok && id > 0
	case len(parts) == 4 && parts[0] == "e":
		id, ok1 := num(parts[1])
		se, ok2 := num(parts[2])
		ep, ok3 := num(parts[3])
		return MediaRef{Kind: "episode", SeriesID: id, Season: int(se), Episode: int(ep)}, ok1 && ok2 && ok3 && id > 0
	}
	return MediaRef{}, false
}

// AttentionCount is how many file languages need the owner — the board's total minus the
// ones that simply have no subtitle anywhere yet (no match, below score, impossible):
// what the Needs-you feed counts.
func (s *Service) AttentionCount(ctx context.Context) (int, error) {
	acts, err := s.Actionable(ctx)
	n := 0
	for _, a := range acts {
		n += a.Count
	}
	return n, err
}

// Actionable is one reason the owner has to do something about, for the Needs-you feed.
type Actionable struct {
	Reason string
	Count  int
	Since  int64 // unix: the oldest row's last run
	Detail string
}

// actionableReasons are the buckets that need a person: a rejected login, a model to
// download, whisper failing. The rest retry by themselves or simply wait for a subtitle
// to exist.
var actionableReasons = []Result{ResultAuthFailed, ResultModelMissing, ResultAIFailed}

// Actionable counts the backoff rows for each reason that needs a person (one cheap
// query; the board's presence check isn't repeated here).
func (s *Service) Actionable(ctx context.Context) ([]Actionable, error) {
	var out []Actionable
	for _, r := range actionableReasons {
		n, since, detail, err := s.ledger.backoffSummary(ctx, r)
		if err != nil {
			return out, err
		}
		if n > 0 {
			out = append(out, Actionable{Reason: string(r), Count: n, Since: since, Detail: detail})
		}
	}
	return out, nil
}

// backoffSummary is how many rows are parked for a reason, since when, and one detail.
func (l *Ledger) backoffSummary(ctx context.Context, reason Result) (int, int64, string, error) {
	if !l.ok() {
		return 0, 0, "", nil
	}
	var n int
	var since *int64
	var detail *string
	err := l.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(updated_at), MAX(detail) FROM subtitle_backoff WHERE primary_reason = ?`, string(reason)).Scan(&n, &since, &detail)
	if err != nil {
		return 0, 0, "", err
	}
	var s int64
	var d string
	if since != nil {
		s = *since
	}
	if detail != nil {
		d = *detail
	}
	return n, s, d, nil
}

// SourceCounts counts the live subtitles by where they came from (forced extras aside).
func (l *Ledger) SourceCounts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	if !l.ok() {
		return out, nil
	}
	rows, err := l.db.QueryContext(ctx, `SELECT source, COUNT(*) FROM subtitle_files WHERE superseded_at IS NULL AND variant != 'forced' GROUP BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, err
		}
		out[src] = n
	}
	return out, rows.Err()
}

// --- retry ---

// RetryItem names one file language to try again.
type RetryItem struct {
	MediaKey string `json:"media_key"`
	Lang     string `json:"lang"`
	Variant  string `json:"variant,omitempty"`
}

// ErrBadRetry is a retry request that names nothing usable.
var ErrBadRetry = errors.New("name items or a bucket to retry")

// Retry tries file languages again now: their backoff rows go, and each file is queued
// at manual priority — an ensure job, or with mode "ai" an AI redo of that language.
// bucket retries every row parked for that reason. Returns how many jobs were queued.
func (s *Service) Retry(ctx context.Context, items []RetryItem, bucket, mode string) (int, error) {
	if mode != "" && mode != ModeRedoAI {
		return 0, fmt.Errorf("%w: mode must be empty or %q", ErrBadRetry, ModeRedoAI)
	}
	if bucket != "" {
		if _, known := reasonRank[Result(bucket)]; !known {
			return 0, fmt.Errorf("%w: %q can't be retried", ErrBadRetry, bucket) // orphans are adopted or replaced
		}
		rows, err := s.ledger.LoadBackoffs(ctx)
		if err != nil {
			return 0, err
		}
		for _, b := range rows {
			if string(b.PrimaryReason) == bucket {
				items = append(items, RetryItem{MediaKey: b.MediaKey, Lang: b.Lang, Variant: b.Variant})
			}
		}
	}
	if len(items) == 0 && bucket == "" {
		return 0, ErrBadRetry
	}
	queued := 0
	seen := map[string]bool{}
	for _, it := range items {
		ref, ok := parseMediaKey(it.MediaKey)
		lang := langs.Canonical(it.Lang)
		if !ok || lang == "" {
			return queued, fmt.Errorf("%w: %q %q", ErrBadRetry, it.MediaKey, it.Lang)
		}
		if err := s.ledger.DeleteBackoff(ctx, it.MediaKey, lang, it.Variant); err != nil {
			return queued, err
		}
		key := it.MediaKey
		if mode != "" {
			key += "|" + lang
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		var err error
		switch {
		case (ref.Kind == "movie" && s.movies == nil) || (ref.Kind == "episode" && s.series == nil):
			err = errors.New("no catalog for that kind of title")
		case mode == ModeRedoAI:
			_, err = s.QueueRedo(ctx, ref, lang, "", ModeRedoAI, nil, nil)
		case ref.Kind == "movie":
			_, err = s.QueueMovie(ctx, ref.MovieID, false, PrioManual)
		default:
			_, err = s.QueueEpisode(ctx, ref.SeriesID, ref.Season, ref.Episode, false, PrioManual)
		}
		if err != nil {
			s.log.Debug("subtitles: retry skipped a file", "media_key", it.MediaKey, "err", err)
			continue
		}
		queued++
	}
	return queued, nil
}
