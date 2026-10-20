// Package quality decides which release to grab. It scores parsed releases
// against a profile — a quality ladder (resolution × source), custom-format
// preferences, a size ceiling — and ranks candidates into a decision with
// plain-language reasons. This is the engine behind the "what you'll get"
// experience; the Simple UI hides the numbers, the Advanced UI exposes them.
package quality

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// --- Quality ladder -------------------------------------------------------

var resBase = map[parser.Resolution]int{
	parser.Res2160p: 400,
	parser.Res1080p: 250,
	parser.Res720p:  90,
	parser.Res576p:  60,
	parser.Res480p:  40,
}

// sourceBonus layers a release's provenance on top of the resolution base.
// The values are deliberately compressed: the whole identified-source spread
// (Remux 21 → DVD 2 = 19 points) stays below the tightest resolution gap
// (576p→480p = 20), so a better source can never leapfrog a full resolution
// tier — it decides between releases of the SAME resolution, exactly where
// "1080p HDCAM vs 1080p WEB-DL" needs deciding. Every adjacent gap is at
// least 2, which keeps the +1 properBonus from jumping a source tier either.
// Unknown sources score 0 (map default): below every identified source.
// CAM is the one deliberate outlier — a cam rip is worse than a real encode
// at any resolution, so its penalty drops it out of contention entirely.
var sourceBonus = map[parser.Source]int{
	parser.SourceRemux:  21,
	parser.SourceBluray: 18,
	parser.SourceWebDL:  8,
	parser.SourceWebRip: 6,
	parser.SourceHDTV:   4,
	parser.SourceDVD:    2,
	parser.SourceCAM:    -170,
}

// properBonus rewards a PROPER/REPACK re-release: +1 strictly wins the tie
// against the broken original it replaces, while staying below every source
// gap (min 2) and every resolution gap, so a proper never out-ranks anything
// its base release wouldn't.
const properBonus = 1

var sourceRank = map[parser.Source]int{
	parser.SourceRemux: 6, parser.SourceBluray: 5, parser.SourceWebDL: 4,
	parser.SourceWebRip: 3, parser.SourceDVD: 2, parser.SourceHDTV: 1, parser.SourceCAM: 0,
}

// sourceTier is the coarser ladder the MinSource/MaxSource gates use. WEB-DL and WEBRip
// share one WEB tier: scene TV tags untouched captures plain "WEB", groups mix the two
// labels freely, and a "WEB-DL or better" minimum that refused WEBRip silently starved
// whole shows. Ranking still prefers WEB-DL — sourceBonus and sourceRank keep them apart.
func sourceTier(s parser.Source) int {
	if s == parser.SourceWebRip {
		return sourceRank[parser.SourceWebDL]
	}
	return sourceRank[s]
}

// webTier is the highest minimum an unstated source can be assumed to meet. A release that
// names no source is almost always a web capture (scene TV, fansubs); it is never assumed
// to be a disc.
var webTier = sourceRank[parser.SourceWebDL]

var resRank = map[parser.Resolution]int{
	parser.Res2160p: 5, parser.Res1080p: 4, parser.Res720p: 3, parser.Res576p: 2, parser.Res480p: 1,
}

// qualityScore ranks by resolution first, with a small source bonus layered on
// top so provenance breaks same-resolution contests (a 1080p WEB-DL out-ranks a
// 1080p HDCAM even when the cam file is bigger). The source spread is kept
// smaller than any resolution gap — see sourceBonus — so within a profile's
// allowed range, size (bitrate) still decides between releases whose score
// ties. A PROPER/REPACK gets a +1 nudge so it strictly beats the broken
// original it re-releases.
func qualityScore(r parser.Release) int {
	s := resBase[r.Resolution] + sourceBonus[r.Source]
	if r.Proper || r.Repack {
		s += properBonus
	}
	return s
}

// --- Custom formats -------------------------------------------------------

// ConditionType is what a custom-format condition matches on.
type ConditionType string

const (
	CondDynamicRange ConditionType = "dynamic_range" // Value ∈ HDR tags (DV, HDR10, HDR10+)
	CondAudio        ConditionType = "audio"         // Value ∈ audio tags
	CondCodec        ConditionType = "codec"
	CondSource       ConditionType = "source"
	CondResolution   ConditionType = "resolution"
	CondEdition      ConditionType = "edition"
	CondReleaseGroup ConditionType = "release_group"
	CondLossless     ConditionType = "lossless" // no value: the release has a lossless audio track
)

// Condition is one predicate over a parsed release.
type Condition struct {
	Type   ConditionType `json:"type"`
	Value  string        `json:"value"`
	Negate bool          `json:"negate,omitempty"`
}

// matches tests one condition against a release whose raw name is name (customformat.go
// has the release_title side). A Value may list alternatives ("TrueHD|DTS-HD|FLAC"), any of
// which matches; dynamic range "SDR" matches a release with no HDR tag at all. A condition
// that can't be evaluated — an unknown type, a pattern that doesn't compile — never holds,
// negated or not, so a broken condition can't turn a format into "matches everything".
func (c Condition) matches(name string, r parser.Release, regex map[string]*regexp.Regexp) bool {
	if !knownCondition(c.Type) {
		return false
	}
	hit := false
	if c.Type == CondReleaseTitle {
		re, ok := regex[c.Value]
		if !ok {
			re = cachedRegex(c.Value)
		}
		if re == nil {
			return false
		}
		hit = name != "" && re.MatchString(name)
	} else {
		for _, v := range strings.Split(c.Value, "|") {
			if c.matchOne(r, strings.TrimSpace(v)) {
				hit = true
				break
			}
		}
	}
	if c.Negate {
		return !hit
	}
	return hit
}

// matchOne compares one alternative. Values are compared case-insensitively; NewEngine has
// already rewritten aliases ("HEVC", "Dolby Vision", "Blu-ray") to the parser's own labels.
func (c Condition) matchOne(r parser.Release, v string) bool {
	var hit bool
	switch c.Type {
	case CondDynamicRange:
		hit = containsFold(r.HDR, v) || (strings.EqualFold(v, "SDR") && len(r.HDR) == 0)
	case CondAudio:
		hit = containsFold(r.Audio, v)
	case CondCodec:
		hit = r.Codec != parser.CodecUnknown && strings.EqualFold(string(r.Codec), v)
	case CondSource:
		hit = r.Source != parser.SourceUnknown && strings.EqualFold(string(r.Source), v)
	case CondResolution:
		hit = r.Resolution != parser.ResUnknown && strings.EqualFold(string(r.Resolution), v)
	case CondEdition:
		hit = strings.EqualFold(r.Edition, v) || sameEdition(r.Edition, v)
	case CondReleaseGroup:
		hit = strings.EqualFold(r.Group, v)
	case CondLossless:
		hit = losslessAudio(r)
	default:
		hit = matchTrait(c.Type, r, v) // the parser's newer traits (customformat.go)
	}
	return hit
}

