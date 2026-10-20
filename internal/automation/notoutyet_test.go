package automation

import (
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// Every reject code has words in the search log, so "12 labelled WEB-DL…" never shows as
// a bare code.
func TestEveryRejectCodeHasALabel(t *testing.T) {
	for _, code := range quality.RejectCodes {
		if ReasonLabel(code) == code {
			t.Errorf("reject code %q has no label", code)
		}
	}
}

// tagMovie hands the movie's release dates to the profile once they've been read: a WEB-DL
// on offer weeks before the home release is refused, with the film named.
func TestTagMovieRefusesWebDLBeforeHomeRelease(t *testing.T) {
	home := time.Now().AddDate(0, 0, 21).Format(time.DateOnly)
	m := movies.Movie{Title: "Resident Evil", Year: 2026, Extra: &movies.MovieExtra{
		DatesRead: true, HomeReleaseDate: home, TheatricalDate: time.Now().AddDate(0, 0, -10).Format(time.DateOnly)}}
	cands := tagMovie([]quality.Candidate{quality.NewCandidate("Resident.Evil.2026.1080p.WEB-DL.DDP5.1.H.264-GRP", 4, 80)}, m)
	ev := quality.NewDefaultEngine().Evaluate(quality.Profile{RejectPreRelease: true}, cands[0])
	if ev.RejectCode != quality.RejectNotOutYet {
		t.Fatalf("code = %q (%q), want not_out_yet", ev.RejectCode, ev.RejectReason)
	}

	// Dates not read yet: nothing to judge by, so nothing refused for it.
	m.Extra.DatesRead = false
	cands = tagMovie([]quality.Candidate{quality.NewCandidate("Resident.Evil.2026.1080p.WEB-DL.DDP5.1.H.264-GRP", 4, 80)}, m)
	if ev := quality.NewDefaultEngine().Evaluate(quality.Profile{RejectPreRelease: true}, cands[0]); !ev.Eligible {
		t.Fatalf("unread dates refused it: %q", ev.RejectReason)
	}
}
