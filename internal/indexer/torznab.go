package indexer

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/httpua"
)

// Torznab and Newznab share the same RSS-derived XML shape; the only difference
// is the attr namespace prefix (torznab: vs newznab:), which Go's xml package
// ignores when we match on the local name "attr".

type feed struct {
	XMLName xml.Name `xml:"rss"`
	// Torznab reports pagination in <newznab:response offset="0" total="500"/>. Without
	// reading it there's no way to know a result set was truncated — an indexer returning
	// its page size looks identical to one that genuinely had that many matches.
	Response feedResponse `xml:"channel>response"`
	Items    []feedItem   `xml:"channel>item"`
}

type feedResponse struct {
	Offset int `xml:"offset,attr"`
	Total  int `xml:"total,attr"`
}

type feedItem struct {
	Title       string        `xml:"title"`
	Description string        `xml:"description"`
	Link        string        `xml:"link"`
	Comments    string        `xml:"comments"`
	GUID        string        `xml:"guid"`
	PubDate     string        `xml:"pubDate"`
	Size        int64         `xml:"size"`
	Enclosure   feedEnclosure `xml:"enclosure"`
	Attrs       []feedAttr    `xml:"attr"`
}

type feedEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type feedAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// ParseFeed converts a Torznab/Newznab XML response body into releases.
func ParseFeed(data []byte) ([]Release, error) {
	releases, _, err := parseFeedPage(data)
	return releases, err
}

// parseFeedPage also returns the total the indexer claims to have, so Search knows whether
// more pages exist.
//
// An indexer's own <error> answer comes back as its TorznabError ("Incorrect user
// credentials") and a web page as errWebPage, instead of an XML parse error.
func parseFeedPage(data []byte) ([]Release, int, error) {
	if root, attrs, _, ok := xmlRoot(data); ok {
		switch root {
		case "error":
			return nil, 0, torznabErrorFrom(attrs)
		case "html":
			return nil, 0, errWebPage
		}
	}
	var f feed
	if err := xml.Unmarshal(data, &f); err != nil {
		return nil, 0, fmt.Errorf("parse feed: %w", err)
	}

	releases := make([]Release, 0, len(f.Items))
	for _, it := range f.Items {
		r := Release{
			Title:       it.Title,
			Description: it.Description,
			DownloadURL: firstNonEmpty(it.Enclosure.URL, it.Link),
			InfoURL:     firstURL(it.Comments, it.GUID),
			SizeBytes:   it.Size,
		}
		if t, ok := parseFeedDate(it.PubDate); ok {
			r.PublishedAt = t
		}
		for _, a := range it.Attrs {
			switch strings.ToLower(a.Name) {
			case "seeders":
				r.Seeders = atoi(a.Value)
			case "peers":
				r.Peers = atoi(a.Value)
			case "infohash":
				r.InfoHash = strings.ToLower(a.Value)
			case "size":
				if r.SizeBytes == 0 {
					r.SizeBytes = atoi64(a.Value)
				}
			case "category":
				if n := atoi(a.Value); n > 0 {
					r.Categories = append(r.Categories, n)
				}
			case "imdb", "imdbid":
				if id := NormalizeIMDB(a.Value); id != "" {
					r.IMDBID = id
				}
			case "tmdbid":
				r.TMDBID = atoi(a.Value)
			case "tvdbid":
				r.TVDBID = atoi(a.Value)
			}
		}
		releases = append(releases, r)
	}
	return releases, f.Response.Total, nil
}

// torznabRequestDelay is the minimum gap between requests to the SAME host. The
// scraped trackers (TorrentLeech, 1337x) have always throttled; torznab did not, which
// is what earns HTTP 429s once many series are monitored — every indexer behind one
// Prowlarr shares that host, so a burst of per-series searches hits it all at once.
const torznabRequestDelay = 1000 * time.Millisecond

// torznabRequestTimeout bounds one Torznab request once its slot in the host's queue has
// been granted. The queue wait itself only counts against the whole search's deadline, so
// a person's search waiting behind a sweep doesn't time out before it's asked; this cap
// keeps one slow host from using all of that.
const torznabRequestTimeout = 20 * time.Second

// TorznabSearcher queries Torznab/Newznab endpoints over HTTP. It implements
// Searcher for both the KindTorznab and KindNewznab kinds.
type TorznabSearcher struct {
	log  *slog.Logger
	http *http.Client
	// queue spaces requests per host (not per URL) and serves people's searches first.
	queue *hostThrottle
}

