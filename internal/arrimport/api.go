package arrimport

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// What the import reads from Radarr and Sonarr (their v3 API, which Radarr 3–6 and Sonarr
// 3–4 all serve). Only the fields used are decoded. The shapes were checked against a real
// Radarr 6.4 and Sonarr 4.0 (testdata/live holds their answers).

// Status is the app's /system/status: its name, the instance name the person gave it and
// its version.
type Status struct {
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
	Version      string `json:"version"`
}

// Status reads the app's name and version — the cheapest call that proves the address
// and the key, and says which app answered.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	if err := c.get(ctx, "/api/v3/system/status", &st); err != nil {
		return st, err
	}
	if app := strings.ToLower(strings.TrimSpace(st.AppName)); app != "" && app != c.app {
		if app == Radarr || app == Sonarr {
			return st, fmt.Errorf("%s is %s, not %s — pick %s above", c.shown, st.AppName, appName(c.app), st.AppName)
		}
		return st, fmt.Errorf("%s is %s — Arrmada brings libraries over from Radarr and Sonarr", c.shown, st.AppName)
	}
	return st, nil
}

func appName(app string) string {
	if app == Sonarr {
		return "Sonarr"
	}
	return "Radarr"
}

// Name is the app's own name for itself ("Radarr", or the instance name when it has one,
// like "Radarr 4K"), for events and the summary.
func (st Status) Name(app string) string {
	if n := strings.TrimSpace(st.InstanceName); n != "" {
		return n
	}
	if n := strings.TrimSpace(st.AppName); n != "" {
		return n
	}
	return appName(app)
}

// Profile is one of the app's quality profiles: its id, name and the highest resolution it
// allows (0 when it allows only unknown-resolution qualities).
type Profile struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	MaxResolution int    `json:"max_resolution"`
}

type profileItem struct {
	Quality *struct {
		Resolution int `json:"resolution"`
	} `json:"quality"`
	Items   []profileItem `json:"items"`
	Allowed bool          `json:"allowed"`
}

// maxAllowed is the highest resolution among the allowed qualities; a group's members count
// when the group is allowed (Radarr and Sonarr keep "allowed" on the group).
func maxAllowed(items []profileItem, parentAllowed bool) int {
	best := 0
	for _, it := range items {
		allowed := it.Allowed || parentAllowed
		if len(it.Items) > 0 {
			if r := maxAllowed(it.Items, allowed); r > best {
				best = r
			}
			continue
		}
		if allowed && it.Quality != nil && it.Quality.Resolution > best {
			best = it.Quality.Resolution
		}
	}
	return best
}

// Profiles lists the app's quality profiles.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var raw []struct {
		ID    int           `json:"id"`
		Name  string        `json:"name"`
		Items []profileItem `json:"items"`
	}
	if err := c.get(ctx, "/api/v3/qualityprofile", &raw); err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(raw))
	for _, p := range raw {
		out = append(out, Profile{ID: p.ID, Name: strings.TrimSpace(p.Name), MaxResolution: maxAllowed(p.Items, false)})
	}
	return out, nil
}

// rootFolders lists the app's root folders.
func (c *Client) rootFolders(ctx context.Context) ([]rootFolder, error) {
	var folders []rootFolder
	err := c.get(ctx, "/api/v3/rootfolder", &folders)
	return folders, err
}

type rootFolder struct {
	Path      string `json:"path"`
	FreeSpace int64  `json:"freeSpace"`
}

// File is a movie or episode file as the app records it: its path in the app's own
// container, and its size.
type File struct {
	ID           int    `json:"id"`
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	SeasonNumber int    `json:"seasonNumber"`
	MovieID      int    `json:"movieId"`
}

// Movie is one of Radarr's films.
type Movie struct {
	ID                  int    `json:"id"`
	Title               string `json:"title"`
	Year                int    `json:"year"`
	TMDBID              int    `json:"tmdbId"`
	ProfileID           int    `json:"qualityProfileId"`
	Monitored           bool   `json:"monitored"`
	MinimumAvailability string `json:"minimumAvailability"`
	Path                string `json:"path"`
	RootFolderPath      string `json:"rootFolderPath"`
	HasFile             bool   `json:"hasFile"`
	MovieFileID         int    `json:"movieFileId"`
	File                *File  `json:"movieFile"`
}

// Root is the root folder the film is filed under (older Radarr leaves rootFolderPath
// empty: then it's the film folder's parent).
func (m Movie) Root() string { return rootOf(m.RootFolderPath, m.Path) }

