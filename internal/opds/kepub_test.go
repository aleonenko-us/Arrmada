package opds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/kepub"
)

// fakeKepub stands in for the KEPUB cache: it "converts" by prefixing the EPUB's bytes.
type fakeKepub struct {
	ready bool
	fail  error
	dir   string
	calls []int64
}

func (f *fakeKepub) Ready(context.Context) bool { return f.ready }

func (f *fakeKepub) Open(_ context.Context, bookID int64, src string) (*os.File, error) {
	f.calls = append(f.calls, bookID)
	if f.fail != nil {
		return nil, f.fail
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(f.dir, fmt.Sprintf("book-%d.kepub.epub", bookID))
	if err := os.WriteFile(p, append([]byte("kepub:"), b...), 0o600); err != nil {
		return nil, err
	}
	return os.Open(p)
}

const (
	koboUA     = "Mozilla/5.0 (Linux; U; Android 2.0; en-us;) AppleWebKit/538.1 (KHTML, like Gecko) Version/4.0 Mobile Safari/538.1 (Kobo Touch 0377/4.38.21908)"
	koreaderUA = "KOReader/2024.04 (Kobo_frost; Linux; arm; https://github.com/koreader/koreader) LuaSocket/3.0.0"
)

func (x *harness) doUA(path, ua string) *httptest.ResponseRecorder {
	x.t.Helper()
	r := httptest.NewRequest("GET", "http://books.example"+path, nil)
	r.SetBasicAuth("reader", "reader-pass-1")
	r.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, r)
	return rec
}

// Every EPUB entry carries a second download, the KEPUB, while conversion is ready; other
// formats don't, and with it off no entry does.
func TestKepubLinks(t *testing.T) {
	x := newHarness(t)
	fk := &fakeKepub{ready: true, dir: t.TempDir()}
	x.h.d.Kepub = fk
	d := x.book("Dune", "Frank Herbert", "Dune.epub", nil)
	m := x.book("Middlemarch", "George Eliot", "m.pdf", nil)

	acq := func(f pFeed, id int64) []pLink {
		for _, e := range f.Entries {
			if e.ID == fmt.Sprintf("urn:arrmada:book:%d", id) {
				var out []pLink
				for _, l := range e.Links {
					if l.Rel == relAcquisition {
						out = append(out, l)
					}
				}
				return out
			}
		}
		t.Fatalf("book %d not in the feed", id)
		return nil
	}
	f := x.feed("/opds/all", "acquisition")
	dl := acq(f, d.ID)
	if len(dl) != 2 || dl[0].Type != "application/epub+zip" || dl[0].Href != fmt.Sprintf("/opds/books/%d/file", d.ID) ||
		dl[1].Type != "application/kepub+zip" || dl[1].Href != fmt.Sprintf("/opds/books/%d/kepub", d.ID) {
		t.Errorf("EPUB downloads = %+v, want the EPUB first, then the KEPUB", dl)
	}
	if ml := acq(f, m.ID); len(ml) != 1 || ml[0].Type != "application/pdf" {
		t.Errorf("PDF downloads = %+v, want the PDF alone", ml)
	}

	fk.ready = false // the setting switched off
	f = x.feed("/opds/all", "acquisition")
	if dl := acq(f, d.ID); len(dl) != 1 {
		t.Errorf("with conversion off, EPUB downloads = %+v, want the EPUB alone", dl)
	}
	x.h.d.Kepub = nil
	f = x.feed("/opds/all", "acquisition")
	if dl := acq(f, d.ID); len(dl) != 1 {
		t.Errorf("with no converter, EPUB downloads = %+v, want the EPUB alone", dl)
	}
}

