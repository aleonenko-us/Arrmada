package movies

import "context"

// UpcomingMovie is a library movie with a release date, for the Calendar module.
type UpcomingMovie struct {
	ReleaseDate string `json:"release_date"`
	ID          int64  `json:"id"`
	TMDBID      int    `json:"tmdb_id"`
	Title       string `json:"title"` // the shown title (Settings → Library → "Show titles as")
	Year        int    `json:"year"`
	PosterURL   string `json:"poster_url"`
	HasFile     bool   `json:"has_file"`
	Monitored   bool   `json:"monitored"`
	// CanonicalTitle is the film's own (TMDB English) title when Title is its shown one:
	// the adult filter judges both.
	CanonicalTitle string `json:"-"`
}

// Upcoming returns library movies whose release date falls within [from, to] (inclusive,
// YYYY-MM-DD). Release date lives in the Extra JSON, so we filter the list in memory.
func (s *Service) Upcoming(ctx context.Context, from, to string) ([]UpcomingMovie, error) {
	ms, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []UpcomingMovie
	for _, m := range ms {
		if m.Extra == nil {
			continue
		}
		rd := m.Extra.ReleaseDate
		if len(rd) < 10 {
			continue
		}
		rd = rd[:10] // guard against a time component
		if rd >= from && rd <= to {
			out = append(out, UpcomingMovie{
				ReleaseDate: rd, ID: m.ID, TMDBID: m.TMDBID, Title: m.Shown(), Year: m.Year,
				PosterURL: m.PosterURL, HasFile: m.HasFile, Monitored: m.Monitored, CanonicalTitle: m.Title,
			})
		}
	}
	return out, nil
}