// losslessAudio reports a lossless track. The parser decides it from the name (DTS-HD
// MA yes, DTS-HD HRA no); the labels are read too, so a Release built from probed facts
// rather than parsed still counts.
func losslessAudio(r parser.Release) bool {
	return r.AudioLossless || containsStr(r.Audio, "TrueHD") || containsStr(r.Audio, "FLAC") || containsStr(r.Audio, "LPCM")
}

// CustomFormat is a named set of conditions. Match says how they combine: MatchAll (the
// default, and what every format saved before it existed means) needs every condition,
// MatchAny needs one.
type CustomFormat struct {
	Name       string      `json:"name"`
	Match      string      `json:"match,omitempty"`
	Conditions []Condition `json:"conditions"`
}

// Matches reports whether the format holds for a parsed release. It has no release name,
// so release_title conditions never hold here; the engine matches with the name.
func (f CustomFormat) Matches(r parser.Release) bool {
	return normalizeFormat(f).holds("", r, nil)
}

// holds evaluates an already-normalised format (see normalizeFormat).
func (f CustomFormat) holds(name string, r parser.Release, regex map[string]*regexp.Regexp) bool {
	if len(f.Conditions) == 0 {
		return false
	}
	anyOf := f.Match == MatchAny
	for _, c := range f.Conditions {
		hit := c.matches(name, r, regex)
		if anyOf && hit {
			return true
		}
		if !anyOf && !hit {
			return false
		}
	}
	return !anyOf
}

// capFor is the bitrate ceiling for a resolution: its window's, else the profile-wide one.
func (p Profile) capFor(res parser.Resolution) float64 {
	if w, ok := p.Windows[ResolutionKey(res)]; ok && w.Max > 0 {
		return w.Max
	}
	return p.BitrateCapMbps
}

// missingRequired returns a required format the release lacks ("" when it has them all).
// Required formats that are alternatives of one another (two codecs, two HDR formats)
// need only one of them: requiring HEVC and AV1 means "either", not "both".
func (e *Engine) missingRequired(required []string, relName string, r parser.Release) string {
	groups := map[string][]string{}
	var order []string
	for _, name := range required {
		f, ok := e.formats[name]
		if !ok {
			continue
		}
		g := anyOfGroup(name)
		if g == "" {
			if !e.match(f, relName, r) {
				return name
			}
			continue
		}
		if _, seen := groups[g]; !seen {
			order = append(order, g)
		}
		groups[g] = append(groups[g], name)
	}
	for _, g := range order {
		hit := false
		for _, name := range groups[g] {
			if e.match(e.formats[name], relName, r) {
				hit = true
				break
			}
		}
		if !hit {
			return strings.Join(groups[g], " or ")
		}
	}
	return ""
}

// --- Profile & candidates -------------------------------------------------

// Profile expresses "what a good release looks like".
type Profile struct {
	Name               string              `json:"name"`
	AllowedResolutions []parser.Resolution `json:"allowed_resolutions"` // empty = any
	MinSource          parser.Source       `json:"min_source,omitempty"`
	MaxSource          parser.Source       `json:"max_source,omitempty"`       // empty = no upper bound
	BitrateCapMbps     float64             `json:"bitrate_cap_mbps,omitempty"` // 0 = no cap; rejects releases whose bitrate exceeds it (length-independent)
	// Windows are the target's bitrate windows by resolution key (ResolutionKey). A
	// window's ceiling replaces BitrateCapMbps for its resolution; falling under its floor
	// puts a release in the avoided tier.
	Windows      map[string]BitrateWindow `json:"windows,omitempty"`
	SmallBias    float64                  `json:"small_bias,omitempty"` // score penalty per GB; any value > 0 also breaks score ties toward the smaller file (0 = ties go to the larger, i.e. higher-bitrate, file)
	FormatScores map[string]int           `json:"format_scores,omitempty"`
	// Required names formats a release must carry to be eligible at all — "Atmos"
	// here rejects everything without an Atmos track, where a score only prefers it.
	Required []string `json:"required_formats,omitempty"`
	// MinFormatScore refuses a release whose format score is under it. A built-in format's
	// Avoid (target or "Other formats") is left out of that sum — it ranks in the avoided
	// tier instead. Custom formats and keywords count in full.
	MinFormatScore int       `json:"min_format_score,omitempty"`
	Keywords       []Keyword `json:"keywords,omitempty"`
	Rejected       []string  `json:"rejected,omitempty"`
	MinSeeders     int       `json:"min_seeders,omitempty"`
	// RejectPreRelease refuses cams, telesyncs, telecines, screeners and workprints
	// outright (they parse as CAM), rather than only ranking them last.
	RejectPreRelease bool `json:"reject_prerelease,omitempty"`
	// Unwanted is the profile's Unwanted pack choices (unwanted.go); the zero value is the
	// defaults. NoUnwanted turns the pack off — book and music profiles, whose names are
	// titles and authors, not video tags.
	Unwanted   Unwanted `json:"unwanted,omitempty"`
	NoUnwanted bool     `json:"no_unwanted,omitempty"`
	// GroupTiers order releases by group under ranking v2 (grouptiers.go); never a reject.
	GroupTiers GroupTiers `json:"group_tiers,omitempty"`
	// OwnerLanguages are the audio languages the household keeps (Convert's audio
	// languages); a release in one of them is never foreign-only. Empty means English.
	OwnerLanguages []string `json:"owner_languages,omitempty"`
	// Languages is the target's Audio language row (lang.go): base codes or LangOriginal
	// to must/want/avoid. Empty is no language rules.
	Languages map[string]string `json:"languages,omitempty"`
	// MultiCountsLanguage reads a MULTi or Dual release that names no language as maybe
	// carrying a Must language (unknown: not refused), the way some trackers label the local
	// dub MULTi. Off — the default — a Must has to be named, or be the title's original
	// language (lang.go). StoredProfile.MultiCountsLanguage.
	MultiCountsLanguage bool `json:"multi_counts_language,omitempty"`
	// PreferNetwork is the download_network_preference setting (NetworkUsenet or
	// NetworkTorrent) when it prefers one: between a usenet and a torrent release that
	// score the same, the preferred network ranks first (KeyNetwork). "" or NetworkBest
	// adds no step. Set by the service from the setting, never stored with a profile.
	PreferNetwork string `json:"-"`
	// onDisk judges a file already in the library rather than a release on offer: an
	// Unwanted item set to reject only avoids it, so a file the pack now refuses (a 3D
	// copy) isn't treated as worthless and replaced by any release at all (current.go).
	onDisk bool
	// noLangGate lifts the language Musts, for describing a file the profile no longer
	// accepts (describe), like the other gates it lifts.
	noLangGate bool
}

