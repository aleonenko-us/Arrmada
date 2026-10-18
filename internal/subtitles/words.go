package subtitles

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Word-timed cues from whisper's full JSON output (-ojf) with DTW token timestamps
// (-dtw). Each token carries t_dtw, "the moment in audio in which the token was
// output" — in practice the token's end. A word ends at its last token's t_dtw and
// starts where the previous word ended. Cues are then built from words the way a
// subtitler would: broken at pauses and sentence ends, never past two lines, held on
// screen long enough to read. Segment start/end timestamps — the ones that put a cue
// up early after a pause — are only used as a bound.

type wsJSON struct {
	Transcription []wsSegment `json:"transcription"`
}

type wsSegment struct {
	Offsets wsOffsets `json:"offsets"`
	Text    string    `json:"text"`
	Tokens  []wsToken `json:"tokens"`
}

type wsToken struct {
	Text    string    `json:"text"`
	Offsets wsOffsets `json:"offsets"`
	TDTW    int64     `json:"t_dtw"`
	P       float64   `json:"p"`

	// raw is the token's bytes exactly as whisper wrote them. Whisper's tokens are byte
	// pieces, and a Chinese or Japanese character is often split across two or three of
	// them; Text has each piece replaced by U+FFFD, raw keeps the bytes so the pieces can
	// be put back together.
	raw string
}

