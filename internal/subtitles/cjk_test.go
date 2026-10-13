package subtitles

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A Japanese transcription as whisper-cli -ojf -dtw writes it: no spaces anywhere, one
// token per kana or kanji run, and 気 split across two byte tokens (\xe6\xb0 + \x97) the
// way whisper's byte-level vocabulary splits rarer characters. The second segment has a
// Latin name in it and a pause after its first sentence.
const jaSegments = "{\"transcription\":[" +
	"{\"offsets\":{\"from\":0,\"to\":4000},\"text\":\"今日はとても良い天気ですね。散歩に行きましょうか？\",\"tokens\":[" +
	"{\"text\":\"[_BEG_]\",\"t_dtw\":-1}," +
	"{\"text\":\"今日\",\"t_dtw\":50},{\"text\":\"は\",\"t_dtw\":70},{\"text\":\"とても\",\"t_dtw\":110}," +
	"{\"text\":\"良い\",\"t_dtw\":150},{\"text\":\"天\",\"t_dtw\":180}," +
	"{\"text\":\"\xe6\xb0\",\"t_dtw\":195},{\"text\":\"\x97\",\"t_dtw\":200}," +
	"{\"text\":\"です\",\"t_dtw\":230},{\"text\":\"ね\",\"t_dtw\":250},{\"text\":\"。\",\"t_dtw\":260}," +
	"{\"text\":\"散\",\"t_dtw\":280},{\"text\":\"歩\",\"t_dtw\":300},{\"text\":\"に\",\"t_dtw\":310}," +
	"{\"text\":\"行き\",\"t_dtw\":340},{\"text\":\"ましょう\",\"t_dtw\":370},{\"text\":\"か\",\"t_dtw\":380},{\"text\":\"？\",\"t_dtw\":390}]}," +
	"{\"offsets\":{\"from\":5000,\"to\":12000},\"text\":\"それではiPhoneの使い方を説明します。まず電源ボタンを長押ししてください。\",\"tokens\":[" +
	"{\"text\":\"それでは\",\"t_dtw\":560},{\"text\":\"i\",\"t_dtw\":580},{\"text\":\"Phone\",\"t_dtw\":610},{\"text\":\"の\",\"t_dtw\":620}," +
	"{\"text\":\"使い\",\"t_dtw\":650},{\"text\":\"方\",\"t_dtw\":670},{\"text\":\"を\",\"t_dtw\":680},{\"text\":\"説明\",\"t_dtw\":720}," +
	"{\"text\":\"します\",\"t_dtw\":760},{\"text\":\"。\",\"t_dtw\":770}," +
	"{\"text\":\"まず\",\"t_dtw\":830},{\"text\":\"電源\",\"t_dtw\":870},{\"text\":\"ボタン\",\"t_dtw\":910},{\"text\":\"を\",\"t_dtw\":920}," +
	"{\"text\":\"長\",\"t_dtw\":940},{\"text\":\"押し\",\"t_dtw\":970},{\"text\":\"して\",\"t_dtw\":1000},{\"text\":\"ください\",\"t_dtw\":1050},{\"text\":\"。\",\"t_dtw\":1060}]}" +
	"]}"

func TestSegmentWordsCJK(t *testing.T) {
	words, err := wordsFromJSONIn([]byte(jaSegments), 0, noSpaceLayout)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, w := range words {
		texts = append(texts, w.text)
	}
	want := []string{"今日", "は", "とても", "良い", "天", "気", "です", "ね。", "散", "歩", "に", "行き", "ましょう", "か？",
		"それでは", "iPhone", "の", "使い", "方", "を", "説明", "します。", "まず", "電源", "ボタン", "を", "長", "押し", "して", "ください。"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("words:\n got %q\nwant %q", texts, want)
	}
	// The split character is put back together and timed by its last byte token.
	if words[5].text != "気" || words[5].end != 2*time.Second {
		t.Errorf("split character = %q ending %v, want 気 at 2s", words[5].text, words[5].end)
	}
	// Spaced languages read the same JSON as before: one "word" per space-led run, and
	// the split character stays as encoding/json decodes it.
	spaced, _ := wordsFromJSON([]byte(jaSegments), 0)
	if len(spaced) != 2 || !strings.Contains(spaced[0].text, string(utf8.RuneError)) {
		t.Errorf("spaced reading changed: %+v", spaced)
	}
}

