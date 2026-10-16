package indexer

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Some indexers sit behind Cloudflare (or a proxy like it) that only admits the apps the
// indexer chose — SABnzbd, NZBHydra, Prowlarr — and answers everything else with a block
// page. That answer used to be read as a status code: a 403 became "refused the API key",
// which sent people chasing a key that was fine. A blocked request is reported as what it
// is, and (for an NZB download) the usenet client may be handed the link to fetch itself.

// BlockedError is an indexer, or a proxy in front of it, answering Arrmada's request with
// a web page or a block instead of an NZB or search results.
//
// It isn't counted against the indexer for an NZB download: its searches may well work
// (through Prowlarr they do), and pausing them over a blocked download would stop the
// searches that find the releases.
type BlockedError struct {
	Indexer string
	Status  int
	// Page is true when the answer was a web page (HTML), false for a bare refusal.
	Page bool
	// AppGate is true for a 403 or a Cloudflare challenge/block: the site lets some apps in
	// and not others.
	AppGate bool
	// What was asked for: "an NZB" or "results".
	What string

	// link is, for an NZB download, the address that was blocked (after any redirect) for
	// the usenet client to fetch itself; "" when it mustn't be handed over. It carries the
	// indexer's key, so it is never part of the error's text.
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
	msg := fmt.Sprintf("%s blocked Arrmada's request (HTTP %d, %s)", e.Indexer, e.Status, got)
	if e.AppGate {
		msg += " — it may only allow certain apps"
	}
	return msg
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
// cf-mitigated header it sets on a challenge, its cf-ray header on a 403 or 503, or its
// challenge page's own markers. The markers are looked for only in a body that is an HTML
// document, never in a feed that merely came labelled text/html (a release could be
// called anything).
func isCloudflareBlock(status int, h http.Header, body []byte) bool {
	if h.Get("Cf-Mitigated") != "" {
		return true
	}
	if h.Get("Cf-Ray") != "" && (status == http.StatusForbidden || status == http.StatusServiceUnavailable) {
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
// Cloudflare challenge or block, a 403, or a web page — but never a 429 (a limit), a 404
// or 410 (gone), or a plain server error (5xx without Cloudflare's markers), which keep
// their own meaning. The caller has already ruled out the indexer's own <error> document.
func blockedAnswer(status int, h http.Header, body []byte) (blocked, page, appGate bool) {
	page = isWebPage(h, body)
	cf := isCloudflareBlock(status, h, body)
	switch {
	case status == http.StatusTooManyRequests || status == http.StatusNotFound || status == http.StatusGone:
		return false, page, false
	case cf:
		return true, page, true
	case status == http.StatusForbidden:
		return true, page, true
	case page && status < 500:
		return true, page, false
	}
	return false, page, false
}

// handoffLink is the address the usenet client may be given when the indexer blocked
// Arrmada's download: the last address asked (after Prowlarr's redirect to the indexer,
// say), but never a Cloudflare challenge address, only http(s), and never a link that
// carries a Prowlarr-synced row's key — that is Prowlarr's own API key, which must not end
// up in the usenet client's history.
func handoffLink(idx Indexer, link string) string {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	if strings.HasPrefix(u.Path, "/cdn-cgi/") {
		return ""
	}
	if idx.ProwlarrID != 0 && strings.TrimSpace(idx.APIKey) != "" {
		for _, vs := range u.Query() {
			for _, v := range vs {
				if v == idx.APIKey {
					return ""
				}
			}
		}
		if strings.Contains(link, idx.APIKey) || strings.Contains(link, url.QueryEscape(idx.APIKey)) {
			return ""
		}
	}
	return link
}
