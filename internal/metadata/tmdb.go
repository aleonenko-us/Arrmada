package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tristenlammi/arrmada/internal/adultfilter"
	"github.com/tristenlammi/arrmada/internal/httpua"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/safego"
)

const (
	tmdbBaseURL      = "https://api.themoviedb.org/3"
	tmdbImageBase    = "https://image.tmdb.org/t/p/w500"
	tmdbBackdropBase = "https://image.tmdb.org/t/p/w1280"
	tmdbProfileBase  = "https://image.tmdb.org/t/p/w185"
	maxCast          = 15
)

// TMDB is a The Movie Database provider (v3 API key).
type TMDB struct {
	key    func() string
	http   *http.Client
	base   string        // API base URL (tmdbBaseURL; overridable in tests)
	region func() string // admin's discovery region (ISO alpha-2); nil/"" = global

	discMu    sync.Mutex
	discCache map[string]discoverCacheEntry // browse/search list cache (TTL-only)
	disk      *DiskCache                    // survives restarts; stale served while refreshing

	genreMu  sync.Mutex
	genreMap map[int]string // genre id → name, for the cards
	genreAt  time.Time
}

// SetDiskCache keeps browse lists across restarts (see DiskCache).
func (t *TMDB) SetDiskCache(c *DiskCache) { t.disk = c }

// NewTMDB builds a TMDB provider from a static key (env/config). Empty means unconfigured.
func NewTMDB(apiKey string) *TMDB { return NewTMDBFunc(func() string { return apiKey }) }

// NewTMDBFunc builds a TMDB provider that reads its key lazily, so a key added in the
// settings menu takes effect without a restart.
func NewTMDBFunc(key func() string) *TMDB {
	return &TMDB{
		key:       key,
		http:      httpua.Client(20 * time.Second),
		base:      tmdbBaseURL,
		discCache: map[string]discoverCacheEntry{},
	}
}

// SetRegionFunc installs a lazy resolver for the admin's discovery region
// (ISO 3166-1 alpha-2, e.g. "AU"). Read per call — like the API key — so a
// settings change applies without a restart. Region localizes the release-date
// driven lists (popular, upcoming, genre discovery): without it TMDB's global
// mix surfaces regional oddities from everywhere except where the user lives.
func (t *TMDB) SetRegionFunc(f func() string) { t.region = f }

// regionCode returns the configured region normalized to two upper-case ASCII
// letters, or "" when unset/invalid (TMDB then falls back to its global lists).
func (t *TMDB) regionCode() string {
	if t.region == nil {
		return ""
	}
	r := strings.ToUpper(strings.TrimSpace(t.region()))
	if len(r) != 2 || r[0] < 'A' || r[0] > 'Z' || r[1] < 'A' || r[1] > 'Z' {
		return ""
	}
	return r
}

// sanitizeErr rewrites err so its text cannot leak an API key embedded in the request
// URL. Transport failures (*url.Error and friends) reproduce the full URL — key
// included — and error strings from this package are written back to API clients and
// logs, so redaction has to happen at the point the error is created.
func sanitizeErr(rawURL string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if u, e := url.Parse(rawURL); e == nil {
		q := u.Query()
		for _, param := range []string{"api_key", "apikey", "key"} {
			if key := q.Get(param); key != "" {
				msg = strings.ReplaceAll(msg, key, "REDACTED")
				if esc := url.QueryEscape(key); esc != key {
					msg = strings.ReplaceAll(msg, esc, "REDACTED")
				}
			}
		}
	}
	return errors.New(msg)
}

// Available reports whether an API key is configured.
func (t *TMDB) Available() bool { return t.key() != "" }

// ErrNotFound is TMDB answering 404: no such title, person or collection.
var ErrNotFound = errors.New("tmdb: HTTP 404")

// ErrInvalidKey is TMDB refusing the API key (HTTP 401).
var ErrInvalidKey = errors.New("tmdb: invalid API key")

// Validate makes the cheapest real request (GET /configuration) with the current key:
// nil when TMDB accepts it, ErrInvalidKey when it refuses it, ErrNotConfigured when there
// is no key, and any other error when TMDB couldn't be asked — which says nothing about
// the key. Never cached: it's for the key test button and the health check.
func (t *TMDB) Validate(ctx context.Context) error {
	_, err := t.get(ctx, "/configuration", url.Values{})
	return err
}