func TestShapeCuesJapaneseNoSpaces(t *testing.T) {
	words, err := wordsFromJSONIn([]byte(jaSegments), 0, layoutFor("ja", false))
	if err != nil {
		t.Fatal(err)
	}
	cues := shapeWordCuesIn(words, layoutFor("ja", false))
	if len(cues) < 3 {
		t.Fatalf("got %d cues: %+v", len(cues), cues)
	}
	var all string
	for i, c := range cues {
		if strings.ContainsAny(c.text, " "+string(utf8.RuneError)) {
			t.Errorf("cue %d has a space or a broken character: %q", i+1, c.text)
		}
		lines := strings.Split(c.text, "\n")
		if len(lines) > 2 {
			t.Errorf("cue %d has %d lines: %q", i+1, len(lines), c.text)
		}
		for _, ln := range lines {
			if n := utf8.RuneCountInString(ln); n > 16 {
				t.Errorf("cue %d line is %d characters: %q", i+1, n, ln)
			}
			if strings.HasPrefix(ln, "。") || strings.HasPrefix(ln, "、") {
				t.Errorf("cue %d line starts with punctuation: %q", i+1, ln)
			}
		}
		if strings.Contains(c.text, "iPh\n") || strings.Contains(c.text, "i\nP") {
			t.Errorf("cue %d splits a Latin word: %q", i+1, c.text)
		}
		all += strings.ReplaceAll(c.text, "\n", "")
	}
	if want := "今日はとても良い天気ですね。散歩に行きましょうか？それではiPhoneの使い方を説明します。まず電源ボタンを長押ししてください。"; all != want {
		t.Errorf("text changed:\n got %q\nwant %q", all, want)
	}
	// The first sentence pair is one cue, broken after 。 rather than mid-phrase.
	if cues[0].text != "今日はとても良い天気ですね。\n散歩に行きましょうか？" {
		t.Errorf("first cue = %q", cues[0].text)
	}
	// Casing and the stock-phrase filter leave CJK cues alone.
	// A sentence end closes a cue inside a long segment, and lines break where a
	// Japanese phrase ends (after a particle, before kanji) rather than mid-word.
	if len(cues) != 3 || cues[1].text != "それではiPhoneの\n使い方を説明します。" || cues[2].text != "まず電源ボタンを\n長押ししてください。" {
		t.Errorf("cues = %+v", cues)
	}
	srt := formatSRT(cues)
	if filterStockPhrases(srt) != srt {
		t.Errorf("stock-phrase filter changed CJK cues:\n%s", filterStockPhrases(srt))
	}
	// A 32-character cue is held long enough to read at CJK speed (8 chars/s).
	padded := []cue{{0, 500 * time.Millisecond, strings.Repeat("あ", 32)}}
	padCuesIn(padded, noSpaceLayout)
	if padded[0].end < 4*time.Second {
		t.Errorf("a 32-character cue released after %v", padded[0].end)
	}
}

func TestLineSplitRunes(t *testing.T) {
	if got := layoutLinesRunes("短い文です。", 16); got != "短い文です。" {
		t.Errorf("short line changed: %q", got)
	}
	// A break after 、 wins over the exact middle.
	if got := layoutLinesRunes("私たちは明日の朝早く、駅前の喫茶店で会いましょう", 16); got != "私たちは明日の朝早く、\n駅前の喫茶店で会いましょう" {
		t.Errorf("punctuation break = %q", got)
	}
	// No punctuation: the nearest even split, on a character boundary.
	got := layoutLinesRunes("我们今天下午去公园散步然后回家吃晚饭吧好不好", 16)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || utf8.RuneCountInString(lines[0]) > 16 || utf8.RuneCountInString(lines[1]) > 16 {
		t.Errorf("even split = %q", got)
	}
	// Never before ー or closing punctuation, never inside a Latin word.
	for _, s := range []string{"あいうえおかきくけこさしすせそター」たちつてとなにぬねのはひふへ", "これはとても長い説明ですがiPhoneSEの話をしたいと思います"} {
		got := layoutLinesRunes(s, 16)
		parts := strings.Split(got, "\n")
		if len(parts) != 2 {
			t.Errorf("%q not split: %q", s, got)
			continue
		}
		r, _ := utf8.DecodeRuneInString(parts[1])
		last, _ := utf8.DecodeLastRuneInString(parts[0])
		if strings.ContainsRune("ー」。、", r) || (isWordRune(last) && isWordRune(r)) {
			t.Errorf("bad break: %q", got)
		}
	}
	// Too long for two lines: still two lines, split near the middle.
	long := strings.Repeat("あ", 40)
	if got := layoutLinesRunes(long, 16); got != strings.Repeat("あ", 20)+"\n"+strings.Repeat("あ", 20) {
		t.Errorf("overlong = %q", got)
	}
}