// NewTorznabSearcher returns a searcher with a sane request timeout.
func NewTorznabSearcher() *TorznabSearcher {
	c := &TorznabSearcher{http: httpua.Client(30 * time.Second)}
	c.queue = newHostThrottle(func() *slog.Logger { return c.log })
	return c
}

// SetLogger attaches a logger so each request page can be traced. Optional — the searcher
// works without one.
func (c *TorznabSearcher) SetLogger(l *slog.Logger) { c.log = l }

// queuesPerHost marks a searcher whose requests wait in a per-host queue and carry their
// own per-request deadline, so the fan-out must not put the queue wait under a
// per-indexer deadline (see Service.Search).
func (c *TorznabSearcher) queuesPerHost() {}

// waitUntil sleeps until the reserved slot time, or until ctx is cancelled.
// Shared by the native trackers' rate limiters so none of them block a
// cancelled search (or hold a mutex) while waiting out a request delay.
func waitUntil(ctx context.Context, slot time.Time) error {
	wait := time.Until(slot)
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// maxSearchPages bounds pagination so a misbehaving indexer — one that ignores offset, or
// reports a total it never delivers — can't spin forever.
const maxSearchPages = 10

// Search runs a search against a single indexer and returns its releases, stamped with
// the indexer's name and transport.
//
// It is id-first: when the query names the title by an id the indexer's stored caps say
// it takes for this kind of search (idParam), the indexer is asked by that id, and its
// releases are marked MatchedByID. An id search that finds nothing — a tracker with
// incomplete id tagging — or that the indexer refuses with its own error document is
// followed by today's text search, so asking by id can never find less than before.
func (c *TorznabSearcher) Search(ctx context.Context, idx Indexer, q SearchQuery) ([]Release, error) {
	// Said once per indexer search, so result counts with and without the default
	// categories can be compared in the log before relying on them.
	if c.log != nil && len(q.Categories) == 0 && len(idx.Categories) == 0 {
		if cats := defaultCategories(idx, q); len(cats) > 0 {
			c.log.Info("torznab: cat filter applied", "indexer", idx.Name, "media", q.MediaType, "cat", joinInts(cats))
		}
	}
	if name, _ := idParam(idx, q); name != "" {
		releases, err := c.searchPages(ctx, idx, q)
		if err == nil && len(releases) > 0 {
			for i := range releases {
				releases[i].MatchedByID = true
			}
			return releases, nil
		}
		// A transport failure or an HTTP error says the indexer is struggling: asking it
		// again by text would only add load. Its own <error> answer ("unsupported
		// parameter") is a refusal of the id, not of the search.
		var te *TorznabError
		if err != nil && !errors.As(err, &te) {
			return nil, err
		}
		if c.log != nil {
			c.log.Info("torznab id search found nothing; searching by text", "indexer", idx.Name, "by", name, "err", err)
		}
		q = q.withoutIDs()
		if q.Text == "" {
			// Nothing to fall back to: an empty text search is the indexer's newest
			// uploads, not an answer to this one.
			return nil, err
		}
	}
	return c.searchPages(ctx, idx, q)
}

// searchPages fetches results, following Torznab pagination until it has what was asked for.
//
// Indexers commonly cap a response at their own page size regardless of the requested
// limit: asking TorrentLeech for 400 returns 35, and without paging the other 400+ matches
// are simply invisible. A show's season packs can be entirely absent for this reason while
// being plainly present on the tracker.
func (c *TorznabSearcher) searchPages(ctx context.Context, idx Indexer, q SearchQuery) ([]Release, error) {
	var all []Release
	seen := map[string]bool{}

	for page := 0; page < maxSearchPages; page++ {
		endpoint, err := buildURL(idx, "search", q)
		if err != nil {
			return nil, err
		}
		body, err := c.get(ctx, idx, endpoint)
		if err != nil {
			// A failure on a later page must not discard the pages already fetched —
			// returning nil here silently threw away every release from pages 0..N-1.
			// Only a first-page failure means the search itself failed.
			if page > 0 {
				if c.log != nil {
					c.log.Warn("torznab page failed; keeping earlier pages",
						"indexer", idx.Name, "page", page, "have", len(all), "err", err)
				}
				return all, nil
			}
			var be *BlockedError
			if errors.As(err, &be) {
				return nil, err // it already names the indexer
			}
			return nil, fmt.Errorf("indexer %q: %w", idx.Name, err)
		}
		releases, total, err := parseFeedPage(body)
		if err != nil {
			err = scrub(endpoint, err)
			if page > 0 {
				if c.log != nil {
					c.log.Warn("torznab page unparseable; keeping earlier pages",
						"indexer", idx.Name, "page", page, "have", len(all), "err", err)
				}
				return all, nil
			}
			return nil, fmt.Errorf("indexer %q: %w", idx.Name, err)
		}
		// Per-page trace. The aggregate count can't show whether paging happened, what
		// categories were asked for, or what total the indexer declared — all of which
		// decide whether a missing release was never offered or never requested.
		if c.log != nil {
			c.log.Info("torznab page", "indexer", idx.Name, "page", page,
				"offset", q.Offset, "items", len(releases), "declared_total", total,
				"url", redactKey(endpoint))
		}

		added := 0
		for _, r := range releases {
			// Dedupe across pages: some indexers repeat items when offset lands oddly.
			key := r.DownloadURL
			if key == "" {
				key = r.Title
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			r.Indexer = idx.Name
			r.Transport = idx.Transport()
			all = append(all, r)
			added++
		}

		// Stop when the page was empty, added nothing new, we have everything the indexer
		// says exists, or we've reached what the caller asked for.
		if added == 0 || len(releases) == 0 ||
			(total > 0 && len(all) >= total) ||
			(q.Limit > 0 && len(all) >= q.Limit) {
			break
		}
		q.Offset += len(releases)
	}
	if q.Limit > 0 && len(all) > q.Limit {
		all = all[:q.Limit]
	}
	return all, nil
}

// Recent fetches the newest releases (Torznab returns the RSS feed when no
// search term is given), for RSS-sync monitoring.
func (c *TorznabSearcher) Recent(ctx context.Context, idx Indexer, limit int) ([]Release, error) {
	return c.Search(ctx, idx, SearchQuery{Limit: limit})
}

// Test performs a capabilities query to verify URL + API key.
func (c *TorznabSearcher) Test(ctx context.Context, idx Indexer) error {
	_, err := c.TestCaps(ctx, idx)
	return err
}

// TestCaps asks the indexer for its capabilities and reads them. Only a real <caps>
// answer passes: an HTTP 200 web page (Prowlarr's root URL) or an <error> document (a
// wrong key) fails with what it is.
func (c *TorznabSearcher) TestCaps(ctx context.Context, idx Indexer) (Caps, error) {
	endpoint, err := buildURL(idx, "caps", SearchQuery{})
	if err != nil {
		return Caps{}, err
	}
	body, err := c.get(ctx, idx, endpoint)
	if err != nil {
		return Caps{}, err
	}
	caps, err := parseCaps(body)
	return caps, scrub(endpoint, err)
}

// get asks the indexer one API question. A non-200 answer is an HTTPStatusError, unless it
// is a block — a Cloudflare challenge, a 403 or a web page where results should be — which
// is a BlockedError, so nobody is told the key was refused when it never got that far.
// A 200 web page without Cloudflare's markers is left to the parser: that is most often a
// wrong address (Prowlarr's own page), and errWebPage says how to fix it.
func (c *TorznabSearcher) get(ctx context.Context, idx Indexer, endpoint string) ([]byte, error) {
	if err := c.queue.wait(ctx, endpoint); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, torznabRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, sanitizeErr(endpoint, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, sanitizeErr(endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, sanitizeErr(endpoint, err)
	}
	root, attrs, _, isXML := xmlRoot(body)
	isError := isXML && root == "error"
	if !isError {
		if blocked, page, gate := blockedAnswer(resp.StatusCode, resp.Header, body); blocked &&
			(resp.StatusCode != http.StatusOK || gate) {
			return nil, &BlockedError{Indexer: idx.Name, Status: resp.StatusCode, Page: page, AppGate: gate, What: "results"}
		}
	}
	if resp.StatusCode != http.StatusOK {
		he := &HTTPStatusError{Code: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
		// Some indexers send their <error> document with a 4xx: its description says why.
		if isError {
			he.Detail = redactIn(endpoint, torznabErrorFrom(attrs).Error())
		}
		return nil, he
	}
	return body, nil
}

// HTTPStatusError is an indexer's non-200 answer: "HTTP 429", or "HTTP 401: Invalid API
// Key" when the indexer said why. RetryAfter carries the server's Retry-After so the
// integration status tracker can pause the indexer for at least that long instead of
// asking again on the next search.
type HTTPStatusError struct {
	Code       int
	RetryAfter time.Duration
	Detail     string
}

func (e *HTTPStatusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("HTTP %d", e.Code)
}

// parseRetryAfter reads a Retry-After header: whole seconds or an HTTP date. Anything
// else, or a time already past, is zero.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n <= 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// torznabText applies a book edition the only way a general tracker can express it: as a
// word in the query, because its release NAMES carry "audiobook" while its categories are
// whatever the indexer decided to define. (A dedicated book tracker has a real category
// for the edition and does the opposite — see mamCatsFor.)
//
// Ebook is deliberately not appended: "ebook" rarely appears in a release name, so adding
// it would filter out the very releases it's meant to find.
func torznabText(q SearchQuery) string {
	if q.Text != "" && q.BookEdition == "audiobook" {
		return q.Text + " audiobook"
	}
	return q.Text
}

func buildURL(idx Indexer, t string, q SearchQuery) (string, error) {
	u, err := url.Parse(idx.URL)
	if err != nil {
		return "", fmt.Errorf("invalid indexer url %q: %w", idx.URL, err)
	}
	qs := u.Query()
	mode := searchMode(idx, t, q)
	qs.Set("t", mode)
	if idx.APIKey != "" {
		qs.Set("apikey", idx.APIKey)
	}
	cats := q.Categories
	if len(cats) == 0 {
		cats = idx.Categories
	}
	text := torznabText(q)
	if len(cats) == 0 && t == "search" {
		cats = defaultCategories(idx, q)
		if slices.Contains(cats, catAudiobook) {
			// The default audiobook category already says it; the word would only drop
			// the releases in it whose names don't happen to carry "audiobook".
			text = q.Text
		}
	}
	// An id the indexer takes replaces the text: q alongside it would narrow the id match
	// to releases that also carry the text, which is exactly what a foreign or re-titled
	// release doesn't. A book or music search says the same thing in its own fields.
	name, value := idParam(idx, q)
	switch {
	case t == "search" && name != "":
		qs.Set(name, value)
	case mode == "book":
		setNonEmpty(qs, "author", q.Author)
		setNonEmpty(qs, "title", q.BookTitle)
	case mode == "music":
		setNonEmpty(qs, "artist", q.Artist)
		setNonEmpty(qs, "album", q.Album)
	default:
		setNonEmpty(qs, "q", text)
	}
	if len(cats) > 0 {
		qs.Set("cat", joinInts(cats))
	}
	// tvsearch takes the season/episode directly. This is what surfaces season packs: a
	// bare q= returns one capped page of general matches — 35, in the case that prompted
	// this — whereas season=7 asks the indexer for that season and gets its packs.
	if q.Season > 0 {
		qs.Set("season", strconv.Itoa(q.Season))
		if q.Episode > 0 {
			qs.Set("ep", strconv.Itoa(q.Episode))
		}
	}
	if q.Limit > 0 {
		qs.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		qs.Set("offset", strconv.Itoa(q.Offset))
	}
	u.RawQuery = qs.Encode()
	return u.String(), nil
}

// --- small helpers ---

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstURL returns the first value that is an http(s) URL — used to pick a
// release's details page (comments/guid), ignoring non-URL guids.
func firstURL(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			return v
		}
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

func parseFeedDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// sanitizeErr rewrites err so its text cannot leak the apikey embedded in the
// request URL. Transport failures (url.Error and friends) reproduce the full
// URL — apikey included — and error strings from this package end up in the
// UI's per-indexer Errors map and in warn logs, so redaction has to happen at
// the point the error is created, not just on the trace-log path.
func sanitizeErr(endpoint string, err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redactIn(endpoint, err.Error()))
}

// redactIn removes the endpoint's apikey from msg.
func redactIn(endpoint, msg string) string {
	if u, e := url.Parse(endpoint); e == nil {
		if key := u.Query().Get("apikey"); key != "" {
			msg = strings.ReplaceAll(msg, key, "REDACTED")
			if esc := url.QueryEscape(key); esc != key {
				msg = strings.ReplaceAll(msg, esc, "REDACTED")
			}
		}
	}
	return msg
}

// scrub is sanitizeErr for an answer the indexer sent: a TorznabError keeps its type (and
// so its description as the text) with any echoed key removed.
func scrub(endpoint string, err error) error {
	var te *TorznabError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &te):
		return &TorznabError{Code: te.Code, Description: redactIn(endpoint, te.Description)}
	case redactIn(endpoint, err.Error()) == err.Error():
		return err // nothing to hide; keep its identity (errWebPage)
	}
	return sanitizeErr(endpoint, err)
}

// redactKey strips the apikey from a URL so it can be logged safely.
func redactKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	qs := u.Query()
	if qs.Get("apikey") != "" {
		qs.Set("apikey", "REDACTED")
	}
	u.RawQuery = qs.Encode()
	return u.String()
}

