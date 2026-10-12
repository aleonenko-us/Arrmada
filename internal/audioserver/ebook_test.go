package audioserver

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/books"
)

// giveEbook gives the harness book an EPUB edition (a synthetic file in the test's own
// ebook folder) and returns its path.
func (h *harness) giveEbook() string {
	h.t.Helper()
	dir := filepath.Join(h.t.TempDir(), "ebooks", "Matt Dinniman", "Dungeon Crawler Carl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	path := filepath.Join(dir, "Dungeon Crawler Carl.epub")
	if err := os.WriteFile(path, []byte("PK\x03\x04 not really an epub"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	if err := h.srv.books.MarkImported(context.Background(), h.book.ID, books.KindEbook, path, "EPUB", 30, 1); err != nil {
		h.t.Fatal(err)
	}
	h.srv.Invalidate()
	return path
}

// media.ebookFile and the ebook's libraryFiles entry are shaped exactly as
// Audiobookshelf records them, and list entries name the format.
func TestEbookFileMatchesAudiobookshelf(t *testing.T) {
	h := newHarness(t)
	h.giveEbook()
	h.signIn()
	key := itemKeyFor(h.book.ID, 0)
	rec := obj1(t, loadReply(t, newestFixtures(t), "item"))
	got := h.json("GET", "/api/items/"+key, nil)

	wantMedia, gotMedia := obj1(t, rec["media"]), obj1(t, got["media"])
	for _, d := range diffShape("$.media.ebookFile", wantMedia["ebookFile"], gotMedia["ebookFile"]) {
		t.Error(d)
	}
	if gotMedia["ebookFormat"] != "epub" {
		t.Errorf("media.ebookFormat = %v", gotMedia["ebookFormat"])
	}
	files := list1(t, got["libraryFiles"])
	var ebookEntry any
	for _, f := range files {
		if obj1(t, f)["fileType"] == "ebook" {
			ebookEntry = f
		}
	}
	if ebookEntry == nil {
		t.Fatalf("no ebook in libraryFiles: %v", files)
	}
	for _, f := range list1(t, rec["libraryFiles"]) {
		if obj1(t, f)["fileType"] == "ebook" {
			for _, d := range diffShape("$.libraryFiles[]", f, ebookEntry) {
				t.Error(d)
			}
		}
	}
	ef := obj1(t, gotMedia["ebookFile"])
	if ef["ino"] != obj1(t, ebookEntry)["ino"] || ef["ebookFormat"] != "epub" {
		t.Errorf("ebookFile %v doesn't match its library file", ef)
	}
	for _, af := range list1(t, gotMedia["audioFiles"]) {
		if obj1(t, af)["ino"] == ef["ino"] {
			t.Fatal("the ebook's ino collides with an audio file's")
		}
	}

	items := h.json("GET", "/api/libraries/"+libraryID+"/items?limit=10&page=0", nil)
	if f := obj1(t, obj1(t, list1(t, items["results"])[0])["media"])["ebookFormat"]; f != "epub" {
		t.Errorf("list entry ebookFormat = %v", f)
	}
	// A book with no ebook still says null, and the library stays audiobooks-only.
	h2 := newHarness(t)
	h2.signIn()
	if f := obj1(t, h2.json("GET", "/api/items/"+itemKeyFor(h2.book.ID, 0), nil)["media"])["ebookFile"]; f != nil {
		t.Errorf("ebookFile without an ebook = %v", f)
	}
	lib := obj1(t, list1(t, h.json("GET", "/api/libraries", nil)["libraries"])[0])
	if obj1(t, lib["settings"])["audiobooksOnly"] != true {
		t.Error("audiobooksOnly must stay true")
	}
}

// The ebook is served behind the same sign-in as the audio: by the ebook routes and by
// its ino on the file routes. No token is 401; an item without an ebook is 404.
func TestEbookRouteServesWithToken(t *testing.T) {
	h := newHarness(t)
	h.giveEbook()
	key := itemKeyFor(h.book.ID, 0)
	ino := inoFor("ebook/Dungeon Crawler Carl.epub")
	if code, _ := h.do("GET", "/api/items/"+key+"/ebook", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("no token: HTTP %d, want 401", code)
	}
	h.signIn()
	for _, path := range []string{"/ebook", "/ebook/" + ino, "/file/" + ino, "/file/" + ino + "/download"} {
		req, _ := http.NewRequest("GET", h.http.URL+"/api/items/"+key+path, nil)
		req.Header.Set("Authorization", "Bearer "+h.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/epub+zip" || string(body[:2]) != "PK" {
			t.Fatalf("%s: HTTP %d %q", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	if code, _ := h.do("GET", "/api/items/"+key+"/ebook/12345", nil, nil); code != http.StatusNotFound {
		t.Fatalf("wrong file id: HTTP %d, want 404", code)
	}

	bare := newHarness(t)
	bare.signIn()
	if code, _ := bare.do("GET", "/api/items/"+itemKeyFor(bare.book.ID, 0)+"/ebook", nil, nil); code != http.StatusNotFound {
		t.Fatalf("no ebook: HTTP %d, want 404", code)
	}
}

// Reading progress from an ebook reader (ebookLocation / ebookProgress) isn't kept, and
// leaves the audio place exactly where it was.
func TestEbookProgressPatchIsNoop(t *testing.T) {
	h := newHarness(t)
	h.giveEbook()
	h.signIn()
	key := itemKeyFor(h.book.ID, 0)
	sid := h.play(key)
	if code, out := h.do("POST", "/api/session/"+sid+"/sync", map[string]float64{"timeListened": 10, "currentTime": 300}, nil); code != 200 {
		t.Fatalf("sync: HTTP %d %s", code, out)
	}
	before := h.json("GET", "/api/me/progress/"+key, nil)
	if code, out := h.do("PATCH", "/api/me/progress/"+key, map[string]any{"ebookLocation": "epubcfi(/6/4!/4/2/1:0)", "ebookProgress": 0.42}, nil); code != 200 {
		t.Fatalf("patch: HTTP %d %s", code, out)
	}
	after := h.json("GET", "/api/me/progress/"+key, nil)
	for _, k := range []string{"currentTime", "isFinished", "lastUpdate", "progress"} {
		if before[k] != after[k] {
			t.Errorf("%s moved: %v → %v", k, before[k], after[k])
		}
	}
}
