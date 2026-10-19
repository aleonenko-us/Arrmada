package library

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The naming engine that book and music naming share: a template is text with {Tokens} in
// it, "/" starts a folder, "<…>" is an optional part written only when every token inside
// it has a value, and "<A|B>" writes A when it can and B otherwise. Each scheme brings its
// own tokens (a tokenVocab) and fills them (a tokenValuer); parsing, the optional parts,
// tidying what empty tokens leave behind, cleaning values and the length limit are the
// same for every scheme. Lifted from the book naming (issue #25) unchanged, so books name
// exactly as before.

// tokenVocab is one scheme's tokens.
type tokenVocab struct {
	// spellings maps a token's name, folded (foldTokenName), to its canonical name.
	spellings map[string]string
	// tokens lists the canonical names, for the "isn't a token" message.
	tokens []string
	// paddable are the tokens "{Name:00}" may zero-pad; padHelp says which, in words.
	paddable map[string]bool
	padHelp  string
}

// tokenValuer fills tokens for one render. top says the token is outside every optional
// part, where a scheme may give a fallback rather than nothing ("Unknown Author").
type tokenValuer interface {
	value(n bnNode, top bool) string
}

// bnNode is one piece of a parsed template: literal text, a token, or an optional group
// of alternatives.
type bnNode struct {
	lit   string
	tok   string
	pad   int
	group bool
	alts  [][]bnNode
}

// parseTemplate parses a template in vocabulary v, or says what's wrong with it in words
// the settings page can show.
func parseTemplate(s string, v tokenVocab) ([]bnNode, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("a name template can't be empty")
	}
	if utf8.RuneCountInString(s) > 500 {
		return nil, errors.New("a name template can be at most 500 characters")
	}
	p := &bnParser{s: []rune(s), v: v}
	alts, err := p.seq(0)
	if err != nil {
		return nil, err
	}
	return alts[0], nil
}

type bnParser struct {
	s []rune
	i int
	v tokenVocab
}

// seq reads nodes up to the end (depth 0) or the ">" closing a group, splitting a group's
// alternatives at "|".
func (p *bnParser) seq(depth int) ([][]bnNode, error) {
	var alts [][]bnNode
	var cur []bnNode
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			cur = append(cur, bnNode{lit: lit.String()})
			lit.Reset()
		}
	}
	for p.i < len(p.s) {
		switch c := p.s[p.i]; c {
		case '{':
			flush()
			end := -1
			for j := p.i + 1; j < len(p.s); j++ {
				if p.s[j] == '{' {
					return nil, errors.New("tokens can't go inside other tokens")
				}
				if p.s[j] == '}' {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, errors.New("a { has no closing }")
			}
			n, err := parseToken(string(p.s[p.i+1:end]), p.v)
			if err != nil {
				return nil, err
			}
			cur = append(cur, n)
			p.i = end + 1
		case '}':
			return nil, errors.New("a } has no opening {")
		case '<':
			flush()
			p.i++
			g, err := p.seq(depth + 1)
			if err != nil {
				return nil, err
			}
			cur = append(cur, bnNode{group: true, alts: g})
		case '>':
			if depth == 0 {
				return nil, errors.New("a > has no opening <")
			}
			flush()
			p.i++
			return append(alts, cur), nil
		case '|':
			if depth == 0 {
				return nil, errors.New("| only works inside < >, between alternatives")
			}
			flush()
			alts = append(alts, cur)
			cur = nil
			p.i++
		default:
			lit.WriteRune(c)
			p.i++
		}
	}
	if depth > 0 {
		return nil, errors.New("a < has no closing >")
	}
	flush()
	return [][]bnNode{cur}, nil
}

// parseToken reads "Name" or "Name:00" (zero-padded to that many digits).
func parseToken(inner string, v tokenVocab) (bnNode, error) {
	name, format, hasFormat := strings.Cut(inner, ":")
	canon, ok := v.spellings[foldTokenName(name)]
	if !ok {
		if strings.TrimSpace(name) == "" {
			return bnNode{}, errors.New("{} needs a token name inside it")
		}
		return bnNode{}, fmt.Errorf("{%s} isn't a token — use one of {%s}", strings.TrimSpace(name), strings.Join(v.tokens, "}, {"))
	}
	n := bnNode{tok: canon}
	if hasFormat {
		format = strings.TrimSpace(format)
		if !v.paddable[canon] {
			return bnNode{}, fmt.Errorf("only %s can be zero-padded, not {%s}", v.padHelp, canon)
		}
		if format == "" || len(format) > 6 || strings.Trim(format, "0") != "" {
			return bnNode{}, fmt.Errorf("{%s:%s}: write the padding as zeros, like {%s:00}", canon, format, canon)
		}
		n.pad = len(format)
	}
	return n, nil
}

