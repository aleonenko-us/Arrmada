package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// Release previews: before a request that needs approval is decided, Arrmada looks up the
// release a search would grab for it (automation.PreviewTitle — a dry run that grabs
// nothing, stores no search attempt and leaves the title's backoff alone), so whoever
// approves it sees what they'd get, or that nothing is out yet. Staff only: requesters
// never see a preview (shapeRequests drops it).
//
// Pacing, so a burst of requests can't flood the indexers:
//   - a new pending request is queued, not searched: the request-previews task takes at
//     most previewQueuePerRun of the oldest every minute;
//   - every preview, queued or opened on demand, takes a slot in one budget of
//     previewPerMinute a minute; the queue leaves one slot of it for staff opening a
//     request, who wait for a slot rather than skip it;
//   - one request is previewed by one check at a time (a second waits for the first);
//   - previews are background searches: an indexer backing off is left alone, and a
//     title being searched for real is retried later instead.
// Fifty requests at once are therefore previewed over about seventeen minutes.

const (
	// previewStaleAfter is how old a preview may be before opening the request checks
	// again. Releases come and go slowly enough that six hours is still news.
	previewStaleAfter = 6 * time.Hour
	// previewQueuePerRun is the most queued previews one run of the task takes. The task
	// runs every minute.
	previewQueuePerRun = 3
	// previewPerMinute is the budget every preview shares, queued and on demand.
	previewPerMinute = 4
	// previewRetry is when a preview that couldn't be made (the title was being searched,
	// metadata wasn't there) is tried again.
	previewRetry = 10 * time.Minute
	// previewJobTimeout bounds one preview, its wait for a slot included.
	previewJobTimeout = 4 * time.Minute
)

// Preview states, as stored.
const (
	PreviewQueued = "queued" // waiting its turn in the queue
	PreviewDone   = "done"   // checked; Result says what was found
	PreviewBusy   = "busy"   // the title was being searched or downloaded for real; tried again later
	// PreviewChecking is a check running now. Never stored: read from the running set.
	PreviewChecking = "checking"
)

// previewer is the acquisition pipeline's dry run and the grab that follows it, narrowed.
type previewer interface {
	PreviewTitle(ctx context.Context, in automation.PreviewInput) (automation.Preview, error)
	GrabPreviewed(ctx context.Context, kind string, id int64, tokens []string, key string) (automation.SearchOutcome, error)
	PreviewTokensLive(tokens []string, key, kind string) bool
}

var _ previewer = (*automation.Coordinator)(nil)

// SetPreviewer replaces the release previewer (tests; NewService wires the coordinator).
func (s *Service) SetPreviewer(p previewer) { s.previewer = p }

// Preview is a request's release preview, as staff see it.
type Preview struct {
	State     string `json:"state"`      // queued | checking | done | busy
	CheckedAt int64  `json:"checked_at"` // unix seconds of the last finished check; 0 = never
	// Profile is the quality profile the result was ranked under; a different pick in the
	// sheet makes it a preview "for" that profile.
	Profile string `json:"profile"`
	Seasons []int  `json:"seasons,omitempty"` // series: the seasons it covered
	// Stale: never checked, or longer ago than previewStaleAfter. Opening the request
	// checks again.
	Stale bool `json:"stale"`
	// Grabbable: "Approve & grab this" would take exactly these releases (their tokens are
	// still good). False sends that approval to the ordinary search.
	Grabbable bool                `json:"grabbable"`
	Result    *automation.Preview `json:"result,omitempty"`
	tokens    []string
}

// previewKey is what a request's preview tokens are scoped to.
func previewKey(id int64) string { return "request:" + strconv.FormatInt(id, 10) }

// ---- storage ---------------------------------------------------------------

const previewCols = `request_id, state, queued_at, checked_at, profile, seasons, result, tokens`

