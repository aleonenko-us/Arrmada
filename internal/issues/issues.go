// Package issues is "Report a problem": a requester tells staff something is wrong with a
// title in the library ("episode 4 has no English audio"), staff fix it in one click from
// Requests → Issues, and everyone who reported it is told when it's fixed.
//
// An issue is about one library item — a movie, or one episode of a show — and one kind of
// problem. A second person reporting the same thing joins the open issue instead of opening
// another. Staff fix it by replacing the copy (blocklist the release and grab another) or by
// starting a subtitle job; the issue then waits in "fixing" and resolves itself when the new
// file lands (the import's outbox row) or the subtitle job finishes, with a backstop sweep
// for a missed signal.
package issues

import (
	"errors"
	"fmt"
)

// Media types an issue can be about.
const (
	MediaMovie  = "movie"
	MediaSeries = "series"
)

// Issue statuses.
const (
	StatusOpen      = "open"
	StatusFixing    = "fixing"
	StatusResolved  = "resolved"
	StatusDismissed = "dismissed"
)

// Fixes staff can start from an issue.
const (
	// ActionReplace blocklists the release the current file came from and searches for
	// another copy; the new file replaces the old one on import.
	ActionReplace = "replace"
	// ActionSubtitles queues a subtitle job that makes the kept languages again.
	ActionSubtitles = "subtitles"
)

// Categories are the kinds of problem a report can name, as the sheet offers them
// ("Picture", "Sound", "Subtitles", "Wrong file", "Something else").
var Categories = []string{"video", "audio", "subtitles", "wrong_file", "other"}

func validCategory(c string) bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// NoteMax is the longest note a reporter can leave, in characters.
const NoteMax = 500

// ResolutionMax is the longest note staff can resolve or dismiss with.
const ResolutionMax = 500

// maxPage caps one List page.
const maxPage = 200

// Errors the API maps to answers.
var (
	ErrNotFound = errors.New("issue not found")
	// ErrNotInLibrary: the title (or episode) isn't in the library, or isn't one a
	// requester may see. The two read the same, so a report can't probe for either.
	ErrNotInLibrary = errors.New("that title isn't in the library")
	// ErrNoFile: the item is in the library but has no file to have a problem with.
	ErrNoFile = errors.New("there's no file for that yet — nothing to report")
	// ErrClosed: the issue is already resolved or dismissed.
	ErrClosed = errors.New("that problem is already closed")
	// ErrNoFix: the fix isn't wired (no subtitles service, no search).
	ErrNoFix = errors.New("that fix isn't available")
)

// InvalidError is a report or action the caller got wrong (a bad category, a note too
// long); its message says what.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalid(format string, a ...any) error { return &InvalidError{fmt.Sprintf(format, a...)} }

// Issue is one reported problem.
type Issue struct {
	ID        int64  `json:"id"`
	MediaType string `json:"media_type"`
	LibraryID int64  `json:"library_id"`
	TMDBID    int    `json:"tmdb_id"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
	Category  string `json:"category"`
	// Note and ReportedBy are the first reporter's. Staff only: a requester who joined
	// someone else's issue sees their own note (MyNote), never the other person's.
	Note           string `json:"note,omitempty"`
	ReportedBy     int64  `json:"reported_by,omitempty"`
	ReportedByName string `json:"reported_by_name,omitempty"`
	Status         string `json:"status"`
	// Resolution is the note it was resolved or dismissed with: what the reporters are told.
	Resolution string `json:"resolution,omitempty"`
	// Action is the fix staff started (replace, subtitles), ActionNote what became of it
	// ("The subtitle job couldn't find any: …"). ActionFile, ActionJob: staff only.
	Action     string `json:"action,omitempty"`
	ActionNote string `json:"action_note,omitempty"`
	ActionFile string `json:"action_file,omitempty"`
	ActionSize int64  `json:"-"`
	ActionJob  int64  `json:"action_job,omitempty"`
	ActionAt   int64  `json:"action_at,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	ResolvedAt int64  `json:"resolved_at,omitempty"`
	ResolvedBy int64  `json:"-"`
	// ResolvedByName is who closed it ("" when it resolved itself). Staff only.
	ResolvedByName string `json:"resolved_by_name,omitempty"`
	// Reporters is everyone who reported it (staff only); ReporterCount how many.
	Reporters     []Reporter `json:"reporters,omitempty"`
	ReporterCount int        `json:"reporter_count"`
	// MyNote is the asking requester's own note, on the requester view.
	MyNote string `json:"my_note,omitempty"`

	toldAt int64
}

// Target is the library item an issue is about.
func (i Issue) Target() Target {
	return Target{MediaType: i.MediaType, LibraryID: i.LibraryID, Season: i.Season, Episode: i.Episode}
}

// Reporter is one person who reported an issue.
type Reporter struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Note   string `json:"note,omitempty"`
	At     int64  `json:"at"`
}

// Target is one library item: a movie (season and episode 0) or one episode of a show.
type Target struct {
	MediaType string
	LibraryID int64
	Season    int
	Episode   int
}

// what names a target's title for a sentence: "“Dune”", "“Severance” S01E04".
func what(title string, season, episode int) string {
	s := "“" + title + "”"
	if episode > 0 {
		s += fmt.Sprintf(" S%02dE%02d", season, episode)
	}
	return s
}

// Report is a requester's report of a problem.
type Report struct {
	MediaType string
	TMDBID    int
	Season    int
	Episode   int
	Category  string
	Note      string
	UserID    int64
	UserName  string
}

// List filters.
const (
	FilterOpen   = "open"   // open or fixing
	FilterClosed = "closed" // resolved or dismissed
)

// ListFilter narrows List. UserID > 0 keeps only the issues that person reported (the
// requester view); Status is FilterOpen, FilterClosed or "" for all.
type ListFilter struct {
	Status string
	UserID int64
	ID     int64
	Limit  int
}

// Counts is how many issues are open (open or fixing) and closed.
type Counts struct {
	Open   int `json:"open"`
	Closed int `json:"closed"`
}

// ShapeForRequester trims an issue list to what a requester may see of the issues they
// reported: their own note, the status and what they're told — never who else reported
// it, their notes, the file, or which staff member acted.
func ShapeForRequester(list []Issue, userID int64) {
	for k := range list {
		i := &list[k]
		i.MyNote = ""
		for _, r := range i.Reporters {
			if r.UserID == userID {
				i.MyNote = r.Note
			}
		}
		i.Note, i.ReportedBy, i.ReportedByName = "", 0, ""
		i.Reporters = nil
		i.ActionFile, i.ActionJob, i.ActionNote = "", 0, ""
		i.ResolvedByName = ""
		i.LibraryID = 0
	}
}
