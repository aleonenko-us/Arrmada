package requests

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// fakePreviewer stands in for the coordinator's dry run: it counts previews, answers
// each with outcome, and records the grabs Approve & grab asks for.
type fakePreviewer struct {
	mu      sync.Mutex
	outcome string
	live    bool
	block   chan struct{} // when set, a preview waits for it to close
	started chan struct{} // when set, signalled as a preview starts
	inputs  []automation.PreviewInput
	grabs   []string // "<kind>:<id>:<key>:<tokens>"
	// libProfile, when set, is a library title's own profile: previews rank under it and so
	// does the search after approving.
	libProfile string
	panics     bool
}

func (f *fakePreviewer) SearchProfile(_ context.Context, in automation.PreviewInput) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.libProfile != "" {
		return f.libProfile
	}
	return in.Profile
}

func (f *fakePreviewer) PreviewTitle(_ context.Context, in automation.PreviewInput) (automation.Preview, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	outcome, block, started, panics := f.outcome, f.block, f.started, f.panics
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if block != nil {
		<-block
	}
	if panics {
		panic("preview blew up")
	}
	if outcome == "" {
		outcome = automation.PreviewFound
	}
	f.mu.Lock()
	profile := in.Profile
	if f.libProfile != "" {
		profile = f.libProfile
	}
	f.mu.Unlock()
	p := automation.Preview{Outcome: outcome, Profile: profile, InLibrary: profile != in.Profile, Releases: []automation.PreviewRelease{}}
	if outcome == automation.PreviewFound {
		p.Reason = automation.ReasonGrabbed
		p.Releases = append(p.Releases, automation.PreviewRelease{Title: "Heat.1995.1080p.BluRay.x264-GRP", Indexer: "Fake", Resolution: "1080p", SizeGB: 12})
		p.Tokens = []string{"tok-" + in.Key}
	}
	return p, nil
}

func (f *fakePreviewer) GrabPreviewed(_ context.Context, kind string, id int64, tokens []string, key string) (automation.SearchOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := ""
	if len(tokens) > 0 {
		t = tokens[0]
	}
	f.grabs = append(f.grabs, kind+":"+strconv.FormatInt(id, 10)+":"+key+":"+t)
	return automation.SearchOutcome{Grabbed: 1, Reason: automation.ReasonGrabbed}, nil
}

func (f *fakePreviewer) PreviewTokensLive([]string, string, string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live
}

func (f *fakePreviewer) previews() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

// previewFixture is quietFixture with a fake previewer and a clock the test moves.
func previewFixture(t *testing.T) (*Service, *fakePreviewer, *recordJobs, *time.Time) {
	t.Helper()
	s, _, rec := quietFixture(t)
	fp := &fakePreviewer{live: true}
	s.SetPreviewer(fp)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, fp, rec, &now
}

