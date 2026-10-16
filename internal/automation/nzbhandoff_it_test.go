package automation

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download/nntptest"
	"github.com/tristenlammi/arrmada/internal/httpua"
)

// An indexer that blocks Arrmada but admits SABnzbd, against the rig's real SABnzbd
// (scripts/usenet-it.sh). The indexer and a Prowlarr in front of it run in this process,
// where SABnzbd reaches them by ARRMADA_SABNZBD_NNTP_HOST: Prowlarr's download link
// redirects to the indexer's own, and the indexer answers any User-Agent but SABnzbd's with
// a Cloudflare-style block page. Nothing here talks to a real indexer, and the "keys" are
// made up.
//
// Arrmada's own fetch is blocked and says so; SABnzbd is handed the indexer's link, fetches
// the NZB with its own User-Agent, downloads it from the fake news server, and Arrmada
// imports the episode under SABnzbd's job id. Then an indexer that turns SABnzbd away too:
// the job fails in SABnzbd ("URL Fetching failed; …"), which is not blocklisted, and what
// Arrmada reads of it carries no key.
func TestUsenetBlockedIndexerHandoffLive(t *testing.T) {
	r := newUsenetRig(t, "arrmada-nzbua-handoff")
	r.c.fetchRelease = nil // the real indexer fetch
	host := os.Getenv("ARRMADA_SABNZBD_NNTP_HOST")
	if host == "" {
		t.Fatal("ARRMADA_SABNZBD_NNTP_HOST is required")
	}
	run := rigRun()
	const finderKey = "FINDERDOWNLOADKEY987654321"
	const prowlarrKey = "PROWLARRAPIKEY0123456789"

	payload := make([]byte, 52<<20) // above the importer's 50 MB sample floor
	for i := range payload {
		payload[i] = byte(i*37 + i/4091)
	}
	title := "Show.S01E01.1080p.WEB-DL-HANDOFF" + run
	nzb := nntptest.NZBFiles(r.news.AddFile("handoff-"+run+"@arrmada.test", title+".mkv", payload, 768_000))

	var mu sync.Mutex
	hits := map[string][]string{} // path → User-Agents, in order
	sabRefused := false           // the indexer turns SABnzbd away too
	mux := http.NewServeMux()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://%s:%d", host, ln.Addr().(*net.TCPAddr).Port)
	mux.HandleFunc("/7/download", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		hits[req.URL.Path] = append(hits[req.URL.Path], req.UserAgent())
		mu.Unlock()
		http.Redirect(w, req, base+"/getnzb/"+req.URL.Query().Get("id")+".nzb?i=1&r="+finderKey, http.StatusMovedPermanently)
	})
	mux.HandleFunc("/getnzb/", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		hits[req.URL.Path] = append(hits[req.URL.Path], req.UserAgent())
		refuse := sabRefused
		mu.Unlock()
		if strings.HasPrefix(req.UserAgent(), "SABnzbd/") && req.URL.Query().Get("r") == finderKey {
			if refuse {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/x-nzb")
			w.Header().Set("Content-Disposition", `attachment; filename="`+title+`.nzb"`)
			_, _ = w.Write(nzb)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Header().Set("Cf-Ray", "8d1f2e3a4b5c6d7e-SYD")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Just a moment...</title></head><body>Sorry, you have been blocked</body></html>`))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	link := func(id string) string {
		return base + "/7/download?apikey=" + prowlarrKey + "&id=" + id + "&file=" + id
	}
	noSecrets := func(what, s string) {
		t.Helper()
		for _, sec := range []string{finderKey, prowlarrKey, r.key} {
			if strings.Contains(s, sec) {
				t.Errorf("%s carries a secret: %q", what, s)
			}
		}
	}

	// --- Blocked for Arrmada, fetched by SABnzbd, downloaded and imported.
	key, err := r.c.grabTo(r.ctx, "NZBIdx", link("good"+run), title, seriesCategory)
	if err != nil {
		t.Fatalf("grab: %s", scrubKey(err, r.key))
	}
	r.c.recordSeriesGrab(r.ctx, r.seriesID, title, "NZBIdx", "", key)
	r.waitFor(t, "SABnzbd to fetch the blocked NZB and the episode to import", 6*time.Minute, func() bool { return r.imported(t, title) })
	if g := grabRowOf(t, r.c, title); g.hash != key || g.transport != "usenet" {
		t.Errorf("grab = %+v", g)
	}
	mu.Lock()
	finder := append([]string(nil), hits["/getnzb/good"+run+".nzb"]...)
	prowlarr := append([]string(nil), hits["/7/download"]...)
	mu.Unlock()
	t.Logf("Prowlarr saw %q; the indexer saw %q", prowlarr, finder)
	if len(prowlarr) != 1 || prowlarr[0] != httpua.UserAgent() {
		t.Errorf("Prowlarr saw %q, want Arrmada's one request", prowlarr)
	}
	if len(finder) < 2 || finder[0] != httpua.UserAgent() || !strings.HasPrefix(finder[len(finder)-1], "SABnzbd/") {
		t.Errorf("the indexer saw %q, want Arrmada (blocked) then SABnzbd", finder)
	}
	var detail string
	if err := r.c.db.QueryRow(`SELECT detail FROM series_events WHERE series_id = ? AND event = ?`, r.seriesID, handoffEvent).Scan(&detail); err != nil {
		t.Errorf("no hand-off line in the history: %v", err)
	}
	noSecrets("the history", detail)

	// --- Turned away for SABnzbd too: it fails the job, and that's not the release's fault.
	mu.Lock()
	sabRefused = true
	mu.Unlock()
	refused := "Show.S01E02.1080p.WEB-DL-HANDOFF" + run
	key2, err := r.c.grabTo(r.ctx, "NZBIdx", link("refused"+run), refused, seriesCategory)
	if err != nil {
		t.Fatalf("grab: %s", scrubKey(err, r.key))
	}
	r.c.recordSeriesGrab(r.ctx, r.seriesID, refused, "NZBIdx", "", key2)
	var failMsg, name string
	r.waitFor(t, "SABnzbd to fail fetching the refused NZB", 3*time.Minute, func() bool {
		q, err := r.c.downloads.Queue(r.ctx)
		if err != nil {
			return false
		}
		for _, it := range q {
			if it.Hash == key2 && it.FailMessage != "" {
				failMsg, name = it.FailMessage, it.Name
				return true
			}
		}
		return false
	})
	t.Logf("SABnzbd: %q (job %q)", failMsg, name)
	noSecrets("the fail message", failMsg)
	noSecrets("the job's name", name)
	f := ClassifyUsenetFailure(failMsg)
	if !f.Environment() || !f.Fetch {
		t.Errorf("%q reads as %+v, want a fetch failure left for a person", failMsg, f)
	}
	for i := 0; i < 3; i++ {
		r.pass()
		time.Sleep(time.Second)
	}
	if reason, ok := r.blocklisted(t, refused); ok {
		t.Errorf("a release SABnzbd couldn't fetch was blocklisted: %s", reason)
	}
	if g := grabRowOf(t, r.c, refused); g.status == grabStatusFailed {
		t.Errorf("the grab was failed over: %+v", g)
	}
	if strings.Contains(sabArchivedFailMessage(t, r.url, r.key, strings.TrimPrefix(key2, "sab-")), "URL Fetching failed") {
		t.Log("SABnzbd's own history keeps the failed link (accepted: it's SABnzbd's)")
	}
}