// rejectedKey is a provider refusing a key, worded for the person who pasted it. It
// still matches ErrInvalidKey, so callers that branch on that keep working.
type rejectedKey string

func (e rejectedKey) Error() string        { return string(e) }
func (e rejectedKey) Is(target error) bool { return target == ErrInvalidKey }

// VerifyKey is the key Test: the same GET /configuration as Validate, but with candidate
// when one is given (a key typed and not yet saved — it's sent once and kept nowhere),
// else with the saved key. It answers in words: where TMDB serves images from on success;
// on a 401, an error wrapping ErrInvalidKey that says to use the v3 API key, and says so
// louder when the key looks like a v4 read-access token (a JWT, which starts "eyJ").
func (t *TMDB) VerifyKey(ctx context.Context, candidate string) (string, error) {
	key := strings.TrimSpace(candidate)
	if key == "" {
		key = t.key()
	}
	if key == "" {
		return "", ErrNotConfigured
	}
	body, err := t.getKey(ctx, key, "/configuration", url.Values{})
	if errors.Is(err, ErrInvalidKey) {
		msg := "TMDB rejected the key; use the v3 API key, not the v4 read access token"
		switch {
		case strings.HasPrefix(key, "eyJ"):
			msg += " (this looks like a v4 token)"
		case len(key) != 32:
			// A browser filling in a saved password lands here: say what a key looks like.
			msg += fmt.Sprintf(" (a v3 key is 32 letters and numbers; this is %d characters)", len(key))
		}
		return "", rejectedKey(msg)
	}
	if err != nil {
		return "", err
	}
	var cfg struct {
		Images struct {
			SecureBaseURL string `json:"secure_base_url"`
		} `json:"images"`
	}
	if json.Unmarshal(body, &cfg) != nil || cfg.Images.SecureBaseURL == "" {
		return "OK: TMDB accepted the key.", nil
	}
	return "OK: images from " + cfg.Images.SecureBaseURL, nil
}

func (t *TMDB) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if !t.Available() {
		return nil, ErrNotConfigured
	}
	return t.getKey(ctx, t.key(), path, q)
}

