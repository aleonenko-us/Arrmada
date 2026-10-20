package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/netutil"
)

// The personal calendar feed: the Calendar as an iCal feed that Google or Apple Calendar
// subscribes to. Those services fetch it on their own, without a session — Google from
// the internet — so the feed is the one route a link alone opens:
//
//   - The link carries a 256-bit random token (auth/calendarfeed.go). Only its hash is
//     stored; the person sees the link once, when they make it, and can make a new one
//     (the old stops at once) or turn it off.
//   - The token opens this feed and nothing else, and the feed shows what that person's
//     own Calendar shows them — episodes and film releases in the library (album releases
//     too for staff while Music is on), the adult filter applied — never anything about
//     audiobooks.
//   - It's reachable from outside the network (calendar services fetch from there) and
//     rate-limited per address. A wrong, old or turned-off token, or a disabled account,
//     all get the same 404, so it says nothing about who exists.
//   - The request log records the route pattern only (logRequests), never the query
//     string that carries the token.
//
// Making and turning off the link are ordinary signed-in routes, LAN-only for requesters
// like the Calendar itself.

const (
	icsDaysBack  = 30
	icsDaysAhead = 120
	// icsPerMinute is how often one address may fetch a feed. A calendar app polls a few
	// times an hour; this only stops a flood.
	icsPerMinute = 60
)

// calendarFeedView is GET /me/calendar-feed: whether the person has a link (never the
// link itself — it isn't stored).
type calendarFeedView struct {
	auth.CalendarFeed
	// Mine is what a new link shows by default: only the person's own requests for a
	// requester, everything for staff.
	Mine bool `json:"mine"`
}

// feedMineDefault is whether a new feed shows only the person's requests.
func feedMineDefault(u *auth.User) bool { return !u.Role.AtLeast(auth.RoleManager) }

// handleMyCalendarFeed says whether the signed-in person has a feed link.
func (a *api) handleMyCalendarFeed(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	f, err := a.deps.Auth.CalendarFeedFor(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the calendar feed")
		return
	}
	a.writeJSON(w, http.StatusOK, calendarFeedView{CalendarFeed: f, Mine: feedMineDefault(u)})
}

// handleRotateCalendarFeed makes the signed-in person a new feed link, replacing any
// earlier one, and answers with it — the only time it's shown.
func (a *api) handleRotateCalendarFeed(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	token, f, err := a.deps.Auth.RotateCalendarFeed(r.Context(), u.ID)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not make a calendar feed link")
		return
	}
	mine := feedMineDefault(u)
	link := a.feedURL(r, token, mine)
	a.writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "created_at": f.CreatedAt, "mine": mine,
		"url": link, "webcal_url": webcalURL(link),
		// Whether the link uses the outside address (Settings → System): Google Calendar
		// can only fetch a link that reaches the server from the internet.
		"outside": a.deps.Settings != nil && a.deps.Settings.PublicURL(r.Context()) != "",
	})
}

// handleRevokeCalendarFeed turns the signed-in person's feed link off.
func (a *api) handleRevokeCalendarFeed(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r)
	if !ok || u == nil {
		a.writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := a.deps.Auth.RevokeCalendarFeed(r.Context(), u.ID); err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not turn the calendar feed off")
		return
	}
	a.writeJSON(w, http.StatusOK, calendarFeedView{Mine: feedMineDefault(u)})
}

// feedURL is the https (or http) feed link: at the outside address when one is set, else
// at the address this request reached.
func (a *api) feedURL(r *http.Request, token string, mine bool) string {
	base := ""
	if a.deps.Settings != nil {
		base = a.deps.Settings.PublicURL(r.Context())
	}
	if base == "" {
		base = baseURL(r)
	}
	q := url.Values{}
	q.Set("token", token)
	if mine {
		q.Set("mine", "1")
	} else {
		q.Set("mine", "0")
	}
	return base + "/api/v1/calendar.ics?" + q.Encode()
}

// webcalURL is the same link as webcal://, which Apple Calendar and most desktop apps
// open as a subscription.
func webcalURL(link string) string {
	if rest, ok := strings.CutPrefix(link, "https://"); ok {
		return "webcal://" + rest
	}
	return "webcal://" + strings.TrimPrefix(link, "http://")
}

