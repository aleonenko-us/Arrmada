package httpapi

import (
	"context"
	"database/sql"
	"time"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/music"
)

// Music outside its own pages (MUS-24): its part of the Dashboard, the homepage widget and
// the Calendar. All of it is there only while the Music module is on, and the Calendar's
// albums only for staff: requesters can't ask for music or open an album page, so an
// album on their calendar would be a title they can do nothing with.

// dashboardMusic is Music's part of the Dashboard.
type dashboardMusic struct {
	musicCounts
	// NewReleases is the newest of the New releases list (what the refresh found in the
	// last newReleaseDays), NewReleasesTotal how many that list holds.
	NewReleases      []dashboardNewRelease `json:"new_releases"`
	NewReleasesTotal int                   `json:"new_releases_total"`
	// Imports are the newest music imports, which the mixed Recent activity can bury.
	Imports []activityEvent `json:"imports"`
}

// musicCounts is the library's music in numbers, shared by the Dashboard and the widget.
type musicCounts struct {
	Artists int `json:"artists"` // library artists (not ones only credited on an album)
	// Albums is albums with at least one track on disk: an artist's whole discography is
	// listed, so counting every listed album would say little about what's here.
	Albums    int   `json:"albums"`
	Tracks    int   `json:"tracks"` // tracks with a file on disk
	SizeBytes int64 `json:"size_bytes"`
	// AlbumsWanted is monitored albums of monitored artists that aren't complete, by the
	// Wanted page's rules; AlbumsMissing is the released ones among them, what the music
	// sweep is searching for (the rest are on the Wanted page's Upcoming tab). One thing
	// differs from the Wanted page: it leaves out an album while a download for it is in
	// the client, and these count it, as movies_missing counts a downloading movie.
	AlbumsWanted  int `json:"albums_wanted"`
	AlbumsMissing int `json:"albums_missing"`
}

// dashboardNewRelease is one New releases row, as the Dashboard lists it.
type dashboardNewRelease struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	ArtistName  string `json:"artist_name"`
	AlbumType   string `json:"album_type,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
	Monitored   bool   `json:"monitored"`
	FoundAs     string `json:"found_as,omitempty"`
}

const (
	// newReleaseDays is the New releases list's own window (components/music/NewReleases).
	newReleaseDays = 30
	// dashboardMusicRows is how many new releases and imports the Dashboard lists.
	dashboardMusicRows = 5
)

// musicOn says whether Music is switched on and wired.
func (a *api) musicOn(ctx context.Context) bool {
	return a.deps.Music != nil && a.deps.Settings != nil && a.musicEnabled(ctx)
}

// dashboardMusic is Music's part of the Dashboard; nil while the module is off.
func (a *api) dashboardMusic(ctx context.Context) *dashboardMusic {
	if !a.musicOn(ctx) {
		return nil
	}
	out := &dashboardMusic{musicCounts: a.musicCounts(ctx), NewReleases: []dashboardNewRelease{}, Imports: []activityEvent{}}
	// The few rows shown and an exact count: the Dashboard reloads on every import, so it
	// never reads the whole list.
	if list, total, err := a.deps.Music.RecentReleases(ctx, newReleaseDays, dashboardMusicRows); err != nil {
		a.deps.Log.Debug("dashboard: new releases unavailable", "err", err)
	} else {
		out.NewReleasesTotal = total
		for _, nr := range list {
			out.NewReleases = append(out.NewReleases, dashboardNewRelease{ID: nr.ID, Title: nr.Title, ArtistName: nr.ArtistName,
				AlbumType: nr.AlbumType, ReleaseDate: nr.ReleaseDate, Monitored: nr.Monitored, FoundAs: nr.FoundAs})
		}
	}
	if a.deps.Store != nil {
		events, err := eventsFeed(ctx, a.deps.Store.DB(), eventsFilter{Kinds: []string{"music"}, Categories: []string{evImported}, Limit: dashboardMusicRows})
		if err != nil {
			a.deps.Log.Debug("dashboard: music imports unavailable", "err", err)
		}
		for _, e := range events {
			out.Imports = append(out.Imports, activityEvent{Kind: e.Kind, ID: e.ItemID, Title: e.Title, Event: e.Event, Detail: e.Detail, AtMS: e.AtMS})
		}
	}
	return out
}

// musicCounts counts the music library. A count that fails reads zero, as libraryCounts'
// do.
func (a *api) musicCounts(ctx context.Context) musicCounts {
	var mc musicCounts
	if a.deps.Store != nil {
		db := a.deps.Store.DB()
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artists WHERE in_library = 1`).Scan(&mc.Artists); err != nil {
			a.deps.Log.Debug("dashboard: artist count failed", "err", err)
		}
		// One pass over the files: the tracks and albums they fill, and the space they take
		// (extras included, as on the album page).
		var size sql.NullInt64
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(release_track_id), COUNT(DISTINCT CASE WHEN release_track_id IS NOT NULL THEN album_id END), SUM(size_bytes)
			  FROM music_track_files WHERE missing_since IS NULL`).Scan(&mc.Tracks, &mc.Albums, &size); err != nil {
			a.deps.Log.Debug("dashboard: music file counts failed", "err", err)
		}
		mc.SizeBytes = size.Int64
	}
	if a.deps.Music != nil {
		// The Wanted page's list and rules (WantedAlbums, Album.Complete), worked out in one
		// grouped statement that returns only the incomplete albums' dates; released or not
		// is the music sweep's own date rule.
		if albums, err := a.deps.Music.IncompleteWanted(ctx); err != nil {
			a.deps.Log.Debug("dashboard: wanted albums unavailable", "err", err)
		} else {
			today := time.Now().Format("2006-01-02")
			mc.AlbumsWanted = len(albums)
			for _, al := range albums {
				if automation.AlbumReleased(al, today) {
					mc.AlbumsMissing++
				}
			}
		}
	}
	return mc
}

// calendarMusicFor says whether u's Calendar (and calendar feed) shows albums: staff
// only, and only while Music is on.
func (a *api) calendarMusicFor(ctx context.Context, u *auth.User) bool {
	return u != nil && u.Role.AtLeast(auth.RoleManager) && a.musicOn(ctx)
}

// calendarAlbums is the albums dated within [start, end] as Calendar items. The words
// follow the episodes': an album that isn't wanted is Unmonitored (the artist's New
// releases option left it unwanted, or someone switched it off), and a wanted album of an
// artist whose switch is off is Searching off, like a wanted episode of a paused show.
func (a *api) calendarAlbums(ctx context.Context, start, end string) []CalendarItem {
	albums, err := a.deps.Music.CalendarAlbums(ctx, start, end)
	if err != nil {
		a.deps.Log.Warn("calendar: reading album release dates failed", "err", err)
		return nil
	}
	out := make([]CalendarItem, 0, len(albums))
	for _, al := range albums {
		out = append(out, calendarAlbumItem(al))
	}
	return out
}

func calendarAlbumItem(al music.CalendarAlbum) CalendarItem {
	kind := al.AlbumType
	if kind == "" {
		kind = "Album"
	}
	return CalendarItem{
		Date: al.ReleaseDate, Type: "album", Title: al.Title, Subtitle: al.ArtistName + " · " + kind,
		PosterURL: al.CoverURL, RefID: al.ID, HasFile: al.Complete(),
		Monitored: al.Monitored && al.ArtistMonitored, SearchingOff: al.Monitored && !al.ArtistMonitored,
		MediaType: "music", Year: al.Year, Artist: al.ArtistName, AlbumType: al.AlbumType,
	}
}
