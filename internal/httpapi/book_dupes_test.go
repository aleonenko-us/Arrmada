package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/listening"
)

// The duplicates review is manager-only, lists a novel under two keys, merges only books
// listed together (400 otherwise), never reports listening data, and "Not the same"
// hides a pair for good.
func TestBookDuplicatesReview(t *testing.T) {
	var bs *books.Service
	s := newRouteServer(t, func(d *Deps) {
		bs = books.NewService(d.Store.DB(), nil, d.Log)
		bs.SetMergeHooks(listening.NewStore(d.Store.DB()), nil)
		d.Books = bs
	})
	_, mgr := s.user(t, "manager@example.com", auth.RoleManager)
	kidUser, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	ctx := context.Background()
	add := func(key, title, author string) books.Book {
		t.Helper()
		b, err := books.NewRepo(s.st.DB()).Create(ctx, books.Book{OLKey: key, Title: title, Author: author})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	ol := add("OL1W", "Dune", "Frank Herbert")
	hc := add("hc:1", "Dune", "Frank Herbert")
	stranger := add("OL2W", "Emma", "Jane Austen")
	if err := bs.MarkImported(ctx, ol.ID, books.KindEbook, "/lib/e/ol.epub", "EPUB", 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := bs.MarkImported(ctx, hc.ID, books.KindEbook, "/lib/e/hc.epub", "EPUB", 10, 1); err != nil {
		t.Fatal(err)
	}
	// Someone is listening to one of them: nothing about it may reach the review.
	if _, err := s.st.DB().Exec(`INSERT INTO listen_progress (user_id, item_key, position, updated_at) VALUES (?, ?, 99, 1)`,
		kidUser.ID, listening.ItemKey(hc.ID, 0)); err != nil {
		t.Fatal(err)
	}

	for _, rq := range []struct{ method, path, body string }{
		{"GET", "/api/v1/books/duplicates", ""},
		{"POST", "/api/v1/books/duplicates/merge", fmt.Sprintf(`{"keep_id":%d,"drop_ids":[%d]}`, ol.ID, hc.ID)},
		{"POST", "/api/v1/books/duplicates/ignore", fmt.Sprintf(`{"ids":[%d,%d]}`, ol.ID, hc.ID)},
	} {
		if rec := s.doJSON(rq.method, rq.path, kid, rq.body); rec.Code != http.StatusForbidden {
			t.Fatalf("requester %s %s: HTTP %d", rq.method, rq.path, rec.Code)
		}
	}

	rec := s.do("GET", "/api/v1/books/duplicates", mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: HTTP %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Groups []books.DupGroup  `json:"groups"`
		Past   []books.PastMerge `json:"past_merges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].Books) != 2 || got.Past == nil {
		t.Fatalf("review = %s", rec.Body)
	}
	for _, word := range []string{"listen", "progress", "position", "bookmark"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), word) {
			t.Fatalf("the review mentions %q: %s", word, rec.Body)
		}
	}

	// A drop outside keep's group is refused, and nothing changes.
	if rec := s.doJSON("POST", "/api/v1/books/duplicates/merge", mgr, fmt.Sprintf(`{"keep_id":%d,"drop_ids":[%d]}`, ol.ID, stranger.ID)); rec.Code != http.StatusBadRequest {
		t.Fatalf("merge outside the group: HTTP %d %s", rec.Code, rec.Body)
	}
	if _, err := bs.Get(ctx, stranger.ID); err != nil {
		t.Fatal("the stranger was merged")
	}

	rec = s.doJSON("POST", "/api/v1/books/duplicates/merge", mgr, fmt.Sprintf(`{"keep_id":%d,"drop_ids":[%d]}`, ol.ID, hc.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: HTTP %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"left_files":["/lib/e/hc.epub"]`) {
		t.Fatalf("merge reply = %s", body)
	}
	for _, word := range []string{"listen", "progress", "bookmark", "place"} {
		if strings.Contains(strings.ToLower(body), word) {
			t.Fatalf("the merge reply mentions %q: %s", word, body)
		}
	}
	if _, err := bs.Get(ctx, hc.ID); err == nil {
		t.Fatal("the dropped row is still there")
	}

	// "Not the same" hides a pair for good.
	again := add("hc:2", "Dune", "Frank Herbert")
	if rec := s.doJSON("POST", "/api/v1/books/duplicates/ignore", mgr, fmt.Sprintf(`{"ids":[%d,%d]}`, ol.ID, again.ID)); rec.Code != http.StatusNoContent {
		t.Fatalf("ignore: HTTP %d %s", rec.Code, rec.Body)
	}
	rec = s.do("GET", "/api/v1/books/duplicates", mgr)
	got.Groups, got.Past = nil, nil
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Groups) != 0 || len(got.Past) != 1 || got.Past[0].LostKey != "hc:1" {
		t.Fatalf("after ignore: %s", rec.Body)
	}
	if rec := s.doJSON("POST", "/api/v1/books/duplicates/ignore", mgr, `{"ids":[1]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("ignore one id: HTTP %d", rec.Code)
	}
}