// idParam is the one id parameter a search sends instead of q, and its value: only for a
// movie or TV search, only when the indexer's stored caps say that kind of search is
// available and lists the parameter, and only one, best first — imdbid then tmdbid for a
// movie, tvdbid then imdbid for a show. One id per request is what every indexer
// understands; some read several as "all of these" and some as "any". No stored caps, or
// no id the indexer takes, is "" and the search is by text exactly as it always was.
func idParam(idx Indexer, q SearchQuery) (name, value string) {
	caps, ok := idx.StoredCaps()
	if !ok {
		return "", ""
	}
	// Radarr's form: the digits without "tt", leading zeros kept.
	imdb := strings.TrimPrefix(NormalizeIMDB(q.IMDBID), "tt")
	switch searchType("search", q) {
	case "movie":
		switch {
		case imdb != "" && caps.Movie.Takes("imdbid"):
			return "imdbid", imdb
		case q.TMDBID > 0 && caps.Movie.Takes("tmdbid"):
			return "tmdbid", strconv.Itoa(q.TMDBID)
		}
	case "tvsearch":
		switch {
		case q.TVDBID > 0 && caps.TV.Takes("tvdbid"):
			return "tvdbid", strconv.Itoa(q.TVDBID)
		case imdb != "" && caps.TV.Takes("imdbid"):
			return "imdbid", imdb
		}
	}
	return "", ""
}