// Candidate is a release under consideration (release + indexer metadata).
type Candidate struct {
	Name       string         `json:"name"`
	Release    parser.Release `json:"release"`
	SizeGB     float64        `json:"size_gb"`
	Seeders    int            `json:"seeders"`
	RuntimeMin int            `json:"runtime_min,omitempty"` // content length this release covers; 0 = unknown (bitrate cap can't apply)
	// OrigLanguage is the title's original language (TMDB's original_language), so a
	// release dubbed only into another language can be told apart; "" = no judgement.
	OrigLanguage string `json:"orig_language,omitempty"`
	// Usenet marks a release from a usenet indexer. It has no seeders to count: it is
	// always alive, the profile's minimum seeders doesn't apply, and seeders never decide
	// between it and a torrent.
	Usenet bool `json:"usenet,omitempty"`
	// ruleLang is the title's original language for the language rules alone, where
	// OrigLanguage is deliberately left unset (a file on disk, the import gate's release):
	// setting OrigLanguage there would start the Unwanted pack's foreign-only check on
	// files it has never judged.
	ruleLang string
	// onDisk marks the candidate as a file in the library (CurrentFile.candidate): its
	// languages are read by fileLangs, never assumed from an untagged name, and heard is
	// its probed facts (nil without an analysis).
	onDisk bool
	heard  *FileFacts
	// tested marks a name typed into the profile builder's "Test a release name"
	// (Service.Explain): there's no title behind it, so no original language, and a
	// refusal says so.
	tested bool
	// window is the movie's release window (WithReleaseWindow, notoutyet.go); the zero
	// value judges nothing.
	window ReleaseWindow
}

// alive reports whether the release can download at all: a usenet release always can, a
// torrent only with someone seeding it.
func (c Candidate) alive() bool { return c.Usenet || c.Seeders > 0 }

// seedersComparable reports whether seeders may tell a and b apart: only between two
// torrents. A usenet release's 0 is "not counted", not "dead".
func seedersComparable(a, b Candidate) bool { return !a.Usenet && !b.Usenet }

// NewCandidate parses a release name into a Candidate.
func NewCandidate(name string, sizeGB float64, seeders int) Candidate {
	return Candidate{Name: name, Release: parser.Parse(name), SizeGB: sizeGB, Seeders: seeders}
}

// WithRuntime attaches the content runtime (minutes) so the bitrate ceiling can apply. For a
// movie that's the film's runtime; for a single episode the episode runtime; for a season pack
// the sum across its episodes. 0 leaves the bitrate cap inert for this candidate.
func (c Candidate) WithRuntime(min int) Candidate {
	c.RuntimeMin = min
	return c
}

// WithLanguage attaches the title's original language, so the Unwanted pack can tell a
// foreign-only dub. "" (unknown) means no judgement.
func (c Candidate) WithLanguage(lang string) Candidate {
	c.OrigLanguage = strings.TrimSpace(lang)
	return c
}

// gibToMegabit converts one GiB to megabits (2^30 bytes × 8 bits ÷ 1e6), so bitrateMbps yields
// Mbps (decimal megabits per second) from a GiB size and a minutes runtime.
const gibToMegabit = 1024.0 * 1024.0 * 1024.0 * 8.0 / 1e6 // ≈ 8589.93

// bitrateMbps is the release's average bitrate in Mbps, or 0 when the runtime is unknown.
func (c Candidate) bitrateMbps() float64 { return BitrateMbps(c.SizeGB, c.RuntimeMin) }

// sizeOver is the candidate's size scaled to runtimeMin minutes of content: its share when
// it covers more than that (an S01E01E02 double weighed against one episode's file). The
// raw size when either runtime is unknown or the two agree, which is every movie.
func sizeOver(c Candidate, runtimeMin int) float64 {
	if c.RuntimeMin <= 0 || runtimeMin <= 0 || c.RuntimeMin == runtimeMin {
		return c.SizeGB
	}
	return c.SizeGB * float64(runtimeMin) / float64(c.RuntimeMin)
}

// codecEfficiency scales a bitrate into H.264-equivalent terms.
//
// Raw bitrate is a poor quality measure across codecs: a 60 Mbps H.264 encode looks WORSE
// than a 55 Mbps HEVC one, because HEVC delivers roughly the same picture in far fewer
// bits. Comparing the raw numbers therefore penalizes the better release and lets a bloated
// older-codec encode through a ceiling that a superior modern one fails.
//
// These are deliberately conservative round numbers, not precise measurements — the point
// is to stop comparing across codecs as though a bit is a bit.
func codecEfficiency(c parser.Codec) float64 {
	switch c {
	case parser.CodecX265:
		return 1.6 // HEVC needs ~40% fewer bits than H.264 for the same picture
	case parser.CodecAV1:
		return 2.0 // AV1 is the most bit-efficient of the lot
	case parser.CodecXvid, parser.CodecVC1:
		return 0.6 // older codecs need MORE bits for the same picture
	}
	return 1.0 // H.264 is the reference, and the safe assumption when unknown
}

// effectiveBitrateMbps is the bitrate expressed in H.264-equivalent terms, so ceilings and
// comparisons mean the same thing regardless of the codec a release used.
func (c Candidate) effectiveBitrateMbps() float64 {
	return c.bitrateMbps() * codecEfficiency(c.Release.Codec)
}

// BitrateMbps is the average bitrate in Mbps for a GiB size over a minutes
// runtime, or 0 when either is unknown/zero.
func BitrateMbps(sizeGB float64, runtimeMin int) float64 {
	if runtimeMin <= 0 || sizeGB <= 0 {
		return 0
	}
	return sizeGB * gibToMegabit / float64(runtimeMin*60)
}