func (t *wsToken) UnmarshalJSON(b []byte) error {
	type plain wsToken
	var v struct {
		plain
		RawText json.RawMessage `json:"text"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*t = wsToken(v.plain)
	if len(v.RawText) > 0 {
		if err := json.Unmarshal(v.RawText, &t.Text); err != nil {
			return err
		}
		t.raw, _ = rawJSONString(v.RawText)
	}
	return nil
}

// rawJSONString unquotes a JSON string without replacing invalid UTF-8 — the one thing
// encoding/json won't do.
func rawJSONString(b []byte) (string, bool) {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return "", false
	}
	b = b[1 : len(b)-1]
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			out = append(out, b[i])
			continue
		}
		if i++; i >= len(b) {
			return "", false
		}
		switch b[i] {
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			if i+4 >= len(b) {
				return "", false
			}
			n, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
			if err != nil {
				return "", false
			}
			r := rune(n)
			i += 4
			if utf16.IsSurrogate(r) && i+6 < len(b) && b[i+1] == '\\' && b[i+2] == 'u' {
				if lo, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16); err == nil {
					if pair := utf16.DecodeRune(r, rune(lo)); pair != utf8.RuneError {
						r = pair
						i += 6
					}
				}
			}
			out = utf8.AppendRune(out, r)
		default: // \" \\ \/
			out = append(out, b[i])
		}
	}
	return string(out), true
}

type wsOffsets struct {
	From int64 `json:"from"` // ms
	To   int64 `json:"to"`   // ms
}

type word struct {
	start, end time.Duration
	text       string
}

const (
	wordMaxDur    = 800 * time.Millisecond  // bound on a first word's length when only its end is known
	cuePauseBreak = 1200 * time.Millisecond // a pause this long ends a cue
	cueSentGap    = 300 * time.Millisecond  // a sentence end plus this much pause ends a cue
	cueSentChars  = 40                      // ...once the cue has this much text
	cueEndPad     = 250 * time.Millisecond  // hold a cue a beat past its last word
	cueReadCPS    = 20                      // characters per second a viewer can read
)

// wordsFromJSON extracts timed words from whisper's JSON, adding base (the chunk's
// start) to every time. Segments whose DTW times are missing or don't make sense fall
// back to sharing the segment's time by character count.
func wordsFromJSON(data []byte, base time.Duration) ([]word, error) {
	return wordsFromJSONIn(data, base, spacedLayout)
}

func wordsFromJSONIn(data []byte, base time.Duration, lay cueLayout) ([]word, error) {
	var js wsJSON
	if err := json.Unmarshal(data, &js); err != nil {
		return nil, fmt.Errorf("whisper json: %w", err)
	}
	var out []word
	for _, seg := range js.Transcription {
		out = append(out, segmentWords(seg, base, lay.noSpace)...)
	}
	return out, nil
}

// isSpecialToken filters whisper's control tokens ("[_BEG_]", "[_TT_123]", "<|en|>").
func isSpecialToken(t string) bool {
	t = strings.TrimSpace(t)
	return t == "" || strings.HasPrefix(t, "[_") || strings.HasPrefix(t, "<|")
}

// segmentWords groups a segment's tokens into words (a token starting with a space
// starts a word; the rest attach) and times them. Chinese and Japanese (noSpace) have no
// spaces to go by, so every Han or kana token is a word of its own, and so is the first
// Latin or digit token after one; punctuation stays on its word.
func segmentWords(seg wsSegment, base time.Duration, noSpace bool) []word {
	segFrom := base + time.Duration(seg.Offsets.From)*time.Millisecond
	segTo := base + time.Duration(seg.Offsets.To)*time.Millisecond
	type group struct {
		text    string
		lastTok int // index into seg.Tokens of the word's last token
	}
	var groups []group
	pending := "" // the first bytes of a character whisper split across tokens
	for i, t := range seg.Tokens {
		if isSpecialToken(t.Text) {
			continue
		}
		text := t.Text
		if noSpace && t.raw != "" {
			text = pending + t.raw
			if endsMidRune(text) {
				pending = text
				continue
			}
			pending = ""
			text = strings.ToValidUTF8(text, string(utf8.RuneError))
		}
		startsWord := strings.HasPrefix(text, " ") || len(groups) == 0
		txt := strings.TrimSpace(text)
		if txt == "" {
			continue
		}
		if noSpace && !startsWord {
			first, _ := utf8.DecodeRuneInString(txt)
			prev, _ := utf8.DecodeLastRuneInString(groups[len(groups)-1].text)
			startsWord = isNoSpaceScript(first) || unicode.In(first, unicode.Ps, unicode.Pi) ||
				(noSpaceEdge(prev) && !unicode.IsPunct(first) && !isCJKPunct(first))
		}
		if startsWord {
			groups = append(groups, group{text: txt, lastTok: i})
		} else {
			groups[len(groups)-1].text += txt
			groups[len(groups)-1].lastTok = i
		}
	}
	if pending != "" && len(groups) > 0 {
		// The segment ended mid-character: keep what there is rather than drop it.
		groups[len(groups)-1].text += strings.ToValidUTF8(pending, string(utf8.RuneError))
	}
	if len(groups) == 0 {
		return nil
	}
	unit := dtwUnit(seg, segFrom-base, segTo-base)
	words := make([]word, 0, len(groups))
	if unit > 0 {
		prevEnd := segFrom
		for i, g := range groups {
			end := base + time.Duration(seg.Tokens[g.lastTok].TDTW)*unit
			start := prevEnd
			if i == 0 && end-start > wordMaxDur {
				// Only the end of the first word is known; whisper's segment start can
				// sit well before the speech, which is exactly the "too early" case.
				start = end - wordMaxDur
			}
			if end < start {
				end = start + 50*time.Millisecond
			}
			words = append(words, word{start: start, end: end, text: g.text})
			prevEnd = end
		}
		return words
	}
	// No usable DTW: share the segment's span by character count.
	total := 0
	for _, g := range groups {
		total += utf8.RuneCountInString(g.text) + 1
	}
	at := segFrom
	acc := 0
	for i, g := range groups {
		acc += utf8.RuneCountInString(g.text) + 1
		end := segFrom + time.Duration(float64(segTo-segFrom)*float64(acc)/float64(total))
		if i == len(groups)-1 {
			end = segTo
		}
		words = append(words, word{start: at, end: end, text: g.text})
		at = end
	}
	return words
}

// endsMidRune reports whether s stops partway through a multi-byte character.
func endsMidRune(s string) bool {
	for i := len(s) - 1; i >= 0 && i > len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			return !utf8.FullRuneInString(s[i:])
		}
	}
	return false
}

// dtwUnit works out what t_dtw is counted in for this segment — centiseconds by the
// header's word, but checked against the segment's own millisecond offsets so a build
// that changes it can't silently shift every cue 10×. Returns 0 when the values are
// missing or don't land inside the segment (±2 s), which means "don't trust them".
func dtwUnit(seg wsSegment, from, to time.Duration) time.Duration {
	const slack = 2 * time.Second
	fits := func(unit time.Duration) bool {
		n := 0
		for _, t := range seg.Tokens {
			if isSpecialToken(t.Text) {
				continue
			}
			if t.TDTW < 0 {
				return false
			}
			at := time.Duration(t.TDTW) * unit
			if at < from-slack || at > to+slack {
				return false
			}
			n++
		}
		return n > 0
	}
	if fits(10 * time.Millisecond) {
		return 10 * time.Millisecond
	}
	if fits(time.Millisecond) {
		return time.Millisecond
	}
	return 0
}

// cuesFromWords packs timed words into cues: a new cue at a pause, at a sentence end
// once there's enough text, when the next word wouldn't fit on two lines, or when the
// cue would run too long.
func cuesFromWords(words []word) []cue { return cuesFromWordsIn(words, spacedLayout) }

func cuesFromWordsIn(words []word, lay cueLayout) []cue {
	var out []cue
	var cur *cue
	var curChars int
	for _, w := range words {
		wl := utf8.RuneCountInString(w.text)
		if cur != nil {
			gap := w.start - cur.end
			// A Chinese or Japanese segment often runs several sentences, and DTW words
			// inside a segment have no gap between them, so there a sentence end closes
			// the cue without waiting for a pause.
			sentGap := gap >= cueSentGap || lay.noSpace
			brk := gap > cuePauseBreak ||
				utf8.RuneCountInString(lay.join(cur.text, w.text)) > lay.cueChars ||
				w.end-cur.start > cueMaxDur ||
				(lay.endsSentence(cur.text) && curChars >= lay.sentChars && sentGap)
			if brk {
				out = append(out, *cur)
				cur = nil
			}
		}
		if cur == nil {
			c := cue{start: w.start, end: w.end, text: w.text}
			cur = &c
			curChars = wl
			continue
		}
		cur.text = lay.join(cur.text, w.text)
		cur.end = w.end
		curChars = utf8.RuneCountInString(cur.text)
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

func endsSentence(text string) bool {
	t := strings.TrimRight(text, `"')]`)
	return strings.HasSuffix(t, ".") || strings.HasSuffix(t, "!") || strings.HasSuffix(t, "?") || strings.HasSuffix(t, "…")
}

// padCues holds each cue a beat past its last word and long enough to read (at least
// a second, or the text's length at a comfortable reading speed), stopping short of
// the next cue.
func padCues(cues []cue) { padCuesIn(cues, spacedLayout) }

func padCuesIn(cues []cue, lay cueLayout) {
	for i := range cues {
		c := &cues[i]
		end := c.end + cueEndPad
		if read := c.start + time.Duration(float64(utf8.RuneCountInString(c.text))/lay.readCPS*float64(time.Second)); read > end {
			end = read
		}
		if end-c.start < cueMinDur {
			end = c.start + cueMinDur
		}
		if i+1 < len(cues) {
			if limit := cues[i+1].start - cueGuardGap; end > limit && limit > c.start {
				end = limit
			}
		}
		c.end = end
	}
}

// dropRepeats removes a hallucination loop: the same line three or more times in a
// row (whisper on music or crowd noise), keeping the first.
func dropRepeats(cues []cue) []cue {
	norm := func(s string) string {
		return strings.ToLower(strings.TrimSpace(strings.Trim(s, ".!?,…- ")))
	}
	out := make([]cue, 0, len(cues))
	run := 0
	for i, c := range cues {
		if i > 0 && norm(c.text) == norm(cues[i-1].text) {
			run++
		} else {
			run = 0
		}
		if run >= 2 {
			// Third identical line: this and every further repeat go; the first two
			// are dropped too since a doubled line is the loop's start.
			if run == 2 && len(out) >= 2 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

// shapeWordCues is the whole pipeline from timed words to laid-out cues.
func shapeWordCues(words []word) []cue { return shapeWordCuesIn(words, spacedLayout) }

func shapeWordCuesIn(words []word, lay cueLayout) []cue {
	cues := cuesFromWordsIn(words, lay)
	cues = mergeFragmentsIn(cues, lay)
	cues = dropRepeats(cues)
	padCuesIn(cues, lay)
	for i := range cues {
		cues[i].text = lay.layoutLines(cues[i].text)
	}
	return cues
}
