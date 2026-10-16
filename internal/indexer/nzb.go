package indexer

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// A usenet release is grabbed by downloading its NZB here, on the server, and handing the
// usenet client the file — never the link. A Newznab download link carries the indexer's
// API key (…&apikey=… or …&r=…), and a client given the link keeps it in its history for
// anyone who can open that. So the link never leaves this file: it isn't logged, isn't put
// in an error, and the client only ever sees the bytes.

// fetchNZBTimeout bounds one NZB download. Indexers answer these from cache; a minute is
// generous, and a grab is someone waiting.
const fetchNZBTimeout = 60 * time.Second

// maxNZBBytes caps an NZB. A season pack's NZB runs to a few MB; anything this size isn't
// one, and reading it whole would only hold memory.
const maxNZBBytes = 64 << 20

// Plain answers for the ways an NZB download goes wrong, worded for the person who pressed
// Grab (or the log line a sweep writes).
var (
	errNZBTooLarge = errors.New("the indexer sent something larger than 64 MB instead of an NZB")
	errNotNZB      = errors.New("the indexer didn't send an NZB file — check the indexer in Settings → Connections")
)

// fetchNZBPayload downloads a Newznab release's NZB and returns its bytes. Every failure is
// a plain sentence with no link or key in it: the indexer's own <error> answer (bad key,
// download limit reached, item gone), an HTTP status, a block (BlockedError: a web page or
// a Cloudflare challenge instead of the NZB), a file too large, or a body that isn't an NZB.
func fetchNZBPayload(ctx context.Context, idx Indexer, downloadURL string) (FetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchNZBTimeout)
	defer cancel()
	// last is the address asked most recently: through Prowlarr, the indexer's own link it
	// redirects to. It is what a block is about, so it is what a usenet client would fetch.
	last := downloadURL
	client := &http.Client{
		Transport: httpua.Transport(nil),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if !strings.HasPrefix(req.URL.Path, "/cdn-cgi/") {
				last = req.URL.String()
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("%s gave an NZB link Arrmada can't read", idx.Name)
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, &nzbIndexerError{err: fmt.Errorf("couldn't download the NZB from %s: %s", idx.Name, scrubNZBError(err, idx, downloadURL, last))}
	}
	defer resp.Body.Close()

	body, err := readNZB(resp.Body)
	if err != nil && !errors.Is(err, errNZBTooLarge) {
		return FetchResult{}, &nzbIndexerError{err: fmt.Errorf("couldn't download the NZB from %s: %s", idx.Name, scrubNZBError(err, idx, downloadURL, last))}
	}
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	// The indexer's own <error> answer says why, whatever status it came with.
	if root, attrs, _, ok := xmlRoot(body); ok && root == "error" {
		return FetchResult{}, newznabFetchError(idx, resp.StatusCode, torznabErrorFrom(attrs), downloadURL, retryAfter)
	}
	// An NZB is an NZB, whatever Content-Type it was served with.
	if resp.StatusCode == http.StatusOK && err == nil && looksLikeNZBFile(body) {
		return FetchResult{File: body, Filename: nzbFilename(resp.Header.Get("Content-Disposition"), resp.Request)}, nil
	}
	if blocked, page, gate := blockedAnswer(resp.StatusCode, resp.Header, body); blocked {
		return FetchResult{}, &BlockedError{
			Indexer: idx.Name, Status: resp.StatusCode, Page: page, AppGate: gate, What: "an NZB",
			link: handoffLink(idx, last), key: idx.APIKey, original: downloadURL,
		}
	}
	if resp.StatusCode != http.StatusOK {
		return FetchResult{}, newznabFetchError(idx, resp.StatusCode, nil, downloadURL, retryAfter)
	}
	if errors.Is(err, errNZBTooLarge) {
		return FetchResult{}, fmt.Errorf("%s: %w", idx.Name, errNZBTooLarge)
	}
	if !looksLikeNZBFile(body) {
		return FetchResult{}, fmt.Errorf("%s: %w", idx.Name, errNotNZB)
	}
	return FetchResult{File: body, Filename: nzbFilename(resp.Header.Get("Content-Disposition"), resp.Request)}, nil
}

// readNZB reads at most maxNZBBytes of body, unpacking a gzipped NZB (some indexers serve
// .nzb.gz). errNZBTooLarge, with what was read, when there is more.
func readNZB(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxNZBBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, zerr := gzip.NewReader(bytes.NewReader(body))
		if zerr != nil {
			return nil, zerr
		}
		body, err = io.ReadAll(io.LimitReader(zr, maxNZBBytes+1))
		if err != nil {
			return nil, err
		}
	}
	if len(body) > maxNZBBytes {
		return body[:maxNZBBytes], errNZBTooLarge
	}
	return body, nil
}

// looksLikeNZBFile reports whether data is an NZB document: XML whose root element is <nzb>.
func looksLikeNZBFile(data []byte) bool {
	root, _, _, ok := xmlRoot(data)
	return ok && root == "nzb"
}