// Evaluation is the scored result for a single candidate.
type Evaluation struct {
	Candidate    Candidate `json:"candidate"`
	Eligible     bool      `json:"eligible"`
	RejectReason string    `json:"reject_reason,omitempty"`
	// RejectCode is RejectReason as a stable code (Reject* below), set at the same place.
	// The reason is a sentence for a person; the code is what a stored search outcome
	// counts by, so "22 over the bitrate ceiling" survives rewording the sentence.
	RejectCode   string   `json:"reject_code,omitempty"`
	QualityScore int      `json:"quality_score"`
	FormatScore  int      `json:"format_score"`
	SizeScore    int      `json:"size_score"`
	Total        int      `json:"total"`
	Matched      []string `json:"matched,omitempty"` // preferred formats that matched
	// PreferBonus is the part of FormatScore that came from preferences (positive
	// format and keyword scores). Decide takes it back when the release's bitrate has
	// collapsed against the best alternative: preferring HEVC must not pick a 1 Mbps
	// file over a 9 Mbps one. BonusWaived says that happened.
	PreferBonus int  `json:"prefer_bonus,omitempty"`
	BonusWaived bool `json:"bonus_waived,omitempty"`
	// TargetScore and CustomScore split FormatScore by where a score came from: the
	// target's own formats (codec, HDR, audio — targetFormats) versus everything else
	// (custom formats, keywords and the other built-ins). They always add up to
	// FormatScore; the split only exists so an upgrade can say which kind of gain it is
	// (see improvementKey).
	TargetScore int `json:"target_score,omitempty"`
	CustomScore int `json:"custom_score,omitempty"`
	// targetBonus is the part of PreferBonus that came from target formats, so a waived
	// bonus comes off the right half of the split.
	targetBonus int
	// Avoided is set when the release carries a format the profile scores negatively (the
	// "Avoid" toggle). Such a release drops to a lower tier: it's only ever the winner when
	// no non-avoided release is eligible, so "Avoid Dolby Vision" means "never pick DV while
	// there's an alternative" — not merely a score penalty a better source could overcome.
	Avoided        bool     `json:"avoided,omitempty"`
	AvoidedFormats []string `json:"avoided_formats,omitempty"`

	// Rank keys (rankkeys.go): the evaluation taken apart into what ranking v2 compares,
	// key by key. Pure description — Total and Avoided above are computed exactly as
	// before. Set on eligible evaluations.
	//
	// TargetAvoided: a built-in format's Avoid (the target's or an "Other formats" one),
	// the bitrate floor, or an Unwanted item set to avoid — the avoids that are a tier of
	// their own. CustomAvoided: a negative custom format or keyword score, which v2 treats
	// as a score, not a cliff.
	TargetAvoided bool `json:"target_avoided,omitempty"`
	CustomAvoided bool `json:"custom_avoided,omitempty"`
	// ResTier ranks the resolution (2160p 5 … 480p 1, unknown 0); the goal is the top
	// allowed tier. PrefCount is how many of the target's preferred or required formats
	// the release has (0 when its bonus was waived for a collapsed bitrate).
	ResTier   int `json:"res_tier,omitempty"`
	PrefCount int `json:"pref_count,omitempty"`
	// SourceTier: Remux 6, BluRay 5, WEB 4 (WEB-DL and WEBRip alike), HDTV 2, DVD 1,
	// unstated 0, a cam -1. ProperFix is a PROPER or REPACK.
	SourceTier int  `json:"source_tier,omitempty"`
	ProperFix  bool `json:"proper_fix,omitempty"`
	// GroupTier is the release group's tier: 2 for tier 1, 1 for tier 2, 0 for none.
	GroupTier int `json:"group_tier,omitempty"`
	// EffBitrate is the bitrate in H.264-equivalent Mb/s (0 without a runtime), and
	// InWindow says it is inside the resolution's window (or there is none to judge).
	EffBitrate float64 `json:"eff_bitrate,omitempty"`
	InWindow   bool    `json:"in_window,omitempty"`
	// LostOn and LostOnText say why an eligible release ranked below the one just above it
	// in Decide's order, in either ranking mode: the deciding RankKey (KeyTie when nothing
	// told them apart) and the plain-language reason ("lower resolution", "fewer seeders",
	// explain.go). Empty for the winner and for rejected releases.
	LostOn     RankKey `json:"lost_on,omitempty"`
	LostOnText string  `json:"lost_on_text,omitempty"`
	// AudioLangs are the audio languages the release carries as the language rules read
	// them (lang.go) — base codes, set on every evaluation so a release row can show them —
	// and AudioLangsNote qualifies them in words ("not tagged — read as the original
	// language", "and others it doesn't name"). Both empty when nothing can be told.
	AudioLangs     []string `json:"audio_langs,omitempty"`
	AudioLangsNote string   `json:"audio_langs_note,omitempty"`
	// langPrefs counts the preferred or required languages it has (the preferences key and
	// classic's score), langMatched names them, and langShort says a Must or a Prefer is
	// missing, so a file without one isn't the target yet (judge).
	langPrefs   int
	langMatched []string
	langShort   bool
	// langPrefCodes are the codes behind langPrefs, and langUnknown the Must or Prefer
	// languages it may or may not carry: an upgrade gets no credit for a language the file
	// might already have (upgradeBar.creditable).
	langPrefCodes []string
	langUnknown   []string
	// langAvoided: every language it carries is avoided (it's in the avoided tier for that).
	langAvoided bool
	// lowGroup says the low-quality-group item put it in the avoided tier, so an upgrade
	// out of it can be told apart (current.go).
	lowGroup bool
	// ceiling names the profile ceiling a rejected release is above ("40 Mb/s", "BluRay"),
	// "" for every other outcome. It lets a downgrade prompt tell "too big for this
	// profile" from "not what this profile wants" without parsing the reason text.
	ceiling string
}

// Decision is the ranked outcome over a set of candidates.
type Decision struct {
	Winner     *Evaluation  `json:"winner"`
	Why        []string     `json:"why,omitempty"`
	ChosenOver string       `json:"chosen_over,omitempty"`
	Eligible   []Evaluation `json:"eligible"`
	Rejected   []Evaluation `json:"rejected"`
	// Ranking is the mode that ordered Eligible (RankingV1 or RankingV2), and DecidedBy
	// the key the winner beat the runner-up on (KeyOnly for a lone release) — every pick
	// records what decided it.
	Ranking   string  `json:"ranking,omitempty"`
	DecidedBy RankKey `json:"decided_by,omitempty"`
	// Shadow is what the other ranking would have picked (shadow mode, shadow.go): v2's
	// while classic decides, classic's while v2 decides. Set by the service only; nil with
	// nothing eligible.
	Shadow *ShadowPick `json:"shadow,omitempty"`
}

