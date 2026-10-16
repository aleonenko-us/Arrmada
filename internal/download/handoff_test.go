package download

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

const handoffLink = "https://nzbfinder.example/getnzb/abc123def.nzb?i=4242&r=FINDERDOWNLOADKEY987654321"

func sabFakeWithUA(t *testing.T) (*sabFake, *uaRecorder, Client) {
	t.Helper()
	f := &sabFake{t: t, version: "5.1.3", answer: map[string]func(url.Values) (int, string){}}
	rec := &uaRecorder{next: f}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return f, rec, Client{ID: 7, Name: "SABnzbd", Kind: KindSABnzbd, URL: srv.URL, Password: sabTestKey}
}

// An indexer blocked Arrmada's own download: SABnzbd is handed the link (addurl) with the
// same name, category, priority and post-processing an upload gets, and the job's id is
// the download key — the placeholder keeps it once SABnzbd has the NZB.
func TestSABnzbdAddURLWhenAsked(t *testing.T) {
	f, rec, dc := sabFakeWithUA(t)
	f.setCats("*", "arrmada", "arrmada-tv")
	f.setAnswer("addurl", func(url.Values) (int, string) {
		return 200, `{"status": true, "nzo_ids": ["SABnzbd_nzo_HandOff1"]}`
	})
	key, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{
		Name: "Film.2021.1080p.WEB-DL-GRP", URL: handoffLink, ClientFetches: true, Category: "arrmada-tv", Transport: TransportUsenet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != "sab-sabnzbd_nzo_handoff1" {
		t.Errorf("key = %q", key)
	}
	cmds := f.commands()
	if len(cmds) != 1 || cmds[0].mode != "addurl" {
		t.Fatalf("commands = %+v, want one addurl", cmds)
	}
	for field, want := range map[string]string{
		"name": handoffLink, "nzbname": "Film.2021.1080p.WEB-DL-GRP", "cat": "arrmada-tv",
		"priority": "-100", "pp": "3", "apikey": sabTestKey, "output": "json",
	} {
		if got := cmds[0].form.Get(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	for _, ua := range rec.all() {
		if ua != httpua.UserAgent() {
			t.Errorf("SABnzbd saw User-Agent %q, want %q", ua, httpua.UserAgent())
		}
	}

	// A paused add stays paused.
	f.reset()
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", URL: handoffLink, ClientFetches: true, Category: "arrmada", Paused: true}); err != nil {
		t.Fatal(err)
	}
	if c := f.commands(); len(c) != 1 || c[0].form.Get("priority") != "-2" {
		t.Errorf("paused addurl = %+v", c)
	}

	// SABnzbd refusing the link, and an error that quotes it, never carry it on.
	f.reset()
	f.setAnswer("addurl", func(url.Values) (int, string) {
		return 200, `{"status": false, "error": "bad link ` + handoffLink + `"}`
	})
	_, err = newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", URL: handoffLink, ClientFetches: true, Category: "arrmada"})
	if err == nil || strings.Contains(err.Error(), "FINDERDOWNLOADKEY") || strings.Contains(err.Error(), "r=") {
		t.Errorf("refused addurl = %v", err)
	}
	f.setAnswer("addurl", func(url.Values) (int, string) { return 200, `{"status": false}` })
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", URL: handoffLink, ClientFetches: true, Category: "arrmada"}); err == nil || !strings.Contains(err.Error(), "refused the NZB link") {
		t.Errorf("refused addurl = %v", err)
	}

	// A missing category is refused before the link is sent anywhere.
	f.reset()
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", URL: handoffLink, ClientFetches: true, Category: "arrmada-books"}); err == nil {
		t.Error("an addurl into a missing category was accepted")
	}
	if n := len(f.commands()); n != 0 {
		t.Errorf("%d commands sent", n)
	}
}

// Without the explicit ask, a link is still refused (the key would sit in SABnzbd's
// history), and a file always wins over a link.
func TestSABnzbdLinkOnlyWhenAsked(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	f.setCats("*", "arrmada")
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{URL: handoffLink, Category: "arrmada"}); !errors.Is(err, ErrNZBRequired) {
		t.Errorf("unasked link = %v", err)
	}
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{ClientFetches: true, Category: "arrmada"}); !errors.Is(err, ErrNZBRequired) {
		t.Errorf("asked with no link = %v", err)
	}
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{File: []byte("<nzb/>"), URL: handoffLink, ClientFetches: true, Category: "arrmada"}); err != nil {
		t.Fatal(err)
	}
	if c := f.commands(); len(c) != 1 || c[0].mode != "addfile" {
		t.Errorf("file and link = %+v, want the file uploaded", c)
	}
}

// While SABnzbd fetches a handed-over link, and when that fails, it names the job after the
// link. What Arrmada reads keeps the address but never its query (the key).
func TestSABnzbdNamesHideLinks(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	f.setLists(
		`{"queue":{"paused":false,"kbpersec":"0","slots":[{"nzo_id":"SABnzbd_nzo_a","filename":"Trying to fetch NZB from `+handoffLink+`","cat":"arrmada","status":"Grabbing","mb":"0","mbleft":"0","percentage":"0","timeleft":"0:00:00"}]}}`,
		`{"history":{"slots":[{"nzo_id":"SABnzbd_nzo_b","name":"Film.2021 - `+handoffLink+`","category":"arrmada","status":"Failed","fail_message":"URL Fetching failed; Maximum retries (`+handoffLink+`)","bytes":0}]}}`,
	)
	items, err := newSAB().List(context.Background(), dc)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	for _, it := range items {
		for _, s := range []string{it.Name, it.FailMessage} {
			if strings.Contains(s, "FINDERDOWNLOADKEY") || strings.Contains(s, "?") {
				t.Errorf("a link's query reached Arrmada: %q", s)
			}
		}
	}
	if items[0].Name != "Trying to fetch NZB from https://nzbfinder.example/getnzb/abc123def.nzb" || items[0].Phase() != "queued" {
		t.Errorf("queue item = %q (%s)", items[0].Name, items[0].Phase())
	}
	if items[1].Name != "Film.2021 - https://nzbfinder.example/getnzb/abc123def.nzb" ||
		!strings.HasPrefix(items[1].FailMessage, "URL Fetching failed; Maximum retries") {
		t.Errorf("history item = %q / %q", items[1].Name, items[1].FailMessage)
	}
	for in, want := range map[string]string{
		"plain name":                      "plain name",
		"http://u:p@host:8080/a/b?k=v#f":  "http://host:8080/a/b",
		"see https://x.example/?apikey=z": "see https://x.example/",
	} {
		if got := hideLinks(in); got != want {
			t.Errorf("hideLinks(%q) = %q, want %q", in, got, want)
		}
	}
}
