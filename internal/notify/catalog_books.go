package notify

import (
	"fmt"
	"strings"
)

// EventAuthorRelease: the check-author-releases job added new books by a followed author.
// No bus topic: the job emits it with EmitOnce, one alert per author per check, so a
// restart can't send it twice. It is an admin alert; requesters hear about books through
// their own requests.
const EventAuthorRelease = "book.author_release"

func init() {
	Register(EventDef{
		Key: EventAuthorRelease, Group: GroupLibrary, DefaultOn: true, Module: "books",
		Label: "New books by followed authors", Hint: "A followed author has a new book, and it was added to the library.",
		Format: formatAuthorRelease,
	})
}

// formatAuthorRelease writes "2 new books from Brandon Sanderson". Payload: author_id,
// author, count and titles (the added books' titles, in order).
func formatAuthorRelease(d map[string]any) (Message, bool) {
	author := strings.TrimSpace(str(d, "author"))
	titles := strs(d, "titles")
	n := num(d, "count")
	if n < int64(len(titles)) {
		n = int64(len(titles))
	}
	if author == "" || n < 1 || len(titles) == 0 {
		return Message{}, false
	}
	m := Message{Title: "New book", Link: link("/books/authors/", num(d, "author_id"))}
	if n == 1 {
		m.Body = fmt.Sprintf("📚 New from %s: %s", author, titles[0])
		return m, true
	}
	m.Title = "New books"
	m.Body = fmt.Sprintf("📚 %d new books from %s: %s", n, author, listSome(titles, int(n), 3))
	return m, true
}
