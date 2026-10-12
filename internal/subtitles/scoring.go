package subtitles

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// Scoring OpenSubtitles candidates against the file on disk. Only a hash match is in sync
// by construction; anything else was timed against some release, and the closer that
// release is to ours (same group, source, edition, frame rate) the likelier it fits. The
// most-downloaded upload is often a different cut or frame rate, so popularity only
// breaks ties.

const (
	keyMinScore     = "subs_min_score"
	defaultMinScore = 60
	// scoreFloor: below this a candidate is never used. Between it and the minimum, a
	// candidate is used only if a sync check against the audio passes (syncGate).
	scoreFloor     = 35
	hashMatchScore = 100
	// maxDownloadTries caps how many candidates one language may download when the sync
	// check keeps refusing them, so a bad batch can't drain the daily quota.
	maxDownloadTries = 3
)

// ErrBadMinScore is a minimum match score outside 0-100.
var ErrBadMinScore = errors.New("minimum match score must be between 0 and 100")

// MatchContext is what candidates are matched against: the file on disk.
type MatchContext struct {
	SourceRelease string  // the release name the file was imported from, when recorded
	FileBase      string  // the file's name without extension; used when SourceRelease is empty
	FPS           float64 // the video's frame rate from the probe; 0 = unknown
	Season        int     // 0 for movies
	Episode       int
	Forced        bool // the want is the forced (foreign-parts-only) subtitle, not the full one
	// SDH is the want's profile preference for hearing-impaired uploads: SDHAny (a plain
	// one wins a tie), SDHPrefer (an SDH one wins a tie) or SDHAvoid (never used).
	SDH string
}

func matchContextFor(ref fileRef) MatchContext {
	base := filepath.Base(ref.Path)
	return MatchContext{
		SourceRelease: ref.SourceRelease,
		FileBase:      strings.TrimSuffix(base, filepath.Ext(base)),
		FPS:           ref.FPS,
		Season:        ref.Season,
		Episode:       ref.Episode,
	}
}

// ScoredCandidate is a provider result with its match score and why. The interactive
// search (SUB-28) shows these as they are.
type ScoredCandidate struct {
	SubtitleResult
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"` // "+25 same group (FLUX)", "−40 25 vs 23.976 fps", …
	// Rejected says why the candidate can never be used for this want ("" = usable).
	Rejected    string `json:"rejected,omitempty"`
	Blocklisted bool   `json:"blocklisted,omitempty"`
}

func (c ScoredCandidate) usable() bool { return c.Rejected == "" && !c.Blocklisted }

// mismatches is the explanation for a weak score: the reasons that cost points.
func (c ScoredCandidate) mismatches() string {
	var out []string
	for _, r := range c.Reasons {
		if strings.HasPrefix(r, "−") {
			out = append(out, strings.TrimSpace(strings.TrimLeft(r, "−0123456789")))
		}
	}
	if len(out) == 0 {
		return "nothing in the release name matched this file"
	}
	return strings.Join(out, ", ")
}

// releaseOf parses a name, treating one that yields nothing useful as unknown.
func releaseOf(names ...string) (parser.Release, bool) {
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			continue
		}
		// An uploaded file name ends in its extension, which would read as part of the group.
		switch strings.ToLower(filepath.Ext(n)) {
		case ".srt", ".sub", ".ass", ".ssa", ".vtt", ".txt", ".mkv", ".mp4", ".avi", ".m4v":
			n = strings.TrimSuffix(n, filepath.Ext(n))
		}
		r := parser.Parse(n)
		if r.Source != "" || r.Group != "" || r.Resolution != "" || r.Edition != "" {
			return r, true
		}
	}
	return parser.Release{}, false
}

// sourceTier groups sources whose encodes share timing: a remux and a Blu-ray encode come
// from the same disc.
func sourceTier(s parser.Source) string {
	switch s {
	case parser.SourceRemux, parser.SourceBluray:
		return "BluRay"
	case parser.SourceWebDL, parser.SourceWebRip, parser.SourceHDTV, parser.SourceDVD:
		return string(s)
	}
	return ""
}

func fmtFPS(f float64) string {
	return strconv.FormatFloat(math.Round(f*1000)/1000, 'f', -1, 64)
}

