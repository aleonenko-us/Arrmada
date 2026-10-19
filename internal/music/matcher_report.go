package music

import "fmt"

// MatchReport is a match's explanation, stored on a review (import_reviews.match_json) so
// Reviews can say in words why a download is waiting: the candidates weighed, each one's
// distance and per-field breakdown, and the files it would leave.
type MatchReport struct {
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary"`
	// Expected says the download was grabbed for the best candidate's album; Threshold
	// is the distance that would have imported it automatically.
	Expected   bool              `json:"expected,omitempty"`
	Threshold  float64           `json:"threshold"`
	Candidates []ReportCandidate `json:"candidates,omitempty"`
	Unplaced   []ReportFile      `json:"unplaced,omitempty"`
	// Fingerprint says whether identifying the files by sound (AcoustID) was tried:
	// "used", or "off" when it would have been but no AcoustID key is set.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// ReportCandidate is one candidate in a report.
type ReportCandidate struct {
	AlbumID   int64  `json:"album_id"`
	ReleaseID int64  `json:"release_id"`
	Album     string `json:"album"`
	Artist    string `json:"artist,omitempty"`
	// Edition describes the edition: "GB CD 1997 · 12 tracks".
	Edition    string        `json:"edition"`
	Target     bool          `json:"target,omitempty"`
	Distance   float64       `json:"distance"`
	Fit        float64       `json:"fit"`
	Components []Component   `json:"components"`
	Tracks     []ReportTrack `json:"tracks,omitempty"`
	Missing    []string      `json:"missing,omitempty"` // tracks with no file: "2-04 Title"
	Extra      []string      `json:"extra,omitempty"`   // files with no track
}

// ReportTrack is one file↔track pair with something to say about it.
type ReportTrack struct {
	File     string   `json:"file"`
	Track    string   `json:"track"`
	Distance float64  `json:"distance"`
	Notes    []string `json:"notes,omitempty"`
}

// ReportFile is a file the match would leave in the download, and why.
type ReportFile struct {
	File string `json:"file"`
	Why  string `json:"why"`
}

// reportLimits bound a report so a 300-track box set doesn't store a novel on a review.
const (
	reportCandidates = 3
	reportTracks     = 40
	reportFiles      = 40
)

func (r *MatchResult) report(files []*MatchFile, in MatchInput, s MatchSettings) MatchReport {
	rep := MatchReport{Reason: r.Reason, Summary: r.Summary, Expected: r.Expected, Threshold: s.Strong}
	if r.Expected {
		rep.Threshold = s.Expected
	}
	// The winner first (it may not be ranked first when the target edition won a close
	// call), then the rest in rank order.
	order := make([]*Scored, 0, len(r.Ranked))
	if r.Best != nil {
		order = append(order, r.Best)
	}
	for _, sc := range r.Ranked {
		if sc != r.Best {
			order = append(order, sc)
		}
	}
	for _, sc := range order {
		if len(rep.Candidates) == reportCandidates {
			break
		}
		rep.Candidates = append(rep.Candidates, reportCandidate(sc, files))
	}
	for _, u := range r.Unplaced {
		if len(rep.Unplaced) == reportFiles {
			break
		}
		if u.Why == "held for review" {
			continue // every file is, when the whole download is held: nothing to add
		}
		rep.Unplaced = append(rep.Unplaced, ReportFile{File: u.File.Name(), Why: u.Why})
	}
	return rep
}

func reportCandidate(sc *Scored, files []*MatchFile) ReportCandidate {
	c := sc.Cand
	tracks := c.trackList()
	discs := c.discCount()
	rc := ReportCandidate{
		AlbumID: c.AlbumID, ReleaseID: c.Edition.ID, Album: c.AlbumTitle, Artist: c.Credit,
		Edition: EditionLabel(c.Edition), Target: c.Target, Distance: sc.Distance, Fit: sc.Fit,
		Components: sc.dist.components(),
	}
	for _, p := range sc.Pairs {
		if len(p.Notes) == 0 || len(rc.Tracks) == reportTracks {
			continue
		}
		rc.Tracks = append(rc.Tracks, ReportTrack{
			File: files[p.File].Name(), Track: tracks[p.Track].label(discs), Distance: p.Distance, Notes: p.Notes,
		})
	}
	for _, j := range sc.Missing {
		if len(rc.Missing) == reportTracks {
			rc.Missing = append(rc.Missing, fmt.Sprintf("and %d more", len(sc.Missing)-reportTracks))
			break
		}
		rc.Missing = append(rc.Missing, tracks[j].label(discs))
	}
	for _, i := range sc.Extra {
		if len(rc.Extra) == reportFiles {
			rc.Extra = append(rc.Extra, fmt.Sprintf("and %d more", len(sc.Extra)-reportFiles))
			break
		}
		rc.Extra = append(rc.Extra, files[i].Name())
	}
	return rc
}
