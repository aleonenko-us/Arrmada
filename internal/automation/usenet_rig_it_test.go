package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/download/nntptest"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// More of a usenet grab's life against a real SABnzbd, run by scripts/usenet-it.sh with
// the same ARRMADA_SABNZBD_* settings as TestUsenetLifecycleLive, plus
//
//	ARRMADA_SABNZBD_FIXTURES  a folder of releases made with SABnzbd's own tools: repair/
//	                          (a file and its par2 set) and pw/ (a password-protected 7z)
//
// Damage the par2 set covers is repaired and imported; a password-protected archive fails
// as a bad release and is replaced; SABnzbd restarting mid-download condemns nothing.

// usenetRig is one test's harness wired to the rig's SABnzbd and its own fake news server.
type usenetRig struct {
	*lifecycleHarness
	ctx      context.Context
	key, url string
	news     *nntptest.Server
	nzbs     map[string][]byte
	fixtures string
}

func newUsenetRig(t *testing.T, server string) *usenetRig {
	t.Helper()
	if os.Getenv("ARRMADA_SABNZBD_IT") == "" {
		t.Skip("set ARRMADA_SABNZBD_IT=1 and the ARRMADA_SABNZBD_* settings (scripts/usenet-it.sh) to run against a real SABnzbd")
	}
	key, sabURL := os.Getenv("ARRMADA_SABNZBD_KEY"), os.Getenv("ARRMADA_SABNZBD_URL")
	remote, local := os.Getenv("ARRMADA_SABNZBD_REMOTE"), os.Getenv("ARRMADA_SABNZBD_LOCAL")
	fixtures := os.Getenv("ARRMADA_SABNZBD_FIXTURES")
	if key == "" || sabURL == "" || remote == "" || local == "" || fixtures == "" {
		t.Fatal("ARRMADA_SABNZBD_URL, _KEY, _REMOTE, _LOCAL and _FIXTURES are required")
	}
	h := newLifecycleHarness(t)
	h.c.removeTorrent = nil
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Minute)
	t.Cleanup(cancel)
	h.c.downloads.SetFolders(func(context.Context) download.PathEnv {
		return download.PathEnv{Downloads: local, DataDir: "/nonexistent-arrmada-data"}
	})
	sab, err := h.c.downloads.Create(ctx, download.Client{Name: "SABnzbd", Kind: download.KindSABnzbd, URL: sabURL, Password: key,
		Enabled: true, Priority: 1, PathMaps: download.PathMaps{{Remote: remote, Local: local}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.downloads.AddMissingCategories(ctx, sab.ID); err != nil {
		t.Fatalf("categories: %s", scrubKey(err, key))
	}
	news := nntptest.Start(t, "0.0.0.0:0")
	sabCall(t, sabURL, key, "set_config", url.Values{"section": {"servers"}, "keyword": {server}, "name": {server},
		"host": {os.Getenv("ARRMADA_SABNZBD_NNTP_HOST")}, "port": {fmt.Sprint(news.Port())}, "connections": {"2"},
		"enable": {"1"}, "ssl": {"0"}, "retention": {"0"}})
	t.Cleanup(func() {
		sabCall(t, sabURL, key, "del_config", url.Values{"section": {"servers"}, "keyword": {server}})
	})
	if _, err := h.c.indexers.Create(ctx, indexer.Indexer{Name: "NZBIdx", Kind: indexer.KindNewznab, URL: "http://nzb.invalid", Priority: 10, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r := &usenetRig{lifecycleHarness: h, ctx: ctx, key: key, url: sabURL, news: news, nzbs: map[string][]byte{}, fixtures: fixtures}
	h.c.fetchRelease = func(_ context.Context, name, link string) (indexer.FetchResult, error) {
		if b, ok := r.nzbs[link]; ok && name == "NZBIdx" {
			return indexer.FetchResult{File: b, Filename: "release.nzb"}, nil
		}
		return indexer.FetchResult{}, errors.New("no such NZB")
	}
	return r
}

// pass is one round of what the scheduler runs: reconcile, stall check, import.
func (r *usenetRig) pass() {
	r.c.downloads.InvalidateSnapshot()
	_, _ = r.c.ReconcileAcquisitions(r.ctx)
	r.c.DetectStalled(r.ctx)
	r.c.ImportSeriesDownloads(r.ctx)
}

func (r *usenetRig) waitFor(t *testing.T, what string, limit time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !done() {
		if time.Now().After(deadline) {
			r.c.downloads.InvalidateSnapshot()
			q, _ := r.c.downloads.Queue(r.ctx)
			t.Fatalf("timed out waiting for %s; queue: %+v", what, q)
		}
		time.Sleep(time.Second)
		r.pass()
	}
}

// post puts every file of a fixture folder on the news server as multi-part posts and
// returns the NZB's files, by name.
func (r *usenetRig) post(t *testing.T, dir, prefix string) map[string]nntptest.File {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(r.fixtures, dir))
	if err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	out := map[string]nntptest.File{}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(r.fixtures, dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = r.news.AddFile(prefix+"-"+e.Name()+"@arrmada.test", e.Name(), b, 768_000)
	}
	return out
}

// grab hands the release to SABnzbd the way a search does, as episode 1x<ep> of the show.
func (r *usenetRig) grab(t *testing.T, title string, files map[string]nntptest.File) string {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]nntptest.File, 0, len(names))
	for _, n := range names {
		list = append(list, files[n])
	}
	link := "https://nzb.invalid/get?id=" + url.QueryEscape(title)
	r.nzbs[link] = nntptest.NZBFiles(list...)
	key, err := r.c.grabTo(r.ctx, "NZBIdx", link, title, seriesCategory)
	if err != nil || download.KeyTransport(key) != download.TransportUsenet {
		t.Fatalf("grab = %q, %s", key, scrubKey(err, r.key))
	}
	r.c.recordSeriesGrab(r.ctx, r.seriesID, title, "NZBIdx", "", key)
	return key
}

func (r *usenetRig) blocklisted(t *testing.T, title string) (string, bool) {
	t.Helper()
	var reason string
	err := r.c.db.QueryRow(`SELECT reason FROM blocklist WHERE norm_title = ? AND media_type = 'series'`, normTitle(title)).Scan(&reason)
	if err != nil {
		return "", false
	}
	return reason, true
}

func rigRun() string { return fmt.Sprint(time.Now().UnixNano() % 1_000_000) }

// sabArchivedFailMessage reads a job's fail_message from SABnzbd's history, archive
// included (removing a finished job archives it).
func sabArchivedFailMessage(t *testing.T, base, key, nzoID string) string {
	t.Helper()
	form := url.Values{"mode": {"history"}, "archive": {"1"}, "output": {"json"}, "apikey": {key}, "limit": {"200"}}
	resp, err := http.PostForm(strings.TrimRight(base, "/")+"/api", form)
	if err != nil {
		t.Logf("history: %s", scrubKey(err, key))
		return ""
	}
	defer resp.Body.Close()
	var h struct {
		History struct {
			Slots []struct {
				NzoID       string `json:"nzo_id"`
				FailMessage string `json:"fail_message"`
			} `json:"slots"`
		} `json:"history"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return ""
	}
	for _, s := range h.History.Slots {
		if strings.EqualFold(s.NzoID, nzoID) {
			return s.FailMessage
		}
	}
	return ""
}

// About 3% of a release's articles gone from the provider, the par2 set to cover it: SABnzbd
// fetches the repair blocks, repairs, and Arrmada imports the episode as if nothing happened.
func TestUsenetPar2RepairLive(t *testing.T) {
	r := newUsenetRig(t, "arrmada-u5-repair")
	run := rigRun()
	files := r.post(t, "repair", "repair-"+run)
	var media nntptest.File
	for name, f := range files {
		if strings.HasSuffix(name, ".mkv") {
			media = f
		}
	}
	if len(media.Segments) < 40 {
		t.Fatalf("the repair fixture's media file has %d articles, want 40 or more", len(media.Segments))
	}
	// Drop ~3%: every 33rd article, never the first (SABnzbd reads the name from it).
	var dropped []string
	for i := 16; i < len(media.Segments); i += 33 {
		dropped = append(dropped, media.Segments[i].ID)
	}
	r.news.Drop(dropped...)
	t.Logf("%d of %d articles dropped", len(dropped), len(media.Segments))

	title := "Show.S01E01.1080p.WEB-DL-REPAIR" + run
	key := r.grab(t, title, files)
	r.waitFor(t, "the repaired release to import", 6*time.Minute, func() bool { return r.imported(t, title) })
	if g := grabRowOf(t, r.c, title); g.status != grabStatusImported || g.hash != key {
		t.Errorf("grab = %+v", g)
	}
	if reason, ok := r.usenetFailure(t, title); ok {
		t.Errorf("the repaired release was blocklisted: %s", reason)
	}
	// The imported file is the original, byte for byte: the dropped articles were rebuilt.
	var path string
	if err := r.c.db.QueryRow(`SELECT file_path FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, r.seriesID).Scan(&path); err != nil {
		t.Fatalf("the imported file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(r.fixtures, "repair", "Show.S01E01.1080p.WEB-DL-REPAIR.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the imported file (%d bytes) isn't the original (%d bytes): not repaired", len(got), len(want))
	}
}

// imported: the episode has its file and the grab is marked imported (the grab is marked
// on the pass after the import).
func (r *usenetRig) imported(t *testing.T, title string) bool {
	return episodeHasFile(t, r.lifecycleHarness, 1, 1) && grabRowOf(t, r.c, title).status == grabStatusImported
}

// usenetFailure is the blocklist reason when the release was blocklisted for failing in
// SABnzbd (not, say, for an imported season having nothing more to give).
func (r *usenetRig) usenetFailure(t *testing.T, title string) (string, bool) {
	reason, ok := r.blocklisted(t, title)
	if !ok || !strings.HasPrefix(reason, "usenet download failed") {
		return "", false
	}
	return reason, true
}

// A password-protected archive whose NZB carries no password: SABnzbd can't unpack it, and
// Arrmada treats that as a bad release — blocklisted with the reason, and searched again.
func TestUsenetPasswordedLive(t *testing.T) {
	r := newUsenetRig(t, "arrmada-u5-pw")
	run := rigRun()
	files := r.post(t, "pw", "pw-"+run)
	title := "Show.S01E02.1080p.WEB-DL-PW" + run
	searches := r.ix.searchCount()
	key := r.grab(t, title, files)
	failMsg := ""
	r.waitFor(t, "the passworded release to fail over", 4*time.Minute, func() bool {
		if q, err := r.c.downloads.Queue(r.ctx); err == nil {
			for _, it := range q {
				if it.Hash == key && it.FailMessage != "" {
					failMsg = it.FailMessage
				}
			}
		}
		return grabRowOf(t, r.c, title).status == grabStatusFailed
	})
	if failMsg == "" {
		// Failed over within one pass: SABnzbd's reason is in its archived history.
		failMsg = sabArchivedFailMessage(t, r.url, r.key, strings.TrimPrefix(key, "sab-"))
	}
	t.Logf("SABnzbd's reason: %q", failMsg)
	if f := ClassifyUsenetFailure(failMsg); failMsg == "" || f.Kind != UsenetFailBadRelease || !f.Known {
		t.Errorf("SABnzbd's reason %q reads as %+v, want a known bad release", failMsg, f)
	}
	// SABnzbd 5.1 reports a 7z it has no password for as "Unpacking failed, see logfile"
	// (a RAR, as encrypted): either way it's the release, not the disk.
	reason, ok := r.usenetFailure(t, title)
	if !ok || (reason != "usenet download failed: it's password-protected" && reason != "usenet download failed: it couldn't be unpacked") {
		t.Errorf("blocklist = %q (%v), want the password or unpack reason", reason, ok)
	}
	if r.ix.searchCount() <= searches {
		t.Error("no new search after the failure")
	}
	r.c.downloads.InvalidateSnapshot()
	if q, err := r.c.downloads.Queue(r.ctx); err == nil {
		for _, it := range q {
			if it.Hash == key {
				t.Errorf("the failed job is still in SABnzbd: %+v", it)
			}
		}
	}
}

// SABnzbd restarting while a release downloads: while it's away nothing is failed,
// blocklisted or searched for, and once it's back the same job finishes and imports.
func TestUsenetSABRestartLive(t *testing.T) {
	r := newUsenetRig(t, "arrmada-u5-restart")
	run := rigRun()
	title := "Show.S01E01.1080p.WEB-DL-RESTART" + run
	payload := make([]byte, 52<<20) // above the importer's 50 MB sample floor
	for i := range payload {
		payload[i] = byte(i*29 + i/4091)
	}
	r.news.SetDelay(250 * time.Millisecond) // ~70 articles over 2 connections: ~9 s
	file := r.news.AddFile("restart-"+run+"@arrmada.test", title+".mkv", payload, 768_000)
	searches := r.ix.searchCount()
	key := r.grab(t, title, map[string]nntptest.File{title + ".mkv": file})

	r.waitFor(t, "the download to start", 2*time.Minute, func() bool {
		q, err := r.c.downloads.Queue(r.ctx)
		if err != nil {
			return false
		}
		for _, it := range q {
			if it.Hash == key && it.DownloadedBytes > 0 && it.Progress < 0.6 {
				return true
			}
		}
		return false
	})
	sabCall(t, r.url, r.key, "restart", nil)
	t.Log("asked SABnzbd to restart mid-download")

	// While it's down and coming back the scheduler keeps running, and condemns nothing.
	up := func() bool {
		ctx, cancel := context.WithTimeout(r.ctx, time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.url, "/")+"/api", strings.NewReader("mode=version&output=json"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	sawDown, back := false, false
	for end := time.Now().Add(90 * time.Second); time.Now().Before(end) && !back; {
		if !up() {
			sawDown = true
		} else if sawDown {
			back = true
		}
		r.pass()
		if g := grabRowOf(t, r.c, title); g.status == grabStatusFailed {
			t.Fatalf("the grab failed while SABnzbd restarted: %+v", g)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !sawDown || !back {
		t.Fatalf("SABnzbd didn't go away and come back (away %v, back %v)", sawDown, back)
	}
	// A few more rounds with it back but the job still resuming.
	for i := 0; i < 5; i++ {
		r.pass()
		if g := grabRowOf(t, r.c, title); g.status == grabStatusFailed {
			t.Fatalf("the grab failed after SABnzbd came back: %+v", g)
		}
		time.Sleep(time.Second)
	}
	r.c.downloads.InvalidateSnapshot()
	if q, err := r.c.downloads.Queue(r.ctx); err == nil {
		found := false
		for _, it := range q {
			found = found || it.Hash == key
		}
		t.Logf("after the restart the same job is in SABnzbd: %v", found)
	}
	r.news.SetDelay(0)
	r.waitFor(t, "the same job to finish and import", 5*time.Minute, func() bool { return r.imported(t, title) })
	if g := grabRowOf(t, r.c, title); g.status != grabStatusImported || g.hash != key {
		t.Errorf("grab = %+v, want imported under %s", g, key)
	}
	if reason, ok := r.usenetFailure(t, title); ok {
		t.Errorf("blocklisted across the restart: %s", reason)
	}
	if n := r.ix.searchCount(); n != searches {
		t.Errorf("%d searches across the restart, want none", n-searches)
	}
}
