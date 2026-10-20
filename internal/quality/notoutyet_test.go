package quality

import (
	"strings"
	"testing"
	"time"
)

func TestNotOutYet(t *testing.T) {
	e := NewDefaultEngine()
	now := time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC)
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	strict := Profile{RejectPreRelease: true} // a profile that doesn't allow pre-release
	cases := []struct {
		name    string
		p       Profile
		release string
		w       ReleaseWindow
		reject  bool
		reason  string
	}{
		{"web-dl well before home release", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-12")}, true,
			"Says WEB-DL, but Resident Evil isn't out at home until 12 Mar — a copy this early is almost always a cam with a false label"},
		{"bluray before home release", strict, "Resident.Evil.2026.1080p.BluRay.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-12")}, true, "Says BluRay"},
		{"hdtv before home release", strict, "Resident.Evil.2026.720p.HDTV.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-12")}, true, "Says HDTV"},
		{"inside the 3-day tolerance", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-04")}, false, ""},
		{"just outside the tolerance", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-05")}, true, "Says WEB-DL"},
		{"out at home already", strict, "Resident.Evil.2026.1080p.WEBRip.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-02-01"), Theatrical: day("2026-01-01")}, false, ""},
		{"profile allows pre-release", Profile{}, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-12")}, false, ""},
		{"unstated source is not judged", strict, "Resident.Evil.2026.1080p.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Home: day("2026-03-12")}, false, ""},
		{"no home date, in cinemas two weeks", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Theatrical: day("2026-02-14")}, true,
			"Says WEB-DL, but Resident Evil only reached cinemas on 14 Feb and isn't out at home yet"},
		{"no home date, not in cinemas yet", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Theatrical: day("2026-04-03")}, true, "Says WEB-DL, but Resident Evil isn't even in cinemas until 3 Apr"},
		{"no home date, in cinemas 33 days", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Theatrical: day("2026-01-27")}, true, "Says WEB-DL, but Resident Evil only reached cinemas on 27 Jan"},
		{"no home date, cinemas over 60 days ago", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil", Theatrical: day("2025-12-15")}, false, ""},
		{"no dates known", strict, "Resident.Evil.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Resident Evil"}, false, ""},
		{"next year's date names the year", strict, "Film.2026.1080p.WEB-DL.x264-GRP",
			ReleaseWindow{Title: "Film", Home: day("2027-01-08")}, true, "Says WEB-DL, but Film isn't out at home until 8 Jan 2027"},
	}
	for _, tc := range cases {
		tc.w.Now = now
		c := NewCandidate(tc.release, 4, 50).WithReleaseWindow(tc.w)
		ev := e.Evaluate(tc.p, c)
		if got := ev.RejectCode == RejectNotOutYet; got != tc.reject {
			t.Errorf("%s: rejected as not out yet = %v, want %v (code %q, reason %q)", tc.name, got, tc.reject, ev.RejectCode, ev.RejectReason)
			continue
		}
		if tc.reject && !strings.HasPrefix(ev.RejectReason, tc.reason) {
			t.Errorf("%s: reason = %q, want it to start %q", tc.name, ev.RejectReason, tc.reason)
		}
	}
}

// A real cam is still refused as a cam (its own reason), and a file in the library is never
// refused by the release window: it's already here.
func TestNotOutYetLeavesCamsAndFilesAlone(t *testing.T) {
	e := NewDefaultEngine()
	w := ReleaseWindow{Title: "Film", Home: time.Now().Add(60 * 24 * time.Hour)}
	if ev := e.Evaluate(Profile{RejectPreRelease: true}, NewCandidate("Film.2026.HDCAM.x264-GRP", 2, 50).WithReleaseWindow(w)); ev.RejectCode != RejectPrerelease {
		t.Errorf("cam: code %q", ev.RejectCode)
	}
	onDisk := Profile{RejectPreRelease: true, onDisk: true}
	if ev := e.Evaluate(onDisk, NewCandidate("Film.2026.1080p.WEB-DL.x264-GRP", 4, 50).WithReleaseWindow(w)); ev.RejectCode == RejectNotOutYet {
		t.Errorf("a file on disk was refused: %q", ev.RejectReason)
	}
}
