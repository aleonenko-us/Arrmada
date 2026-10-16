package indexer

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Some indexers sit behind Cloudflare (or a proxy like it) that only admits the apps the
// indexer chose — SABnzbd, NZBHydra, Prowlarr — and answers everything else with a block
// page. That answer used to be read as a status code: a 403 became "refused the API key",
// which sent people chasing a key that was fine. A blocked request is reported as what it
// is, and (for an NZB download) the usenet client may be handed the link to fetch itself.

// BlockedError is an indexer, or a proxy in front of it, turning Arrmada's request away —
// a Cloudflare challenge, or a 403 that isn't the indexer's own <error> — instead of
// sending an NZB or search results.
//
// It isn't counted against the indexer for an NZB download: its searches may well work
// (through Prowlarr they do), and pausing them over a blocked download would stop the
// searches that find the releases.
type BlockedError struct {
	Indexer string
	Status  int
	// Page is true when the answer was a web page (HTML), false for a bare refusal.
	Page bool
	// What was asked for: "an NZB" or "results".
	What string

	// link is, for an NZB download, the indexer's link for the usenet client to fetch itself
	// (handoffLink); "" when none may be handed over. It carries the indexer's key, so it is
	// never part of the error's text.
	link string
	// key (the indexer's) and original (the link as the release gave it) are what Redact
	// cuts out, besides link.
	key      string
	original string
}

func (e *BlockedError) Error() string {
	got := "a refusal instead of " + e.What
	if e.Page {
		got = "a web page instead of " + e.What
	}
	// Every block is a 403 or a Cloudflare challenge: the site lets some apps in and not others.
	return fmt.Sprintf("%s blocked Arrmada's request (HTTP %d, %s) — it may only allow certain apps", e.Indexer, e.Status, got)
}

// ClientLink is the NZB link a usenet client may fetch itself because the indexer blocked
// Arrmada's own download, "" when there is none. It carries the indexer's API key: give it
// only to the usenet client, and pass any text that might quote it through Redact.
func (e *BlockedError) ClientLink() string { return e.link }

// Redact cuts the NZB link (as asked for and as redirected to), the indexer's key and any
// key-like value in either link out of msg.
func (e *BlockedError) Redact(msg string) string {
	return redactNZB(msg, Indexer{APIKey: e.key}, e.original, e.link)
}

// cfMarkers are pieces of Cloudflare's challenge and block pages.
var cfMarkers = []string{
	"cf-chl", "/cdn-cgi/challenge-platform", "challenges.cloudflare.com", "cf-browser-verification",
	"attention required! | cloudflare", "cf-error-details", "<title>just a moment...</title>",
}

// isWebPage reports whether an answer is a web page: served as HTML, or starting like one.
func isWebPage(h http.Header, body []byte) bool {
	if ct := strings.ToLower(h.Get("Content-Type")); strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		return true
	}
	return startsHTML(body)
}

// startsHTML reports whether body begins like an HTML document.
func startsHTML(body []byte) bool {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	head = bytes.ToLower(bytes.TrimSpace(head))
	return bytes.HasPrefix(head, []byte("<!doctype html")) || bytes.HasPrefix(head, []byte("<html"))
}

// isCloudflareBlock reports whether an answer is Cloudflare's challenge or block: the
// cf-mitigated header it sets on a challenge, or its challenge page's own markers. The
// markers are looked for only in a body that is an HTML document, never in a feed that
// merely came labelled text/html (a release could be called anything). Cloudflare's cf-ray
// header alone says nothing: every answer through Cloudflare carries it, an indexer down
// for maintenance included.
func isCloudflareBlock(h http.Header, body []byte) bool {
	if h.Get("Cf-Mitigated") != "" {
		return true
	}
	if !startsHTML(body) {
		return false
	}
	head := body
	if len(head) > 64<<10 {
		head = head[:64<<10]
	}
	low := bytes.ToLower(head)
	for _, m := range cfMarkers {
		if bytes.Contains(low, []byte(m)) {
			return true
		}
	}
	return false
}

