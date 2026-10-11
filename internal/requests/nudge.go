package requests

import (
	"context"
	"errors"

	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
)

// An approved request moves through searching → downloading → importing → adding without
// its status changing, so nothing announced those steps: a requester's Discover strip only
// caught up at its next minute poll. NudgeItem announces them on the same topics a status
// change uses — request.updated for staff, user.<id>.request.updated for each party — with
// ids and status only, so open request views refetch at once.

// NudgeItem tells the open request views of everyone behind a library item's undelivered
// requests that it moved: a release was grabbed for it, or a file imported. A lookup that
// fails says nothing — a missed nudge only costs a poll.
func (s *Service) NudgeItem(ctx context.Context, mediaType string, itemID int64) {
	if s.bus == nil || itemID <= 0 {
		return
	}
	reqs, err := s.requestsForItem(ctx, mediaType, itemID)
	if err != nil {
		if s.log != nil {
			s.log.Debug("request nudge: couldn't find the requests for an item", "media", mediaType, "id", itemID, "err", err)
		}
		return
	}
	s.nudge(ctx, reqs)
}

// nudge announces each open request (approved, not yet delivered) to its parties.
func (s *Service) nudge(ctx context.Context, reqs []Request) {
	for _, req := range reqs {
		if req.Status != StatusApproved || req.ReadyAt > 0 {
			continue
		}
		s.publish(req, req.Status, s.parties(ctx, req))
	}
}

// requestsForItem is every request a library movie, show or book answers.
func (s *Service) requestsForItem(ctx context.Context, mediaType string, itemID int64) ([]Request, error) {
	switch mediaType {
	case "movie":
		if s.movies == nil {
			return nil, nil
		}
		m, err := s.movies.Get(ctx, itemID)
		if errors.Is(err, movies.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return s.repo.ListByMedia(ctx, "movie", m.TMDBID)
	case "series":
		if s.series == nil {
			return nil, nil
		}
		sr, err := s.series.Get(ctx, itemID)
		if errors.Is(err, series.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return s.repo.ListByMedia(ctx, "series", sr.TMDBID)
	case "book":
		if s.books == nil {
			return nil, nil
		}
		b, err := s.books.Get(ctx, itemID)
		if errors.Is(err, books.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		keys := []string{b.OLKey}
		if ks, err := s.books.KeysFor(ctx, b.ID); err == nil {
			keys = keys[:0]
			for _, k := range ks {
				keys = append(keys, k.Key)
			}
		}
		return s.repo.ListForBook(ctx, b.ID, keys)
	}
	return nil, nil
}
