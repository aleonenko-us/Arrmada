package quality

import (
	"fmt"
	"time"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// A WEB-DL, WEBRip, BluRay, HDTV or DVD copy of a film can't exist before the film is out
// at home — on a streaming service, on disc or on TV. One on offer earlier is nearly always
// a cam someone relabelled, and the name is all a grab can judge, so a profile that refuses
// cams refuses those too (RejectNotOutYet). Movies only: an episode is out the day it airs.
const (
	// NotOutYetTolerance lets a genuine early leak through: a store selling a few days
	// before the listed date, or TMDB a day out across time zones.
	NotOutYetTolerance = 3 * 24 * time.Hour
	// CinemaOnlyFor is how long after its cinema release a film with no home date known is
	// still treated as cinema-only. Almost no film reaches home sooner, and TMDB is often
	// slow to list the digital date.
	CinemaOnlyFor = 30 * 24 * time.Hour
)

// ReleaseWindow is what a grab knows about when a movie came out (WithReleaseWindow).
// The zero value knows nothing and refuses nothing.
type ReleaseWindow struct {
	// Title names the film in the reason ("Resident Evil").
	Title string
	// Home is the earliest digital, physical or TV release anywhere and Theatrical the
	// earliest cinema release; zero when unknown.
	Home, Theatrical time.Time
	// Now is the day to judge on; zero means today (tests pin it).
	Now time.Time
}

// WithReleaseWindow attaches the movie's release window, so a home-source release from
// before the film is out at home can be refused (RejectNotOutYet).
func (c Candidate) WithReleaseWindow(w ReleaseWindow) Candidate {
	c.window = w
	return c
}

// homeSource reports a source that only exists once a film is out at home.
func homeSource(s parser.Source) bool {
	switch s {
	case parser.SourceWebDL, parser.SourceWebRip, parser.SourceBluray, parser.SourceRemux, parser.SourceHDTV, parser.SourceDVD:
		return true
	}
	return false
}

// notOutYet returns why a release claiming source src can't be genuine yet, or ok=false
// when it can be (or nothing is known to say otherwise).
func (w ReleaseWindow) notOutYet(src parser.Source) (reason string, ok bool) {
	if !homeSource(src) || (w.Home.IsZero() && w.Theatrical.IsZero()) {
		return "", false
	}
	now := w.Now
	if now.IsZero() {
		now = time.Now()
	}
	title := w.Title
	if title == "" {
		title = "this film"
	}
	const tail = " — a copy this early is almost always a cam with a false label"
	if !w.Home.IsZero() {
		if now.Before(w.Home.Add(-NotOutYetTolerance)) {
			return fmt.Sprintf("Says %s, but %s isn't out at home until %s", src, title, shortDay(w.Home, now)) + tail, true
		}
		return "", false
	}
	// No home date known: only a film still in (or not yet in) cinemas is judged.
	if now.Before(w.Theatrical) {
		return fmt.Sprintf("Says %s, but %s isn't even in cinemas until %s", src, title, shortDay(w.Theatrical, now)) + tail, true
	}
	if now.Before(w.Theatrical.Add(CinemaOnlyFor)) {
		return fmt.Sprintf("Says %s, but %s only reached cinemas on %s and isn't out at home yet", src, title, shortDay(w.Theatrical, now)) + tail, true
	}
	return "", false
}

// shortDay is "12 Mar", with the year when it isn't now's.
func shortDay(d, now time.Time) string {
	if d.Year() != now.Year() {
		return d.Format("2 Jan 2006")
	}
	return d.Format("2 Jan")
}
