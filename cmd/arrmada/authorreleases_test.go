package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/notify"
)

// The release check's follow-ups: one admin alert per author with the new books' titles,
// a search for each monitored book that is out (an upcoming one waits for the sweep), and
// no more searching once the indexers can't answer. [BOOK-21]
func TestAuthorReleaseJob(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local)
	type alert struct {
		key, dedupe string
		data        map[string]any
	}
	var alerts []alert
	var searched []int64
	outageAt := int64(0)
	job := authorReleaseJob{
		check: func(_ context.Context, limit int, profile string) (books.AuthorCheck, error) {
			if limit != authorReleaseLimit || profile != "standard" {
				t.Errorf("check(%d, %q)", limit, profile)
			}
			return books.AuthorCheck{Checked: 2, Releases: []books.AuthorRelease{
				{AuthorID: 3, Author: "Brandon Sanderson", Books: []books.Book{
					{ID: 10, Title: "Wind and Truth", Monitored: true, ReleaseDate: "2026-10-01"},
					{ID: 11, Title: "Isles of the Emberdark", Monitored: true, ReleaseDate: "2026-12-01"}, // not out yet
				}},
				{AuthorID: 4, Author: "Robin Hobb", Books: []books.Book{
					{ID: 20, Title: "A", Monitored: true},
					{ID: 21, Title: "B", Monitored: true},
				}},
			}}, nil
		},
		defaultProfile: func(context.Context) string { return "standard" },
		emit: func(_ context.Context, key, dedupe string, data map[string]any) (int, error) {
			alerts = append(alerts, alert{key, dedupe, data})
			return 1, nil
		},
		search: func(_ context.Context, id int64) (automation.SearchOutcome, error) {
			searched = append(searched, id)
			if id == outageAt {
				return automation.SearchOutcome{}, indexer.ErrNoIndexers
			}
			return automation.SearchOutcome{}, nil
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		now: func() time.Time { return now },
	}
	if err := job.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 || alerts[0].key != notify.EventAuthorRelease || alerts[0].dedupe != "author_release:3:10:2026-10-13" {
		t.Fatalf("alerts = %+v", alerts)
	}
	if m, ok := mustEvent(t).Format(alerts[0].data); !ok || m.Body != "📚 2 new books from Brandon Sanderson: Wind and Truth, Isles of the Emberdark" {
		t.Errorf("alert = %+v, %v", m, ok)
	}
	if len(searched) != 3 || searched[0] != 10 || searched[1] != 20 || searched[2] != 21 {
		t.Errorf("searched %v, want 10, 20, 21 (11 isn't out)", searched)
	}

	// The indexers are down at the first search: nothing more is searched, every alert
	// still goes out.
	alerts, searched, outageAt = nil, nil, 10
	if err := job.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(searched) != 1 || len(alerts) != 2 {
		t.Errorf("after an outage: searched %v, %d alerts", searched, len(alerts))
	}

	// A check error is the task's error, so the Tasks table shows the failure.
	job.check = func(context.Context, int, string) (books.AuthorCheck, error) {
		return books.AuthorCheck{}, errors.New("database is locked")
	}
	if err := job.run(context.Background()); err == nil {
		t.Error("a check error should fail the task")
	}
}

func mustEvent(t *testing.T) notify.EventDef {
	t.Helper()
	d, ok := notify.Lookup(notify.EventAuthorRelease)
	if !ok {
		t.Fatal("book.author_release isn't in the alert catalog")
	}
	return d
}