// scoreCandidate rates how likely a non-hash candidate is to fit the file, 0-99; a hash
// match is 100. Each reason carries its signed points.
func scoreCandidate(c SubtitleResult, m MatchContext) (int, []string) {
	if c.HashMatch {
		return hashMatchScore, []string{"+100 hash match"}
	}
	score := 0
	var reasons []string
	add := func(pts int, why string) {
		score += pts
		sign := "+"
		if pts < 0 {
			sign, pts = "−", -pts
		}
		reasons = append(reasons, fmt.Sprintf("%s%d %s", sign, pts, why))
	}
	ref, refOK := releaseOf(m.SourceRelease, m.FileBase)
	cand, candOK := releaseOf(c.Release, c.FileName)
	if refOK && candOK {
		if ref.Group != "" && strings.EqualFold(ref.Group, cand.Group) {
			add(25, "same group ("+cand.Group+")")
		}
		if a, b := sourceTier(cand.Source), sourceTier(ref.Source); a != "" && b != "" {
			if a == b {
				add(20, "same source ("+b+")")
			} else {
				add(-15, a+" vs "+b)
			}
		}
		if ref.Resolution != "" && ref.Resolution == cand.Resolution {
			add(5, "same resolution")
		}
		// No edition in a name means the theatrical cut, so "Extended" against nothing is a
		// mismatch — but only when the file's name says anything at all.
		re, ce := parser.NormalizeEdition(ref.Edition), parser.NormalizeEdition(cand.Edition)
		switch {
		case re == ce && re != "":
			add(10, "same edition ("+cand.Edition+")")
		case re == ce:
			add(10, "same edition")
		default:
			add(-30, editionName(ce)+" vs "+editionName(re))
		}
	}
	if c.FPS > 0 && m.FPS > 0 {
		if math.Abs(c.FPS-m.FPS) <= 0.01 {
			add(15, "same frame rate")
		} else {
			add(-40, fmtFPS(c.FPS)+" vs "+fmtFPS(m.FPS)+" fps")
		}
	}
	if c.Trusted {
		add(5, "trusted uploader")
	}
	return max(0, min(score, hashMatchScore-1)), reasons
}

func editionName(e string) string {
	if e == "" {
		return "theatrical"
	}
	return e
}

// rejectReason is why a candidate can't be used for this want at all, or "".
func rejectReason(c SubtitleResult, m MatchContext) string {
	switch {
	case m.Season > 0 && c.Season > 0 && (c.Season != m.Season || (c.Episode > 0 && c.Episode != m.Episode)):
		return fmt.Sprintf("for S%02dE%02d, not this episode", c.Season, c.Episode)
	case c.AITranslated:
		return "machine-translated"
	case c.ForeignPartsOnly && !m.Forced:
		return "foreign parts only"
	case !c.ForeignPartsOnly && m.Forced:
		return "a full subtitle, not foreign parts"
	case c.HearingImpaired && m.SDH == SDHAvoid && !m.Forced:
		return "hearing-impaired (the profile avoids SDH)"
	}
	return ""
}

// rankCandidates scores every result, marks the blocklisted and rejected ones, and sorts
// best first: score, then a plain subtitle over a hearing-impaired one (the other way
// round when the profile prefers SDH), then downloads.
func rankCandidates(results []SubtitleResult, m MatchContext, blocked map[string]bool) []ScoredCandidate {
	out := make([]ScoredCandidate, 0, len(results))
	for _, r := range results {
		sc := ScoredCandidate{SubtitleResult: r, Rejected: rejectReason(r, m), Blocklisted: blocked[r.FileID]}
		sc.Score, sc.Reasons = scoreCandidate(r, m)
		if sc.Reasons == nil {
			sc.Reasons = []string{}
		}
		out = append(out, sc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.usable() != b.usable() {
			return a.usable()
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.HearingImpaired != b.HearingImpaired {
			return a.HearingImpaired == (m.SDH == SDHPrefer)
		}
		return a.Downloads > b.Downloads
	})
	return out
}

// minScore is the setting, read per job so a change applies to the next one.
func (s *Service) minScore(ctx context.Context) int {
	n, err := strconv.Atoi(strings.TrimSpace(s.settings.Get(ctx, keyMinScore, strconv.Itoa(defaultMinScore))))
	if err != nil || n < 0 || n > 100 {
		return defaultMinScore
	}
	return n
}

// SetMinScore saves the minimum match score a non-hash download needs (0-100).
func (s *Service) SetMinScore(ctx context.Context, n int) error {
	if n < 0 || n > 100 {
		return ErrBadMinScore
	}
	return s.settings.Set(ctx, keyMinScore, strconv.Itoa(n))
}

// blockedFiles is the provider file ids never to use again for this media file. The
// ledger (SUB-11) supplies them through s.blocklist; without it nothing is blocked.
func (s *Service) blockedFiles(ctx context.Context, mediaKey string) (map[string]bool, error) {
	if s.blocklist == nil || mediaKey == "" {
		return nil, nil
	}
	return s.blocklist(ctx, mediaKey)
}