// Why a profile turned a release down: the stable codes behind Evaluation.RejectReason.
// Stored with search outcomes, so never rename one.
const (
	RejectResolution      = "resolution"       // not one of the profile's resolutions
	RejectSourceFloor     = "source_floor"     // below the minimum source, or source not stated where a disc is needed
	RejectPrerelease      = "prerelease"       // a cam, telesync or screener
	RejectSourceCeiling   = "source_ceiling"   // above the maximum source
	RejectBitrateCeiling  = "bitrate_ceiling"  // over the resolution's bitrate ceiling
	RejectSeeders         = "seeders"          // fewer seeders than the profile needs
	RejectTerm            = "rejected_term"    // carries a rejected term
	RejectMissingRequired = "missing_required" // lacks a required format
	RejectMinFormatScore  = "min_format_score" // below the minimum format score
	RejectMissingLanguage = "missing_language" // lacks an audio language the profile requires
	RejectNotOutYet       = "not_out_yet"      // says WEB-DL/BluRay before the film is out at home (notoutyet.go)
	// RejectUnwanted ("unwanted", unwanted.go): an Unwanted item set to reject.
)

// RejectCodes lists every code, for tests and for anything that labels them.
var RejectCodes = []string{
	RejectResolution, RejectSourceFloor, RejectPrerelease, RejectSourceCeiling, RejectBitrateCeiling,
	RejectSeeders, RejectTerm, RejectMissingRequired, RejectMinFormatScore, RejectUnwanted,
	RejectMissingLanguage, RejectNotOutYet,
}

// reject sets both halves of a rejection: the sentence and its code.
func (ev *Evaluation) reject(code, reason string) {
	ev.RejectCode, ev.RejectReason = code, reason
}

// Engine scores releases using a catalog of custom formats.
type Engine struct {
	formats map[string]CustomFormat
	// Ranking is how Decide orders eligible releases: RankingV1 (classic, also "") or
	// RankingV2 (ordered keys, rankv2.go). The service sets it from "quality_ranking",
	// which reads v2 unless "v1" was saved; "" here is only an engine built outside it.
	Ranking string
	// regex holds every release_title pattern the formats use, compiled once here rather
	// than per release (nil for a pattern that doesn't compile: it never matches).
	regex map[string]*regexp.Regexp
}

// NewEngine builds an engine over the given custom-format catalog.
func NewEngine(formats []CustomFormat) *Engine {
	m := make(map[string]CustomFormat, len(formats))
	for _, f := range formats {
		// Aliases are rewritten here, so what's stored stays as the user typed it.
		m[f.Name] = normalizeFormat(f)
	}
	return &Engine{formats: m, regex: compileFormats(m)}
}

// match reports whether f — one of this engine's normalised formats — holds for the
// release called name.
func (e *Engine) match(f CustomFormat, name string, r parser.Release) bool {
	return f.holds(name, r, e.regex)
}

// NewDefaultEngine uses the built-in custom formats.
func NewDefaultEngine() *Engine { return NewEngine(DefaultFormats()) }