// setNonEmpty sets a URL parameter only when there's something to say.
func setNonEmpty(qs url.Values, name, value string) {
	if value != "" {
		qs.Set(name, value)
	}
}

// searchMode is the Torznab function a query uses on this indexer: searchType's, except
// that a book or music search whose parts the indexer's stored caps say its book-search
// (music-search) takes uses t=book (t=music). Every part the query has must be one the
// mode lists — an author dropped because the indexer can't take it would widen the
// search to every book of that title — so anything less stays today's t=search&q=….
func searchMode(idx Indexer, base string, q SearchQuery) string {
	t := searchType(base, q)
	if base != "search" {
		return t
	}
	caps, ok := idx.StoredCaps()
	if !ok {
		return t
	}
	takesAll := func(m SearchMode, parts map[string]string) bool {
		some := false
		for param, v := range parts {
			if v == "" {
				continue
			}
			if !m.Takes(param) {
				return false
			}
			some = true
		}
		return some
	}
	switch q.MediaType {
	case MediaBook:
		if takesAll(caps.Book, map[string]string{"author": q.Author, "title": q.BookTitle}) {
			return "book"
		}
	case MediaMusic:
		parts := map[string]string{"artist": q.Artist, "album": q.Album}
		// Older indexers call it audio-search; the function is t=music either way.
		if takesAll(caps.Music, parts) || (!caps.Music.Available && takesAll(caps.Audio, parts)) {
			return "music"
		}
	}
	return t
}

