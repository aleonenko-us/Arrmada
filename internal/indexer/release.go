package indexer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Release is a single search result from an indexer, normalized across all
// indexer kinds. The acquisition pipeline parses and scores it downstream.
type Release struct {
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"` // indexer blurb; some carry author/narrator (e.g. MyAnonaMouse)
	DownloadURL string    `json:"download_url"`          // .torrent / .nzb / magnet
	InfoURL     string    `json:"info_url,omitempty"`    // the release's details page on the tracker
	InfoHash    string    `json:"info_hash,omitempty"`   // torrent
	SizeBytes   int64     `json:"size_bytes"`
	Seeders     int       `json:"seeders,omitempty"` // torrent
	Peers       int       `json:"peers,omitempty"`   // torrent
	PublishedAt time.Time `json:"published_at,omitempty"`
	Categories  []int     `json:"categories,omitempty"`
	Indexer     string    `json:"indexer"`   // which indexer returned it
	Transport   Transport `json:"transport"` // torrent | usenet

	// Structured book metadata, when the indexer exposes it as real fields (e.g.
	// MyAnonaMouse's API) rather than only in the title. These let the book
	// pipeline show narrator/author/series/format reliably instead of scraping
	// them back out of the release name.
	Author   string `json:"author,omitempty"`
	Narrator string `json:"narrator,omitempty"`
	Series   string `json:"series,omitempty"`
	Language string `json:"language,omitempty"`
	Format   string `json:"format,omitempty"` // ebook/audiobook file type: EPUB, M4B, MP3…

	// The ids the indexer tagged the release with, when it says (Torznab's imdb/imdbid,
	// tmdbid and tvdbid attrs; TorrentLeech's imdbID). IMDBID is normalised to "tt" plus
	// at least seven digits so it compares with the library's.
	IMDBID string `json:"imdb_id,omitempty"`
	TMDBID int    `json:"tmdb_id,omitempty"`
	TVDBID int    `json:"tvdb_id,omitempty"`
	// MatchedByID: the release came back from a search by id rather than by text. It is
	// not proof on its own — some indexers ignore an id they don't index and answer with
	// their newest uploads — so matching still checks the title (or the release's own
	// IMDb tag).
	MatchedByID bool `json:"matched_by_id,omitempty"`
}

// NormalizeIMDB returns an IMDb id as "tt" plus at least seven digits ("tt0111161"), from
// either form indexers use ("tt0111161", "0111161", "111161"); "" when it isn't one.
func NormalizeIMDB(s string) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "tt")
	if s == "" {
		return ""
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// SameIMDB reports whether two IMDb ids name the same title. Unknown ids never match.
func SameIMDB(a, b string) bool {
	na := NormalizeIMDB(a)
	return na != "" && na == NormalizeIMDB(b)
}

// SizeGB returns the release size in gigabytes.
func (r Release) SizeGB() float64 {
	return float64(r.SizeBytes) / (1 << 30)
}