// getKey is get with an explicit key, for testing one that isn't saved yet.
func (t *TMDB) getKey(ctx context.Context, key, path string, q url.Values) ([]byte, error) {
	q.Set("api_key", key)
	full := t.base + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, sanitizeErr(full, err)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, sanitizeErr(full, fmt.Errorf("tmdb request: %w", err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidKey
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// SearchMovie searches TMDB for movies by title.
func (t *TMDB) SearchMovie(ctx context.Context, query string) ([]MovieResult, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	body, err := t.get(ctx, "/search/movie", q)
	if err != nil {
		return nil, err
	}
	return parseTMDBSearch(body), nil
}

// GetMovie fetches full details for a TMDB movie id, including cast, genres,
// studio, collection, and US certification (one round-trip via append_to_response).
func (t *TMDB) GetMovie(ctx context.Context, tmdbID int) (*MovieDetails, error) {
	q := url.Values{}
	q.Set("append_to_response", "credits,release_dates,alternative_titles")
	body, err := t.get(ctx, "/movie/"+strconv.Itoa(tmdbID), q)
	if err != nil {
		return nil, err
	}
	return parseTMDBMovie(body)
}

// SearchSeries searches TMDB for TV series by title.
func (t *TMDB) SearchSeries(ctx context.Context, query string) ([]SeriesResult, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	body, err := t.get(ctx, "/search/tv", q)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []tmdbSeries `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := make([]SeriesResult, 0, len(payload.Results))
	for _, s := range payload.Results {
		out = append(out, s.toResult())
	}
	return out, nil
}

// SeriesByTVDB finds the TMDB id of the show TVDB knows as tvdbID (TMDB's /find), for
// bringing over a Sonarr library — Sonarr names shows by TVDB id. ErrNotFound when TMDB
// has no show for it. Adult entries are never answered.
func (t *TMDB) SeriesByTVDB(ctx context.Context, tvdbID int) (int, error) {
	if tvdbID <= 0 {
		return 0, ErrNotFound
	}
	q := url.Values{}
	q.Set("external_source", "tvdb_id")
	body, err := t.get(ctx, "/find/"+strconv.Itoa(tvdbID), q)
	if err != nil {
		return 0, err
	}
	var payload struct {
		TVResults []struct {
			ID    int  `json:"id"`
			Adult bool `json:"adult"`
		} `json:"tv_results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("tmdb: parse find: %w", err)
	}
	for _, r := range payload.TVResults {
		if r.ID > 0 && !r.Adult {
			return r.ID, nil
		}
	}
	return 0, ErrNotFound
}

// GetSeries fetches full details for a TV series: metadata plus every season and
// its episodes. Seasons are fetched concurrently.
func (t *TMDB) GetSeries(ctx context.Context, tmdbID int) (*SeriesDetails, error) {
	q := url.Values{}
	q.Set("append_to_response", "credits,external_ids,alternative_titles,content_ratings")
	body, err := t.get(ctx, "/tv/"+strconv.Itoa(tmdbID), q)
	if err != nil {
		return nil, err
	}
	var s tmdbSeries
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("tmdb: parse series: %w", err)
	}
	d := &SeriesDetails{
		SeriesResult:  s.toResult(),
		IMDBID:        s.ExternalIDs.IMDBID,
		Status:        s.Status,
		OriginalName:  s.OriginalName,
		OriginalLang:  s.OriginalLanguage,
		OriginCountry: s.OriginCountry,
		TVDBID:        s.ExternalIDs.TVDBID,
		ContentRating: usContentRating(s.ContentRatings.Results),
	}
	if s.BackdropPath != "" {
		d.BackdropURL = tmdbBackdropBase + s.BackdropPath
	}
	if len(s.Networks) > 0 {
		d.Network = s.Networks[0].Name
	}
	for _, g := range s.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	for _, a := range s.AlternativeTitles.Results {
		if title := strings.TrimSpace(a.Title); title != "" {
			d.AltTitles = append(d.AltTitles, AltTitle{Title: title, Country: a.Country, Type: a.Type})
		}
	}
	for _, c := range s.Credits.Cast {
		if len(d.Cast) >= maxCast {
			break
		}
		cm := CastMember{ID: c.ID, Name: c.Name, Character: c.Character}
		if c.ProfilePath != "" {
			cm.ProfileURL = tmdbProfileBase + c.ProfilePath
		}
		d.Cast = append(d.Cast, cm)
	}

	// Fetch each season's episodes concurrently.
	d.Seasons = make([]SeasonDetails, len(s.Seasons))
	d.NumberingSource = "tmdb"
	var wg sync.WaitGroup
	var partial atomic.Bool
	for i, sn := range s.Seasons {
		sd := SeasonDetails{SeasonNumber: sn.SeasonNumber, Name: sn.Name, Overview: sn.Overview, AirDate: sn.AirDate}
		if sn.PosterPath != "" {
			sd.PosterURL = tmdbImageBase + sn.PosterPath
		}
		d.Seasons[i] = sd
		wg.Add(1)
		go func(i, seasonNum int) {
			defer wg.Done()
			// A panic decoding one season counts as that season failing to load.
			var eps []EpisodeDetails
			err := safego.Call(nil, "tmdb season episodes", func() error {
				var e error
				eps, e = t.seasonEpisodes(ctx, tmdbID, seasonNum)
				return e
			})
			if err == nil {
				d.Seasons[i].Episodes = eps
			} else {
				partial.Store(true)
			}
		}(i, sn.SeasonNumber)
	}
	wg.Wait()
	// A season that failed to load comes back empty, which reads exactly like a season
	// with no episodes. The listing is incomplete, so it must not be trusted to number
	// the show — the series module treats it like a failed numbering source.
	d.NumberingFallback = partial.Load()
	return d, nil
}

func (t *TMDB) seasonEpisodes(ctx context.Context, tmdbID, season int) ([]EpisodeDetails, error) {
	body, err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tmdbID, season), url.Values{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Episodes []struct {
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			Overview      string `json:"overview"`
			AirDate       string `json:"air_date"`
			Runtime       int    `json:"runtime"`
			StillPath     string `json:"still_path"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := make([]EpisodeDetails, 0, len(payload.Episodes))
	for _, e := range payload.Episodes {
		ed := EpisodeDetails{EpisodeNumber: e.EpisodeNumber, Title: e.Name, Overview: e.Overview, AirDate: e.AirDate, Runtime: e.Runtime}
		if e.StillPath != "" {
			ed.StillURL = tmdbBackdropBase + e.StillPath
		}
		out = append(out, ed)
	}
	return out, nil
}

type tmdbContentRating struct {
	Country string `json:"iso_3166_1"`
	Rating  string `json:"rating"`
}

type tmdbSeries struct {
	ID               int         `json:"id"`
	Adult            bool        `json:"adult"`
	Name             string      `json:"name"`
	OriginalName     string      `json:"original_name"`
	OriginalLanguage string      `json:"original_language"`
	OriginCountry    []string    `json:"origin_country"`
	FirstAirDate     string      `json:"first_air_date"`
	Overview         string      `json:"overview"`
	PosterPath       string      `json:"poster_path"`
	BackdropPath     string      `json:"backdrop_path"`
	VoteAverage      float64     `json:"vote_average"`
	Status           string      `json:"status"`
	Genres           []tmdbNamed `json:"genres"`
	Networks         []tmdbNamed `json:"networks"`
	Seasons          []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		AirDate      string `json:"air_date"`
		PosterPath   string `json:"poster_path"`
		EpisodeCount int    `json:"episode_count"`
	} `json:"seasons"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
		TVDBID int    `json:"tvdb_id"`
	} `json:"external_ids"`
	// For TV the append is {"results": [...]} (movies use "titles").
	AlternativeTitles struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Title   string `json:"title"`
			Type    string `json:"type"`
		} `json:"results"`
	} `json:"alternative_titles"`
	// content_ratings: one TV rating per country.
	ContentRatings struct {
		Results []tmdbContentRating `json:"results"`
	} `json:"content_ratings"`
	CreatedBy []tmdbCast `json:"created_by"`
	Credits   struct {
		Cast []tmdbCast `json:"cast"`
		Crew []tmdbCrew `json:"crew"`
	} `json:"credits"`
	Videos          tmdbVideos          `json:"videos"`
	Recommendations tmdbRecommendations `json:"recommendations"`
	// Translations is the detail sheet's "translations" append, read for its overviews.
	Translations tmdbOverviewTranslations `json:"translations"`
}

