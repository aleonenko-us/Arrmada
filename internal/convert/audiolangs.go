package convert

import (
	"context"
	"encoding/json"

	langtab "github.com/tristenlammi/arrmada/internal/langs" // "langs" is a test helper here
	"github.com/tristenlammi/arrmada/internal/quality"
)

// The audio languages of library files, from the library index: every movie and episode
// file is analysed there (IndexAll at startup and nightly, IndexMovie/IndexSeries on every
// import), so the language badges and the quality profile's language rules read what was
// recorded and never probe a file on a page load.
//
// A track's own language tag is what it is. An untagged one ("und", or nothing) takes the
// language the subtitle AI heard on it when it has listened (DetectedAudio); otherwise it
// stays unidentified. Commentary tracks aren't the film's audio and are left out.

// DetectedAudio reads, for each path (→ its size in bytes, 0 = unknown), the language the
// subtitle AI heard on each untagged audio track, by 0:a:N index (subtitles.Service.
// DetectedAudio; convert must not import subtitles).
type DetectedAudio func(ctx context.Context, paths map[string]int64) map[string]map[int]string

// SetDetectedAudio wires the subtitle AI's detections.
func (s *Service) SetDetectedAudio(fn DetectedAudio) { s.detected = fn }

// heardFor is what the AI heard on one file's untagged tracks, nil when nothing (or no
// source is wired).
func (s *Service) heardFor(ctx context.Context, path string, sizeBytes int64) map[int]string {
	if s.detected == nil || path == "" {
		return nil
	}
	return s.detected(ctx, map[string]int64{path: sizeBytes})[path]
}

// audioLangs reads the languages of a file's (non-commentary) audio tracks: each tagged
// one's base code, an untagged one's heard language, and whether any track is neither.
func audioLangs(mi *MediaInfo, heard map[int]string) (codes []string, untagged bool) {
	for _, a := range mi.Audio {
		if a.Commentary {
			continue
		}
		code := langtab.AudioBase(a.Lang)
		if code == "" {
			code = langtab.AudioBase(heard[a.AudIndex])
		}
		if code == "" {
			untagged = true
			continue
		}
		if !containsCode(codes, code) {
			codes = append(codes, code)
		}
	}
	return codes, untagged
}

// untaggedAudio reports a (non-commentary) audio track with no language the table knows.
func untaggedAudio(mi *MediaInfo) bool {
	for _, a := range mi.Audio {
		if !a.Commentary && langtab.AudioBase(a.Lang) == "" {
			return true
		}
	}
	return false
}

func containsCode(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// AudioBadges is a file's audio languages as the detail pages show them, one badge per
// (non-commentary) track before OrderAudio dedupes and orders them.
func AudioBadges(mi *MediaInfo, heard map[int]string) []langtab.AudioLang {
	var out []langtab.AudioLang
	for _, a := range mi.Audio {
		if a.Commentary {
			continue
		}
		if l := langtab.NewAudioLang(a.Lang, false); l.Code != "" {
			out = append(out, l)
			continue
		}
		out = append(out, langtab.NewAudioLang(heard[a.AudIndex], true))
	}
	return out
}

// FileAudio is one analysed file's audio, for the detail pages.
type FileAudio struct {
	SizeBytes int64
	// Badges are the file's audio languages, one per track (langs.OrderAudio orders them).
	Badges []langtab.AudioLang
	// Facts are the file's probed facts, untagged tracks read as heard — what the quality
	// profile's language rules judge (quality.StoredProfile.LanguageFit).
	Facts quality.FileFacts
}

// AudioFor is the analysed files of one movie (mediaType "movie", id = the movie) or one
// show ("episode", id = the series), by path, from the library index alone. Files not
// analysed yet, or analysed by an older version, are absent: their badges read "not read
// yet" until the background index reaches them.
func (s *Service) AudioFor(ctx context.Context, mediaType string, id int64) (map[string]FileAudio, error) {
	out := map[string]FileAudio{}
	if s.index == nil || id <= 0 {
		return out, nil
	}
	col := "movie_id"
	if mediaType == "episode" {
		col = "series_id"
	}
	rows, err := s.index.db.QueryContext(ctx,
		`SELECT path, size_bytes, info_json FROM convert_library
		  WHERE media_type = ? AND `+col+` = ? AND info_json <> '' AND info_ver = ? AND video_codec <> ''`,
		mediaType, id, probeSchemaVersion)
	if err != nil {
		return nil, err
	}
	infos := map[string]*MediaInfo{}
	sizes := map[string]int64{}
	untagged := map[string]int64{}
	for rows.Next() {
		var path, infoJSON string
		var size int64
		if err := rows.Scan(&path, &size, &infoJSON); err != nil {
			rows.Close()
			return nil, err
		}
		var mi MediaInfo
		if json.Unmarshal([]byte(infoJSON), &mi) != nil {
			continue
		}
		infos[path], sizes[path] = &mi, size
		if untaggedAudio(&mi) {
			untagged[path] = size
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// One read of the AI's detections for the files that need it.
	var heard map[string]map[int]string
	if len(untagged) > 0 && s.detected != nil {
		heard = s.detected(ctx, untagged)
	}
	for path, mi := range infos {
		out[path] = FileAudio{SizeBytes: sizes[path], Badges: AudioBadges(mi, heard[path]), Facts: FactsHeard(mi, heard[path])}
	}
	return out, nil
}
