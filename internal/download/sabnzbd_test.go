package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The SABnzbd client against recorded answers. testdata/sabnzbd/<version>/ holds answers
// recorded from real SABnzbd 4.5.5 and 5.1.3 containers (linuxserver images; the API key
// redacted) by TestSABnzbdLive with ARRMADA_SABNZBD_CAPTURE. statuses_*.json are built from
// the 5.1.3 shapes to carry every status word SABnzbd uses, which a live run can't produce
// on demand (propagation delays, each post-processing stage).

const sabTestKey = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"

type sabCall struct {
	mode     string
	form     url.Values
	file     []byte
	filename string
}

// sabFake answers like SABnzbd from the fixtures, refuses a wrong key the way SABnzbd does,
// and records each call. Answers can be replaced per mode.
type sabFake struct {
	t       *testing.T
	version string
	mu      sync.Mutex
	calls   []sabCall
	queue   string // fixture file (under testdata/sabnzbd) or raw JSON starting with "{"
	history string
	cats    []string // nil: the version's get_cats.json
	answer  map[string]func(form url.Values) (int, string)
}

func newSABFake(t *testing.T, version string) (*sabFake, Client) {
	t.Helper()
	f := &sabFake{t: t, version: version, answer: map[string]func(url.Values) (int, string){}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, Client{ID: 7, Name: "SABnzbd", Kind: KindSABnzbd, URL: srv.URL, Password: sabTestKey}
}

// The test changes the fake's answers between calls; the handler runs on the server's
// goroutines, so every change goes through the lock.
func (f *sabFake) setLists(queue, history string) {
	f.mu.Lock()
	f.queue, f.history = queue, history
	f.mu.Unlock()
}

func (f *sabFake) setCats(cats ...string) {
	f.mu.Lock()
	f.cats = cats
	f.mu.Unlock()
}

func (f *sabFake) setAnswer(mode string, fn func(form url.Values) (int, string)) {
	f.mu.Lock()
	if fn == nil {
		delete(f.answer, mode)
	} else {
		f.answer[mode] = fn
	}
	f.mu.Unlock()
}

func (f *sabFake) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *sabFake) allCalls() []sabCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sabCall(nil), f.calls...)
}

func (f *sabFake) fixture(name string) string {
	if strings.HasPrefix(name, "{") {
		return name
	}
	p := filepath.Join("testdata", "sabnzbd", name)
	if !strings.HasPrefix(name, "statuses") {
		p = filepath.Join("testdata", "sabnzbd", f.version, name)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		f.t.Errorf("fixture: %v", err)
	}
	return string(b)
}

func (f *sabFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api" {
		f.t.Errorf("SABnzbd asked %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		f.t.Errorf("a query string reached SABnzbd: %q", r.URL.RawQuery)
	}
	var file []byte
	var filename string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			f.t.Errorf("multipart: %v", err)
		}
		if fh, hdr, err := r.FormFile("nzbfile"); err == nil {
			file, _ = io.ReadAll(fh)
			filename = hdr.Filename
		}
	} else if err := r.ParseForm(); err != nil {
		f.t.Errorf("form: %v", err)
	}
	form := r.PostForm
	if r.MultipartForm != nil {
		form = url.Values(r.MultipartForm.Value)
	}
	mode := form.Get("mode")
	if mode != "version" && form.Get("apikey") != sabTestKey {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "API Key Incorrect")
		return
	}
	if form.Get("output") != "json" {
		f.t.Errorf("%s without output=json", mode)
	}
	f.mu.Lock()
	f.calls = append(f.calls, sabCall{mode: mode, form: form, file: file, filename: filename})
	answer := f.answer[mode]
	queue, history := f.queue, f.history
	f.mu.Unlock()
	if answer != nil {
		code, body := answer(form)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
		return
	}
	var body string
	switch mode {
	case "version":
		body = f.fixture("version.json")
	case "get_cats":
		f.mu.Lock()
		cats := f.cats
		f.mu.Unlock()
		if cats == nil {
			body = f.fixture("get_cats.json")
		} else {
			b, _ := json.Marshal(map[string]any{"categories": cats})
			body = string(b)
		}
	case "fullstatus":
		body = f.fixture("fullstatus.json")
	case "get_config":
		if form.Get("section") == "categories" {
			body = f.fixture("get_config_categories.json")
		} else {
			body = f.fixture("get_config_language.json")
		}
	case "queue":
		if form.Get("name") != "" {
			body = `{"status":true}`
		} else {
			body = f.fixture(queue)
		}
	case "history":
		if form.Get("name") != "" {
			body = `{"status":true}`
		} else {
			body = f.fixture(history)
		}
	case "addfile":
		body = f.fixture("addfile.json")
	case "set_config":
		f.mu.Lock()
		if f.cats != nil {
			f.cats = append(f.cats, form.Get("name"))
		}
		f.mu.Unlock()
		body = `{"config":{"categories":[]}}`
	default:
		body = `{"status":true}`
	}
	_, _ = io.WriteString(w, body)
}

// commands is the calls that change something.
func (f *sabFake) commands() []sabCall {
	var out []sabCall
	for _, c := range f.allCalls() {
		switch {
		case c.mode == "queue" || c.mode == "history":
			if c.form.Get("name") != "" {
				out = append(out, c)
			}
		case c.mode == "version" || c.mode == "get_cats" || c.mode == "fullstatus" || c.mode == "get_config":
		default:
			out = append(out, c)
		}
	}
	return out
}