// Evaluate scores one candidate against a profile.
func (e *Engine) Evaluate(p Profile, c Candidate) Evaluation {
	if p.onDisk || c.onDisk {
		// A file in the library (or the release it was converted from) keeps the language
		// reading it had before issue #58's new spellings, so no verdict on it moves.
		c.Release = c.Release.ForFile()
	}
	r := c.Release
	ev := Evaluation{Candidate: c}
	// The audio languages it carries, for the language rules and for the release row. Book
	// and music profiles (NoUnwanted) have none: their names are titles, not video tags.
	var audio langSet
	var lv langVerdict
	if !p.NoUnwanted {
		audio = c.audioLangs()
		ev.AudioLangs, ev.AudioLangsNote = audio.describe()
		lv = judgeLangs(p.languageRules(c), audio, p.mustReading(c))
		ev.langShort, ev.langPrefCodes, ev.langUnknown = lv.short, lv.prefs, lv.unknown
	}

	if len(p.AllowedResolutions) > 0 && !containsRes(p.AllowedResolutions, r.Resolution) {
		ev.reject(RejectResolution, fmt.Sprintf("Not in profile — %s", resLabel(r.Resolution)))
		return ev
	}
	if p.MinSource != "" {
		// An unstated source isn't a cam: it passes any minimum up to WEB and is only
		// refused where the profile insists on a disc, with a reason that says why.
		if r.Source == parser.SourceUnknown {
			if sourceTier(p.MinSource) > webTier {
				ev.reject(RejectSourceFloor, fmt.Sprintf("Source isn't stated in the name — this profile needs %s or better", minSourceLabel(p.MinSource)))
				return ev
			}
		} else if sourceTier(r.Source) < sourceTier(p.MinSource) {
			ev.reject(RejectSourceFloor, fmt.Sprintf("Not %s or better — this is %s", minSourceLabel(p.MinSource), sourceLabel(r.Source)))
			return ev
		}
	}
	if p.RejectPreRelease && r.Source == parser.SourceCAM {
		ev.reject(RejectPrerelease, "A cam, telesync or screener copy — this profile never grabs those")
		return ev
	}
	// The same profiles refuse a "WEB-DL" from before the film is out at home: a cam with a
	// false label. Never a file already in the library — that's the file check's to flag.
	if p.RejectPreRelease && !p.onDisk && !c.onDisk {
		if why, ok := c.window.notOutYet(r.Source); ok {
			ev.reject(RejectNotOutYet, why)
			return ev
		}
	}
	// The Unwanted pack: an item set to reject refuses the release by name, before any
	// ceiling (a disc image is always "over the ceiling", which would hide why).
	unwanted := p.unwantedHits(c)
	for _, h := range unwanted {
		if p.unwantedAction(h.item) == UnwantedReject && !p.onDisk {
			ev.reject(RejectUnwanted, h.reject)
			return ev
		}
	}
	// An unstated source can't be judged against a ceiling, so it passes one.
	if p.MaxSource != "" && r.Source != parser.SourceUnknown && sourceTier(r.Source) > sourceTier(p.MaxSource) {
		ev.reject(RejectSourceCeiling, fmt.Sprintf("Above your %s ceiling — this is %s", minSourceLabel(p.MaxSource), sourceLabel(r.Source)))
		ev.ceiling = minSourceLabel(p.MaxSource)
		return ev
	}
	// Bitrate ceiling (length-independent). Only applies when we know the runtime; without it
	// we can't turn a file size into a bitrate, so the cap is skipped rather than guessed.
	if limit := p.capFor(r.Resolution); limit > 0 {
		// The raw number, in the units the user typed. A ceiling is set to bound file
		// size or streaming bandwidth, and for that a bit is a bit; judging it in
		// H.264-equivalent terms threw out 25 Mbps HEVC releases as "over 40". The codec
		// equivalence stays where it belongs, in deciding what counts as an upgrade.
		if br := c.bitrateMbps(); br > limit {
			ev.reject(RejectBitrateCeiling, fmt.Sprintf("Over your %.0f Mbps ceiling (%.1f Mbps)", limit, br))
			ev.ceiling = fmt.Sprintf("%.0f Mb/s", limit)
			return ev
		}
	}
	if p.MinSeeders > 0 && !c.Usenet && c.Seeders < p.MinSeeders {
		ev.reject(RejectSeeders, fmt.Sprintf("Only %d seeders (needs %d)", c.Seeders, p.MinSeeders))
		return ev
	}
	lcName := strings.ToLower(c.Name)
	for _, term := range p.Rejected {
		if containsTerm(lcName, strings.ToLower(strings.TrimSpace(term))) {
			ev.reject(RejectTerm, "Contains rejected term: "+term)
			return ev
		}
	}

	if missing := e.missingRequired(p.Required, c.Name, r); missing != "" {
		ev.reject(RejectMissingRequired, "No "+missing+" — your profile requires it")
		return ev
	}
	// A language the profile requires: by the name for a release, by its probed tracks for
	// a file on disk. A file without one no longer fits, like any other Must. A release on
	// offer must also say it: one that only might carry it is refused (lv.unnamed).
	if !p.noLangGate {
		if len(lv.missing) > 0 {
			ev.reject(RejectMissingLanguage, "Missing "+langNames(lv.missing)+" audio — your profile requires it")
			return ev
		}
		if len(lv.unnamed) > 0 {
			ev.reject(RejectMissingLanguage, unnamedReason(lv.unnamed, r, baseLang(c.titleLang()) != "", c.tested))
			return ev
		}
	}

	ev.QualityScore = qualityScore(r)
	// tierAvoidScore is the (negative) part of FormatScore that came from built-in formats
	// set to Avoid — the target's and the "Other formats" list's. The avoided tier is how
	// those are honoured, so the minimum-score floor below leaves them out — see there.
	tierAvoidScore := 0
	for name, score := range p.FormatScores {
		f, ok := e.formats[name]
		if !ok || !e.match(f, c.Name, r) {
			continue
		}
		ev.FormatScore += score
		target := isTargetFormat(name)
		if target {
			ev.TargetScore += score
		} else {
			ev.CustomScore += score
		}
		if score > 0 {
			ev.Matched = append(ev.Matched, name)
			ev.PreferBonus += score
			if target {
				ev.targetBonus += score
			}
		} else if score < 0 {
			ev.Avoided = true
			ev.AvoidedFormats = append(ev.AvoidedFormats, name)
			// A built-in format's Avoid button means what the target's does: the avoided
			// tier, under both rankings. Only a format the owner wrote is a plain score.
			if target || isBuiltinFormat(name) {
				ev.TargetAvoided = true
				tierAvoidScore += score
			} else {
				ev.CustomAvoided = true
			}
		}
	}
	for _, k := range p.Keywords {
		// Token-bounded like Rejected terms: keyword "cam" must not match
		// "American", "web" must not match inside "cobweb".
		if !containsTerm(lcName, strings.ToLower(strings.TrimSpace(k.Term))) {
			continue
		}
		ev.FormatScore += k.Score
		ev.CustomScore += k.Score
		if k.Score > 0 {
			ev.Matched = append(ev.Matched, k.Term)
			ev.PreferBonus += k.Score
		} else if k.Score < 0 {
			ev.CustomAvoided = true
		}
	}
	// A required format that carries no score still counts as matched, so the
	// recommendation can say the release has it.
	for _, name := range p.Required {
		// Only the ones the release has: of "HEVC or AV1", it carries one.
		if f, ok := e.formats[name]; ok && e.match(f, c.Name, r) && !containsStr(ev.Matched, name) {
			ev.Matched = append(ev.Matched, name)
		}
	}
	// The preferred and required languages it has, named like formats ("German audio").
	for _, code := range lv.prefs {
		ev.langMatched = append(ev.langMatched, langLabel(code))
	}
	ev.Matched = append(ev.Matched, ev.langMatched...)
	sort.Strings(ev.Matched) // stable output
	sort.Strings(ev.AvoidedFormats)

	// Under the resolution's bitrate floor: a starved encode. Still grabbable, but in the
	// avoided tier — anything comfortably inside the window is picked first.
	if w, ok := p.Windows[ResolutionKey(r.Resolution)]; ok && w.Min > 0 {
		if br := c.bitrateMbps(); br > 0 && br < w.Min {
			ev.Avoided = true
			ev.TargetAvoided = true
			// Read as "it has <this>, which you avoid" in the decision's explanation.
			ev.AvoidedFormats = append(ev.AvoidedFormats, fmt.Sprintf("a bitrate under your %.0f Mbps floor", w.Min))
		}
	}
	// Unwanted items set to avoid join the avoided tier under their own names ("it has
	// Low-quality group (YTS), which you avoid"), never as a hidden score.
	// Every language it carries is one the profile avoids: the avoided tier, named.
	if lv.avoided {
		ev.Avoided = true
		ev.TargetAvoided = true
		ev.langAvoided = true
		ev.AvoidedFormats = append(ev.AvoidedFormats, "only "+langNames(audio.codes)+" audio")
	}
	for _, h := range unwanted {
		// The language rules already avoid it; the foreign-only item would say it twice.
		if h.item == UnwantedForeign && lv.avoided {
			continue
		}
		if a := p.unwantedAction(h.item); a == UnwantedAvoid || (a == UnwantedReject && p.onDisk) {
			ev.Avoided = true
			ev.TargetAvoided = true
			ev.lowGroup = ev.lowGroup || h.item == UnwantedLowQualityGroup
			ev.AvoidedFormats = append(ev.AvoidedFormats, h.avoid)
		}
	}

	// The minimum score is a floor on what the release earns — preferences, custom formats
	// and keywords. An Avoid on a built-in format (the target's, or one in "Other formats")
	// isn't part of that: it's a tier ("grabbed only when nothing else is available"), like
	// the bitrate floor and the Unwanted pack's avoids, which carry no score at all.
	// Counting its −50 here turned Avoid into Reject at the default floor of 0, so a Dolby
	// Vision or an AMZN release was refused even when it was all there was. A negative
	// custom format or keyword the owner wrote is a plain score and still counts — that is
	// the deliberate way to push something under the floor.
	if ev.FormatScore-tierAvoidScore < p.MinFormatScore {
		ev.reject(RejectMinFormatScore, "Below the profile's minimum format score")
		return ev
	}
	// Each preferred or required language it has scores like a preferred target format —
	// after the floor above, so a language rule never lets in a release the minimum score
	// refused. It isn't a bonus Decide waives for a thin bitrate: the language is the point.
	if n := len(lv.prefs); n > 0 {
		ev.FormatScore += n * preferScore
		ev.TargetScore += n * preferScore
		ev.langPrefs = n
	}
	// An avoided language costs what an avoided target format does, so under classic
	// ranking leaving it is a preferences gain too (under v2 the avoided tier says it).
	if lv.avoided {
		ev.FormatScore -= preferScore
		ev.TargetScore -= preferScore
	}

	// Any positive SmallBias nudges the score per GB — a mild bias (0.15) is a
	// gentle lean toward smaller files, a strong one (8) is "smallest decent".
	// SmallBias == 0 leaves size out of the score entirely (ties then go to the
	// bigger, higher-bitrate file in Decide).
	if p.SmallBias > 0 {
		ev.SizeScore = -int(c.SizeGB * p.SmallBias)
	}
	ev.Total = ev.QualityScore + ev.FormatScore + ev.SizeScore
	ev.fillRankKeys(p)
	ev.Eligible = true
	return ev
}

