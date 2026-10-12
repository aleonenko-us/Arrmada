package quality

import "testing"

// A custom format's avoid goes through the engine's own matching (Match any/all, NOT, a
// release-title regex), so CustomAvoided is set exactly when the format matches.
func TestCustomAvoidedFollowsFormatMatching(t *testing.T) {
	p, e := storedFixture(StoredProfile{Name: "cf", MinFormatScore: -100,
		CustomFormats: []CustomFormat{
			{Name: "Either", Match: MatchAny, Conditions: []Condition{
				{Type: CondReleaseGroup, Value: "AAA"}, {Type: CondReleaseTitle, Value: `(?i)\bhardsub\b`}}},
			{Name: "Not BluRay HEVC", Conditions: []Condition{
				{Type: CondSource, Value: "BluRay", Negate: true}, {Type: CondCodec, Value: "x265"}}},
		},
		FormatScores: map[string]int{"Either": -5, "Not BluRay HEVC": -5}})
	cases := []struct {
		name string
		want bool
	}{
		{"Film.2021.1080p.WEB-DL.x264-AAA", true},         // any: the group
		{"Film.2021.1080p.WEB-DL.HardSub.x264-BBB", true}, // any: the title regex
		{"Film.2021.1080p.WEB-DL.x264-BBB", false},        // neither
		{"Film.2021.1080p.WEB-DL.x265-BBB", true},         // all: not BluRay, and HEVC
		{"Film.2021.1080p.BluRay.x265-BBB", false},        // NOT fails on BluRay
	}
	for _, tc := range cases {
		ev := e.Evaluate(p, NewCandidate(tc.name, 8, 10))
		if !ev.Eligible || ev.CustomAvoided != tc.want || ev.TargetAvoided {
			t.Errorf("%s: eligible %v custom-avoided %v target-avoided %v, want custom %v",
				tc.name, ev.Eligible, ev.CustomAvoided, ev.TargetAvoided, tc.want)
		}
	}
}
