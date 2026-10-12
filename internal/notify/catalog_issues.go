package notify

import (
	"fmt"
	"strings"
)

// EventIssueReported: someone reported a problem with a title in the library. No bus
// topic: the issues service emits it with EmitOnce when a new issue opens (a second
// person joining it says nothing), and the same words go to staff inboxes and Web Push.
const EventIssueReported = "issue.reported"

// The words for an issue's category, as the report sheet offers them.
var issueCategoryWords = map[string]string{
	"video": "picture", "audio": "sound", "subtitles": "subtitles", "wrong_file": "wrong file", "other": "something else",
}

func init() {
	Register(EventDef{
		Key: EventIssueReported, Group: GroupRequests, DefaultOn: true,
		Label: "Problem reported", Hint: "Someone reported a problem with a movie or episode in the library.",
		Format: formatIssueReported,
	})
}

// formatIssueReported writes the "Problem reported" alert. Payload: id, title, year,
// season and episode (0 for a movie), category, note and reported_by_name.
func formatIssueReported(d map[string]any) (Message, bool) {
	title := withYear(str(d, "title"), num(d, "year"))
	if title == "" {
		return Message{}, false
	}
	if ep := num(d, "episode"); ep > 0 {
		title = fmt.Sprintf("%s S%02dE%02d", str(d, "title"), num(d, "season"), ep)
	}
	who := str(d, "reported_by_name")
	if who == "" {
		who = "Someone"
	}
	what := issueCategoryWords[str(d, "category")]
	if what == "" {
		what = "something else"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s reported a problem with %s: %s.", who, title, what)
	if note := strings.TrimSpace(str(d, "note")); note != "" {
		fmt.Fprintf(&b, "\nNote: “%s”", clipText(note, 300))
	}
	m := Message{Title: "Problem reported", Body: b.String(), Link: "/requests?tab=issues"}
	if id := num(d, "id"); id > 0 {
		m.Link = fmt.Sprintf("/requests?tab=issues&issue=%d", id)
	}
	return m, true
}
