package quality

import (
	"strconv"
	"testing"
)

func TestPrefersDolbyVision(t *testing.T) {
	s, ctx := testService(t)
	ref := func(sp StoredProfile) string {
		t.Helper()
		got, err := s.Create(ctx, sp)
		if err != nil {
			t.Fatal(err)
		}
		return "custom:" + strconv.FormatInt(got.ID, 10)
	}
	plain := ref(StoredProfile{MediaType: MediaMovie, Name: "Plain"})
	wants := ref(StoredProfile{MediaType: MediaMovie, Name: "DV", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefWant}}})
	avoids := ref(StoredProfile{MediaType: MediaMovie, Name: "No DV", Ideal: &IdealFile{HDR: map[string]string{"DV": PrefAvoid}}})
	scored := ref(StoredProfile{MediaType: MediaSeries, Name: "Scored", FormatScores: map[string]int{"Dolby Vision": 50}})

	for _, c := range []struct {
		ref, media string
		want       bool
	}{
		{plain, MediaMovie, false},
		{wants, MediaMovie, true},
		{avoids, MediaMovie, false},
		{scored, MediaSeries, true},
		{"n/a", MediaMovie, false}, // resolves to the movie default: the first, Plain
		{"custom:999", MediaSeries, true},
	} {
		if got := s.PrefersDolbyVision(ctx, c.ref, c.media); got != c.want {
			t.Errorf("PrefersDolbyVision(%s, %s) = %v, want %v", c.ref, c.media, got, c.want)
		}
	}
}