// The file route sends a KEPUB to Kobo's own software and the EPUB to everyone else,
// KOReader on a Kobo included; the KEPUB route sends a KEPUB to anyone who asks.
func TestKepubServing(t *testing.T) {
	x := newHarness(t)
	fk := &fakeKepub{ready: true, dir: t.TempDir()}
	x.h.d.Kepub = fk
	d := x.book("Dune", "Frank Herbert", "Dune.epub", nil)
	m := x.book("Middlemarch", "George Eliot", "m.pdf", nil)
	file := fmt.Sprintf("/opds/books/%d/file", d.ID)
	kp := fmt.Sprintf("/opds/books/%d/kepub", d.ID)

	isKepub := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == 200 && rec.Body.String() == "kepub:ebook bytes of Dune" &&
			rec.Header().Get("Content-Type") == "application/kepub+zip" &&
			rec.Header().Get("Content-Disposition") == `attachment; filename="Dune - Frank Herbert.kepub.epub"`
	}
	isEPUB := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == 200 && rec.Body.String() == "ebook bytes of Dune" &&
			rec.Header().Get("Content-Type") == "application/epub+zip" &&
			rec.Header().Get("Content-Disposition") == `attachment; filename="Dune - Frank Herbert.epub"`
	}
	if rec := x.doUA(file, koboUA); !isKepub(rec) {
		t.Errorf("Kobo on /file: HTTP %d %q %v, want the KEPUB", rec.Code, rec.Body, rec.Header())
	}
	for _, ua := range []string{koreaderUA, "Moon+ Reader", ""} {
		if rec := x.doUA(file, ua); !isEPUB(rec) {
			t.Errorf("%q on /file: HTTP %d %q, want the EPUB", ua, rec.Code, rec.Body)
		}
	}
	if rec := x.doUA(kp, koreaderUA); !isKepub(rec) {
		t.Errorf("/kepub: HTTP %d %q, want the KEPUB", rec.Code, rec.Body)
	}
	if cc := x.doUA(kp, "").Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	// A PDF has no KEPUB: Kobo gets the PDF, and so does a stale /kepub link.
	for _, p := range []string{fmt.Sprintf("/opds/books/%d/file", m.ID), fmt.Sprintf("/opds/books/%d/kepub", m.ID)} {
		if rec := x.doUA(p, koboUA); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" {
			t.Errorf("%s: HTTP %d %q, want the PDF", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// Switched off: everyone, Kobo included, gets the EPUB, and nothing is converted.
	fk.ready = false
	n := len(fk.calls)
	if rec := x.doUA(file, koboUA); !isEPUB(rec) {
		t.Errorf("off, Kobo: HTTP %d %q, want the EPUB", rec.Code, rec.Body)
	}
	if rec := x.doUA(kp, koboUA); !isEPUB(rec) {
		t.Errorf("off, /kepub: HTTP %d %q, want the EPUB", rec.Code, rec.Body)
	}
	if len(fk.calls) != n {
		t.Error("converted with the setting off")
	}
	// The adult filter and the sign-in still stand in front of the KEPUB.
	adult := x.book("Brazzers Presents", "Someone", "b.epub", nil)
	fk.ready = true
	if rec := x.doUA(fmt.Sprintf("/opds/books/%d/kepub", adult.ID), koboUA); rec.Code != http.StatusNotFound {
		t.Errorf("adult /kepub: HTTP %d, want 404", rec.Code)
	}
	if rec := x.do(kp, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no sign-in /kepub: HTTP %d, want 401", rec.Code)
	}
}

// A failed conversion sends the EPUB and logs that it failed — never which book.
func TestKepubFallsBackOnFailure(t *testing.T) {
	x := newHarness(t)
	var logs bytes.Buffer
	x.h.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	x.h.d.Kepub = &fakeKepub{ready: true, fail: errors.New("kepubify: exit status 1: zip: not a valid zip file")}
	d := x.book("Dune", "Frank Herbert", "Dune.epub", nil)
	rec := x.doUA(fmt.Sprintf("/opds/books/%d/kepub", d.ID), koboUA)
	if rec.Code != 200 || rec.Body.String() != "ebook bytes of Dune" || rec.Header().Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("HTTP %d %q %q, want the EPUB", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(logs.String(), "KEPUB") {
		t.Errorf("log = %q, want the failure logged", logs.String())
	}
	if strings.Contains(logs.String(), "Dune") || strings.Contains(logs.String(), "reader") {
		t.Errorf("log names the book or the person: %q", logs.String())
	}
	// An expected refusal (too large) isn't worth a line.
	logs.Reset()
	x.h.d.Kepub = &fakeKepub{ready: true, fail: kepub.ErrTooLarge}
	if rec := x.doUA(fmt.Sprintf("/opds/books/%d/kepub", d.ID), koboUA); rec.Code != 200 {
		t.Errorf("too large: HTTP %d", rec.Code)
	}
	if logs.Len() != 0 {
		t.Errorf("logged an expected refusal: %q", logs.String())
	}
}

func TestIsKobo(t *testing.T) {
	for ua, want := range map[string]bool{
		koboUA:      true,
		koreaderUA:  false,
		"kobo":      true,
		"Mozilla/5": false,
		"":          false,
	} {
		if got := isKobo(ua); got != want {
			t.Errorf("isKobo(%q) = %v, want %v", ua, got, want)
		}
	}
}
