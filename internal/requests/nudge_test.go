package requests

import (
	"context"
	"fmt"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// A grab for a requested film tells the open request views of its requester and
// followers — and nobody else — with ids and status only, so a strip moves from
// "Searching" to "Downloading" without waiting for its next poll.
func TestRequestsChangedPublished(t *testing.T) {
	s := newTestService(t)
	mv := newFakeMovies(movies.Movie{TMDBID: 42, Title: "Heat"}, movies.Movie{TMDBID: 99, Title: "Ran"}, movies.Movie{TMDBID: 5, Title: "Alien"})
	s.movies = mv
	ctx := context.Background()

	heat, err := s.repo.Create(ctx, Request{MediaType: "movie", TMDBID: 42, Title: "Heat", Status: StatusApproved, RequestedBy: 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.AddSubscriber(ctx, heat.ID, 8, "bob"); err != nil {
		t.Fatal(err)
	}
	// Someone else's request, for another film: never part of Heat's nudge.
	if _, err := s.repo.Create(ctx, Request{MediaType: "movie", TMDBID: 99, Title: "Ran", Status: StatusApproved, RequestedBy: 9}); err != nil {
		t.Fatal(err)
	}
	// Delivered already: nothing left to move.
	alien, err := s.repo.Create(ctx, Request{MediaType: "movie", TMDBID: 5, Title: "Alien", Status: StatusApproved, RequestedBy: 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MarkReady(ctx, alien.ID, 1); err != nil {
		t.Fatal(err)
	}

	s.bus = eventbus.New(nil)
	events, cancel := s.bus.Subscribe("*")
	defer cancel()

	heatMovie, _ := mv.GetByTMDB(ctx, 42)
	s.NudgeItem(ctx, "movie", heatMovie.ID)
	if got := drain(t, events); fmt.Sprint(got) != "[request.updated approved user.7.request.updated approved user.8.request.updated approved]" {
		t.Errorf("grab nudge: %v", got)
	}

	alienMovie, _ := mv.GetByTMDB(ctx, 5)
	s.NudgeItem(ctx, "movie", alienMovie.ID)
	s.NudgeItem(ctx, "movie", 12345) // not in the library
	s.NudgeItem(ctx, "music", 1)     // no requests for music
	if got := drain(t, events); len(got) != 0 {
		t.Errorf("delivered, unknown or unrequested items nudged: %v", got)
	}

	// The import consumer nudges too, before (and besides) any "ready".
	s.NudgeItem(ctx, "movie", heatMovie.ID)
	if got := drain(t, events); len(got) != 3 {
		t.Errorf("second nudge: %v", got)
	}
}

// A pending request isn't nudged by a grab (staff hear about pending ones when they change).
func TestNudgeSkipsPending(t *testing.T) {
	s := newTestService(t)
	mv := newFakeMovies(movies.Movie{TMDBID: 42, Title: "Heat"})
	s.movies = mv
	ctx := context.Background()
	if _, err := s.repo.Create(ctx, Request{MediaType: "movie", TMDBID: 42, Title: "Heat", Status: StatusPending, RequestedBy: 7}); err != nil {
		t.Fatal(err)
	}
	s.bus = eventbus.New(nil)
	events, cancel := s.bus.Subscribe("*")
	defer cancel()
	m, _ := mv.GetByTMDB(ctx, 42)
	s.NudgeItem(ctx, "movie", m.ID)
	if got := drain(t, events); len(got) != 0 {
		t.Errorf("pending request nudged: %v", got)
	}
}
