package quality

import (
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// seriesTemplate is the builder's "1080p efficient" template as a series profile, with the
// 1080p window given: HEVC or AV1 preferred, 1080p and 720p.
func seriesTemplate(w1080 BitrateWindow) StoredProfile {
	return StoredProfile{
		MediaType: MediaSeries, Name: "1080p efficient", AllowedResolutions: []string{"1080p", "720p"}, MinSeeders: 1,
		Ideal: &IdealFile{
			Codec:   map[string]string{"hevc": PrefWant, "av1": PrefWant},
			Bitrate: map[string]BitrateWindow{"1080p": w1080, "720p": {Min: 2, Max: 6}},
		},
	}
}

// A series profile is previewed on episode-length samples: the heavy BluRay episode is over
// a TV ceiling, a WEB release wins, and the fansub (no source in its name) is eligible.
func TestSeriesPreviewScoresEpisodes(t *testing.T) {
	s := &Service{}
	for _, tc := range []struct {
		window  BitrateWindow
		ceiling string
	}{
		{BitrateWindow{Min: 3, Max: 12}, "Over your 12 Mbps ceiling"}, // the template's TV window
		{BitrateWindow{Min: 5, Max: 15}, "Over your 15 Mbps ceiling"},
	} {
		d := s.Preview(seriesTemplate(tc.window))
		if d.Winner == nil {
			t.Fatalf("%v: no winner", tc.window)
		}
		if src := d.Winner.Candidate.Release.Source; src != parser.SourceWebDL && src != parser.SourceWebRip {
			t.Errorf("%v: winner %s isn't a WEB release", tc.window, d.Winner.Candidate.Name)
		}
		if !strings.Contains(d.Winner.Candidate.Name, "Harbor") || d.Winner.Candidate.RuntimeMin != seriesEpisodeMin {
			t.Errorf("%v: winner %s (%d min) isn't an episode sample", tc.window, d.Winner.Candidate.Name, d.Winner.Candidate.RuntimeMin)
		}
		var heavy, fansub *Evaluation
		for i := range d.Rejected {
			if strings.Contains(d.Rejected[i].Candidate.Name, "BRASSWORK") {
				heavy = &d.Rejected[i]
			}
		}
		for i := range d.Eligible {
			if strings.Contains(d.Eligible[i].Candidate.Name, "[Tidepool]") {
				fansub = &d.Eligible[i]
			}
		}
		if heavy == nil || !strings.HasPrefix(heavy.RejectReason, tc.ceiling) {
			t.Errorf("%v: heavy BluRay episode = %+v, want %q", tc.window, heavy, tc.ceiling)
		}
		if fansub == nil {
			t.Errorf("%v: the fansub sample isn't eligible (rejected: %+v)", tc.window, d.Rejected)
		}
	}
}

// The movie preview is the film it always was, with the same pick.
func TestMoviePreviewUnchanged(t *testing.T) {
	sp := StoredProfile{
		MediaType: MediaMovie, Name: "4K HDR", AllowedResolutions: []string{"2160p", "1080p"}, MinSeeders: 1,
		Ideal: &IdealFile{
			Codec: map[string]string{"hevc": PrefWant, "av1": PrefWant}, HDR: map[string]string{"HDR10+": PrefWant},
			Audio:   map[string]string{"atmos": PrefWant},
			Bitrate: map[string]BitrateWindow{"2160p": {Min: 15, Max: 35}, "1080p": {Min: 5, Max: 15}},
		},
	}
	d := (&Service{}).Preview(sp)
	if d.Winner == nil || !strings.HasSuffix(d.Winner.Candidate.Name, "-FLUX") || d.Winner.Candidate.RuntimeMin != duneRuntimeMin {
		t.Fatalf("movie winner = %+v", d.Winner)
	}
	if n := len(d.Eligible) + len(d.Rejected); n != len(SampleCandidates()) {
		t.Errorf("judged %d samples, want the %d Dune releases", n, len(SampleCandidates()))
	}
	if _, label := Samples(MediaMovie); label != "Dune: Part Two (2024), 166 min" {
		t.Errorf("movie label %q", label)
	}
	if _, label := Samples(MediaSeries); label != "An episode of a 50-minute show" {
		t.Errorf("series label %q", label)
	}
}