// Mixed-script joins: flush whenever a CJK character or CJK punctuation is on either
// side, a space between two spaced-script runs.
func TestJoinWordsMixedScript(t *testing.T) {
	for _, c := range [][3]string{
		{"これは", "iPhone", "これはiPhone"},
		{"iPhone", "です", "iPhoneです"},
		{"ね。", "Apple", "ね。Apple"},
		{"我们用", "Python", "我们用Python"},
		{"Hello", "world", "Hello world"},
		{"version", "2", "version 2"},
		{"", "今日", "今日"},
	} {
		if got := joinWords(c[0], c[1]); got != c[2] {
			t.Errorf("joinWords(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	// Korean is spaced: it keeps the space layout, and Hangul is no no-space script.
	if layoutFor("ko", false).noSpace || isNoSpaceScript('한') {
		t.Error("Korean laid out without spaces")
	}
	for _, l := range []string{"ja", "zh", "zh-tw", "zh-cn", "jpn", "chi"} {
		if !layoutFor(l, false).noSpace {
			t.Errorf("%s not laid out by characters", l)
		}
	}
	// Translating produces English whatever the audio.
	if layoutFor("ja", true).noSpace || layoutFor("en", false).noSpace {
		t.Error("translated or English output laid out by characters")
	}
}

// The English pipeline is byte-identical to before character layout existed: this
// SRT was produced by the code as it stood before SUB-35, from the same words.
func TestShapeEnglishByteIdentical(t *testing.T) {
	words, _ := wordsFromJSONIn([]byte(dtwSegment), 0, layoutFor("en", false))
	words = append(words, wordsAt(goldenEnglishWords...)...)
	lay := layoutFor("en", false)
	got := formatSRT(fixCasing(shapeWordCuesIn(words, lay), true))
	if got != goldenEnglishSRT {
		t.Errorf("English output changed:\n%s", got)
	}
}

var goldenEnglishWords = []string{
	"we@10.0-10.2", "should@10.2-10.4", "go@10.4-10.6", "now.@10.6-10.9",
	"right?@11.1-11.4",
	"he'll@15.0-15.2", "come@15.2-15.4", "when@15.4-15.5", "he's@15.5-15.7", "ready,@15.7-16.0",
	"and@16.1-16.2", "if@16.2-16.3", "he@16.3-16.4", "doesn't@16.4-16.7", "we@16.7-16.8", "know@16.8-17.0", "where@17.0-17.2", "we@17.2-17.3", "stand.@17.3-17.7",
	"i@18.2-18.3", "told@18.3-18.5", "him@18.5-18.6", "we'd@18.6-18.8", "be@18.8-18.9", "there@18.9-19.1", "by@19.1-19.2", "nine,@19.2-19.6",
	"but@19.7-19.8", "the@19.8-19.9", "traffic@19.9-20.2", "on@20.2-20.3", "the@20.3-20.4", "bridge@20.4-20.7", "was@20.7-20.8", "worse@20.8-21.1",
	"than@21.1-21.2", "anyone@21.2-21.5", "expected,@21.5-22.0", "so@22.1-22.2", "we@22.2-22.3", "missed@22.3-22.6", "the@22.6-22.7", "whole@22.7-22.9", "first@22.9-23.1", "act.@23.1-23.5",
	"okay.@24.0-24.3",
	"thank@30.0-30.3", "you.@30.3-30.6",
	"no.@31.0-31.2", "no.@31.4-31.6", "no.@31.8-32.0", "no.@32.2-32.4",
	"what@33.0-33.2", "i'm@33.2-33.4", "saying@33.4-33.7", "is@33.7-33.8", "that@33.8-34.0", "nobody@34.0-34.4", "here@34.4-34.6", "really@34.6-34.9", "knows@34.9-35.2", "what@35.2-35.3", "happened@35.3-35.7", "that@35.7-35.8", "night@35.8-36.1", "at@36.1-36.2", "the@36.2-36.3", "lake@36.3-36.6", "house...@36.6-37.2",
}

const goldenEnglishSRT = `1
00:00:02,400 --> 00:00:05,730
We should go now. Right?

2
00:00:10,000 --> 00:00:11,650
We should go now. Right?

3
00:00:15,000 --> 00:00:18,150
He'll come when he's ready, and if he doesn't we know where we stand.

4
00:00:18,200 --> 00:00:21,150
I told him we'd be there by nine, but the traffic on the bridge was worse than

5
00:00:21,200 --> 00:00:23,750
anyone expected, so we missed the whole first act.

6
00:00:24,000 --> 00:00:25,000
Okay.

7
00:00:30,000 --> 00:00:35,250
Thank you. No. No. No. No. What I'm saying is that nobody here really knows what

8
00:00:35,300 --> 00:00:37,450
happened that night at the lake house...

`

func TestRawJSONStringKeepsBytes(t *testing.T) {
	for in, want := range map[string]string{
		`"plain"`:             "plain",
		`" \"q\" \\ \/ \n\t"`: " \"q\" \\ / \n\t",
		`"` + `\` + `u00e9` + `\` + `ud83d` + `\` + `ude00"`: "é😀", // escaped é and a surrogate pair
		"\"\xe6\xb0\"": "\xe6\xb0",
	} {
		if got, ok := rawJSONString([]byte(in)); !ok || got != want {
			t.Errorf("rawJSONString(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if !endsMidRune("天\xe6\xb0") || endsMidRune("天気") || endsMidRune("abc") {
		t.Error("endsMidRune wrong")
	}
}
