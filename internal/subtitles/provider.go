package subtitles

import (
	"context"
	"errors"
)

// ErrNotConfigured means the subtitle provider is missing required config (API key etc.).
var ErrNotConfigured = errors.New("subtitle provider not configured")

// SearchRequest describes a subtitle search for one media file in one language.
type SearchRequest struct {
	IMDBID   string // e.g. "tt1375666" or "1375666" (empty falls back to title search)
	Title    string
	Year     int
	Season   int // 0 for movies
	Episode  int
	Language string // canonical internal/langs code ("en", "pt-br"); each provider maps it to its own
	// MovieHash is the OpenSubtitles hash of the actual file (see osHash). Optional; when
	// present, results uploaded against this exact release rank first — those are the
	// ones that are genuinely in sync.
	MovieHash string
	// ForeignParts is "exclude" (the default when empty) or "only". Foreign-parts-only
	// uploads are forced subtitles: a few lines, never the full track, so a search for
	// the full subtitle must not return them.
	ForeignParts string
}

// SubtitleResult is one candidate subtitle from a provider. The JSON is what the drawer's
// interactive search shows (SUB-28).
type SubtitleResult struct {
	FileID          string `json:"file_id"`
	Language        string `json:"language"`
	Release         string `json:"release"`
	Downloads       int    `json:"downloads"`
	HearingImpaired bool   `json:"hearing_impaired"`
	HashMatch       bool   `json:"hash_match"` // uploaded against this exact file — in sync by construction

	// What scoring (scoring.go) compares against the file on disk.
	FPS              float64 `json:"fps,omitempty"`           // frame rate the subtitle was timed for; 0 = not stated
	Trusted          bool    `json:"trusted,omitempty"`       // from a trusted uploader
	FileName         string  `json:"file_name,omitempty"`     // the uploaded file's name; a fallback when Release says nothing
	UploaderRank     string  `json:"uploader_rank,omitempty"` // e.g. "trusted", "administrator"
	Season           int     `json:"season,omitempty"`        // the episode it was uploaded for (0 for movies / not stated)
	Episode          int     `json:"episode,omitempty"`
	AITranslated     bool    `json:"ai_translated,omitempty"`      // machine or AI translation: never used
	ForeignPartsOnly bool    `json:"foreign_parts_only,omitempty"` // forced subtitle: only for a forced want
}

// Provider searches for and downloads external subtitles. Providers sit behind this
// interface (OpenSubtitles first) so more can be added with fallback, per the roadmap.
type Provider interface {
	// Available reports whether the provider is configured enough to search.
	Available() bool
	// CanDownload reports whether downloads are possible (some providers need an account).
	CanDownload() bool
	Search(ctx context.Context, req SearchRequest) ([]SubtitleResult, error)
	// Download returns the raw subtitle bytes (SRT) for a result's FileID.
	Download(ctx context.Context, fileID string) ([]byte, error)
}