// containsTerm reports whether term appears in name as a whole token — bounded by non-alphanumeric
// characters (the "." / " " / "-" / "_" that separate release-name parts) or the string ends. This
// keeps a reject term like "com" (the .com executable extension) from matching inside "Complete".
//
// Both sides are compared with their separators folded to spaces, so "Directors Cut" matches
// "Directors.Cut" and "WEB-DL" matches "WEB.DL".
func containsTerm(name, term string) bool {
	name, term = foldSeparators(name), foldSeparators(term)
	if term == "" {
		return false
	}
	for start := 0; ; {
		i := strings.Index(name[start:], term)
		if i < 0 {
			return false
		}
		i += start
		leftOK := i == 0 || !isAlnum(name[i-1])
		end := i + len(term)
		rightOK := end >= len(name) || !isAlnum(name[end])
		if leftOK && rightOK {
			return true
		}
		start = i + 1
	}
}

func isAlnum(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') }

// Decide ranks candidates and explains the winner.
func (e *Engine) Decide(p Profile, cands []Candidate) Decision {
	var d Decision
	for _, c := range cands {
		ev := e.Evaluate(p, c)
		if ev.Eligible {
			d.Eligible = append(d.Eligible, ev)
		} else {
			d.Rejected = append(d.Rejected, ev)
		}
	}
	waiveCollapsedBonuses(d.Eligible)
	d.Ranking = NormalizeRanking(e.Ranking)
	if d.Ranking == RankingV2 {
		sortV2(p, d.Eligible)
	} else {
		sortV1(p, d.Eligible)
	}
	// Every rank explained (explain.go), from the comparator that actually ordered it.
	d.DecidedBy = explainRanks(p, d.Eligible, d.Ranking)
	if len(d.Eligible) > 0 {
		d.Winner = &d.Eligible[0]
		d.Why = whyReasons(p, *d.Winner, d.Eligible)
		if d.DecidedBy == KeyNetwork {
			d.Why = append(d.Why, p.networkWhy())
		}
		// Every eligible release carries a format you avoid — say so, so a Dolby Vision
		// recommendation doesn't look like the avoid was ignored when it's simply all
		// there was. Under v2 only the avoids that are a tier count: a negative custom
		// score there is a score.
		avoided := d.Winner.Avoided
		if d.Ranking == RankingV2 {
			avoided = d.Winner.TargetAvoided
		}
		if avoided {
			d.Why = append([]string{"Only releases with a format you avoid were available"}, d.Why...)
		}
		if len(d.Eligible) > 1 {
			ru := d.Eligible[1]
			var reason string
			if d.Ranking == RankingV2 {
				reason = v2Reason(p, ru.LostOn, *d.Winner, ru)
			} else {
				_, reason = decidingFactor(p, *d.Winner, ru)
			}
			d.ChosenOver = fmt.Sprintf("Chosen over the %s — %s", releaseLabel(ru.Candidate.Release), reason)
		}
	}
	return d
}

// sortV1 is classic ranking: the avoided tier, dead torrents last, then the summed score,
// the preferred network (when one is set), then magnitude (seeders deciding within 10%),
// source and seeders — seeders only ever between two torrents. It fills each release's
// LostOn from decidingFactor, which walks the same steps.
func sortV1(p Profile, evs []Evaluation) {
	// Rank by score, then by bitrate. For one movie every candidate is the same
	// runtime, so a larger file = higher bitrate = better — unless the profile
	// expresses any small-size preference, in which case smaller wins the tie.
	preferSmaller := p.SmallBias > 0
	sort.SliceStable(evs, func(i, j int) bool {
		a, b := evs[i], evs[j]
		// Avoided releases are a strictly lower tier: a release carrying an avoided format
		// never out-ranks one that doesn't, no matter how much better its resolution,
		// source or bitrate is. This is what makes "Avoid" mean "don't pick it while there's
		// an alternative" rather than a penalty a nicer release can outweigh.
		if a.Avoided != b.Avoided {
			return !a.Avoided // non-avoided first
		}
		// A dead torrent (no seeders, or an indexer that doesn't say) never downloads, so
		// within a tier it goes after every seeded release however well it scores. Ranks
		// only: it's still the pick when nothing seeded is eligible. A usenet release is
		// never dead.
		if a.Candidate.alive() != b.Candidate.alive() {
			return a.Candidate.alive()
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		// "Prefer Usenet" / "Prefer torrents": after the score, before size — a preference
		// between equals, never a filter.
		if decided, aFirst := p.networkOrder(a.Candidate, b.Candidate); decided {
			return aFirst
		}
		seeders := seedersComparable(a.Candidate, b.Candidate)
		if am, bm := magnitudes(a.Candidate, b.Candidate); am != bm {
			// Releases within 10% of each other are the same encode for every practical
			// purpose, so health decides: a 10 GB release with 2 seeders losing to a 9.5 GB
			// one with 150 is the right call. Outside the band, size (bitrate) still rules.
			if seeders && nearEqual(am, bm) && a.Candidate.Seeders != b.Candidate.Seeders {
				return a.Candidate.Seeders > b.Candidate.Seeders
			}
			if preferSmaller {
				return am < bm
			}
			return am > bm // higher bitrate
		}
		// Same bitrate → prefer the better source, then more seeders (torrents only).
		if sourceRank[a.Candidate.Release.Source] != sourceRank[b.Candidate.Release.Source] {
			return sourceRank[a.Candidate.Release.Source] > sourceRank[b.Candidate.Release.Source]
		}
		return seeders && a.Candidate.Seeders > b.Candidate.Seeders
	})
	fillLostOn(evs, func(above, ev *Evaluation) RankKey {
		if k, _ := decidingFactor(p, *above, *ev); k != "" {
			return k
		}
		return KeyTie
	})
}

// nearEqualBand is how close two magnitudes must be for seeders to decide between them.
const nearEqualBand = 0.10

// nearEqual reports whether a and b are within nearEqualBand of the larger.
func nearEqual(a, b float64) bool {
	hi, lo := a, b
	if lo > hi {
		hi, lo = lo, hi
	}
	return hi > 0 && hi-lo <= hi*nearEqualBand
}

// magnitudes is what an equal-score tie compares: the two bitrates when both runtimes are
// known, else the two sizes. For one movie every candidate has the same runtime, so the
// order is the size order it always was. Series candidates cover different lengths — an
// episode, a season, the whole show — and comparing raw size there let a huge pack beat a
// better-encoded episode just by holding more of them.
func magnitudes(a, b Candidate) (float64, float64) {
	if a.RuntimeMin > 0 && b.RuntimeMin > 0 {
		return a.bitrateMbps(), b.bitrateMbps()
	}
	return a.SizeGB, b.SizeGB
}

// bonusCollapseRatio: a release keeps its preference bonuses only while its bitrate
// (in H.264-equivalent terms, so HEVC gets its due) is at least this fraction of the
// best eligible release's. Below it the preference is waived — a preferred format is
// a tie-breaker between comparable encodes, not a licence to pick a file with a
// fraction of the picture.
const bonusCollapseRatio = 1.0 / 3.0

// waiveCollapsedBonuses strips the preference bonuses from releases whose bitrate has
// collapsed against the best one on offer. Needs runtimes; without them nothing changes.
func waiveCollapsedBonuses(evs []Evaluation) {
	best := 0.0
	for _, ev := range evs {
		if br := ev.Candidate.effectiveBitrateMbps(); br > best {
			best = br
		}
	}
	if best <= 0 {
		return
	}
	for i := range evs {
		ev := &evs[i]
		if ev.PreferBonus <= 0 {
			continue
		}
		if br := ev.Candidate.effectiveBitrateMbps(); br > 0 && br < best*bonusCollapseRatio {
			ev.Total -= ev.PreferBonus
			ev.FormatScore -= ev.PreferBonus
			ev.TargetScore -= ev.targetBonus
			ev.CustomScore -= ev.PreferBonus - ev.targetBonus
			ev.BonusWaived = true
			// The preferences stopped counting, for the preferences key too — all but the
			// languages, which were never part of the bonus.
			ev.PrefCount = ev.langPrefs
		}
	}
}

func whyReasons(p Profile, e Evaluation, eligible []Evaluation) []string {
	r := e.Candidate.Release
	var out []string

	top := bestAllowedRes(p)
	if (top == parser.ResUnknown || r.Resolution == top) && sourceRank[r.Source] >= sourceRank[parser.SourceWebDL] {
		out = append(out, "Highest quality that matches your goal")
	} else {
		out = append(out, "Best available that fits your profile")
	}
	for _, m := range e.Matched {
		out = append(out, m+" — matched")
	}
	// Say what actually decided it: any small-size lean picks the smaller of equals, so
	// "highest bitrate" would be untrue however the ceiling is set — and so it is whenever
	// a preference picked a lighter file over a heavier one.
	switch {
	case p.SmallBias >= 4:
		out = append(out, "Smallest watchable size")
	case p.SmallBias > 0:
		out = append(out, "Best quality for the size")
	case !heaviest(e, eligible):
		if out[0] != "Best available that fits your profile" {
			out = append(out, "Best fit for your profile")
		}
	case p.capFor(r.Resolution) > 0:
		out = append(out, fmt.Sprintf("Highest bitrate under your %.0f Mbps ceiling", p.capFor(r.Resolution)))
	default:
		out = append(out, "Highest bitrate available")
	}
	return out
}

// heaviest reports whether e's bitrate (its size, without runtimes) is the highest of the
// eligible releases it competes with. Avoided releases don't compete with a clean winner:
// they were never in the running.
func heaviest(e Evaluation, eligible []Evaluation) bool {
	for _, o := range eligible {
		if o.Avoided && !e.Avoided {
			continue
		}
		if me, them := magnitudes(e.Candidate, o.Candidate); them > me {
			return false
		}
	}
	return true
}

// --- helpers --------------------------------------------------------------

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func containsRes(xs []parser.Resolution, v parser.Resolution) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func bestAllowedRes(p Profile) parser.Resolution {
	best := parser.ResUnknown
	for _, r := range p.AllowedResolutions {
		if resRank[r] > resRank[best] {
			best = r
		}
	}
	return best
}

func resLabel(r parser.Resolution) string {
	if r == parser.ResUnknown {
		return "unknown"
	}
	return string(r)
}

// sourceLabel names a source inside a sentence. An unstated one is said as such: "this is
// unknown" read like a cam, and a release that simply doesn't name its source isn't one.
func sourceLabel(s parser.Source) string {
	if s == parser.SourceUnknown {
		return "an unstated source"
	}
	return string(s)
}

// minSourceLabel names a MinSource/MaxSource setting by its tier: either WEB label means WEB.
func minSourceLabel(s parser.Source) string {
	if s == parser.SourceWebDL || s == parser.SourceWebRip {
		return "WEB"
	}
	return sourceLabel(s)
}

// releaseLabel is "1080p WEB-DL", or "1080p release" when the name states no source.
func releaseLabel(r parser.Release) string {
	if r.Source == parser.SourceUnknown {
		return resLabel(r.Resolution) + " release"
	}
	return resLabel(r.Resolution) + " " + string(r.Source)
}
