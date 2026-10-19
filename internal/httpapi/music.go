package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/jobs"
	"github.com/tristenlammi/arrmada/internal/music"
	"github.com/tristenlammi/arrmada/internal/quality"
)

// handleListArtists returns the music library.
func (a *api) handleListArtists(w http.ResponseWriter, r *http.Request) {
	list, err := a.deps.Music.ListArtists(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not list artists")
		return
	}
	if list == nil {
		list = []music.Artist{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"artists": list})
}

// handleLookupArtists searches MusicBrainz for artists to add.
func (a *api) handleLookupArtists(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		a.writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	res, err := a.deps.Music.Lookup(r.Context(), q)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "artist search failed: "+err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// handleAddArtist adds an artist and pulls its album list.
func (a *api) handleAddArtist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MBID           string `json:"mbid"`
		QualityProfile string `json:"quality_profile"`
		Monitored      *bool  `json:"monitored"`
		// Monitor is which of the artist's current albums are wanted: all (default),
		// future, latest or none. MonitorNew and ReleaseTypes are the artist's own
		// options (MUS-14); left out, it follows the library defaults.
		Monitor      string              `json:"monitor"`
		MonitorNew   string              `json:"monitor_new"`
		ReleaseTypes *music.ReleaseTypes `json:"release_types"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MBID) == "" {
		a.writeError(w, http.StatusBadRequest, "mbid is required")
		return
	}
	if req.Monitor != "" && !music.ValidMonitor(req.Monitor) {
		a.writeError(w, http.StatusBadRequest, `monitor must be "all", "future", "latest" or "none"`)
		return
	}
	if !music.ValidMonitorNew(req.MonitorNew) {
		a.writeError(w, http.StatusBadRequest, `monitor_new must be "all", "none" or "" (the library default)`)
		return
	}
	if req.ReleaseTypes != nil {
		if _, err := req.ReleaseTypes.Normalize(); err != nil {
			a.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.QualityProfile == "" {
		req.QualityProfile = a.deps.Quality.DefaultProfile(r.Context(), quality.MediaMusic)
	} else if a.deps.Automation != nil && !a.deps.Automation.KnownProfile(r.Context(), req.QualityProfile) {
		a.writeError(w, http.StatusBadRequest, "unknown quality profile")
		return
	}
	monitored := req.Monitored == nil || *req.Monitored
	monitor := req.Monitor
	if monitor == "" {
		monitor = music.MonitorAll
		if !monitored {
			monitor = music.MonitorNone
		}
	}
	art, err := a.deps.Music.AddArtistWith(r.Context(), strings.TrimSpace(req.MBID), req.QualityProfile, music.AddOptions{
		Monitored: monitored, Monitor: monitor, MonitorNew: req.MonitorNew, ReleaseTypes: req.ReleaseTypes,
	})
	switch {
	case errors.Is(err, music.ErrExists):
		a.writeError(w, http.StatusConflict, "that artist is already in your library")
		return
	case err != nil:
		a.writeError(w, http.StatusBadGateway, "could not add the artist: "+err.Error())
		return
	}
	a.writeJSON(w, http.StatusCreated, art)
}

// handleGetArtist returns one artist with its albums.
func (a *api) handleGetArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	art, err := a.deps.Music.GetArtist(r.Context(), id)
	if err != nil {
		a.musicErr(w, err, "could not load the artist")
		return
	}
	a.writeJSON(w, http.StatusOK, art)
}

// handleRefreshArtist re-pulls an artist's metadata and album list.
func (a *api) handleRefreshArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	art, err := a.deps.Music.Refresh(r.Context(), id)
	if err != nil {
		a.musicErr(w, err, "could not refresh the artist")
		return
	}
	a.writeJSON(w, http.StatusOK, art)
}

// handleSetArtistMonitored toggles an artist (cascading to its albums and tracks).
func (a *api) handleSetArtistMonitored(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Monitored bool `json:"monitored"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if err := a.deps.Music.SetArtistMonitored(r.Context(), id, req.Monitored); err != nil {
		a.musicErr(w, err, "could not update the artist")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"monitored": req.Monitored})
}

// handleSetArtistProfile changes an artist's quality profile.
func (a *api) handleSetArtistProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		QualityProfile string `json:"quality_profile"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if !a.deps.Automation.KnownProfile(r.Context(), req.QualityProfile) {
		a.writeError(w, http.StatusBadRequest, "unknown quality profile")
		return
	}
	if err := a.deps.Music.SetQualityProfile(r.Context(), id, req.QualityProfile); err != nil {
		a.musicErr(w, err, "could not update the profile")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"quality_profile": req.QualityProfile})
}

// handleDeleteArtist removes an artist; albums and tracks cascade away.
func (a *api) handleDeleteArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Music.Delete(r.Context(), id); err != nil {
		a.musicErr(w, err, "could not delete the artist")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// handleArtistHistory returns an artist's activity timeline.
func (a *api) handleArtistHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	events, err := a.deps.Music.Events(r.Context(), id, 100)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read history")
		return
	}
	if events == nil {
		events = []music.Event{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// handleGetAlbum returns one album with its track listing. The listing is fetched from
// MusicBrainz the first time an album is opened — see music.Service.EnsureTracks for why it
// isn't pulled when the artist is added.
func (a *api) handleGetAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	al, err := a.deps.Music.GetAlbum(r.Context(), id)
	if err != nil {
		a.musicErr(w, err, "could not load the album")
		return
	}
	a.writeJSON(w, http.StatusOK, al)
}

// handleSetAlbumMonitored toggles one album (and its tracks).
func (a *api) handleSetAlbumMonitored(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Monitored bool `json:"monitored"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if err := a.deps.Music.SetAlbumMonitored(r.Context(), id, req.Monitored); err != nil {
		a.musicErr(w, err, "could not update the album")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"monitored": req.Monitored})
}

