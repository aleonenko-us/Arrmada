package quality

import (
	"fmt"
	"strings"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// Conflict is a target setting that works against another one, so the profile would quietly
// do the opposite of what it says: preferring HDR10+ while avoiding HDR10 avoids every HDR10+
// release, because HDR10+ is HDR10 underneath. The editor shows each under its row as a
// warning; none of them stops a save.
type Conflict struct {
	Row string `json:"row"` // the target row it belongs under: codec | hdr | audio | bitrate
	Msg string `json:"msg"`
}

// rejectTokens are the words a release name uses for each target option: a Must whose word
// is also a rejected term refuses the very releases the Must asks for.
var rejectTokens = map[string][]string{
	"hevc": {"x265", "hevc", "h265", "h.265"}, "av1": {"av1"}, "h264": {"x264", "h264", "h.264", "avc"},
	"DV": {"dv", "dovi", "dolby vision", "dolbyvision"}, "HDR10+": {"hdr10+", "hdr10plus"},
	"HDR10": {"hdr", "hdr10"}, "HLG": {"hlg"},
	"atmos": {"atmos"}, "lossless": {"truehd", "flac", "lpcm", "dts-hd", "dts-x", "dtsx"},
}

// optionLabels names each target option the way the editor does.
var optionLabels = map[string]string{
	"hevc": "HEVC", "av1": "AV1", "h264": "H.264",
	"DV": "Dolby Vision", "HDR10+": "HDR10+", "HDR10": "HDR10", "HLG": "HLG", "SDR": "SDR",
	"atmos": "Dolby Atmos", "lossless": "Lossless audio",
}

// Conflicts lists the contradictions in a profile's target, in the order the editor shows
// its rows. A clean profile — Avoid Dolby Vision on its own, say — has none.
func (sp StoredProfile) Conflicts() []Conflict {
	ideal := IdealFile{}
	if sp.Ideal != nil {
		ideal = *sp.Ideal
	}
	var out []Conflict
	add := func(row, msg string) { out = append(out, Conflict{Row: row, Msg: msg}) }

	// Every option in a row avoided: almost every release carries one of them, so all of
	// them sink to the avoided tier together and the avoid steers nothing. Audio is a set of
	// features, not alternatives, so avoiding both of those is a real choice.
	if allAvoided(ideal.Codec, []string{"hevc", "av1", "h264"}) {
		add("codec", "Every codec is set to Avoid. Nearly every release has one of them, so nothing ranks above anything else for it.")
	}
	if ideal.Codec["av1"] == PrefMust && sourceAtLeast(sp.MinSource, parser.SourceBluray) {
		add("codec", "AV1 releases are almost all WEB, so with a "+minSourceLabel(parser.Source(sp.MinSource))+" minimum very few will qualify.")
	}

	hdrPlus, hdr10, dv := ideal.HDR["HDR10+"], ideal.HDR["HDR10"], ideal.HDR["DV"]
	if hdr10 == PrefAvoid && (hdrPlus == PrefMust || hdrPlus == PrefWant) {
		add("hdr", "Avoiding HDR10 also avoids HDR10+ (it's HDR10 underneath).")
	}
	if hdr10 == PrefAvoid && dv == PrefMust {
		add("hdr", "Most Dolby Vision releases have HDR10 underneath, so avoiding HDR10 avoids them too — only Dolby Vision with no fallback is left.")
	}
	if allAvoided(ideal.HDR, []string{"HDR10+", "HDR10", "DV", "HLG", "SDR"}) {
		add("hdr", "Every HDR option is set to Avoid, including SDR. Every release is one of them, so nothing ranks above anything else for it.")
	}
	if sp.MaxSource == string(parser.SourceDVD) {
		for _, k := range []string{"HDR10+", "HDR10", "DV", "HLG"} {
			if ideal.HDR[k] == PrefMust {
				add("hdr", "DVDs have no HDR, so with an up-to-DVD limit nothing can have "+optionLabels[k]+".")
				break
			}
		}
	}

	// A Must whose own word is a rejected term: the releases it asks for are refused first.
	for _, row := range []struct {
		name string
		m    map[string]string
		keys []string
	}{
		{"codec", ideal.Codec, []string{"hevc", "av1", "h264"}},
		{"hdr", ideal.HDR, []string{"HDR10+", "HDR10", "DV", "HLG"}},
		{"audio", ideal.Audio, []string{"atmos", "lossless"}},
	} {
		for _, k := range row.keys {
			if row.m[k] != PrefMust {
				continue
			}
			if term := rejectedToken(sp.Rejected, rejectTokens[k]); term != "" {
				add(row.name, fmt.Sprintf("%s is a Must, but %q is a rejected term — releases named with it are refused.", optionLabels[k], term))
			}
		}
	}

	for _, key := range []string{"2160p", "1080p", "720p", "SD"} {
		w := ideal.Bitrate[key]
		if w.Min > 0 && w.Max > 0 && w.Min > w.Max {
			label := key
			if key == "2160p" {
				label = "4K"
			}
			add("bitrate", fmt.Sprintf("The %s floor (%.0f Mb/s) is above its ceiling (%.0f Mb/s), so nothing fits that window.", label, w.Min, w.Max))
		}
	}
	return out
}

// allAvoided reports whether every one of keys is set to Avoid in row.
func allAvoided(row map[string]string, keys []string) bool {
	for _, k := range keys {
		if row[k] != PrefAvoid {
			return false
		}
	}
	return true
}

// sourceAtLeast reports whether a MinSource setting is s's tier or above it.
func sourceAtLeast(min string, s parser.Source) bool {
	return min != "" && sourceTier(parser.Source(min)) >= sourceTier(s)
}

// rejectedToken returns the rejected term (as the profile spells it) that is one of tokens.
func rejectedToken(rejected, tokens []string) string {
	for _, term := range rejected {
		t := strings.ToLower(strings.TrimSpace(term))
		for _, tok := range tokens {
			if t == tok {
				return strings.TrimSpace(term)
			}
		}
	}
	return ""
}
