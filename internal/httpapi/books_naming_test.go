package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The book templates read back as the defaults until set, and a template that would break
// the library (no {Title} in the book's folder, a / in a file name) is refused with a
// reason, saving nothing.
func TestBookNamingSettings(t *testing.T) {
	a := settingsAPI(t)
	_, got := getSettings(t, a)
	for key, want := range map[string]string{
		"naming_audiobook_folder": library.DefaultAudiobookFolder, "naming_audiobook_file": library.DefaultAudiobookFile,
		"naming_audiobook_track": library.DefaultAudiobookTrack, "naming_ebook_folder": library.DefaultEbookFolder,
		"naming_ebook_file": library.DefaultEbookFile,
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want the default %q", key, got[key], want)
		}
	}
	for body, why := range map[string]string{
		`{"naming_audiobook_folder":"{Author}"}`:                        "{Title}",
		`{"naming_audiobook_folder":"{Author}/{Nope}/{Title}"}`:         "isn't a token",
		`{"naming_ebook_file":"{Author}/{Title}"}`:                      "can't have /",
		`{"naming_audiobook_track":"{Title}"}`:                          "{Part} or {Original}",
		`{"naming_audiobook_folder":""}`:                                "can't be empty",
		`{"write_nfo":true,"naming_ebook_folder":"<{Title}>/{Author}"}`: "{Title}",
	} {
		w := putSettings(t, a, body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), why) {
			t.Errorf("%s: HTTP %d %s, want 400 mentioning %q", body, w.Code, w.Body.String(), why)
		}
	}
	if _, after := getSettings(t, a); after["write_nfo"] != false {
		t.Error("a refused body must save nothing at all")
	}
	if w := putSettings(t, a, `{"naming_audiobook_folder":"{Author}/{Series}/{Series Position} - {Title}"}`); w.Code != http.StatusOK {
		t.Fatalf("Audiobookshelf layout: HTTP %d %s", w.Code, w.Body.String())
	}
	// A manager runs naming, as for movies.
	if w := putSettingsAs(t, a, auth.RoleManager, `{"naming_ebook_file":"{Title} - {Author}"}`); w.Code != http.StatusOK {
		t.Errorf("manager: HTTP %d %s", w.Code, w.Body.String())
	}
}