func newSAB() *SABnzbd { return NewSABnzbd() }

// --- adding ---------------------------------------------------------------------------

func TestSABnzbdAddReturnsKey(t *testing.T) {
	for version, id := range map[string]string{"5.1.3": "5a2cc24f-9be3-4b43-bfc1-9f6d04ee939b", "4.5.5": "SABnzbd_nzo_vqsbikxw"} {
		t.Run(version, func(t *testing.T) {
			f, dc := newSABFake(t, version)
			f.setCats("*", "arrmada", "arrmada-tv", "arrmada-books", "arrmada-music")
			nzb := testNZB("pa ss&<word>", map[string]string{"film.mkv": "a@b"}, map[string]int{"film.mkv": 10})
			key, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{
				Name: "Film.2020.1080p.WEB-DL", File: nzb, Filename: "Film.2020.nzb", Category: "arrmada-tv", Transport: TransportUsenet,
			})
			if err != nil {
				t.Fatal(err)
			}
			if want := UsenetKey(id); key != want || key == "" {
				t.Fatalf("key = %q, want %q", key, want)
			}
			cmds := f.commands()
			if len(cmds) != 1 || cmds[0].mode != "addfile" {
				t.Fatalf("calls = %+v", cmds)
			}
			c := cmds[0]
			for field, want := range map[string]string{
				"nzbname": "Film.2020.1080p.WEB-DL", "cat": "arrmada-tv", "priority": "-100", "pp": "3",
				"password": "pa ss&<word>", "apikey": sabTestKey,
			} {
				if got := c.form.Get(field); got != want {
					t.Errorf("%s = %q, want %q", field, got, want)
				}
			}
			if !bytes.Equal(c.file, nzb) || c.filename != "Film.2020.nzb" {
				t.Errorf("nzbfile = %q (%d bytes)", c.filename, len(c.file))
			}
		})
	}
}

// SABnzbd files a job under * when it doesn't know the category, and says nothing. That
// download would never be imported, so the add is refused before anything is sent.
func TestSABnzbdAddUnknownCategoryRefused(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	f.setCats("*", "movies", "tv")
	_, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", File: []byte("<nzb/>"), Category: "arrmada-tv"})
	if err == nil || !strings.Contains(err.Error(), `"arrmada-tv"`) || !strings.Contains(err.Error(), "Add missing categories") {
		t.Fatalf("err = %v", err)
	}
	if NeverReached(err) {
		t.Error("a missing category reads as an unreachable client (it would fail over silently)")
	}
	if n := len(f.commands()); n != 0 {
		t.Errorf("%d commands sent", n)
	}
	// No category at all is refused too.
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{Name: "x", File: []byte("<nzb/>")}); err == nil {
		t.Error("an add with no category was accepted")
	}
}

func TestSABnzbdAddRefusals(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	// A link is never passed on: SABnzbd would keep the indexer's key in its history.
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{URL: "https://indexer.example/getnzb?apikey=x", Category: "arrmada"}); !errors.Is(err, ErrNZBRequired) {
		t.Errorf("URL add = %v", err)
	}
	f.setAnswer("addfile", func(url.Values) (int, string) { return 200, f.fixture("addfile_refused.json") })
	if _, err := newSAB().AddKeyed(context.Background(), dc, AddRequest{File: []byte("junk"), Category: "arrmada"}); err == nil || !strings.Contains(err.Error(), "refused the NZB") {
		t.Errorf("refused add = %v", err)
	}
}

func TestNZBPassword(t *testing.T) {
	for nzb, want := range map[string]string{
		string(testNZB("s3cret", map[string]string{"a": "b"}, nil)):                                    "s3cret",
		string(testNZB("", map[string]string{"a": "b"}, nil)):                                          "",
		`<nzb><head><meta type="title">x</meta><meta type="PASSWORD"> p&amp;w </meta></head></nzb>`: "p&w",
		`<nzb><file><meta type="password">late</meta></file></nzb>`:                                    "",
		`not xml`: "",
	} {
		if got := nzbPassword([]byte(nzb)); got != want {
			t.Errorf("nzbPassword(%.40q) = %q, want %q", nzb, got, want)
		}
	}
}

// --- reading --------------------------------------------------------------------------