// tmdbVideo is one entry from TMDB's /videos append (trailers, teasers, clips).
type tmdbVideo struct {
	Key      string `json:"key"`
	Site     string `json:"site"`
	Type     string `json:"type"`
	Official bool   `json:"official"`
}

type tmdbVideos struct {
	Results []tmdbVideo `json:"results"`
}

// tmdbRecommendations is TMDB's /recommendations append (rows share the discover shape).
type tmdbRecommendations struct {
	Results []tmdbDiscoverItem `json:"results"`
}

func (s tmdbSeries) toResult() SeriesResult {
	poster := ""
	if s.PosterPath != "" {
		poster = tmdbImageBase + s.PosterPath
	}
	return SeriesResult{
		TMDBID: s.ID, Title: s.Name, Year: yearOf(s.FirstAirDate),
		Overview: s.Overview, PosterURL: poster, VoteAverage: s.VoteAverage,
		OriginCountry: s.OriginCountry,
	}
}

// GetCollection fetches a TMDB collection's member movies, sorted by release
// year (earliest first) so a franchise reads in order. Kept for 6 hours (stale served
// while it refreshes): Discover's collection rows and pages read the same few often.
func (t *TMDB) GetCollection(ctx context.Context, collectionID int) (*Collection, error) {
	return swr(ctx, t.disk, "tmdb:collection:v1:"+strconv.Itoa(collectionID), collectionTTL, func(ctx context.Context) (*Collection, error) {
		body, err := t.get(ctx, "/collection/"+strconv.Itoa(collectionID), url.Values{})
		if err != nil {
			return nil, err
		}
		return parseTMDBCollection(body)
	})
}

