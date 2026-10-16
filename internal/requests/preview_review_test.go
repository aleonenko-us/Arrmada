package requests

import (
	"context"
	"testing"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
)

// foundPreview stores a found preview for request id, checked for seasons.
func foundPreview(t *testing.T, s *Service, id int64, profile string, seasons []int) {
	t.Helper()
	res := automation.Preview{Outcome: automation.PreviewFound, Profile: profile, Season: 1,
		Releases: []automation.PreviewRelease{{Title: "Show.S01-S04.1080p.WEB-DL", Indexer: "Fake"}}}
	if ok, err := s.repo.savePreview(context.Background(), id, Preview{CheckedAt: 100, Profile: profile, Seasons: seasons, Result: &res, tokens: []string{"t"}}); err != nil || !ok {
		t.Fatalf("save preview: %v %v", ok, err)
	}
}

// Approve & grab takes a show's previewed releases only for exactly the seasons they were
// checked for — the whole show only as the whole show. A pack picked for four seasons is no
// pick for an approval trimmed to one, nor for the whole show spelled out season by season.
func TestPreviewGrabNeedsTheSameSeasons(t *testing.T) {
	s, _, _, _ := previewFixture(t)
	ctx := context.Background()
	req, err := s.repo.Create(ctx, Request{MediaType: "series", TMDBID: 1399, Title: "Endshow", Status: StatusPending, Seasons: []int{1, 2, 3, 4}})
	if err != nil {
		t.Fatal(err)
	}
	foundPreview(t, s, req.ID, "hd", []int{4, 3, 2, 1})
	for _, c := range []struct {
		seasons []int
		mode    string
	}{
		{[]int{1, 2, 3, 4}, "preview"},
		{[]int{1}, "search"},
		{[]int{1, 2, 3}, "search"},
		{nil, "search"},
	} {
		r := req
		r.Seasons = c.seasons
		_, mode, err := s.planPreviewGrab(ctx, r, "hd", 0)
		if err != nil || mode.Mode != c.mode || (c.mode == "search" && mode.Reason != "seasons") {
			t.Errorf("approving %v: %+v, %v; want %s", c.seasons, mode, err, c.mode)
		}
	}
	whole, err := s.repo.Create(ctx, Request{MediaType: "series", TMDBID: 1400, Title: "Wholeshow", Status: StatusPending})
	if err != nil {
		t.Fatal(err)
	}
	foundPreview(t, s, whole.ID, "hd", nil)
	if _, mode, _ := s.planPreviewGrab(ctx, whole, "hd", 0); mode.Mode != "preview" {
		t.Errorf("whole show as asked: %+v", mode)
	}
	spelled := whole
	spelled.Seasons = []int{1, 2, 3, 4}
	if _, mode, _ := s.planPreviewGrab(ctx, spelled, "hd", 0); mode.Mode != "search" || mode.Reason != "seasons" {
		t.Errorf("whole show spelled out: %+v", mode)
	}
}

// A title already in the library keeps its own profile through approval: the preview is
// ranked under it and stored with it, Approve & grab holds whatever the approval picks, and
// goes back to searching once the title's own profile changes.
func TestPreviewGrabFollowsTheLibraryProfile(t *testing.T) {
	s, fp, _, _ := previewFixture(t)
	ctx := context.Background()
	fp.libProfile = "custom:own"
	req := pendingMovie(t, s, 949, "Heat")
	pv, err := s.RefreshPreview(ctx, req.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Profile != "custom:own" || pv.Result == nil || !pv.Result.InLibrary {
		t.Fatalf("preview = %+v", pv)
	}
	if _, mode, _ := s.planPreviewGrab(ctx, req, "uhd", pv.CheckedAt); mode.Mode != "preview" {
		t.Fatalf("library title approved under another pick: %+v", mode)
	}
	fp.mu.Lock()
	fp.libProfile = "custom:changed"
	fp.mu.Unlock()
	if _, mode, _ := s.planPreviewGrab(ctx, req, "uhd", pv.CheckedAt); mode.Mode != "search" || mode.Reason != "profile" {
		t.Fatalf("after the title's profile changed: %+v", mode)
	}
}

// A preview that panics never leaves its request marked as being checked.
func TestPreviewPanicDoesNotStickChecking(t *testing.T) {
	s, fp, _, _ := previewFixture(t)
	req := pendingMovie(t, s, 949, "Heat")
	fp.panics = true
	func() {
		defer func() { _ = recover() }()
		_ = s.RunPreviewQueue(context.Background())
	}()
	if s.previewChecking(req.ID) {
		t.Fatal("a panicking preview left the request checking")
	}
	fp.panics = false
	if _, err := s.RefreshPreview(context.Background(), req.ID, "", nil); err != nil {
		t.Fatalf("the request can't be previewed again: %v", err)
	}
}

// Approve & grab when a search of the film is already queued: that search runs, and the
// answer says so rather than claiming the previewed release was grabbed.
func TestApproveGrabSaysWhenASearchIsAlreadyQueued(t *testing.T) {
	s, fp, rec, _ := previewFixture(t)
	ctx := context.Background()
	req := pendingMovie(t, s, 949, "Heat")
	pv, err := s.RefreshPreview(ctx, req.ID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A Search click already queued the film's search (the film is id 1 once added).
	if _, err := s.coord.EnqueueMovieSearch(ctx, rec, 1, jobs.Spec{Kind: "movie.search", Target: "movie:1",
		Fn: func(context.Context, *jobs.Progress) (any, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Approve(ctx, req.ID, ApproveOptions{GrabPreview: true, PreviewAt: pv.CheckedAt})
	if err != nil {
		t.Fatal(err)
	}
	if got.Grab == nil || got.Grab.Mode != "search" || got.Grab.Reason != "already queued" {
		t.Fatalf("grab = %+v", got.Grab)
	}
	if len(fp.grabs) != 0 {
		t.Fatalf("grabbed %v", fp.grabs)
	}
}
