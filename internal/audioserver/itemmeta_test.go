package audioserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// An edit in Arrmada — a manual correction, a series learned, a new cover file — moves
// the item's updatedAt, so an app that keys its cover cache on it fetches the new one.
func TestItemUpdatedAtFollowsEdits(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	ctx := context.Background()
	key := itemKeyFor(h.book.ID, 0)
	updated := func() float64 {
		t.Helper()
		v, ok := h.json("GET", "/api/items/"+key, nil)["updatedAt"].(float64)
		if !ok {
			t.Fatal("updatedAt isn't a number")
		}
		return v
	}
	last := updated()
	step := func(what string, edit func() error) {
		t.Helper()
		if err := edit(); err != nil {
			t.Fatal(err)
		}
		h.srv.Invalidate()
		if now := updated(); now <= last {
			t.Fatalf("after %s updatedAt is %v, want more than %v", what, now, last)
		} else {
			last = now
		}
	}
	books := h.srv.books
	step("a manual correction", func() error {
		return books.OverrideMetadata(ctx, h.book.ID, "Dungeon Crawler Carl (Book 1)", h.book.Author, 2020, "", h.book.CoverURL)
	})
	step("a series learned", func() error { return books.SetSeries(ctx, h.book.ID, "Dungeon Crawler Carl", 1) })
	step("a cover file replaced", func() error { return books.TouchMeta(ctx, h.book.ID) })

	// The list entry says the same.
	items := h.json("GET", "/api/libraries/"+libraryID+"/items?limit=10&page=0", nil)
	if got := obj1(t, list1(t, items["results"])[0])["updatedAt"].(float64); got != last {
		t.Fatalf("list updatedAt %v, item %v", got, last)
	}
}

// A cover asked for by version (?ts= from an app, ?v= from the web player) is cached for
// a year; without one, the headers stay as they were.
func TestCoverCacheHeaders(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	if err := os.MkdirAll(h.srv.coverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.srv.coverDir, fmt.Sprintf("book-%d.jpg", h.book.ID)), []byte("\xff\xd8\xff not really"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := itemKeyFor(h.book.ID, 0)
	cacheOf := func(query string) string {
		t.Helper()
		req, _ := http.NewRequest("GET", h.http.URL+"/api/items/"+key+"/cover"+query, nil)
		req.Header.Set("Authorization", "Bearer "+h.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("cover%s: HTTP %d", query, resp.StatusCode)
		}
		return resp.Header.Get("Cache-Control")
	}
	if got := cacheOf(""); got != "public, max-age=86400" {
		t.Fatalf("plain cover: Cache-Control %q", got)
	}
	for _, q := range []string{"?ts=1760000000000", "?v=3"} {
		if got := cacheOf(q); got != "public, max-age=31536000, immutable" {
			t.Fatalf("cover%s: Cache-Control %q", q, got)
		}
	}
}