// blockedAnswer decides whether an answer that isn't what was asked for is a block: a
// Cloudflare challenge (cf-mitigated, or its challenge page) or a plain 403. Anything else
// keeps its own meaning — a 429 is a limit, a 404 or 410 gone, a 5xx the indexer struggling
// (backed off, its Retry-After honoured), a 401 the key, and a web page served with a 200
// "not an NZB". The caller has already ruled out the indexer's own <error> document.
func blockedAnswer(status int, h http.Header, body []byte) (blocked, page bool) {
	page = isWebPage(h, body)
	switch {
	case status == http.StatusTooManyRequests || status == http.StatusNotFound || status == http.StatusGone:
		return false, page
	case isCloudflareBlock(h, body):
		return true, page
	case status == http.StatusForbidden:
		return true, page
	}
	return false, page
}

// Hops whose path says they're not the NZB: a login, a block or a challenge page an
// indexer or its proxy redirects to.
var notNZBPath = regexp.MustCompile(`(?i)(^|/)(cdn-cgi|login|log-in|signin|sign-in|logon|auth|account|blocked|block|captcha|challenge|forbidden|denied|error)(/|$|\.|\?)`)

// prowlarrDownloadPath is Prowlarr's own download link (/{id}/download) and the v1 API's.
var prowlarrDownloadPath = regexp.MustCompile(`(?i)^(/[^/]+)*/\d+/download/?$|/api/v1/indexer/\d+/download`)

// isAggregatorLink reports whether link points at Prowlarr or NZBHydra rather than at an
// indexer: its own download link carries the aggregator's API key, which unlocks every
// indexer it holds and must never end up in a usenet client's history. aggregatorHosts are
// the addresses (host:port, lowercase) Prowlarr-synced rows reach Prowlarr by. When in
// doubt, it says yes.
func isAggregatorLink(idx Indexer, u *url.URL, aggregatorHosts map[string]bool) bool {
	host := strings.ToLower(u.Hostname())
	switch {
	case prowlarrDownloadPath.MatchString(u.Path):
		return true
	case strings.Contains(strings.ToLower(u.Path), "/getnzb/api/"): // NZBHydra2's download link
		return true
	case strings.Contains(host, "prowlarr") || strings.Contains(host, "hydra"):
		return true
	case u.Port() == "9696" || u.Port() == "5076" || u.Port() == "5075":
		return true
	case aggregatorHosts[strings.ToLower(u.Host)]:
		return true
	}
	if idx.ProwlarrID != 0 {
		// The row's key is Prowlarr's, and its address Prowlarr's.
		if row, err := url.Parse(idx.URL); err == nil && strings.EqualFold(row.Host, u.Host) {
			return true
		}
		if k := strings.TrimSpace(idx.APIKey); k != "" && (strings.Contains(u.String(), k) || strings.Contains(u.String(), url.QueryEscape(k))) {
			return true
		}
	}
	return false
}

// handoffLink is the address the usenet client may be given when the indexer blocked
// Arrmada's download, "" when none may be. hops are every address asked, the release's own
// link first and each redirect after.
//
// Through Prowlarr (or NZBHydra) it is the first hop away from it — the indexer's own link
// it redirected to — and never the aggregator's link itself. Straight from an indexer it is
// the release's own link. A later redirect (to a login page, a block page, the indexer's
// home page) is never what's handed over: the usenet client follows redirects itself. Only
// http(s), and never an address whose path looks like a login, block or challenge page.
func handoffLink(idx Indexer, hops []string, aggregatorHosts map[string]bool) string {
	if len(hops) == 0 {
		return ""
	}
	first, err := url.Parse(hops[0])
	if err != nil {
		return ""
	}
	link := hops[0]
	if isAggregatorLink(idx, first, aggregatorHosts) {
		if len(hops) < 2 {
			return "" // blocked at the aggregator itself: its link is its key
		}
		link = hops[1]
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	if notNZBPath.MatchString(u.Path) || isAggregatorLink(idx, u, aggregatorHosts) {
		return ""
	}
	return link
}
