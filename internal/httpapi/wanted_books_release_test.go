package httpapi

import (
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// A wanted book that isn't out yet is Upcoming with its release day — the sweep won't
// search it — and one just out is due now. [BOOK-18]
func TestWantedUpcomingBook(t *testing.T) {
	s := wantedServer(t, nil)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	weekAgo := time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	mustExecAll(t, s,
		`INSERT INTO books (id, ol_key, title, author, monitored, release_date) VALUES (30, 'hc:30', 'Soon', 'A', 1, '`+future+`')`,
		// Out today, last searched (and missed) a week ago: due now, not on the ladder.
		`INSERT INTO books (id, ol_key, title, author, monitored, release_date, last_search_at, search_misses)
			VALUES (31, 'hc:31', 'Today', 'A', 1, '`+today+`', '`+weekAgo+`', 6)`,
	)
	w := getWanted(t, s, mgr, "?kind=book")
	if r, ok := rowFor(w.Upcoming, "book", 30); !ok || r.State != wantedNotReleased || r.AvailableAt != future {
		t.Errorf("upcoming book = %+v (listed %v), want not_released on %s", r, ok, future)
	}
	if _, ok := rowFor(w.Searching, "book", 30); ok {
		t.Error("an upcoming book is listed as searching")
	}
	if r, ok := rowFor(w.Searching, "book", 31); !ok || !r.Due {
		t.Errorf("book out today = %+v (listed %v), want due now", r, ok)
	}
}