// handleCalendarICS serves GET /api/v1/calendar.ics?token=…&mine=0|1.
func (a *api) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	if l := a.icsLimiter; l != nil {
		if ok, retry := l.allow("ics:" + netutil.ClientIP(r)); !ok {
			a.tooManyAttempts(w, retry)
			return
		}
	}
	q := r.URL.Query()
	token := q.Get("token")
	if token == "" {
		a.writeError(w, http.StatusBadRequest, "this link is missing its token")
		return
	}
	u, err := a.deps.Auth.UserByCalendarToken(r.Context(), token)
	if errors.Is(err, auth.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "no such calendar feed")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the calendar feed")
		return
	}
	mine := feedMineDefault(u)
	switch q.Get("mine") {
	case "1":
		mine = true
	case "0":
		mine = false
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	now := time.Now()
	items := a.calendarItems(ctx, now.AddDate(0, 0, -icsDaysBack).Format("2006-01-02"), now.AddDate(0, 0, icsDaysAhead).Format("2006-01-02"),
		a.calendarMusicFor(ctx, u))
	items = a.markRequestedBy(ctx, items, u.ID, mine)
	kept := items[:0]
	for _, it := range items {
		if !calendarAdult(it) {
			kept = append(kept, it)
		}
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	// private: the answer belongs to one link's owner, so no shared cache may keep it.
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("Content-Disposition", `inline; filename="arrmada.ics"`)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	_ = writeICS(w, kept, a.icsHost(r), now)
}

// icsHost is the domain part of each event's UID: the outside address's host when one is
// set, else the host this request reached. A subscription always fetches the same link,
// so its UIDs stay the same from one fetch to the next.
func (a *api) icsHost(r *http.Request) string {
	host := ""
	if a.deps.Settings != nil {
		if pub := a.deps.Settings.PublicURL(r.Context()); pub != "" {
			if u, err := url.Parse(pub); err == nil {
				host = u.Hostname()
			}
		}
	}
	if host == "" {
		host = r.Host
		if h, _, ok := strings.Cut(host, ":"); ok && !strings.Contains(h, "[") {
			host = h
		}
	}
	host = strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' {
			return c
		}
		return -1
	}, host)
	if host == "" {
		return "arrmada"
	}
	return strings.ToLower(host)
}

// writeICS writes items as an iCalendar (RFC 5545) document: all-day events with stable
// UIDs, CRLF line ends, text escaped and long lines folded at 75 octets without
// splitting a character.
func writeICS(w io.Writer, items []CalendarItem, host string, now time.Time) error {
	var b strings.Builder
	line := func(s string) { foldLine(&b, s) }
	stamp := now.UTC().Format("20060102T150405Z")
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Arrmada//Calendar//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:Arrmada")
	line("X-PUBLISHED-TTL:PT1H")
	for _, it := range items {
		day, err := time.Parse("2006-01-02", it.Date)
		if err != nil {
			continue
		}
		var uid, summary string
		switch it.Type {
		case "episode":
			uid = "arrmada-ep-" + strconv.FormatInt(it.RefID, 10) + "-" + strconv.Itoa(it.Season) + "x" + strconv.Itoa(it.Episode) + "@" + host
			summary = it.Title + " — S" + pad2(it.Season) + "E" + pad2(it.Episode)
			if it.EpisodeTitle != "" {
				summary += " · " + it.EpisodeTitle
			}
		case "album":
			uid = "arrmada-album-" + strconv.FormatInt(it.RefID, 10) + "@" + host
			summary = it.Title
			if it.Artist != "" {
				summary += " — " + it.Artist
			}
		default:
			uid = "arrmada-movie-" + strconv.FormatInt(it.RefID, 10) + "@" + host
			summary = it.Title
			if it.Year > 0 {
				summary += " (" + strconv.Itoa(it.Year) + ")"
			}
		}
		line("BEGIN:VEVENT")
		line("UID:" + icsEscape(uid))
		line("DTSTAMP:" + stamp)
		line("DTSTART;VALUE=DATE:" + day.Format("20060102"))
		line("DTEND;VALUE=DATE:" + day.AddDate(0, 0, 1).Format("20060102"))
		line("SUMMARY:" + icsEscape(summary))
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	_, err := io.WriteString(w, b.String())
	return err
}

func pad2(n int) string {
	if n >= 0 && n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// icsEscape escapes TEXT per RFC 5545: backslash, semicolon, comma and line breaks.
// Other control characters are dropped.
func icsEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldLine writes one content line, folded so no physical line is longer than 75
// octets: each continuation starts with a space, and a fold never falls inside a
// multi-byte character.
func foldLine(b *strings.Builder, s string) {
	const maxOctets = 75
	first := true
	for len(s) > 0 {
		limit := maxOctets
		if !first {
			limit = maxOctets - 1 // the leading space counts
		}
		if len(s) <= limit {
			if !first {
				b.WriteByte(' ')
			}
			b.WriteString(s)
			break
		}
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n")
		s = s[cut:]
		first = false
	}
	b.WriteString("\r\n")
}
