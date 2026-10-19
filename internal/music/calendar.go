package music

import "context"

// The Calendar's music (MUS-24): albums of library artists whose first release falls on a
// day inside the window — the ones the refresh has dated ahead, and the ones just out.

// calendarLimit bounds one window's albums. A month holds a handful even for a large
// library; this only stops a window of years from loading the whole discography.
const calendarLimit = 2000

// CalendarAlbum is one dated album with what the Calendar says about its artist.
type CalendarAlbum struct {
	Album
	ArtistName string
	// ArtistMonitored is the artist's own switch: off, nothing of theirs is searched.
	ArtistMonitored bool
}

// CalendarAlbums lists the albums of library artists dated within [start, end]
// (YYYY-MM-DD, inclusive), by date. Only a full date counts: an album MusicBrainz dates to
// a year or a month has no day to stand on. An album MusicBrainz no longer lists is left
// out, since the date it was given may no longer hold.
func (s *Service) CalendarAlbums(ctx context.Context, start, end string) ([]CalendarAlbum, error) {
	return s.repo.CalendarAlbums(ctx, start, end)
}

// CalendarAlbums is Service.CalendarAlbums.
func (r *Repo) CalendarAlbums(ctx context.Context, start, end string) ([]CalendarAlbum, error) {
	albums, err := r.albums(ctx, `
		JOIN artists ar ON ar.id = al.artist_id
		WHERE ar.in_library = 1 AND al.stale_since IS NULL
		  AND length(al.release_date) = 10 AND date(al.release_date) = al.release_date
		  AND al.release_date >= ? AND al.release_date <= ?
		ORDER BY al.release_date, al.id
		LIMIT ?`, start, end, calendarLimit)
	if err != nil {
		return nil, err
	}
	artists := map[int64]Artist{}
	out := make([]CalendarAlbum, 0, len(albums))
	for _, al := range albums {
		a, ok := artists[al.ArtistID]
		if !ok {
			if a, err = r.GetArtist(ctx, al.ArtistID); err != nil {
				return nil, err
			}
			artists[al.ArtistID] = a
		}
		out = append(out, CalendarAlbum{Album: al, ArtistName: a.Name, ArtistMonitored: a.Monitored})
	}
	return out, nil
}