func scanPreview(row interface{ Scan(...any) error }) (int64, Preview, error) {
	var (
		id                    int64
		p                     Preview
		queuedAt              int64
		seasons, result, toks string
	)
	if err := row.Scan(&id, &p.State, &queuedAt, &p.CheckedAt, &p.Profile, &seasons, &result, &toks); err != nil {
		return 0, Preview{}, err
	}
	p.Seasons = decodeSeasons(seasons)
	if result != "" {
		var res automation.Preview
		if json.Unmarshal([]byte(result), &res) == nil {
			p.Result = &res
		}
	}
	if toks != "" {
		_ = json.Unmarshal([]byte(toks), &p.tokens)
	}
	return id, p, nil
}

// getPreview is one request's stored preview; ok is false when it has none.
func (r *Repo) getPreview(ctx context.Context, id int64) (Preview, bool, error) {
	_, p, err := scanPreview(r.db.QueryRowContext(ctx, `SELECT `+previewCols+` FROM request_previews WHERE request_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Preview{}, false, nil
	}
	return p, err == nil, err
}

// previewsFor is the stored previews of these requests, by request id.
func (r *Repo) previewsFor(ctx context.Context, ids []int64) (map[int64]Preview, error) {
	out := map[int64]Preview{}
	for _, part := range chunks(ids) {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := r.db.QueryContext(ctx, `SELECT `+previewCols+` FROM request_previews WHERE request_id IN (`+marks+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			id, p, err := scanPreview(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = p
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// queuePreview asks for request id to be previewed from at (unix seconds), keeping any
// result it already has to show meanwhile.
func (r *Repo) queuePreview(ctx context.Context, id, at int64) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO request_previews (request_id, state, queued_at) VALUES (?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET state = excluded.state, queued_at = excluded.queued_at`,
		id, PreviewQueued, at)
	return err
}

// retryPreview puts a preview that couldn't be made back in the queue for later, as
// state (queued, or busy for a title being searched), keeping what it had.
func (r *Repo) retryPreview(ctx context.Context, id, at int64, state string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO request_previews (request_id, state, queued_at) VALUES (?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET state = excluded.state, queued_at = excluded.queued_at`, id, state, at)
	return err
}

// duePreviews is the queued previews due by now whose request still waits for approval,
// oldest first.
func (r *Repo) duePreviews(ctx context.Context, now int64, limit int) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT p.request_id FROM request_previews p
		JOIN requests r ON r.id = p.request_id
		WHERE p.state IN (?, ?) AND p.queued_at > 0 AND p.queued_at <= ? AND r.status = ?
		ORDER BY p.queued_at, p.request_id LIMIT ?`, PreviewQueued, PreviewBusy, now, StatusPending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// savePreview stores a finished check — only while the request still waits for approval:
// a check that finishes after the decision has nothing left to inform.
func (r *Repo) savePreview(ctx context.Context, id int64, p Preview) (bool, error) {
	result, err := json.Marshal(p.Result)
	if err != nil {
		return false, err
	}
	toks := ""
	if len(p.tokens) > 0 {
		b, _ := json.Marshal(p.tokens)
		toks = string(b)
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO request_previews (request_id, state, queued_at, checked_at, profile, seasons, result, tokens)
		SELECT ?, ?, 0, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM requests WHERE id = ? AND status = ?)
		ON CONFLICT(request_id) DO UPDATE SET state = excluded.state, queued_at = 0, checked_at = excluded.checked_at,
			profile = excluded.profile, seasons = excluded.seasons, result = excluded.result, tokens = excluded.tokens`,
		id, PreviewDone, p.CheckedAt, p.Profile, encodeSeasons(p.Seasons), string(result), toks, id, StatusPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// deletePreview drops a request's preview: it was decided or removed.
func (r *Repo) deletePreview(ctx context.Context, id int64) {
	_, _ = r.db.ExecContext(ctx, `DELETE FROM request_previews WHERE request_id = ?`, id)
}

// ---- pacing ----------------------------------------------------------------

// takePreviewSlot takes a slot in the per-minute budget when fewer than limit were taken
// in the last minute, else says how long until one frees up.
func (s *Service) takePreviewSlot(limit int) time.Duration {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	now := s.clock()
	kept := s.previewWindow[:0]
	for _, t := range s.previewWindow {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	s.previewWindow = kept
	if len(kept) < limit {
		s.previewWindow = append(s.previewWindow, now)
		return 0
	}
	return kept[0].Add(time.Minute).Sub(now)
}

// beginPreview marks request id as being checked. ok is false while another check of it
// runs; wait closes when that one finishes.
func (s *Service) beginPreview(id int64) (done func(), wait <-chan struct{}, ok bool) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if ch, busy := s.previewRunning[id]; busy {
		return nil, ch, false
	}
	if s.previewRunning == nil {
		s.previewRunning = map[int64]chan struct{}{}
	}
	ch := make(chan struct{})
	s.previewRunning[id] = ch
	return func() {
		s.previewMu.Lock()
		delete(s.previewRunning, id)
		s.previewMu.Unlock()
		close(ch)
	}, nil, true
}

func (s *Service) previewChecking(id int64) bool {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	_, ok := s.previewRunning[id]
	return ok
}

// ---- the queue -------------------------------------------------------------

// queuePreviewFor puts a request that now waits for approval in the preview queue.
// Nothing happens without a previewer (tests, no acquisition pipeline).
func (s *Service) queuePreviewFor(ctx context.Context, req Request) {
	if s.previewer == nil || req.Status != StatusPending {
		return
	}
	if err := s.repo.queuePreview(ctx, req.ID, s.clock().Unix()); err != nil {
		s.log.Warn("request: couldn't queue the release preview", "request", req.ID, "err", err)
	}
}

// RunPreviewQueue checks the oldest queued previews, at most previewQueuePerRun of them
// and only while the per-minute budget leaves a slot for staff opening a request. The
// request-previews task runs it every minute.
func (s *Service) RunPreviewQueue(ctx context.Context) error {
	if s.previewer == nil {
		return nil
	}
	ids, err := s.repo.duePreviews(ctx, s.clock().Unix(), previewQueuePerRun)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return nil
		}
		req, err := s.repo.Get(ctx, id)
		if err != nil || req.Status != StatusPending {
			continue
		}
		done, _, ok := s.beginPreview(id)
		if !ok {
			continue // being checked on demand right now
		}
		if s.takePreviewSlot(previewPerMinute-1) > 0 {
			done()
			return nil // the budget is spent; the next run carries on
		}
		_, err = s.checkPreview(ctx, req, "")
		done()
		if err != nil {
			s.log.Info("request: release preview failed; trying again later", "request", id, "err", err)
			_ = s.repo.retryPreview(ctx, id, s.clock().Add(previewRetry).Unix(), PreviewQueued)
		}
	}
	return nil
}

// RefreshPreview checks a pending request's release preview now, ranked under profile
// ("" = the profile approving it would use): someone opened the request and the preview
// was missing or old, or they picked another profile. It waits for a slot in the
// per-minute budget, and for a check of the same request already running.
func (s *Service) RefreshPreview(ctx context.Context, id int64, profile string, p *jobs.Progress) (Preview, error) {
	if s.previewer == nil {
		return Preview{}, errors.New("release previews aren't available")
	}
	req, err := s.repo.Get(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	if req.Status != StatusPending {
		return Preview{}, ErrNotPending
	}
	if profile != "" && s.quality != nil && !s.quality.Known(ctx, profile) {
		return Preview{}, ErrUnknownProfile
	}
	for {
		done, wait, ok := s.beginPreview(id)
		if ok {
			defer done()
			break
		}
		p.SetMessage("Checking indexers…")
		select {
		case <-wait:
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		}
		// The check that was running may have been for this profile: that answers it.
		if got, ok, _ := s.repo.getPreview(ctx, id); ok && got.State == PreviewDone && got.CheckedAt >= s.clock().Add(-time.Minute).Unix() &&
			got.Profile == s.previewProfile(ctx, req, profile) {
			return s.shapePreview(got, req), nil
		}
	}
	for {
		wait := s.takePreviewSlot(previewPerMinute)
		if wait <= 0 {
			break
		}
		p.SetMessage("Waiting for a free indexer slot…")
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return Preview{}, ctx.Err()
		}
	}
	p.SetMessage("Checking indexers…")
	return s.checkPreview(ctx, req, profile)
}

// checkPreview runs one preview of req and stores what it found. A title being searched
// for real keeps the result it had and is queued again for later.
func (s *Service) checkPreview(ctx context.Context, req Request, profile string) (Preview, error) {
	profile = s.previewProfile(ctx, req, profile)
	in := automation.PreviewInput{
		MediaType: req.MediaType, TMDBID: req.TMDBID, Seasons: req.Seasons,
		BookID: req.BookID, Title: req.Title, Author: req.Author,
		Profile: profile, Key: previewKey(req.ID),
	}
	if in.MediaType == "book" && in.BookID == 0 && s.books != nil {
		if id, ok := s.books.BookIDForKey(ctx, req.OLKey); ok {
			in.BookID = id
		}
	}
	res, err := s.previewer.PreviewTitle(ctx, in)
	if err != nil {
		return Preview{}, err
	}
	if res.Outcome == automation.PreviewBusy {
		_ = s.repo.retryPreview(ctx, req.ID, s.clock().Add(previewRetry).Unix(), PreviewBusy)
		got, _, _ := s.repo.getPreview(ctx, req.ID)
		got.State = PreviewBusy
		if got.Result == nil {
			got.Result = &res
		}
		return s.shapePreview(got, req), nil
	}
	p := Preview{State: PreviewDone, CheckedAt: s.clock().Unix(), Profile: profile, Seasons: req.Seasons, Result: &res, tokens: res.Tokens}
	saved, err := s.repo.savePreview(ctx, req.ID, p)
	if err != nil {
		return Preview{}, err
	}
	if saved {
		s.log.Info("request: release preview", "request", req.ID, "media", req.MediaType, "outcome", res.Outcome,
			"releases", len(res.Releases), "queries", res.Queries)
		s.publishPreview(req)
	}
	return s.shapePreview(p, req), nil
}

// previewProfile is the profile a preview ranks under: the one asked for, else the one
// approving the request would use (the request's own while it exists, else the default).
func (s *Service) previewProfile(ctx context.Context, req Request, profile string) string {
	if profile != "" {
		return profile
	}
	profile = req.QualityProfile
	if s.quality == nil {
		return profile
	}
	if profile != "" && !s.quality.Known(ctx, profile) {
		profile = ""
	}
	if profile == "" {
		profile = s.quality.DefaultProfile(ctx, req.MediaType)
	}
	return profile
}

// publishPreview tells staff pages a request's preview changed. The staff topic only:
// requesters never see previews, and their pages have nothing to refresh.
func (s *Service) publishPreview(req Request) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(eventbus.TopicRequestUpdated, map[string]any{
		"id": req.ID, "status": req.Status, "media_type": req.MediaType, "requested_by": req.RequestedBy, "preview": true,
	})
}

// shapePreview fills in what a stored preview says at read time.
func (s *Service) shapePreview(p Preview, req Request) Preview {
	if s.previewChecking(req.ID) {
		p.State = PreviewChecking
	}
	p.Stale = p.CheckedAt == 0 || s.clock().Sub(time.Unix(p.CheckedAt, 0)) > previewStaleAfter
	p.Grabbable = p.Result != nil && p.Result.Outcome == automation.PreviewFound && len(p.tokens) > 0 &&
		s.previewer != nil && s.previewer.PreviewTokensLive(p.tokens, previewKey(req.ID), req.MediaType)
	if p.Result != nil {
		p.Result.Tokens = nil
	}
	return p
}

// AttachPreviews fills in the release preview of each request waiting for approval. Staff
// only — the HTTP layer calls it for staff, and shapeRequests drops it for anyone else.
// A request with none yet says so as an empty, stale preview, which the sheet checks.
func (s *Service) AttachPreviews(ctx context.Context, reqs []Request) {
	if s.previewer == nil {
		return
	}
	var ids []int64
	for _, rq := range reqs {
		if rq.Status == StatusPending {
			ids = append(ids, rq.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	stored, err := s.repo.previewsFor(ctx, ids)
	if err != nil {
		s.log.Warn("requests: couldn't read the release previews", "err", err)
		return
	}
	for i := range reqs {
		if reqs[i].Status != StatusPending {
			continue
		}
		p, ok := stored[reqs[i].ID]
		if !ok {
			p = Preview{State: PreviewQueued}
		}
		p = s.shapePreview(p, reqs[i])
		reqs[i].Preview = &p
	}
}

// ---- approving with the previewed release ----------------------------------

// ErrPreviewChanged is "Approve & grab this" on a preview that was checked again since the
// approver saw it: they'd be grabbing something they never looked at.
var ErrPreviewChanged = errors.New("the release preview was updated — look at it again before approving")

// ApproveGrab says what an approval's search will be: the previewed release(s) grabbed
// ("preview"), or the ordinary search ("search", with why when a grab was asked for).
type ApproveGrab struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"` // expired | profile | seasons | none
}

// previewGrab is the previewed releases an approval grabs. used is set once the approval
// has handed them to a search job (a title already on disk starts none).
type previewGrab struct {
	tokens []string
	key    string
	used   bool
}

// planPreviewGrab decides whether approving req under profile can grab its previewed
// releases. seenAt is the preview the approver looked at (0: don't check). A preview
// that can't be used falls back to the ordinary search and says why.
func (s *Service) planPreviewGrab(ctx context.Context, req Request, profile string, seenAt int64) (*previewGrab, ApproveGrab, error) {
	search := func(reason string) (*previewGrab, ApproveGrab, error) {
		return nil, ApproveGrab{Mode: "search", Reason: reason}, nil
	}
	if s.previewer == nil {
		return search("none")
	}
	p, ok, err := s.repo.getPreview(ctx, req.ID)
	if err != nil {
		return nil, ApproveGrab{}, err
	}
	if !ok || p.Result == nil || p.CheckedAt == 0 {
		return search("none")
	}
	if seenAt != 0 && seenAt != p.CheckedAt {
		return nil, ApproveGrab{}, ErrPreviewChanged
	}
	if p.Result.Outcome != automation.PreviewFound || len(p.tokens) == 0 {
		return search("none")
	}
	if p.Profile != profile {
		return search("profile")
	}
	if req.MediaType == "series" && p.Result.Season > 0 && len(req.Seasons) > 0 && !containsInt(req.Seasons, p.Result.Season) {
		return search("seasons") // the season it previewed was trimmed off
	}
	key := previewKey(req.ID)
	if !s.previewer.PreviewTokensLive(p.tokens, key, req.MediaType) {
		return search("expired")
	}
	return &previewGrab{tokens: p.tokens, key: key}, ApproveGrab{Mode: "preview"}, nil
}

func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}

// approvalSearch is the search an approval starts for library item id: the ordinary one,
// or with a planned grab, the previewed release(s) — which still falls back to the search
// when they can't be grabbed by then.
func (s *Service) approvalSearch(g *previewGrab, kind string, id int64, search func(ctx context.Context) (automation.SearchOutcome, error)) func(ctx context.Context) (automation.SearchOutcome, error) {
	if g == nil || s.previewer == nil {
		return search
	}
	g.used = true
	return func(ctx context.Context) (automation.SearchOutcome, error) {
		return s.previewer.GrabPreviewed(ctx, kind, id, g.tokens, g.key)
	}
}

// PreviewJobTarget is the job target of a request's preview check.
func PreviewJobTarget(id int64) string { return fmt.Sprintf("request:%d", id) }

// PreviewJobTimeout bounds a preview check started on demand.
const PreviewJobTimeout = previewJobTimeout
