package quality

import "testing"

// Decide explains every eligible release after the first against the one ranked just above
// it, so each row of an interactive search can say why it isn't higher.
func TestDecideExplainsEveryRank(t *testing.T) {
	p := Profile{FormatScores: map[string]int{"HEVC": 50}}
	cands := []Candidate{
		NewCandidate("Film.2021.1080p.WEB-DL.x264-GRPE", 19, 2),  // ≈ D's size, fewer seeders
		NewCandidate("Film.2021.1080p.BluRay.x264-GRPC", 20, 10), // no HEVC
		NewCandidate("Film.2021.2160p.WEB-DL.x264-GRPA", 8, 10),  // the goal resolution
		NewCandidate("Film.2021.1080p.WEB-DL.x264-GRPD", 20, 10), // WEB-DL, not BluRay
		NewCandidate("Film.2021.1080p.WEB-DL.x265-GRPB", 6, 10),  // HEVC, but 1080p
		NewCandidate("Film.2021.720p.WEB-DL.x264-GRPX", 1, 10),   // a rejected term
	}
	p.Rejected = []string{"GRPX"}
	d := NewDefaultEngine().Decide(p, cands)
	want := []struct {
		group string
		key   RankKey
		text  string
	}{
		{"GRPA", "", ""},
		{"GRPB", KeyResolution, "lower resolution"},
		{"GRPC", KeyPreferences, "no HEVC, which you prefer"},
		{"GRPD", KeySource, "WEB-DL, not BluRay"},
		{"GRPE", KeyHealth, "fewer seeders"},
	}
	if len(d.Eligible) != len(want) {
		t.Fatalf("eligible %d, want %d (rejected %+v)", len(d.Eligible), len(want), d.Rejected)
	}
	for i, w := range want {
		ev := d.Eligible[i]
		if ev.Candidate.Release.Group != w.group || ev.LostOn != w.key || ev.LostOnText != w.text {
			t.Errorf("rank %d: %s lost on (%q, %q), want %s (%q, %q)", i, ev.Candidate.Release.Group, ev.LostOn, ev.LostOnText, w.group, w.key, w.text)
		}
	}
	if d.DecidedBy != KeyResolution {
		t.Errorf("DecidedBy = %q, want resolution", d.DecidedBy)
	}
	for _, ev := range d.Rejected {
		if ev.LostOn != "" || ev.LostOnText != "" {
			t.Errorf("rejected %s carries a rank explanation", ev.Candidate.Name)
		}
	}

	// Two releases nothing tells apart: the second is an equal match, recorded as a tie
	// (every pick records its deciding key — "tie" is the order they came in).
	same := NewDefaultEngine().Decide(Profile{}, []Candidate{
		NewCandidate("Film.2021.1080p.WEB-DL.x264-GRPA", 8, 10), NewCandidate("Film.2021.1080p.WEB-DL.x264-GRPB", 8, 10)})
	if same.Eligible[1].LostOn != KeyTie || same.Eligible[1].LostOnText != equalMatch || same.DecidedBy != KeyTie {
		t.Errorf("tie: (%q, %q), decided by %q", same.Eligible[1].LostOn, same.Eligible[1].LostOnText, same.DecidedBy)
	}
}

// Under ranking v2 the rows explain v2's order, from its own comparator: the same release
// list reads differently because it is ranked differently.
func TestDecideExplainsV2Ranks(t *testing.T) {
	p := Profile{FormatScores: map[string]int{"HEVC": 50, "Atmos": 50, "HDR10": 50, "HDR10+": 50}}
	e := NewDefaultEngine()
	e.Ranking = RankingV2
	d := e.Decide(p, []Candidate{
		NewCandidate("Film.2021.1080p.BluRay.HDR10+.TrueHD.Atmos.x265-FHD", 14, 50),
		NewCandidate("Film.2021.2160p.WEB-DL.DDP5.1.H.265-UHD", 20, 50),
	})
	if d.Winner == nil || d.Winner.Candidate.Release.Group != "UHD" || d.DecidedBy != KeyResolution ||
		d.Eligible[1].LostOn != KeyResolution || d.Eligible[1].LostOnText != "lower resolution" {
		t.Fatalf("v2: winner %+v decided by %q, runner-up (%q, %q)", d.Winner, d.DecidedBy, d.Eligible[1].LostOn, d.Eligible[1].LostOnText)
	}
	// A lone release was the only one.
	if one := e.Decide(p, []Candidate{NewCandidate("Film.2021.2160p.WEB-DL.H.265-UHD", 20, 50)}); one.DecidedBy != KeyOnly {
		t.Fatalf("lone: %q", one.DecidedBy)
	}
}