// collectionTTL is how long a collection's member list is kept.
const collectionTTL = 6 * time.Hour

// parseTMDBCollection maps /collection/{id}. A member TMDB flags adult, or whose title
// trips the shared adult filter, is left out: the always-on filter holds here as on
// every Discover card, and for "add the whole collection" too.
func parseTMDBCollection(body []byte) (*Collection, error) {
	var payload struct {
		ID           int         `json:"id"`
		Name         string      `json:"name"`
		Overview     string      `json:"overview"`
		PosterPath   string      `json:"poster_path"`
		BackdropPath string      `json:"backdrop_path"`
		Parts        []tmdbMovie `json:"parts"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("tmdb: parse collection: %w", err)
	}
	c := &Collection{ID: payload.ID, Name: payload.Name, Overview: payload.Overview}
	if payload.PosterPath != "" {
		c.PosterURL = tmdbImageBase + payload.PosterPath
	}
	if payload.BackdropPath != "" {
		c.BackdropURL = tmdbBackdropBase + payload.BackdropPath
	}
	for _, m := range payload.Parts {
		if m.Adult || adultfilter.Matches(m.Title) {
			continue
		}
		c.Members = append(c.Members, m.toResult())
	}
	sort.Slice(c.Members, func(i, j int) bool {
		if c.Members[i].Year != c.Members[j].Year {
			return c.Members[i].Year < c.Members[j].Year
		}
		return c.Members[i].Title < c.Members[j].Title
	})
	return c, nil
}

// --- parsing (split out for offline tests) ---

type tmdbMovie struct {
	ID           int     `json:"id"`
	Adult        bool    `json:"adult"`
	Title        string  `json:"title"`
	ReleaseDate  string  `json:"release_date"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	VoteAverage  float64 `json:"vote_average"`
	IMDBID       string  `json:"imdb_id"`
	Runtime      int     `json:"runtime"`
	Status       string  `json:"status"`
	OriginalLang string  `json:"original_language"`
	// OriginalTitle, OriginCountry and ProductionCountries feed the titles a release may
	// be named by (movieAltTitles).
	OriginalTitle       string   `json:"original_title"`
	OriginCountry       []string `json:"origin_country"`
	ProductionCountries []struct {
		Code string `json:"iso_3166_1"`
	} `json:"production_countries"`
	// For movies the append is {"titles": [...]} (TV uses "results").
	AlternativeTitles struct {
		Titles []struct {
			Country string `json:"iso_3166_1"`
			Title   string `json:"title"`
			Type    string `json:"type"`
		} `json:"titles"`
	} `json:"alternative_titles"`

	Genres              []tmdbNamed `json:"genres"`
	ProductionCompanies []tmdbNamed `json:"production_companies"`
	BelongsToCollection *tmdbNamed  `json:"belongs_to_collection"`

	Credits struct {
		Cast []tmdbCast `json:"cast"`
		Crew []tmdbCrew `json:"crew"`
	} `json:"credits"`
	ReleaseDates struct {
		Results []tmdbReleaseDates `json:"results"`
	} `json:"release_dates"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
	Videos          tmdbVideos          `json:"videos"`
	Recommendations tmdbRecommendations `json:"recommendations"`
	// Translations is the detail sheet's "translations" append, read for its overviews.
	Translations tmdbOverviewTranslations `json:"translations"`
}

type tmdbCrew struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	ProfilePath string `json:"profile_path"`
}

type tmdbNamed struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tmdbCast struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}

type tmdbReleaseDates struct {
	Country      string `json:"iso_3166_1"`
	ReleaseDates []struct {
		Certification string `json:"certification"`
	} `json:"release_dates"`
}

func (m tmdbMovie) toResult() MovieResult {
	poster := ""
	if m.PosterPath != "" {
		poster = tmdbImageBase + m.PosterPath
	}
	return MovieResult{
		TMDBID:      m.ID,
		Title:       m.Title,
		Year:        yearOf(m.ReleaseDate),
		Overview:    m.Overview,
		PosterURL:   poster,
		VoteAverage: m.VoteAverage,
		ReleaseDate: m.ReleaseDate,
	}
}

func parseTMDBSearch(body []byte) []MovieResult {
	var payload struct {
		Results []tmdbMovie `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	out := make([]MovieResult, 0, len(payload.Results))
	for _, m := range payload.Results {
		out = append(out, m.toResult())
	}
	return out
}

func parseTMDBMovie(body []byte) (*MovieDetails, error) {
	var m tmdbMovie
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("tmdb: parse movie: %w", err)
	}
	d := &MovieDetails{
		MovieResult:      m.toResult(),
		IMDBID:           m.IMDBID,
		Runtime:          m.Runtime,
		Status:           m.Status,
		ReleaseDate:      m.ReleaseDate,
		OriginalLanguage: m.OriginalLang,
	}
	if m.BackdropPath != "" {
		d.BackdropURL = tmdbBackdropBase + m.BackdropPath
	}
	for _, g := range m.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	for _, c := range m.ProductionCompanies {
		d.Studios = append(d.Studios, c.Name)
	}
	if m.BelongsToCollection != nil {
		d.CollectionID = m.BelongsToCollection.ID
		d.CollectionName = m.BelongsToCollection.Name
	}
	d.Certification = usCertification(m.ReleaseDates.Results)
	d.OriginalTitle, d.AltTitles = movieAltTitles(m)
	for _, c := range m.Credits.Cast {
		if len(d.Cast) >= maxCast {
			break
		}
		cm := CastMember{ID: c.ID, Name: c.Name, Character: c.Character}
		if c.ProfilePath != "" {
			cm.ProfileURL = tmdbProfileBase + c.ProfilePath
		}
		d.Cast = append(d.Cast, cm)
	}
	return d, nil
}