// Default Newznab categories per kind of search (defaultCategories).
const (
	catMovies    = 2000
	catAudio     = 3000
	catAudiobook = 3030
	catTV        = 5000
	catBooks     = 7000
	catEbook     = 7020
)

// defaultCategories are the categories a search asks an indexer for when neither the
// query nor the indexer names any — a Prowlarr-synced row has none, so a book search went
// to every category and could come back with films. Only for an indexer whose stored caps
// list categories, only those it lists (a category it doesn't have would match nothing),
// and only while the indexer_default_categories setting is on. Nothing in common, no caps,
// or the setting off is no cat at all: today's URL.
func defaultCategories(idx Indexer, q SearchQuery) []int {
	if !q.defaultCats {
		return nil
	}
	caps, ok := idx.StoredCaps()
	if !ok || len(caps.Categories) == 0 {
		return nil
	}
	var want []int
	switch q.MediaType {
	case MediaMovie:
		want = []int{catMovies}
	case MediaSeries:
		want = []int{catTV}
	case MediaMusic:
		want = []int{catAudio}
	case MediaBook:
		switch q.BookEdition {
		case "ebook":
			want = []int{catBooks, catEbook}
		case "audiobook":
			want = []int{catAudiobook}
		default:
			want = []int{catBooks, catEbook, catAudiobook}
		}
	}
	var out []int
	for _, c := range want {
		for _, have := range caps.Categories {
			if have == c {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// searchType maps a query to the right Torznab endpoint. The generic "search" works
// everywhere but ignores season/episode, so TV queries use tvsearch and movie queries use
// movie — both of which indexers implement with better matching than a plain text search.
func searchType(base string, q SearchQuery) string {
	if base != "search" {
		return base // caps, or an explicit override
	}
	switch q.MediaType {
	case MediaSeries:
		return "tvsearch"
	case MediaMovie:
		return "movie"
	}
	return "search"
}