// FilePath is the film's file in Radarr's container ("" when it has none).
func (m Movie) FilePath() string {
	if !m.HasFile || m.File == nil {
		return ""
	}
	return filePath(m.File, m.Path)
}

func filePath(f *File, titleDir string) string {
	if p := strings.TrimSpace(f.Path); p != "" {
		return p
	}
	if f.RelativePath == "" || titleDir == "" {
		return ""
	}
	sep := "/"
	if strings.Contains(titleDir, `\`) && !strings.HasPrefix(titleDir, "/") {
		sep = `\`
	}
	return strings.TrimRight(titleDir, `/\`) + sep + f.RelativePath
}

func rootOf(root, titlePath string) string {
	if r := cleanArr(root); r != "" {
		return r
	}
	return cleanArr(parentOf(titlePath))
}

// Movies lists Radarr's films, each with its file. Radarr's list carries the file; one
// that says it has a file but came without it is read from /moviefile.
func (c *Client) Movies(ctx context.Context) ([]Movie, error) {
	var list []Movie
	if err := c.get(ctx, "/api/v3/movie", &list); err != nil {
		return nil, err
	}
	var lacking []int
	for _, m := range list {
		if m.HasFile && (m.File == nil || filePath(m.File, m.Path) == "") {
			lacking = append(lacking, m.ID)
		}
	}
	// /moviefile wants the ids; a few dozen to a request keeps the address short.
	for start := 0; start < len(lacking); start += 50 {
		end := min(start+50, len(lacking))
		q := make([]string, 0, end-start)
		for _, id := range lacking[start:end] {
			q = append(q, "movieId="+strconv.Itoa(id))
		}
		var files []File
		if err := c.get(ctx, "/api/v3/moviefile?"+strings.Join(q, "&"), &files); err != nil {
			return nil, err
		}
		byMovie := map[int]File{}
		for _, f := range files {
			byMovie[f.MovieID] = f
		}
		for i := range list {
			if f, ok := byMovie[list[i].ID]; ok && list[i].HasFile && (list[i].File == nil || filePath(list[i].File, list[i].Path) == "") {
				f := f
				list[i].File = &f
			}
		}
	}
	return list, nil
}

// Series is one of Sonarr's shows.
type Series struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	TVDBID     int    `json:"tvdbId"`
	TMDBID     int    `json:"tmdbId"` // Sonarr 4; 0 on Sonarr 3
	ProfileID  int    `json:"qualityProfileId"`
	Monitored  bool   `json:"monitored"`
	SeriesType string `json:"seriesType"` // standard | daily | anime
	// MonitorNewItems is Sonarr 4's "all" | "none" for seasons added later ("" on Sonarr 3).
	MonitorNewItems string   `json:"monitorNewItems"`
	Path            string   `json:"path"`
	RootFolderPath  string   `json:"rootFolderPath"`
	Seasons         []Season `json:"seasons"`
	Statistics      struct {
		EpisodeFileCount int `json:"episodeFileCount"`
	} `json:"statistics"`
}

// Season is one of a show's seasons in Sonarr.
type Season struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// Root is the root folder the show is filed under.
func (s Series) Root() string { return rootOf(s.RootFolderPath, s.Path) }

// AllSeries lists Sonarr's shows.
func (c *Client) AllSeries(ctx context.Context) ([]Series, error) {
	var list []Series
	err := c.get(ctx, "/api/v3/series", &list)
	return list, err
}

// Episode is one of a show's episodes in Sonarr (Sonarr's numbering — TVDB's).
type Episode struct {
	SeasonNumber  int  `json:"seasonNumber"`
	EpisodeNumber int  `json:"episodeNumber"`
	Monitored     bool `json:"monitored"`
	HasFile       bool `json:"hasFile"`
	EpisodeFileID int  `json:"episodeFileId"`
}

// Episodes lists a show's episodes.
func (c *Client) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var list []Episode
	err := c.get(ctx, "/api/v3/episode?seriesId="+strconv.Itoa(seriesID), &list)
	return list, err
}

// EpisodeFiles lists a show's episode files.
func (c *Client) EpisodeFiles(ctx context.Context, seriesID int) ([]File, error) {
	var list []File
	err := c.get(ctx, "/api/v3/episodefile?seriesId="+strconv.Itoa(seriesID), &list)
	return list, err
}

// EpisodeFilePath is an episode file's path in Sonarr's container.
func EpisodeFilePath(f File, show Series) string { return filePath(&f, show.Path) }
