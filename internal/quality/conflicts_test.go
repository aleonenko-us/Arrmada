package quality

import (
	"strings"
	"testing"
)

// One case per rule, plus profiles that are fine and must stay quiet.
func TestConflicts(t *testing.T) {
	cases := []struct {
		name    string
		sp      StoredProfile
		wantRow string // "" = no conflict at all
		want    string // a fragment of the message
	}{
		{"prefer HDR10+ avoid HDR10", StoredProfile{Ideal: &IdealFile{HDR: map[string]string{"HDR10+": PrefWant, "HDR10": PrefAvoid}}},
			"hdr", "Avoiding HDR10 also avoids HDR10+ (it's HDR10 underneath)."},
		{"must HDR10+ avoid HDR10", StoredProfile{Ideal: &IdealFile{HDR: map[string]string{"HDR10+": PrefMust, "HDR10": PrefAvoid}}},
			"hdr", "also avoids HDR10+"},
		{"must DV avoid HDR10", StoredProfile{Ideal: &IdealFile{HDR: map[string]string{"DV": PrefMust, "HDR10": PrefAvoid}}},
			"hdr", "Most Dolby Vision releases have HDR10 underneath"},
		{"every codec avoided", StoredProfile{Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefAvoid, "av1": PrefAvoid, "h264": PrefAvoid}}},
			"codec", "Every codec is set to Avoid"},
		{"every HDR option avoided", StoredProfile{Ideal: &IdealFile{HDR: map[string]string{"HDR10+": PrefAvoid, "HDR10": PrefAvoid, "DV": PrefAvoid, "HLG": PrefAvoid, "SDR": PrefAvoid}}},
			"hdr", "Every HDR option is set to Avoid"},
		{"must AV1 with a BluRay minimum", StoredProfile{MinSource: "BluRay", Ideal: &IdealFile{Codec: map[string]string{"av1": PrefMust}}},
			"codec", "AV1 releases are almost all WEB"},
		{"must HDR up to DVD", StoredProfile{MaxSource: "DVD", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefMust}}},
			"hdr", "DVDs have no HDR"},
		{"floor above ceiling", StoredProfile{Ideal: &IdealFile{Bitrate: map[string]BitrateWindow{"2160p": {Min: 40, Max: 30}}}},
			"bitrate", "The 4K floor (40 Mb/s) is above its ceiling (30 Mb/s)"},
		{"must HEVC rejects x265", StoredProfile{Rejected: []string{"exe", "X265"}, Ideal: &IdealFile{Codec: map[string]string{"hevc": PrefMust}}},
			"codec", `HEVC is a Must, but "X265" is a rejected term`},

		{"clean profile", StoredProfile{MinSource: "WEB-DL", Rejected: []string{"exe"}, Ideal: &IdealFile{
			Codec: map[string]string{"hevc": PrefWant, "av1": PrefWant}, HDR: map[string]string{"HDR10+": PrefWant},
			Bitrate: map[string]BitrateWindow{"1080p": {Min: 5, Max: 15}}}}, "", ""},
		{"avoid DV alone", StoredProfile{Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}}, "", ""},
		{"must DV up to WEB", StoredProfile{MaxSource: "WEB-DL", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefMust}}}, "", ""},
		{"avoid both audio features", StoredProfile{Ideal: &IdealFile{Audio: map[string]string{"atmos": PrefAvoid, "lossless": PrefAvoid}}}, "", ""},
		{"no target", StoredProfile{}, "", ""},
	}
	for _, tc := range cases {
		got := tc.sp.Conflicts()
		if tc.wantRow == "" {
			if len(got) != 0 {
				t.Errorf("%s: unexpected conflicts %+v", tc.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Row != tc.wantRow || !strings.Contains(got[0].Msg, tc.want) {
			t.Errorf("%s: conflicts = %+v, want one under %s containing %q", tc.name, got, tc.wantRow, tc.want)
		}
	}
}