// The live preview renders the sample books, and names the field a broken template is in.
func TestBookNamingPreview(t *testing.T) {
	s := newRouteServer(t, nil)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	q := url.Values{"kind": {"audiobook"}, "folder": {"{Author}/{Series}/{Series Position} - {Title}"}, "file": {"{Title}"}, "track": {"{Title} - Part {Part}"}}
	rec := s.do("GET", "/api/v1/books/naming/preview?"+q.Encode(), mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: HTTP %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Samples []library.BookNamingSample `json:"samples"`
		Error   *library.BookNamingError   `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != nil || len(got.Samples) < 3 || got.Samples[0].Paths[0] != "Pierce Brown/Red Rising Saga/2 - Golden Son/Golden Son.m4b" {
		t.Fatalf("preview = %+v", got)
	}
	if multi := got.Samples[2].Paths; len(multi) != 3 || multi[0] != "Frank Herbert/Dune/1 - Dune/Dune - Part 01.mp3" {
		t.Errorf("multi-file sample = %v", multi)
	}
	q.Set("track", "{Title}")
	rec = s.do("GET", "/api/v1/books/naming/preview?"+q.Encode(), mgr)
	got.Error = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.Error == nil || got.Error.Field != "track" {
		t.Errorf("a broken track template: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("GET", "/api/v1/books/naming/presets", mgr); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "audiobookshelf") {
		t.Errorf("presets: HTTP %d %s", rec.Code, rec.Body)
	}
}

// The library-wide book rename: a preview that changes nothing, a stale token refused,
// then the job moves the files and repoints the book.
func TestBookOrganizePreviewAndApply(t *testing.T) {
	root := t.TempDir()
	var runner *jobs.Runner
	var bs *books.Service
	var imp *library.Importer
	s := newRouteServer(t, func(d *Deps) {
		bs = books.NewService(d.Store.DB(), nil, d.Log)
		d.Books = bs
		imp = library.NewImporter("", d.Log)
		imp.SetRoots("", "", root, root)
		c := automation.New(nil, nil, nil, nil, d.Store.DB(), nil, d.Log, "")
		c.SetSeries(nil, imp)
		c.SetBooks(bs)
		d.Automation = c
		r, err := jobs.New(context.Background(), d.Store.DB(), d.Log, nil)
		if err != nil {
			t.Fatal(err)
		}
		runner = r
		d.Jobs = r
	})
	t.Cleanup(func() { runner.Shutdown(time.Second) })
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	ctx := context.Background()
	added, _ := bs.AddWorks(ctx, []metadata.BookResult{{Key: "hc:1", Title: "Golden Son", Author: "Pierce Brown"}}, "", true)
	if len(added) != 1 {
		t.Fatal("book not added")
	}
	_ = bs.SetSeries(ctx, added[0].ID, "Red Rising Saga", 2)
	old := filepath.Join(root, "Pierce Brown", "Golden Son", "Golden Son.m4b")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("gs"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = bs.MarkImported(ctx, added[0].ID, books.KindAudiobook, old, "M4B", 2, 1)

	preview := func() (p struct {
		Counts bookOrganizeCounts           `json:"counts"`
		Items  []automation.BookRenameItem `json:"items"`
		Token  string                       `json:"token"`
	}) {
		t.Helper()
		rec := s.do("GET", "/api/v1/books/organize", mgr)
		if rec.Code != http.StatusOK {
			t.Fatalf("preview: HTTP %d %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := preview(); p.Counts.Same != 1 || p.Counts.Rename != 0 {
		t.Fatalf("default scheme: %+v", p.Counts)
	}
	imp.SetBookNaming(fixedNaming{audio: library.BookNamingPresets[1].Audiobook, ebook: library.BookNamingPresets[1].Ebook})
	p := preview()
	want := filepath.Join(root, "Pierce Brown", "Red Rising Saga", "2 - Golden Son", "Golden Son.m4b")
	if p.Counts.Rename != 1 || len(p.Items) != 1 || p.Items[0].Proposed != want {
		t.Fatalf("Audiobookshelf scheme: %+v", p)
	}
	if fileBody(t, old) != "gs" {
		t.Fatal("the preview must not move anything")
	}
	if rec := s.doJSON("POST", "/api/v1/books/organize", mgr, `{"token":"stale"}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale token: HTTP %d", rec.Code)
	}
	rec := s.doJSON("POST", "/api/v1/books/organize", mgr, `{"token":"`+p.Token+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: HTTP %d %s", rec.Code, rec.Body)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := runner.Wait(wctx, started.JobID); err != nil {
		t.Fatal(err)
	}
	job, _ := runner.Get(wctx, started.JobID)
	var sum bookOrganizeSummary
	if err := json.Unmarshal(job.Result, &sum); err != nil || sum.Renamed != 1 {
		t.Fatalf("job %+v: %v", job, err)
	}
	if fileBody(t, want) != "gs" {
		t.Error("the audiobook should be at its new name")
	}
	if b, _ := bs.Get(ctx, added[0].ID); b.Audiobook.Path != want {
		t.Errorf("record at %q, want %q", b.Audiobook.Path, want)
	}
	// The per-book rename button answers the same way, with nothing left to do.
	if rec := s.doJSON("POST", "/api/v1/books/"+jsonInt(added[0].ID)+"/rename", mgr, `{}`);rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"renamed":0`) {
		t.Errorf("per-book rename: HTTP %d %s", rec.Code, rec.Body)
	}
}

type fixedNaming struct{ audio, ebook library.BookNaming }

func (n fixedNaming) AudiobookNaming() library.BookNaming { return n.audio }
func (n fixedNaming) EbookNaming() library.BookNaming     { return n.ebook }