// newznabFetchError turns an indexer's refusal into a plain sentence. Newznab's codes:
// 100–102 the key or account, 300 the item, 429/500/501 a request or download limit.
// ne is nil when the indexer sent no <error> document, and the HTTP status speaks.
//
// A limit, a refused key or a server error is about the indexer, not this release, and is
// returned as an nzbIndexerError so Fetch records it against the indexer.
func newznabFetchError(idx Indexer, status int, ne *TorznabError, downloadURL string, retryAfter time.Duration) error {
	code := 0
	detail := ""
	if ne != nil {
		code = ne.Code
		detail = redactNZB(ne.Description, idx, downloadURL)
	}
	switch {
	case status == http.StatusTooManyRequests || code == 429 || code == 500 || code == 501:
		return &nzbIndexerError{err: fmt.Errorf("%s says its API or download limit is reached — try again once it resets", idx.Name), retryAfter: retryAfter}
	// Only the indexer's own auth codes, or a bare 401, are about the key. A 403 is as
	// often a proxy turning Arrmada away (blockedAnswer), and saying "key" there sent
	// people chasing a key that was fine.
	case (code >= 100 && code <= 102) || (status == http.StatusUnauthorized && ne == nil):
		msg := fmt.Sprintf("%s refused the API key", idx.Name)
		if detail != "" {
			msg += " (" + detail + ")"
		}
		return &nzbIndexerError{err: errors.New(msg + " — check the indexer in Settings → Connections")}
	case status == http.StatusNotFound || status == http.StatusGone || code == 300:
		return fmt.Errorf("%s no longer has this NZB", idx.Name)
	case detail != "":
		return fmt.Errorf("%s couldn't send the NZB: %s", idx.Name, detail)
	case status >= 500:
		return &nzbIndexerError{err: fmt.Errorf("%s couldn't send the NZB (HTTP %d)", idx.Name, status), retryAfter: retryAfter}
	}
	return fmt.Errorf("%s couldn't send the NZB (HTTP %d)", idx.Name, status)
}

// nzbIndexerError is an NZB download that failed because of the indexer itself — a limit
// reached, the key refused, the indexer unreachable or erroring — rather than this one
// release. Fetch records it in the indexer's status, so background searches back off an
// indexer that has run out of downloads (honouring its Retry-After) instead of grabbing
// from it again and again.
type nzbIndexerError struct {
	err        error
	retryAfter time.Duration
}

func (e *nzbIndexerError) Error() string { return e.err.Error() }
func (e *nzbIndexerError) Unwrap() error { return e.err }

// scrubNZBError is a transport error without the link or key in it. Go's url.Error quotes
// the whole request URL, so the link is cut out first, then any key that's left. links are
// the NZB link and wherever it redirected to (which carries the indexer's own key).
func scrubNZBError(err error, idx Indexer, links ...string) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // "Get \"<link>\": <cause>" → just the cause
	}
	return redactNZB(err.Error(), idx, links...)
}

// redactNZB removes the NZB links, the indexer's API key and every value of the links'
// queries (where Newznab puts the key, as apikey= or r=) from msg.
func redactNZB(msg string, idx Indexer, links ...string) string {
	secrets := []string{idx.APIKey}
	for _, link := range links {
		if link == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, link, "the NZB link")
		if u, err := url.Parse(link); err == nil {
			for _, vs := range u.Query() {
				secrets = append(secrets, vs...)
			}
			// A key can sit in the path too (/getnzb/<key>/<id>); long segments go as well.
			for _, seg := range strings.Split(u.Path, "/") {
				if len(seg) >= 24 && !strings.ContainsAny(seg, ".") {
					secrets = append(secrets, seg)
				}
			}
		}
	}
	for _, sec := range secrets {
		// Short values (t=get, i=1) are ids and words, not keys, and replacing them would
		// mangle the message.
		if len(sec) < 8 {
			continue
		}
		msg = strings.ReplaceAll(msg, sec, "REDACTED")
		if esc := url.QueryEscape(sec); esc != sec {
			msg = strings.ReplaceAll(msg, esc, "REDACTED")
		}
	}
	return msg
}

// nzbFilename names the NZB for the usenet client: the indexer's Content-Disposition name
// when it gives one, else the last part of the final URL's path, always ending in .nzb.
// Never anything from the query, which is where the key lives.
func nzbFilename(disposition string, req *http.Request) string {
	name := ""
	if _, params, err := mime.ParseMediaType(disposition); err == nil {
		name = params["filename"]
	}
	if name == "" && req != nil && req.URL != nil {
		name = path.Base(req.URL.Path)
	}
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	switch name {
	case "", ".", "/", "api":
		name = "arrmada"
	}
	name = strings.TrimSuffix(name, ".gz")
	if !strings.HasSuffix(strings.ToLower(name), ".nzb") {
		name += ".nzb"
	}
	return name
}
