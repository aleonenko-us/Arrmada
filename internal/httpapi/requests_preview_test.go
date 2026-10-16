package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/requests"
	"github.com/tristenlammi/arrmada/internal/series"
)

// stubPreviewer answers every preview with one release.
type stubPreviewer struct{}

const previewedRelease = "Heat.1995.1080p.BluRay.x264-PREVIEWED"

func (stubPreviewer) PreviewTitle(_ context.Context, in automation.PreviewInput) (automation.Preview, error) {
	return automation.Preview{Outcome: automation.PreviewFound, Reason: automation.ReasonGrabbed, Profile: in.Profile,
		Releases: []automation.PreviewRelease{{Title: previewedRelease, Indexer: "Fake", SizeGB: 12, Seeders: 40}},
		Why:      []string{"Highest resolution on offer"}, Tokens: []string{"secret-preview-token"}}, nil
}
func (stubPreviewer) GrabPreviewed(context.Context, string, int64, []string, string) (automation.SearchOutcome, error) {
	return automation.SearchOutcome{}, nil
}
func (stubPreviewer) PreviewTokensLive([]string, string, string) bool { return true }

// previewServer is requestsServer with release previews wired, and the service at hand.
func previewServer(t *testing.T) (*routeServer, *requests.Service) {
	t.Helper()
	root := t.TempDir()
	var svc *requests.Service
	s := newRouteServer(t, func(d *Deps) {
		db := d.Store.DB()
		d.Books = books.NewService(db, nil, d.Log)
		d.Quality = quality.NewService(db)
		mv := movies.NewService(db, nil, nil, root, "", nil, d.Log)
		sr := series.NewService(db, nil, root, d.Log)
		svc = requests.NewService(db, mv, sr, d.Books, nil, d.Quality, nil, "", d.Log)
		svc.SetPreviewer(stubPreviewer{})
		d.Requests = svc
	})
	return s, svc
}

// What Arrmada would grab for a request is for staff alone: the requester's own detail,
// list and Discover strip never carry it — no preview, no release name — and no page ever
// gets a preview's token.
func TestRequestPreviewIsStaffOnly(t *testing.T) {
	s, svc := previewServer(t)
	requester, asRequester := s.user(t, "rita@example.com", auth.RoleRequester)
	_, asManager := s.user(t, "mo@example.com", auth.RoleManager)
	id := addRequest(t, s, 949, "Heat", "pending", requester.ID, "rita")
	if _, err := svc.RefreshPreview(context.Background(), id, "", nil); err != nil {
		t.Fatal(err)
	}

	staff := s.do("GET", fmt.Sprintf("/api/v1/requests/%d", id), asManager)
	if staff.Code != http.StatusOK || !strings.Contains(staff.Body.String(), previewedRelease) || !strings.Contains(staff.Body.String(), `"preview"`) {
		t.Fatalf("staff detail: HTTP %d %s", staff.Code, staff.Body)
	}
	list := s.do("GET", "/api/v1/requests?section=needs_approval", asManager)
	if !strings.Contains(list.Body.String(), previewedRelease) {
		t.Fatalf("staff list has no preview: %s", list.Body)
	}
	for _, body := range []string{staff.Body.String(), list.Body.String()} {
		if strings.Contains(body, "secret-preview-token") || strings.Contains(body, "tokens") {
			t.Fatalf("a preview token reached staff's browser: %s", body)
		}
	}

	for _, path := range []string{
		fmt.Sprintf("/api/v1/requests/%d", id),
		"/api/v1/requests",
		"/api/v1/requests?section=needs_approval",
		"/api/v1/requests?section=strip",
	} {
		rec := s.do("GET", path, asRequester)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d %s", path, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Heat") {
			t.Fatalf("%s: the requester's own request is missing: %s", path, body)
		}
		if strings.Contains(body, `"preview"`) || strings.Contains(body, previewedRelease) || strings.Contains(body, "secret-preview-token") {
			t.Fatalf("%s: a requester saw the release preview: %s", path, body)
		}
	}
	// Nor can they ask for one.
	if rec := s.do("POST", fmt.Sprintf("/api/v1/requests/%d/preview", id), asRequester); rec.Code != http.StatusForbidden {
		t.Fatalf("requester POST preview: HTTP %d", rec.Code)
	}
}

// Staff check a pending request's preview again as a job; a decided request has none to
// check. Approve & grab on a preview checked again since is refused, the request untouched.
func TestRequestPreviewEndpoint(t *testing.T) {
	s, svc := previewServer(t)
	_, asManager := s.user(t, "mo@example.com", auth.RoleManager)
	id := addRequest(t, s, 949, "Heat", "pending", 7, "rita")

	rec := s.doJSON("POST", fmt.Sprintf("/api/v1/requests/%d/preview", id), asManager, `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST preview: HTTP %d %s", rec.Code, rec.Body)
	}
	declined := addRequest(t, s, 950, "Ronin", "declined", 7, "rita")
	if rec := s.doJSON("POST", fmt.Sprintf("/api/v1/requests/%d/preview", declined), asManager, `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("POST preview of a declined request: HTTP %d", rec.Code)
	}
	if rec := s.doJSON("POST", "/api/v1/requests/999999/preview", asManager, `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("POST preview of no request: HTTP %d", rec.Code)
	}

	pv, err := svc.RefreshPreview(context.Background(), id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"grab_preview": true, "preview_checked_at": %d}`, pv.CheckedAt-60)
	if rec := s.doJSON("POST", fmt.Sprintf("/api/v1/requests/%d/approve", id), asManager, body); rec.Code != http.StatusConflict {
		t.Fatalf("approve & grab on an old preview: HTTP %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Request requests.Request `json:"request"`
	}
	rec = s.do("GET", fmt.Sprintf("/api/v1/requests/%d", id), asManager)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Request.Status != requests.StatusPending || got.Request.Preview == nil || got.Request.Preview.CheckedAt != pv.CheckedAt {
		t.Fatalf("after the refused approval: %+v", got.Request)
	}
}
