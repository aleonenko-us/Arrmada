package applog

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// Scrubbing for log text that leaves the machine (the support bundle). Capture already
// withholds attrs whose key names a secret and secret query parameters (redact.go); these
// passes also catch what older versions wrote, secrets inside free text, and names —
// books, people — that capture deliberately keeps for the admin's own Logs page.

var (
	// key=value / key: value where the key names a secret, in free text or attrs.
	secretPair = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.\-]*(?:apikey|api_key|api-key|token|password|passwd|secret|authorization|cookie|session_id|sessionid|private)[A-Za-z0-9_.\-]*)(\s*[=:]\s*)("[^"]*"|[^\s&",]+)`)
	// The JSON spelling: "password":"…".
	secretJSON = regexp.MustCompile(`(?i)("[A-Za-z0-9_.\-]*(?:apikey|api_key|token|password|passwd|secret|authorization|cookie|session_id|sessionid|private)[A-Za-z0-9_.\-]*"\s*:\s*)"[^"]*"`)
	// An Authorization header's credentials.
	bearer = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`)
	// user:password@ inside a URL.
	userinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*://)[^/\s:@]+:[^/\s@]+@`)
	// Email addresses: account names, Kindle addresses.
	email = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
)

// RedactText takes secrets out of free text: values of secret-named keys and query
// parameters, Authorization credentials, passwords in URLs, and email addresses.
func RedactText(s string) string {
	s = scrubQuery(s)
	s = bearer.ReplaceAllString(s, "${1} "+redacted)
	s = secretPair.ReplaceAllStringFunc(s, func(m string) string {
		sub := secretPair.FindStringSubmatch(m)
		switch v := strings.ToLower(strings.Trim(sub[3], `"`)); v {
		case strings.ToLower(redacted), "", "true", "false", "bearer", "basic":
			// Nothing secret in a yes/no; already-redacted stays as it is; an
			// Authorization scheme's credentials were blanked just above.
			return m
		}
		return sub[1] + sub[2] + redacted
	})
	s = secretJSON.ReplaceAllString(s, `${1}"`+redacted+`"`)
	s = userinfo.ReplaceAllString(s, "${1}"+redacted+"@")
	return email.ReplaceAllString(s, "«email»")
}

// attrKey finds where each key=value pair of an Entry's attrs starts.
var attrKey = regexp.MustCompile(`(?:^|\s)([A-Za-z_][A-Za-z0-9_.\-]*)=`)

// RewriteAttrs passes every key=value pair of an Entry's attrs ("k=v k2=v2", values
// unquoted and possibly holding spaces) through fn, which returns the new value. A value
// runs to the next " key=", so a value that itself contains " word=" is split there —
// both halves still go through fn.
func RewriteAttrs(attrs string, fn func(key, val string) string) string {
	locs := attrKey.FindAllStringSubmatchIndex(attrs, -1)
	if len(locs) == 0 {
		return attrs
	}
	var b strings.Builder
	b.Grow(len(attrs))
	b.WriteString(attrs[:locs[0][2]])
	for i, loc := range locs {
		key := attrs[loc[2]:loc[3]]
		valStart := loc[1]
		valEnd := len(attrs)
		if i+1 < len(locs) {
			valEnd = locs[i+1][0]
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(fn(key, attrs[valStart:valEnd]))
		if i+1 < len(locs) {
			// Keep the separator before the next key.
			b.WriteString(attrs[valEnd:locs[i+1][2]])
		}
	}
	return b.String()
}

// TermScrubber replaces whole-word occurrences of known names — book titles, authors,
// people — in log text. Matching is by words, folded the way every title matcher folds
// (parser.TitleWords), so "Dune Messiah", "dune.messiah" and "DUNE_MESSIAH" are all the
// same name, and "Dunes" is not "Dune".
type TermScrubber struct {
	index map[string][]term // first word → terms starting with it, longest first
	n     int
}

type term struct {
	words []string
	repl  string
}

// minTermLen skips names too short to replace without wrecking ordinary words ("It",
// "Us"): they'd blank every "it" in the log, and alone they say nothing about anyone.
const minTermLen = 3

// NewTermScrubber returns an empty scrubber.
func NewTermScrubber() *TermScrubber { return &TermScrubber{index: map[string][]term{}} }

// Add registers a name to replace with repl.
func (s *TermScrubber) Add(name, repl string) {
	words := parser.TitleWords(name)
	if len(words) == 0 || len(strings.Join(words, "")) < minTermLen {
		return
	}
	s.index[words[0]] = append(s.index[words[0]], term{words: words, repl: repl})
	s.n++
}

// Len is how many names are registered.
func (s *TermScrubber) Len() int { return s.n }

// Seal orders each word's candidates longest first, so "Dune Messiah" wins over "Dune".
// Call it after the last Add.
func (s *TermScrubber) Seal() {
	for k, ts := range s.index {
		sort.SliceStable(ts, func(i, j int) bool { return len(ts[i].words) > len(ts[j].words) })
		s.index[k] = ts
	}
}

type span struct {
	word       string
	start, end int
}

// Scrub returns text with every registered name replaced.
func (s *TermScrubber) Scrub(text string) string {
	if s == nil || s.n == 0 || text == "" {
		return text
	}
	words := splitWords(text)
	var b strings.Builder
	last := 0
	for i := 0; i < len(words); i++ {
		cands := s.index[words[i].word]
		if len(cands) == 0 {
			continue
		}
		for _, t := range cands {
			if i+len(t.words) > len(words) {
				continue
			}
			ok := true
			for k, w := range t.words {
				if words[i+k].word != w {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			if b.Len() == 0 {
				b.Grow(len(text))
			}
			b.WriteString(text[last:words[i].start])
			b.WriteString(t.repl)
			last = words[i+len(t.words)-1].end
			i += len(t.words) - 1
			break
		}
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// splitWords cuts text into runs of letters and digits, folded as parser.TitleWords folds
// a title, with their byte offsets in text. "and" is skipped the way TitleWords drops it
// ("Love & Death" and "Love and Death" are one title), so it still sits inside a match.
func splitWords(text string) []span {
	var out []span
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		raw := text[start:end]
		w := strings.ToLower(raw)
		if !isASCII(raw) {
			w = parser.FoldTitle(raw)
		}
		if w != "and" {
			out = append(out, span{word: w, start: start, end: end})
		}
		start = -1
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
		i += size
	}
	flush(len(text))
	return out
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
