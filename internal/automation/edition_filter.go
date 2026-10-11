package automation

import (
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// Editions are a real requirement when a movie track is searched (MOV-13). A Director's
// Cut track used to grab whatever release scored best — usually the theatrical one
// already on disk — and the importer then skipped it as already imported, so the track
// said "searching" forever and the same release came back every day.
//
// This is a candidate filter rather than a Must condition injected into the profile:
// stored profiles stay as they are, and the reason a release was left out reads plainly.

// offTrackReason is why an interactive list can't give cand to track v, in plain words.
func offTrackReason(cand quality.Candidate, v movies.Version, siblings []movies.Version) string {
	switch editionDrop(cand.Name, cand.Release, v, siblings) {
	case "edition":
		if v.Edition != "" {
			return "Not the " + v.Edition + " edition this version wants"
		}
		return "The " + cand.Release.Edition + " edition — another version of this movie gets it"
	case "sibling":
		return "Another version of this movie already has this release"
	}
	return ""
}

// editionDrop says why editionFilter leaves cand out for track v of a movie whose tracks
// are siblings (v among them, or not), or "" to keep it:
//
//   - a track with an Edition keeps only releases of that edition;
//   - a track without one leaves out releases of an edition another monitored track wants,
//     so the default track never takes the Director's Cut while a DC track exists;
//   - every track leaves out the release another track's file came from — it can't fill
//     two tracks.
func editionDrop(name string, rel parser.Release, v movies.Version, siblings []movies.Version) string {
	got := parser.NormalizeEdition(rel.Edition)
	if want := parser.NormalizeEdition(v.Edition); want != "" {
		if got != want {
			return "edition"
		}
	} else if got != "" {
		for _, o := range siblings {
			if o.ID != v.ID && o.Monitored && parser.NormalizeEdition(o.Edition) == got {
				return "edition"
			}
		}
	}
	key := normTitle(name)
	for _, o := range siblings {
		if o.ID != v.ID && o.SourceRelease != "" && normTitle(o.SourceRelease) == key {
			return "sibling"
		}
	}
	return ""
}

// editionFilter keeps the candidates track v may take (see editionDrop), marking the rest
// on the search's notes. A movie with only its default track and no editions passes
// through untouched.
func editionFilter(notes *searchNotes, cands []quality.Candidate, v movies.Version, siblings []movies.Version) []quality.Candidate {
	if len(siblings) <= 1 && v.Edition == "" {
		return cands
	}
	out := make([]quality.Candidate, 0, len(cands))
	for _, c := range cands {
		switch editionDrop(c.Name, c.Release, v, siblings) {
		case "edition":
			notes.mark(c.Name, DropWrongEdition)
		case "sibling":
			notes.mark(c.Name, DropOutOfScope)
		default:
			out = append(out, c)
		}
	}
	return out
}