func TestSABnzbdListRecorded(t *testing.T) {
	for _, version := range []string{"4.5.5", "5.1.3"} {
		t.Run(version, func(t *testing.T) {
			f, dc := newSABFake(t, version)
			f.setLists("queue_paused_queue.json", "queue_paused_history.json")
			items, err := newSAB().List(context.Background(), dc)
			if err != nil {
				t.Fatal(err)
			}
			var paused *Item
			for i := range items {
				if strings.HasPrefix(items[i].Name, "Arrmada.IT.Missing.") && items[i].RawState == "Queued" {
					paused = &items[i]
				}
			}
			if paused == nil {
				t.Fatalf("no queued job in %+v", items)
			}
			if paused.State != "paused" || paused.Phase() != "paused" || paused.Category != CategoryTV || paused.Complete() ||
				paused.Transport != TransportUsenet || paused.SizeBytes == 0 || paused.RemainingBytes != paused.SizeBytes || paused.AddedOn == 0 {
				t.Errorf("job in a paused queue = %+v", *paused)
			}

			f.setLists("history_done_queue.json", "history_done_history.json")
			items, err = newSAB().List(context.Background(), dc)
			if err != nil {
				t.Fatal(err)
			}
			var done, failed int
			for _, it := range items {
				switch {
				case strings.HasPrefix(it.Name, "Arrmada.IT.Good."):
					done++
					if it.State != StateCompleted || !it.Complete() || it.Progress != 1 || it.ContentPath == "" ||
						!strings.HasPrefix(it.ContentPath, "/downloads/complete/") || it.SizeBytes != 270000 || it.Category != DefaultMovieCategory {
						t.Errorf("completed job = %+v", it)
					}
				case strings.HasPrefix(it.Name, "Arrmada.IT.Missing."):
					failed++
					if it.State != "error" || it.Phase() != "error" || it.Complete() || !strings.Contains(it.FailMessage, "Not on your server") {
						t.Errorf("failed job = %+v", it)
					}
				}
				if KeyTransport(it.Hash) != TransportUsenet {
					t.Errorf("key %q", it.Hash)
				}
			}
			if done != 1 || failed != 1 {
				t.Errorf("done %d failed %d in %+v", done, failed, items)
			}
		})
	}
}

// Every status SABnzbd uses, in the queue and in the history, maps to the right state and
// phase; only a history Completed is complete. Other categories' jobs aren't listed.
func TestSABnzbdListStatuses(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	f.setLists("statuses_queue.json", "statuses_history.json")
	items, err := newSAB().List(context.Background(), dc)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Item{}
	for _, it := range items {
		byName[it.Name] = it
	}
	type want struct {
		state, phase string
		complete     bool
	}
	cases := map[string]want{
		"Film.Downloading.2020.1080p": {"downloading", "downloading", false},
		"Show.S01E01.Queued.1080p":    {"downloading", "queued", false},
		"Book.Paused":                 {"paused", "paused", false},
		"Album.Propagating":           {"downloading", "queued", false},
		"Film.Fetching":               {"downloading", "queued", false},
		"Film.Grabbing":               {"downloading", "queued", false},
		"Film.Encrypted":              {"downloading", "downloading", false},
		"Film.Completed.2020.1080p":   {StateCompleted, StateCompleted, true},
		"Show.S01E02.Failed":          {"error", "error", false},
		"Show.S01E03.FailedNoReason":  {"error", "error", false},
		"Film.QuickCheck":             {PhaseUnpacking, PhaseUnpacking, false},
		"Film.Verifying":              {PhaseUnpacking, PhaseUnpacking, false},
		"Film.Repairing":              {PhaseUnpacking, PhaseUnpacking, false},
		"Film.Extracting":             {PhaseUnpacking, PhaseUnpacking, false},
		"Film.Moving":                 {PhaseUnpacking, PhaseUnpacking, false},
		"Film.Running":                {PhaseUnpacking, PhaseUnpacking, false},
		"Film.PPQueued":               {PhaseUnpacking, PhaseUnpacking, false},
		"Film.PPFetching":             {PhaseUnpacking, PhaseUnpacking, false},
	}
	for name, w := range cases {
		it, ok := byName[name]
		if !ok {
			t.Errorf("%s not listed", name)
			continue
		}
		if it.State != w.state || it.Phase() != w.phase || it.Complete() != w.complete {
			t.Errorf("%s: state %q phase %q complete %v, want %+v", name, it.State, it.Phase(), it.Complete(), w)
		}
	}
	if len(byName) != len(cases) {
		t.Errorf("listed %d jobs, want %d (other categories and unkeyable ids left out)", len(byName), len(cases))
	}
	for _, other := range []string{"Someone.Elses.Job", "Not.Ours", "Unkeyable"} {
		if _, ok := byName[other]; ok {
			t.Errorf("%s was listed", other)
		}
	}

	dl := byName["Film.Downloading.2020.1080p"]
	if dl.Hash != "sab-11111111-1111-4111-8111-111111111111" || dl.Progress != 0.75 || dl.SizeBytes != 1000*mib ||
		dl.RemainingBytes != 250*mib || dl.DownloadedBytes != 750*mib || dl.ETASeconds != 125 || dl.DownSpeed != 2048*1024 || dl.AddedOn != 1791970000 {
		t.Errorf("downloading job = %+v", dl)
	}
	if enc := byName["Film.Encrypted"]; enc.DownSpeed != 0 || enc.ETASeconds != 86400+2*3600+3*60+4 ||
		strings.Join(enc.Labels, ",") != "ENCRYPTED,TOO LARGE" {
		t.Errorf("second downloading job = %+v (only the first gets the queue's speed)", enc)
	}
	if q := byName["Show.S01E01.Queued.1080p"]; q.Hash != "sab-sabnzbd_nzo_queued01" || strings.Join(q.Labels, ",") != "DUPLICATE" {
		t.Errorf("queued job = %+v", q)
	}
	done := byName["Film.Completed.2020.1080p"]
	if done.ContentPath != "/downloads/complete/Film.Completed.2020.1080p" || done.SizeBytes != 2000000000 || done.AddedOn != 1791975000 || done.LastActivity != 1791975695 {
		t.Errorf("completed job = %+v", done)
	}
	if fl := byName["Show.S01E02.Failed"]; !strings.Contains(fl.FailMessage, "Not on your server") || fl.ContentPath != "" {
		t.Errorf("failed job = %+v", fl)
	}
	if fl := byName["Show.S01E03.FailedNoReason"]; fl.FailMessage == "" || fl.AddedOn != 1791975693 {
		t.Errorf("failed job without a reason = %+v", fl)
	}
	if pp := byName["Film.Extracting"]; pp.ContentPath != "" || pp.Progress != 1 {
		t.Errorf("unpacking job = %+v (no content path until it's finished)", pp)
	}

	// Only Arrmada's categories are asked for.
	for _, c := range f.allCalls() {
		if c.mode == "queue" && c.form.Get("cat") != "arrmada,arrmada-tv,arrmada-books,arrmada-music" ||
			c.mode == "history" && c.form.Get("category") != "arrmada,arrmada-tv,arrmada-books,arrmada-music" {
			t.Errorf("%s read with %v", c.mode, c.form)
		}
	}
}