func pendingMovie(t *testing.T, s *Service, tmdb int, title string) Request {
	t.Helper()
	req, _, err := s.Create(context.Background(), Request{MediaType: "movie", TMDBID: tmdb, Title: title, RequestedBy: 7, RequestedByName: "alice"}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// Only a request that waits for approval is queued for a preview: not an auto-approved
// one, and not an import's pending rows.
func TestPreviewQueuedOnlyForPendingRequests(t *testing.T) {
	s, _, _, _ := previewFixture(t)
	ctx := context.Background()
	pending := pendingMovie(t, s, 949, "Heat")
	auto, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 603, Title: "The Matrix", RequestedBy: 7}, CreateOptions{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	imported, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 604, Title: "Ronin", RequestedBy: 7}, CreateOptions{Silent: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id     int64
		queued bool
	}{{pending.ID, true}, {auto.ID, false}, {imported.ID, false}} {
		p, ok, err := s.repo.getPreview(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if ok != c.queued || (ok && (p.State != PreviewQueued)) {
			t.Fatalf("request %d: preview %+v (stored %v), want queued=%v", c.id, p, ok, c.queued)
		}
	}
}

// A burst of requests is previewed a few a minute: the queue takes at most three a run and
// leaves the fourth slot of the minute for staff opening a request; a minute on, it
// carries on. A request is previewed once however often it's queued.
func TestPreviewQueueIsPaced(t *testing.T) {
	s, fp, _, now := previewFixture(t)
	ctx := context.Background()
	var reqs []Request
	for i := 0; i < 8; i++ {
		reqs = append(reqs, pendingMovie(t, s, 1000+i, "Film "+strconv.Itoa(i)))
	}
	// Queued twice: still one row, one preview.
	if err := s.repo.queuePreview(ctx, reqs[0].ID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := s.RunPreviewQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := fp.previews(); n != previewQueuePerRun {
		t.Fatalf("first run previewed %d, want %d", n, previewQueuePerRun)
	}
	if fp.inputs[0].TMDBID != 1000 || fp.inputs[0].Key != previewKey(reqs[0].ID) {
		t.Fatalf("first preview = %+v", fp.inputs[0])
	}
	// Same minute: the queue's share is spent.
	if err := s.RunPreviewQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := fp.previews(); n != previewQueuePerRun {
		t.Fatalf("a second run in the same minute previewed %d more", n-previewQueuePerRun)
	}
	// Staff opening a request still gets the last slot without waiting.
	if _, err := s.RefreshPreview(ctx, reqs[7].ID, "", nil); err != nil {
		t.Fatal(err)
	}
	if n := fp.previews(); n != previewQueuePerRun+1 {
		t.Fatalf("on-demand preview didn't run: %d", n)
	}
	*now = now.Add(61 * time.Second)
	if err := s.RunPreviewQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := fp.previews(); n != 2*previewQueuePerRun+1 {
		t.Fatalf("after a minute: %d previews", n)
	}
	// Every request previewed once: the one checked on demand left the queue.
	*now = now.Add(61 * time.Second)
	_ = s.RunPreviewQueue(ctx)
	*now = now.Add(61 * time.Second)
	_ = s.RunPreviewQueue(ctx)
	seen := map[string]int{}
	for _, in := range fp.inputs {
		seen[in.Key]++
	}
	if len(seen) != 8 {
		t.Fatalf("previewed %d distinct requests, want 8: %v", len(seen), seen)
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("%s previewed %d times", k, n)
		}
	}
}

// Two checks of one request at once make one preview: the second waits for the first and
// takes its answer.
func TestPreviewOneCheckPerRequest(t *testing.T) {
	s, fp, _, _ := previewFixture(t)
	ctx := context.Background()
	req := pendingMovie(t, s, 949, "Heat")
	fp.block, fp.started = make(chan struct{}), make(chan struct{}, 2)
	errs := make(chan error, 2)
	go func() { _, err := s.RefreshPreview(ctx, req.ID, "", nil); errs <- err }()
	<-fp.started
	go func() { _, err := s.RefreshPreview(ctx, req.ID, "", nil); errs <- err }()
	time.Sleep(200 * time.Millisecond) // the second is waiting on the first
	close(fp.block)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := fp.previews(); n != 1 {
		t.Fatalf("%d previews for two simultaneous checks", n)
	}
}

// A title being searched for real keeps the result it had and is tried again later.
func TestPreviewBusyIsRetriedLater(t *testing.T) {
	s, fp, _, now := previewFixture(t)
	ctx := context.Background()
	req := pendingMovie(t, s, 949, "Heat")
	fp.outcome = automation.PreviewBusy
	if err := s.RunPreviewQueue(ctx); err != nil {
		t.Fatal(err)
	}
	p, _, _ := s.repo.getPreview(ctx, req.ID)
	if p.State != PreviewBusy || p.CheckedAt != 0 {
		t.Fatalf("preview = %+v", p)
	}
	*now = now.Add(2 * time.Minute)
	fp.outcome = automation.PreviewFound
	_ = s.RunPreviewQueue(ctx)
	if n := fp.previews(); n != 1 {
		t.Fatalf("retried after 2 minutes: %d previews", n)
	}
	*now = now.Add(previewRetry)
	_ = s.RunPreviewQueue(ctx)
	if p, _, _ := s.repo.getPreview(ctx, req.ID); p.State != PreviewDone || p.Result == nil || p.Result.Outcome != automation.PreviewFound {
		t.Fatalf("after the retry: %+v", p)
	}
}

// A preview older than six hours is stale, so opening the request checks again; a fresh
// one isn't. A decided request carries no preview at all.
func TestPreviewStaleness(t *testing.T) {
	s, _, _, now := previewFixture(t)
	ctx := context.Background()
	req := pendingMovie(t, s, 949, "Heat")
	list := []Request{req}
	s.AttachPreviews(ctx, list)
	if list[0].Preview == nil || !list[0].Preview.Stale || list[0].Preview.State != PreviewQueued {
		t.Fatalf("unchecked preview = %+v", list[0].Preview)
	}
	if _, err := s.RefreshPreview(ctx, req.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Hour)
	list = []Request{req}
	s.AttachPreviews(ctx, list)
	p := list[0].Preview
	if p == nil || p.Stale || p.State != PreviewDone || !p.Grabbable || p.Result == nil || len(p.Result.Releases) != 1 {
		t.Fatalf("fresh preview = %+v", p)
	}
	if len(p.Result.Tokens) != 0 {
		t.Fatal("a preview handed out its tokens")
	}
	*now = now.Add(2 * time.Hour)
	list = []Request{req}
	s.AttachPreviews(ctx, list)
	if !list[0].Preview.Stale {
		t.Fatal("a seven-hour-old preview isn't stale")
	}
	decided := req
	decided.Status = StatusApproved
	list = []Request{decided}
	s.AttachPreviews(ctx, list)
	if list[0].Preview != nil {
		t.Fatal("a decided request carries a preview")
	}
}

// Approve & grab: the approval's search job grabs the previewed release by its tokens
// instead of searching, and the preview is gone once decided.
func TestApproveGrabsThePreviewedRelease(t *testing.T) {
	s, fp, rec, _ := previewFixture(t)
	ctx := context.Background()
	searched := 0
	s.coord.(*fakeSearcher).fn = func(context.Context, int64) { searched++ }
	req := pendingMovie(t, s, 949, "Heat")
	pv, err := s.RefreshPreview(ctx, req.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Approve(ctx, req.ID, ApproveOptions{GrabPreview: true, PreviewAt: pv.CheckedAt, DecidedBy: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Grab == nil || got.Grab.Mode != "preview" {
		t.Fatalf("grab = %+v", got.Grab)
	}
	if len(rec.specs) != 1 || rec.specs[0].Kind != "movie.search" {
		t.Fatalf("jobs = %+v", rec.specs)
	}
	if _, err := rec.specs[0].Fn(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if searched != 0 || len(fp.grabs) != 1 || fp.grabs[0] != "movie:1:"+previewKey(req.ID)+":tok-"+previewKey(req.ID) {
		t.Fatalf("searched %d, grabs %v", searched, fp.grabs)
	}
	if _, ok, _ := s.repo.getPreview(ctx, req.ID); ok {
		t.Fatal("the preview outlived the decision")
	}
}

// A token that has expired (or a restart lost) sends Approve & grab to the ordinary search.
func TestApproveGrabFallsBackToSearch(t *testing.T) {
	s, fp, rec, _ := previewFixture(t)
	ctx := context.Background()
	searched := 0
	s.coord.(*fakeSearcher).fn = func(context.Context, int64) { searched++ }
	req := pendingMovie(t, s, 949, "Heat")
	pv, err := s.RefreshPreview(ctx, req.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	fp.live = false
	got, err := s.Approve(ctx, req.ID, ApproveOptions{GrabPreview: true, PreviewAt: pv.CheckedAt})
	if err != nil {
		t.Fatal(err)
	}
	if got.Grab == nil || got.Grab.Mode != "search" || got.Grab.Reason != "expired" {
		t.Fatalf("grab = %+v", got.Grab)
	}
	if _, err := rec.specs[0].Fn(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if searched != 1 || len(fp.grabs) != 0 {
		t.Fatalf("searched %d, grabs %v", searched, fp.grabs)
	}
}

// A preview checked again since the approver looked refuses the approval; one ranked under
// another profile than the approval's searches instead; a plain Approve always searches.
func TestApproveGrabGuards(t *testing.T) {
	s, fp, rec, now := previewFixture(t)
	ctx := context.Background()
	hd, err := s.quality.Create(ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "HD"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.quality.SetDefaultProfile(ctx, quality.MediaMovie, "custom:"+strconv.FormatInt(hd.ID, 10)); err != nil {
		t.Fatal(err)
	}
	other, err := s.quality.Create(ctx, quality.StoredProfile{MediaType: quality.MediaMovie, Name: "4K"})
	if err != nil {
		t.Fatal(err)
	}
	ref4K := "custom:" + strconv.FormatInt(other.ID, 10)

	req := pendingMovie(t, s, 949, "Heat")
	first, err := s.RefreshPreview(ctx, req.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	if _, err := s.RefreshPreview(ctx, req.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, req.ID, ApproveOptions{GrabPreview: true, PreviewAt: first.CheckedAt}); !errors.Is(err, ErrPreviewChanged) {
		t.Fatalf("approve on an old preview: %v", err)
	}
	if got, _ := s.repo.Get(ctx, req.ID); got.Status != StatusPending || len(rec.specs) != 0 {
		t.Fatalf("a refused approval changed something: %+v, jobs %d", got, len(rec.specs))
	}
	got, err := s.Approve(ctx, req.ID, ApproveOptions{GrabPreview: true, Profile: ref4K})
	if err != nil {
		t.Fatal(err)
	}
	if got.Grab == nil || got.Grab.Mode != "search" || got.Grab.Reason != "profile" {
		t.Fatalf("grab under another profile = %+v", got.Grab)
	}

	plain := pendingMovie(t, s, 950, "Ronin")
	if _, err := s.RefreshPreview(ctx, plain.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.Approve(ctx, plain.ID, ApproveOptions{})
	if err != nil || got.Grab != nil {
		t.Fatalf("plain approve = %+v, %v", got.Grab, err)
	}
	for _, sp := range rec.specs {
		if _, err := sp.Fn(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(fp.grabs) != 0 {
		t.Fatalf("an approval without Approve & grab grabbed the preview: %v", fp.grabs)
	}
}

// A decided request can't be previewed, and a preview that finishes after the decision
// isn't stored.
func TestPreviewOnlyWhilePending(t *testing.T) {
	s, fp, _, _ := previewFixture(t)
	ctx := context.Background()
	req := pendingMovie(t, s, 949, "Heat")
	if err := s.Decline(ctx, req.ID, DeclineOptions{Reason: "no"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefreshPreview(ctx, req.ID, "", nil); !errors.Is(err, ErrNotPending) {
		t.Fatalf("preview of a declined request: %v", err)
	}
	if n := fp.previews(); n != 0 {
		t.Fatalf("%d previews of a declined request", n)
	}
	saved, err := s.repo.savePreview(ctx, req.ID, Preview{CheckedAt: 1})
	if err != nil || saved {
		t.Fatalf("saved a preview for a declined request: %v, %v", saved, err)
	}
}