func foldTokenName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == ' ' || r == '_' || r == '-' || r == '.' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// walkTokens calls fn for every token in nodes, at any depth; top says whether it's
// outside every group.
func walkTokens(nodes []bnNode, top bool, fn func(n bnNode, top bool)) {
	for _, n := range nodes {
		if n.group {
			for _, alt := range n.alts {
				walkTokens(alt, false, fn)
			}
			continue
		}
		if n.tok != "" {
			fn(n, top)
		}
	}
}

func templateHasToken(nodes []bnNode, tok string) bool {
	found := false
	walkTokens(nodes, true, func(n bnNode, _ bool) {
		if n.tok == tok {
			found = true
		}
	})
	return found
}

// hasSlash reports whether nodes write a "/" (or "\") anywhere.
func hasSlash(nodes []bnNode) bool {
	for _, n := range nodes {
		if n.group {
			for _, alt := range n.alts {
				if hasSlash(alt) {
					return true
				}
			}
		} else if n.tok == "" && strings.ContainsAny(n.lit, `/\`) {
			return true
		}
	}
	return false
}

// cleanValue is clean, and nothing at all for a value of only dots: "." and ".." name no
// folder (and ".." would climb out of the library), so they count as empty.
func cleanValue(s string) string {
	v := clean(s)
	if strings.Trim(v, ". ") == "" {
		return ""
	}
	return v
}

// sentinel marks where a token's value goes while the separators around it are tidied:
// the private-use runes can't be in a template (they'd be removed as illegal) and the
// tidying never touches them, so a title is never trimmed or rewritten — only the
// template's own text around it.
const sentinelBase = 0xE000

// renderTemplate renders nodes into path segments (one for a file template).
func renderTemplate(nodes []bnNode, r tokenValuer) []string {
	var sk strings.Builder
	var vals []string
	var emit func(nodes []bnNode, top bool)
	emit = func(nodes []bnNode, top bool) {
		for _, n := range nodes {
			switch {
			case n.group:
				for _, alt := range n.alts {
					if altFilled(alt, r) {
						emit(alt, false)
						break
					}
				}
			case n.tok != "":
				if v := r.value(n, top); v != "" && len(vals) < 0x1800 {
					sk.WriteRune(rune(sentinelBase + len(vals)))
					vals = append(vals, v)
				}
			default:
				// A private-use character typed into the template would read as a value's
				// marker below; it can't be in a file name usefully anyway.
				sk.WriteString(strings.Map(func(c rune) rune {
					if c >= sentinelBase && c <= 0xF8FF {
						return -1
					}
					return c
				}, n.lit))
			}
		}
	}
	emit(nodes, true)
	var out []string
	for _, seg := range strings.FieldsFunc(sk.String(), func(c rune) bool { return c == '/' || c == '\\' }) {
		seg = tidySkeleton(seg)
		var b strings.Builder
		for _, c := range seg {
			if c >= sentinelBase && int(c-sentinelBase) < len(vals) {
				b.WriteString(vals[c-sentinelBase])
				continue
			}
			b.WriteRune(c)
		}
		s := strings.TrimSpace(b.String())
		if strings.Trim(s, ".") == "" {
			continue // "", "." and ".." name no folder (and ".." would leave the library)
		}
		out = append(out, s)
	}
	return out
}

// altFilled reports whether every token directly in an alternative has a value. Optional
// parts nested inside it don't count: they drop out on their own.
func altFilled(alt []bnNode, r tokenValuer) bool {
	for _, n := range alt {
		if n.tok != "" && r.value(n, false) == "" {
			return false
		}
	}
	return true
}

var (
	reEmptyBrackets = regexp.MustCompile(`\(\s*\)|\[\s*\]`)
	// The characters an empty token leaves stranded at either end of a name.
	danglingSeps = " -_.,;:#&+~–—"
)

// tidySkeleton drops what empty tokens left behind in one segment of the template's own
// text: characters a file name can't have, empty brackets, doubled separators, and
// separators left at either end.
func tidySkeleton(seg string) string {
	seg = reIllegal.ReplaceAllString(seg, "")
	for {
		before := seg
		seg = reEmptyBrackets.ReplaceAllString(seg, "")
		seg = strings.Join(strings.Fields(seg), " ")
		for strings.Contains(seg, "- -") {
			seg = strings.ReplaceAll(seg, "- -", "-")
		}
		seg = strings.Trim(seg, danglingSeps)
		if seg == before {
			return seg
		}
	}
}

// maxNameBytes is the 255-byte limit filesystems put on one folder or file name. Only a
// name over it is shortened (a file's together with its extension): such a name could
// never have been created, so every name that worked before renders exactly as it did.
const maxNameBytes = 255

// truncateName shortens a name to at most n bytes on a character boundary.
func truncateName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if i > n {
			break
		}
		cut = i
	}
	return strings.TrimRight(s[:cut], " .")
}