// A configured movie category replaces the default one in what's read and offered.
func TestSABnzbdMovieCategory(t *testing.T) {
	f, dc := newSABFake(t, "5.1.3")
	f.setLists("statuses_queue.json", `{"history":{"slots":[]}}`)
	sab := newSAB()
	sab.SetMovieCategory("films")
	items, err := sab.List(context.Background(), dc)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Category == "arrmada" {
			t.Errorf("a job in the default movie category was listed: %s", it.Name)
		}
	}
	if got := f.allCalls()[0].form.Get("cat"); got != "films,arrmada-tv,arrmada-books,arrmada-music" {
		t.Errorf("cat = %q", got)
	}
}

func TestParseSABDuration(t *testing.T) {
	for in, want := range map[string]int64{"0:00:00": 0, "0:02:05": 125, "12:30": 750, "1:02:03:04": 93784, "": 0, "x:1:2": 0, "1": 0, "1:2:3:4:5": 0} {
		if got := parseSABDuration(in); got != want {
			t.Errorf("parseSABDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

// --- commands -------------------------------------------------------------------------

// SABnzbd 4.x ids aren't lowercase and it matches them exactly (a lowercased id is answered
// "ok" while nothing happens), so every command carries the id as SABnzbd gave it.
func TestSABnzbdCommandsUseRealID(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "5.1.3")
	f.setLists("statuses_queue.json", "statuses_history.json")
	f.setCats("*", "arrmada", "arrmada-tv", "arrmada-books", "arrmada-music")
	sab := newSAB()
	if err := sab.Pause(ctx, dc, "sab-sabnzbd_nzo_queued01"); err != nil {
		t.Fatal(err)
	}
	if err := sab.Resume(ctx, dc, "sab-sabnzbd_nzo_paused01"); err != nil {
		t.Fatal(err)
	}
	if err := sab.SetCategory(ctx, dc, "sab-sabnzbd_nzo_queued01", CategoryBooks); err != nil {
		t.Fatal(err)
	}
	if err := sab.TorrentAction(ctx, dc, "sab-sabnzbd_nzo_failed01", ActionRetry); err != nil {
		t.Fatal(err)
	}
	if err := sab.Pause(ctx, dc, PauseAll); err != nil {
		t.Fatal(err)
	}
	if err := sab.Resume(ctx, dc, PauseAll); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.commands() {
		got = append(got, strings.TrimSpace(c.mode+" "+c.form.Get("name")+" "+c.form.Get("value")+" "+c.form.Get("value2")))
	}
	want := []string{
		"queue pause SABnzbd_nzo_Queued01",
		"queue resume SABnzbd_nzo_Paused01",
		"change_cat  SABnzbd_nzo_Queued01 arrmada-books",
		"retry  SABnzbd_nzo_Failed01",
		"pause",
		"resume",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Refusals: a category SABnzbd lacks, a finished job, retrying what didn't fail, a torrent
	// command, a job that isn't Arrmada's, and a torrent hash.
	f.reset()
	if err := sab.SetCategory(ctx, dc, "sab-sabnzbd_nzo_queued01", "nope"); err == nil {
		t.Error("change to a missing category accepted")
	}
	if err := sab.SetCategory(ctx, dc, "sab-55555555-5555-4555-8555-555555555555", CategoryTV); err == nil {
		t.Error("re-filing a finished job accepted")
	}
	if err := sab.TorrentAction(ctx, dc, "sab-55555555-5555-4555-8555-555555555555", ActionRetry); err == nil {
		t.Error("retry of a finished job accepted")
	}
	if err := sab.TorrentAction(ctx, dc, "sab-sabnzbd_nzo_failed01", "reannounce"); err == nil {
		t.Error("a torrent command accepted")
	}
	if err := sab.Pause(ctx, dc, "sab-44444444-4444-4444-8444-444444444444"); err != nil {
		t.Errorf("pause of another category's job: %v", err)
	}
	if err := sab.Remove(ctx, dc, strings.Repeat("ab", 20), true); err == nil {
		t.Error("a torrent hash was accepted")
	}
	if cmds := f.commands(); len(cmds) != 0 {
		t.Errorf("refused commands still sent: %+v", cmds)
	}
}

// sabRemoveFixture is a history with a finished folder job, a finished one-file job, a job
// that finished outside every allowed folder and a failed job; SABnzbd's /downloads is
// mapped to a temp folder that is also Arrmada's downloads folder.
func sabRemoveFixture(t *testing.T) (*sabFake, Client, *SABnzbd, string, string) {
	t.Helper()
	root := t.TempDir()
	elsewhere := t.TempDir()
	for _, d := range []string{"complete/Film.Folder", "complete/Film.Single", "complete/Film.Kept"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(root, "complete/Film.Folder/film.mkv"), filepath.Join(root, "complete/Film.Single/film.mkv"),
		filepath.Join(root, "complete/Film.Kept/film.mkv"), filepath.Join(elsewhere, "film.mkv")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, dc := newSABFake(t, "5.1.3")
	dc.PathMaps = PathMaps{{Remote: "/downloads", Local: root}, {Remote: "/elsewhere", Local: elsewhere}}
	slot := func(id, name, status, storage string) string {
		return fmt.Sprintf(`{"nzo_id":%q,"name":%q,"category":"arrmada","status":%q,"storage":%q,"bytes":1,"fail_message":""}`, id, name, status, storage)
	}
	f.setLists("statuses_queue.json", `{"history":{"slots":[`+strings.Join([]string{
		slot("SABnzbd_nzo_Folder", "Film.Folder", "Completed", "/downloads/complete/Film.Folder"),
		slot("SABnzbd_nzo_Single", "Film.Single", "Completed", "/downloads/complete/Film.Single/film.mkv"),
		slot("SABnzbd_nzo_Kept", "Film.Kept", "Completed", "/downloads/complete/Film.Kept"),
		slot("SABnzbd_nzo_Outside", "Film.Outside", "Completed", "/elsewhere"),
		slot("SABnzbd_nzo_Whole", "Film.Whole", "Completed", "/downloads/complete"),
		slot("SABnzbd_nzo_Failed", "Film.Failed", "Failed", "/downloads/incomplete/Film.Failed"),
	}, ",")+`]}}`)
	sab := newSAB()
	sab.SetFolders(func(context.Context) PathEnv { return PathEnv{Downloads: root, DataDir: filepath.Join(root, "data-not-here")} })
	return f, dc, sab, root, elsewhere
}

func TestSABnzbdRemove(t *testing.T) {
	ctx := context.Background()
	f, dc, sab, root, elsewhere := sabRemoveFixture(t)

	cmd := func() string {
		cmds := f.commands()
		if len(cmds) == 0 {
			return ""
		}
		c := cmds[len(cmds)-1]
		f.reset()
		return fmt.Sprintf("%s %s %s del_files=%s archive=%s", c.mode, c.form.Get("name"), c.form.Get("value"), c.form.Get("del_files"), c.form.Get("archive"))
	}

	// Still downloading: deleted with its partial files.
	if err := sab.Remove(ctx, dc, "sab-sabnzbd_nzo_queued01", true); err != nil {
		t.Fatal(err)
	}
	if got := cmd(); got != "queue delete SABnzbd_nzo_Queued01 del_files=1 archive=" {
		t.Errorf("queue remove: %s", got)
	}
	// Finished, a folder: archived in SABnzbd, the folder deleted by Arrmada.
	if err := sab.Remove(ctx, dc, "sab-sabnzbd_nzo_folder", true); err != nil {
		t.Fatal(err)
	}
	if got := cmd(); got != "history delete SABnzbd_nzo_Folder del_files=0 archive=1" {
		t.Errorf("finished remove: %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "complete/Film.Folder")); !os.IsNotExist(err) {
		t.Errorf("the finished job's folder is still there: %v", err)
	}
	// Finished, one file: the file and its now-empty folder go.
	if err := sab.Remove(ctx, dc, "sab-sabnzbd_nzo_single", true); err != nil {
		t.Fatal(err)
	}
	cmd()
	if _, err := os.Stat(filepath.Join(root, "complete/Film.Single")); !os.IsNotExist(err) {
		t.Errorf("the one-file job's folder is still there: %v", err)
	}
	// Finished, keep the files: archived only.
	if err := sab.Remove(ctx, dc, "sab-sabnzbd_nzo_kept", false); err != nil {
		t.Fatal(err)
	}
	if got := cmd(); got != "history delete SABnzbd_nzo_Kept del_files=0 archive=1" {
		t.Errorf("keep-files remove: %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "complete/Film.Kept/film.mkv")); err != nil {
		t.Errorf("kept files were deleted: %v", err)
	}
	// Outside every allowed folder, and the completed folder itself: refused before anything
	// is touched — SABnzbd keeps the job, the files stay.
	for _, k := range []string{"sab-sabnzbd_nzo_outside", "sab-sabnzbd_nzo_whole"} {
		err := sab.Remove(ctx, dc, k, true)
		if err == nil {
			t.Errorf("%s: removal accepted", k)
		}
		if got := cmd(); got != "" {
			t.Errorf("%s: SABnzbd was told %s", k, got)
		}
	}
	if !errors.Is(sab.Remove(ctx, dc, "sab-sabnzbd_nzo_outside", true), ErrLeftoverOutside) {
		t.Error("outside: not ErrLeftoverOutside")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "film.mkv")); err != nil {
		t.Errorf("a file outside the downloads folder was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "complete/Film.Kept")); err != nil {
		t.Errorf("the completed folder was emptied: %v", err)
	}
	// Failed: SABnzbd deletes the partial files itself.
	if err := sab.Remove(ctx, dc, "sab-sabnzbd_nzo_failed", true); err != nil {
		t.Fatal(err)
	}
	if got := cmd(); got != "history delete SABnzbd_nzo_Failed del_files=1 archive=1" {
		t.Errorf("failed remove: %s", got)
	}
	// Already gone: no error, nothing sent.
	if err := sab.Remove(ctx, dc, "sab-sabnzbd_nzo_gone", true); err != nil {
		t.Errorf("gone: %v", err)
	}
	if got := cmd(); got != "" {
		t.Errorf("gone: SABnzbd was told %s", got)
	}

	// Without Arrmada's folders, a finished job's files are left alone and SABnzbd keeps it.
	bare := newSAB()
	if err := bare.Remove(ctx, dc, "sab-sabnzbd_nzo_kept", true); err == nil {
		t.Error("removal without folders accepted")
	}
	if got := cmd(); got != "" {
		t.Errorf("without folders: SABnzbd was told %s", got)
	}
}

func TestGuardLeftover(t *testing.T) {
	dl := t.TempDir()
	lib := filepath.Join(dl, "library")
	other := t.TempDir()
	for _, d := range []string{"complete/Job", "complete/tv", "library/Movie", "job-with-lib/x"} {
		if err := os.MkdirAll(filepath.Join(dl, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots := []string{dl, filepath.Join(dl, "complete")}
	protected := append(append([]string{}, roots...), filepath.Join(dl, "complete/tv"))
	keep := []string{lib, filepath.Join(dl, "job-with-lib/x")}
	for p, ok := range map[string]bool{
		filepath.Join(dl, "complete/Job"):            true,
		filepath.Join(dl, "complete/Job/file.mkv"):   true,
		filepath.Join(dl, "complete/Missing"):        true, // already gone: nothing to delete, nothing wrong
		dl:                                           false,
		filepath.Join(dl, "complete"):                false,
		filepath.Join(dl, "complete/tv"):             false,
		filepath.Join(dl, "library/Movie"):           false,
		lib:                                          false,
		filepath.Join(dl, "job-with-lib"):            false, // holds a kept folder
		filepath.Join(dl, "complete/../../escape"):   false,
		other:                                        false,
		filepath.Join(dl+"-sibling", "Job"):          false,
		"relative/path":                              false,
	} {
		_, err := guardLeftover(p, roots, protected, keep)
		if (err == nil) != ok {
			t.Errorf("guardLeftover(%s) err = %v, want ok=%v", p, err, ok)
		}
	}
	// A link inside the downloads folder that points out of it isn't followed out.
	link := filepath.Join(dl, "complete/Link")
	if err := os.Symlink(other, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if _, err := guardLeftover(link, roots, protected, keep); err == nil {
		t.Error("a symlink out of the downloads folder was accepted")
	}
}

// --- errors and the key ---------------------------------------------------------------

func TestSABnzbdErrors(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "5.1.3")
	sab := newSAB()

	// Hostname verification: the fix names the host.
	f.setAnswer("version", func(url.Values) (int, string) {
		return 403, "Access denied - Hostname verification failed: https://sabnzbd.org/hostname-check"
	})
	host := dc
	host.URL = strings.Replace(dc.URL, "127.0.0.1", "localhost", 1)
	err := sab.Test(ctx, host)
	if err == nil || !strings.Contains(err.Error(), "Add localhost to SABnzbd → Config → Special → host_whitelist") || !NeverReached(err) {
		t.Errorf("hostname: %v", err)
	}
	f.setAnswer("version", nil)

	// The wrong key (or the NZB key), plainly.
	wrong := dc
	wrong.Password = "nzbkeynzbkeynzbkeynzbkeynzbkey12"
	if err := sab.Test(ctx, wrong); !errors.Is(err, errSABKey) || !strings.Contains(err.Error(), "full API key") {
		t.Errorf("wrong key: %v", err)
	}
	// Not SABnzbd at all.
	f.setAnswer("version", func(url.Values) (int, string) { return 200, "<html>router login</html>" })
	if err := sab.Test(ctx, dc); err == nil || !strings.Contains(err.Error(), "not as SABnzbd") {
		t.Errorf("not SABnzbd: %v", err)
	}
	f.setAnswer("version", nil)
	// A refused command says SABnzbd's reason.
	f.setAnswer("retry", func(url.Values) (int, string) { return 200, `{"status":false,"error":"item does not exist"}` })
	if _, err := sab.call(ctx, dc, "retry", nil); err == nil || !strings.Contains(err.Error(), "item does not exist") {
		t.Errorf("refused command: %v", err)
	}
	// Nothing listening: never reached, so another client may take the download.
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	down := dc
	down.URL = goneURL
	if err := sab.Test(ctx, down); err == nil || !NeverReached(err) || !strings.Contains(err.Error(), "couldn't reach SABnzbd") {
		t.Errorf("unreachable: %v", err)
	}
	if err := sab.Test(ctx, dc); err != nil {
		t.Errorf("Test against the fixtures: %v", err)
	}
}

// The API key never shows up in an error, a log line or a URL — not even when SABnzbd
// itself echoes it back.
func TestSABnzbdKeyNeverLeaks(t *testing.T) {
	ctx := context.Background()
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.String(), sabTestKey) {
			t.Errorf("the key reached a URL: %s", r.URL)
		}
		_ = r.ParseMultipartForm(1 << 20)
		switch r.FormValue("mode") {
		case "version":
			_, _ = io.WriteString(w, `{"version":"5.1.3"}`)
		case "get_cats":
			_, _ = io.WriteString(w, `{"categories":["*","arrmada","arrmada-tv","arrmada-books","arrmada-music"]}`)
		case "fullstatus":
			_, _ = io.WriteString(w, `{"status":{"apikey":"`+sabTestKey+`","completedir":"/downloads/complete","servers":[{"servername":"x","servererror":"login `+sabTestKey+` refused"}]}}`)
		case "queue":
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "Internal error with apikey="+sabTestKey)
		default:
			_, _ = io.WriteString(w, `{"status":false,"error":"bad request apikey=`+sabTestKey+` `+url.QueryEscape(sabTestKey)+`"}`)
		}
	}))
	defer echo.Close()

	svc, _ := newTestService(t)
	var logs bytes.Buffer
	svc.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: echo.URL, Password: sabTestKey})
	svc.SetFolders(func(context.Context) PathEnv { return PathEnv{Downloads: t.TempDir()} })

	var errs []error
	note := func(err error) { errs = append(errs, err) }
	note(svc.Test(ctx, c.ID))
	_, err := svc.AddDownload(ctx, AddRequest{Name: "x", File: []byte("<nzb/>"), Category: "arrmada", Transport: TransportUsenet})
	note(err)
	note(svc.Remove(ctx, "sab-abc", true))
	note(svc.Pause(ctx, "sab-abc"))
	note(svc.Resume(ctx, PauseAll))
	note(svc.SetCategory(ctx, "sab-abc", CategoryTV))
	note(svc.Action(ctx, "sab-abc", ActionRetry))
	_, err = svc.Queue(ctx)
	note(err)
	facts, err := svc.UsenetFacts(ctx, c.ID)
	note(err)
	_, err = svc.AddMissingCategories(ctx, c.ID)
	note(err)
	_, err = svc.TestPaths(ctx, c.ID, PathEnv{})
	note(err)
	states, err := svc.ClientStates(ctx)
	note(err)
	for _, s := range states {
		note(errors.New(s.Err))
	}
	for _, s := range facts.Servers {
		note(errors.New(s.Error))
	}
	b, _ := json.Marshal(facts)
	note(errors.New(string(b)))
	failed := 0
	for _, e := range errs {
		if e == nil {
			continue
		}
		failed++
		if strings.Contains(e.Error(), sabTestKey) || strings.Contains(e.Error(), url.QueryEscape(sabTestKey)) {
			t.Errorf("an error carries the key: %v", e)
		}
	}
	if failed < 5 {
		t.Errorf("only %d calls failed; the echoing server should have failed most", failed)
	}
	if strings.Contains(logs.String(), sabTestKey) {
		t.Errorf("a log line carries the key:\n%s", logs.String())
	}
}

// --- health -------------------------------------------------------------------------

func TestSABnzbdFacts(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "5.1.3")
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "complete"), 0o755); err != nil {
		t.Fatal(err)
	}
	dc.PathMaps = PathMaps{{Remote: "/downloads", Local: local}}
	f.setCats("*", "arrmada", "movies")
	f.setAnswer("get_config", func(form url.Values) (int, string) {
		if form.Get("section") == "categories" {
			return 200, `{"config":{"categories":[{"name":"*","dir":""},{"name":"arrmada","dir":"films*"},{"name":"movies","dir":"/x"}]}}`
		}
		return 200, `{"config":{"misc":{"language":"de"}}}`
	})
	facts, err := newSAB().Facts(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Version != "5.1.3" || strings.Join(facts.Missing, ",") != "arrmada-tv,arrmada-books,arrmada-music" ||
		!facts.CompleteFolder.Visible || facts.CompleteFolder.Local != filepath.Join(local, "complete") ||
		facts.Language != "de" || !facts.NotEnglish || facts.ServersConfigured != 0 || len(facts.Categories) != 4 {
		t.Errorf("facts = %+v", facts)
	}
	movies := facts.Categories[0]
	if movies.Name != "arrmada" || !movies.Present || movies.For != "movies" || movies.Folder == nil ||
		movies.Folder.Remote != "/downloads/complete/films" || movies.Folder.Visible {
		t.Errorf("movie category = %+v (folder %+v)", movies, movies.Folder)
	}
	joined := strings.Join(facts.Warnings, "\n")
	for _, want := range []string{"no news server", "Add missing categories", "/downloads/complete/films", `language is "de"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
	if cmds := f.commands(); len(cmds) != 0 {
		t.Errorf("reading facts changed SABnzbd: %+v", cmds)
	}

	// Servers: the recorded 5.1.3 status has none; one that works and one that doesn't.
	f.setAnswer("fullstatus", func(url.Values) (int, string) {
		return 200, `{"status":{"version":"5.1.3","completedir":"/downloads/complete","servers":[` +
			`{"servername":"main","servererror":"","serveroptional":0,"servertotalconn":20},` +
			`{"servername":"fill","servererror":"Server fill.example will be ignored for 10 minutes","serveroptional":1,"servertotalconn":4}]}}`
	})
	facts, err = newSAB().Facts(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if facts.ServersConfigured != 2 || facts.ServersWorking != 1 || !facts.Servers[0].OK || facts.Servers[1].OK ||
		!facts.Servers[1].Optional || facts.Servers[0].Connections != 20 {
		t.Errorf("servers = %+v", facts.Servers)
	}
	if strings.Contains(strings.Join(facts.Warnings, "\n"), "news server") {
		t.Errorf("a working server still warned: %v", facts.Warnings)
	}
}

// Categories are created only for what's missing, and only when asked.
func TestSABnzbdAddMissingCategories(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "4.5.5")
	f.setCats("*", "arrmada", "arrmada-music")
	created, err := newSAB().AddMissingCategories(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(created, ",") != "arrmada-tv,arrmada-books" {
		t.Errorf("created %v", created)
	}
	var sets []string
	for _, c := range f.commands() {
		if c.mode != "set_config" || c.form.Get("section") != "categories" || c.form.Get("keyword") != c.form.Get("name") {
			t.Errorf("unexpected command %+v", c)
		}
		sets = append(sets, c.form.Get("name"))
	}
	if strings.Join(sets, ",") != "arrmada-tv,arrmada-books" {
		t.Errorf("set_config for %v", sets)
	}
	f.reset()
	if created, err := newSAB().AddMissingCategories(ctx, dc); err != nil || len(created) != 0 || len(f.commands()) != 0 {
		t.Errorf("second run: %v %v %+v", created, err, f.commands())
	}
	// A category SABnzbd doesn't keep is reported, not counted as added.
	f.setCats("*")
	f.setAnswer("set_config", func(url.Values) (int, string) { return 200, `{"config":{}}` })
	if _, err := newSAB().AddMissingCategories(ctx, dc); err == nil || !strings.Contains(err.Error(), "didn't keep") {
		t.Errorf("unkept category: %v", err)
	}
}

// The path check reads SABnzbd's completed folder and each category folder of its own.
func TestSABnzbdTestPaths(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "5.1.3")
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "complete"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.setLists(`{"queue":{"slots":[]}}`, `{"history":{"slots":[]}}`)
	f.setAnswer("get_config", func(url.Values) (int, string) {
		return 200, `{"config":{"categories":[{"name":"arrmada-tv","dir":"/tv-elsewhere"}]}}`
	})
	svc, _ := newTestService(t)
	c := mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: dc.URL, Password: sabTestKey,
		PathMaps: PathMaps{{Remote: "/downloads", Local: local}}})
	rep, err := svc.TestPaths(ctx, c.ID, PathEnv{Downloads: local})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Checks) != 2 || rep.Checks[0].What != "save_folder" || rep.Checks[0].Remote != "/downloads/complete" || !rep.Checks[0].Visible ||
		rep.Checks[1].What != "category_folder" || rep.Checks[1].Category != "arrmada-tv" || rep.Checks[1].Visible ||
		!strings.Contains(rep.Summary, "/tv-elsewhere (arrmada-tv)") {
		t.Errorf("report = %+v", rep)
	}
	// Without the category folder, all is well.
	f.setAnswer("get_config", func(url.Values) (int, string) { return 200, `{"config":{"categories":[]}}` })
	rep, err = svc.TestPaths(ctx, c.ID, PathEnv{Downloads: local})
	if err != nil || !rep.OK {
		t.Errorf("report = %+v, %v", rep, err)
	}
}

// Through the service: a SABnzbd row gets the built-in client, and the facts and
// categories calls refuse a torrent client.
func TestSABnzbdService(t *testing.T) {
	ctx := context.Background()
	_, dc := newSABFake(t, "5.1.3")
	svc, _ := newTestService(t)
	if svc.HasTransport(ctx, TransportUsenet) {
		t.Fatal("usenet available with no client")
	}
	sab := mustCreate(t, svc, Client{Name: "SAB", Kind: KindSABnzbd, URL: dc.URL, Password: sabTestKey})
	if !svc.HasTransport(ctx, TransportUsenet) {
		t.Error("a SABnzbd row doesn't make usenet available")
	}
	if err := svc.Test(ctx, sab.ID); err != nil {
		t.Errorf("Test: %v", err)
	}
	if _, err := svc.UsenetFacts(ctx, sab.ID); err != nil {
		t.Errorf("UsenetFacts: %v", err)
	}
	qb := mustCreate(t, svc, Client{Name: "QB", Kind: KindQbittorrent, URL: "http://qb.invalid"})
	if _, err := svc.UsenetFacts(ctx, qb.ID); !errors.Is(err, ErrNotUsenet) {
		t.Errorf("facts of a torrent client: %v", err)
	}
	if _, err := svc.AddMissingCategories(ctx, qb.ID); !errors.Is(err, ErrNotUsenet) {
		t.Errorf("categories of a torrent client: %v", err)
	}
	if _, err := svc.UsenetFacts(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("facts of no client: %v", err)
	}
}