// maxMovieAltTitles caps how many alternative titles a film keeps: each is a key a release
// may match, so a long tail of one-off translations only adds chances of a false match.
const maxMovieAltTitles = 15

// movieAltTitles picks the other titles a film's releases may be named by. The original
// title is kept only in the Latin alphabet (releases are named in ASCII; 레옹 never appears
// in one). Alternative titles come from the US, GB and the film's own countries — where
// English-language and original-language releases are named — Latin script only, minus
// any that key the same as the title, the original title or each other (parser.TitleKey),
// at most maxMovieAltTitles.
func movieAltTitles(m tmdbMovie) (original string, alts []string) {
	seen := map[string]bool{parser.TitleKey(m.Title): true}
	if o := strings.TrimSpace(m.OriginalTitle); o != "" && parser.IsLatin(o) {
		original = o
		seen[parser.TitleKey(o)] = true
	}
	countries := map[string]bool{"US": true, "GB": true}
	for _, c := range m.OriginCountry {
		countries[strings.ToUpper(c)] = true
	}
	if len(m.OriginCountry) == 0 {
		// Older answers carry no origin_country; where it was made stands in.
		for _, c := range m.ProductionCountries {
			countries[strings.ToUpper(c.Code)] = true
		}
	}
	for _, a := range m.AlternativeTitles.Titles {
		title := strings.TrimSpace(a.Title)
		k := parser.TitleKey(title)
		if title == "" || k == "" || seen[k] || !countries[strings.ToUpper(a.Country)] || !parser.IsLatin(title) {
			continue
		}
		seen[k] = true
		alts = append(alts, title)
		if len(alts) >= maxMovieAltTitles {
			break
		}
	}
	return original, alts
}

// usContentRating returns a show's US TV rating ("TV-Y7"), if TMDB has one.
func usContentRating(results []tmdbContentRating) string {
	for _, r := range results {
		if r.Country == "US" && strings.TrimSpace(r.Rating) != "" {
			return strings.TrimSpace(r.Rating)
		}
	}
	return ""
}

// usCertification returns the US content rating (e.g. "R"), if present.
func usCertification(results []tmdbReleaseDates) string {
	for _, r := range results {
		if r.Country != "US" {
			continue
		}
		for _, rd := range r.ReleaseDates {
			if rd.Certification != "" {
				return rd.Certification
			}
		}
	}
	return ""
}

func yearOf(releaseDate string) int {
	if len(releaseDate) >= 4 {
		if y, err := strconv.Atoi(releaseDate[:4]); err == nil {
			return y
		}
	}
	return 0
}
