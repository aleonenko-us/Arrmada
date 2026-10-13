package quality

import (
	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// VocabValue is one value a custom-format condition can take: what's stored, and how the
// editor shows it.
type VocabValue struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// audioLabels says what the parser's terse audio labels are.
var audioLabels = map[string]string{
	"DTS-HD": "DTS-HD (MA, DTS:X or HRA)",
	"DDP":    "Dolby Digital Plus (DD+, EAC3)",
	"DD":     "Dolby Digital (AC3)",
	"LPCM":   "LPCM / PCM",
}

// streamingLabels name the parser's streaming tags.
var streamingLabels = map[string]string{
	"AMZN": "Amazon Prime Video (AMZN)", "NF": "Netflix (NF)", "ATVP": "Apple TV+ (ATVP)",
	"DSNP": "Disney+ (DSNP)", "HMAX": "HBO Max (HMAX)", "MAX": "Max (MAX)", "PCOK": "Peacock (PCOK)",
	"PMTP": "Paramount+ (PMTP)", "HULU": "Hulu", "iT": "iTunes (iT)", "CRAV": "Crave (CRAV)",
	"STAN": "Stan", "MA": "Movies Anywhere (MA)",
}

// vocabLanguages are the languages the editor offers. Any language the table knows can be
// typed or imported; these are the ones release names commonly state.
var vocabLanguages = []string{"en", "fr", "de", "it", "es", "pt", "nl", "sv", "da", "no", "fi", "pl", "ru", "uk", "tr", "ar", "hi", "ja", "ko", "zh"}

// ConditionVocabulary lists, per condition type, every value the parser can give a
// release — so the editor offers a dropdown of things that can actually match rather than
// a text box that silently never does. Release group and release name are free text and
// have no entry.
func ConditionVocabulary() map[ConditionType][]VocabValue {
	audio := []VocabValue{}
	for _, a := range parser.AudioLabels() {
		label := audioLabels[a]
		if label == "" {
			label = a
		}
		audio = append(audio, VocabValue{a, label})
	}
	editions := []VocabValue{}
	for _, e := range parser.Editions() {
		editions = append(editions, VocabValue{e, e})
	}
	streaming := []VocabValue{}
	for _, s := range parser.StreamingServices() {
		label := streamingLabels[s]
		if label == "" {
			label = s
		}
		streaming = append(streaming, VocabValue{s, label})
	}
	languages := []VocabValue{{languageMulti, "Several (MULTi, DUAL)"}}
	for _, l := range vocabLanguages {
		languages = append(languages, VocabValue{l, langs.Name(l)})
	}
	return map[ConditionType][]VocabValue{
		CondStreaming: streaming,
		CondLanguage:  languages,
		CondCodec: {
			{string(parser.CodecX265), "HEVC (x265, H.265)"},
			{string(parser.CodecX264), "H.264 (x264, AVC)"},
			{string(parser.CodecAV1), "AV1"},
			{string(parser.CodecVC1), "VC-1"},
			{string(parser.CodecXvid), "XviD"},
		},
		CondSource: {
			{string(parser.SourceRemux), "Remux"},
			{string(parser.SourceBluray), "BluRay (encode)"},
			{string(parser.SourceWebDL), "WEB-DL"},
			{string(parser.SourceWebRip), "WEBRip"},
			{string(parser.SourceHDTV), "HDTV"},
			{string(parser.SourceDVD), "DVD"},
			{string(parser.SourceCAM), "Cam, telesync or screener"},
		},
		CondResolution: {
			{string(parser.Res2160p), "2160p (4K)"},
			{string(parser.Res1080p), "1080p"},
			{string(parser.Res720p), "720p"},
			{string(parser.Res576p), "576p"},
			{string(parser.Res480p), "480p"},
		},
		CondDynamicRange: {
			{"DV", "Dolby Vision"},
			{"HDR10+", "HDR10+"},
			{"HDR10", "HDR10 (also matches HDR10+)"},
			{"HLG", "HLG"},
			{"SDR", "SDR (no HDR)"},
		},
		CondAudio:   audio,
		CondEdition: editions,
	}
}
