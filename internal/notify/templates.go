package notify

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Message templates. Every alert's text is a template: by default "{message}\n{link}" —
// the catalog's own wording, then a link that opens the item in Arrmada when the outside
// address is set (Settings → System). A connection can override any event's template
// with its own words ("🎬 {title} ({year}) is ready — {link}").
//
// The values come from outside: a Plex user names themselves, a requester's account name
// can come from Plex, titles come from TMDB and Open Library. So each value is made safe
// before it's put in: no line breaks or control characters (a name can't add lines), no
// Discord mass mentions or mention syntax, no markdown link that dresses up a URL, and a
// length cap. Rendering is one pass, so a title that contains "{link}" stays text.

// DefaultTemplate is the text an event gets when its connection has no template of its own.
const DefaultTemplate = "{message}\n{link}"

// Placeholders are the names a template may use, in the order the Alerts page lists them.
var Placeholders = []string{"message", "title", "user", "year", "media", "link"}

const (
	maxTemplateLen = 1000
	maxValueRunes  = 200
)

var placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// render fills a template's placeholders from vars in one pass; an unknown placeholder is
// left blank. Spaces at line ends and empty trailing lines that blank values leave behind
// are tidied away.
func render(tmpl string, vars map[string]string) string {
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		return vars[m[1:len(m)-1]]
	})
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// neutralizer breaks the sequences chat services act on, with a zero-width space:
// @everyone and @here ping a whole Discord server, "<@" starts a user or role mention,
// and "](" makes markdown show one URL under another's text.
var neutralizer = strings.NewReplacer(
	"@everyone", "@​everyone",
	"@here", "@​here",
	"<@", "<​@",
	"](", "]​(",
)

// neutralize makes text safe to send without changing how it reads.
func neutralize(s string) string { return neutralizer.Replace(s) }

// cleanValue makes one outside value safe for a template: control characters and line
// breaks become spaces, runs of space collapse, it's neutralized and capped in length.
func cleanValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
	s = neutralize(strings.Join(strings.Fields(s), " "))
	if utf8.RuneCountInString(s) > maxValueRunes {
		s = string([]rune(s)[:maxValueRunes-1]) + "…"
	}
	return s
}

// templateVars are the placeholder values for one event: data is its payload (nil when
// the producer sent a ready message) and link the absolute link ("" when there's none).
func templateVars(key string, data map[string]any, m Message, link string) map[string]string {
	vars := map[string]string{
		"message": neutralize(m.Body),
		"title":   cleanValue(firstOf(str(data, "title"), str(data, "album"))),
		"user":    cleanValue(firstOf(str(data, "user"), str(data, "requested_by"))),
		"media":   cleanValue(mediaWord(key, data)),
		"link":    link,
	}
	if y := num(data, "year"); y > 0 {
		vars["year"] = strconv.FormatInt(y, 10)
	}
	return vars
}

// mediaWord is what kind of thing an event is about, in a word.
func mediaWord(key string, d map[string]any) string {
	switch key {
	case "movie.imported":
		return "movie"
	case "episodes.imported":
		return "show"
	case "book.imported":
		if str(d, "edition") == "audiobook" {
			return "audiobook"
		}
		return "ebook"
	case "music.imported":
		return "album"
	}
	switch t := firstOf(str(d, "media_type"), str(d, "type")); t {
	case "series":
		return "show"
	default:
		return t
	}
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// absoluteLink is the in-app path rel ("/movies/12") as a link from outside, or "" when no
// outside address is set or rel isn't an in-app path.
func (s *Service) absoluteLink(ctx context.Context, rel string) string {
	if rel == "" || !strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "//") || strings.ContainsAny(rel, " \t\r\n\\") {
		return ""
	}
	base := s.publicURL(ctx)
	if base == "" {
		return ""
	}
	return base + rel
}

// bodyFor is the text one connection gets for an event: its own template for the event
// if it has one, else the default. A push connection opens the item when tapped, so its
// text carries no link. An override that renders to nothing falls back to the catalog's
// wording rather than sending an empty message.
func bodyFor(kind string, opts Options, key string, vars map[string]string, fallback string) string {
	if kind == KindN8N {
		// A webhook gets structured JSON; the row's body is only what its delivery log
		// shows, so it's the catalog's own words, with no template or link.
		return neutralize(fallback)
	}
	tmpl := DefaultTemplate
	if t := opts.Templates[key]; strings.TrimSpace(t) != "" {
		tmpl = t
	}
	if kind == KindWebPush {
		v := make(map[string]string, len(vars))
		for k, val := range vars {
			v[k] = val
		}
		v["link"] = ""
		vars = v
	}
	if out := render(tmpl, vars); strings.TrimSpace(out) != "" {
		return out
	}
	return neutralize(fallback)
}

// cleanTemplates checks a connection's template overrides: known events only, a length
// cap, no control characters but line breaks and tabs. Blank ones are dropped.
func cleanTemplates(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for key, t := range in {
		if _, ok := Lookup(key); !ok {
			return nil, fmt.Errorf("unknown alert event %q", key)
		}
		t = strings.ReplaceAll(t, "\r\n", "\n")
		if strings.TrimSpace(t) == "" {
			continue
		}
		if len(t) > maxTemplateLen {
			return nil, fmt.Errorf("a message template can be at most %d characters", maxTemplateLen)
		}
		if strings.ContainsFunc(t, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
			return nil, errors.New("a message template can't contain control characters")
		}
		out[key] = t
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