// handleResolveAlbumNote marks one of an album's upgrade notes as looked at (the files
// were fixed, or the gap is accepted), so the sweep searches the album again.
func (a *api) handleResolveAlbumNote(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	noteID, ok := a.pathValueID(w, r, "note")
	if !ok {
		return
	}
	if err := a.deps.Music.ResolveNote(r.Context(), id, noteID); err != nil {
		a.musicErr(w, err, "could not resolve the note")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "resolved"})
}

// musicErr maps a module error to a status, so a missing id is a 404 rather than a 500 —
// the mistake the Books handlers made on seven endpoints.
func (a *api) musicErr(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, music.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "not found")
		return
	}
	a.writeError(w, http.StatusInternalServerError, msg)
}

// handleScanMusicLibrary catalogues music already on disk. Runs in the background: a big
// collection means one MusicBrainz lookup per unknown artist at one request per second,
// far longer than any sensible request timeout.
func (a *api) handleScanMusicLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Full looks at every folder again, ignoring what earlier scans remembered.
		Full bool `json:"full"`
	}
	if r.ContentLength > 0 && !a.decodeJSON(w, r, &req) { // body optional
		return
	}
	full := req.Full
	// One scan at a time (single-flight): two overlapping runs would double every
	// MusicBrainz lookup and race each other's writes. Its own class, so an hours-long music
	// scan doesn't hold up the other libraries' scans.
	jobID, existing, ok := a.submitOr503(w, r, jobs.Spec{Kind: "music.scan", Target: "all", Class: "music.scan", Timeout: libraryScanTimeout,
		Fn: func(ctx context.Context, p *jobs.Progress) (any, error) {
			p.SetMessage("Walking the music folder…")
			res, err := a.deps.Automation.ScanMusicLibraryWith(ctx, automation.MusicScanOptions{Full: full}, func(done, total int) {
				if total > 0 {
					p.Set(float64(done)/float64(total), fmt.Sprintf("Reading %s / %s album folders", thousands(done), thousands(total)))
				}
			})
			if err != nil {
				return nil, err
			}
			a.deps.Log.Info("music scan complete", "artists", res.Artists, "albums", res.Albums,
				"tracks", res.Tracks, "reviews", res.Reviews)
			a.deps.Bus.Publish("library.scanned", map[string]any{
				"media": "music", "artists": res.Artists, "albums": res.Albums,
				"tracks": res.Tracks, "unmatched": res.Reviews,
			})
			p.SetMessage(musicScanMessage(res))
			return res, nil
		}})
	if !ok {
		return
	}
	if existing {
		a.alreadyRunning(w, jobID, "a music scan is already running")
		return
	}
	a.accepted(w, jobID, false, map[string]any{"status": "scanning"})
}

// musicScanMessage is a finished scan's one line: what it found, and what's left to do.
func musicScanMessage(res automation.MusicScanResult) string {
	msg := fmt.Sprintf("Found %s, %s and %s", countOf(res.Artists, "new artist"), countOf(res.Albums, "album"), countOf(res.Tracks, "track"))
	if res.Moved > 0 {
		msg += "; followed " + countOf(res.Moved, "moved file")
	}
	if res.Reviews > 0 {
		msg += "; " + countOf(res.Reviews, "folder") + " to match in Reviews"
	}
	if n := res.Remaining + res.Failed + res.Waiting; n > 0 {
		msg += "; " + countOf(n, "folder") + " left for the next scan"
	}
	return msg + "."
}

// handleMusicScanStatus answers the last library scan's summary (counts and timings), for
// the scan results drawer.
func (a *api) handleMusicScanStatus(w http.ResponseWriter, r *http.Request) {
	summary, started, finished, ok, err := a.deps.Music.LastScanRun(r.Context())
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not read the last scan")
		return
	}
	out := map[string]any{"last": nil}
	if ok && json.Valid([]byte(summary)) {
		out["last"] = json.RawMessage(summary)
		out["started_at"], out["finished_at"] = started, finished
	}
	a.writeJSON(w, http.StatusOK, out)
}

// handleGrabDiscography grabs a whole-catalogue pack for an artist.
//
// An explicit action rather than part of the automatic sweep: a discography is one torrent
// spanning many albums, which fights per-album upgrades and seeding. It's the right tool
// when you own nothing by an artist, and the wrong one for topping up.
func (a *api) handleGrabDiscography(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	if err := a.deps.Automation.GrabDiscography(r.Context(), id); err != nil {
		if errors.Is(err, automation.ErrModuleOff) {
			// Switched off between the route check and here; same answer as the route gives.
			a.writeError(w, http.StatusNotFound, musicOffMessage)
			return
		}
		a.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"status": "grabbed"})
}
