package notify

import (
	"path"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// What each catalog event carries under "data" in a webhook payload (webhook.go), a
// realistic sample for "Send test", and its default severity. Every catalog event needs an
// entry here (a test fails otherwise), and docs/n8n.md's event reference is written from
// it (TestWebhookDocsUpToDate).
//
// The fields are picked one by one from the event's payload, never passed through whole:
// what a producer adds to its bus event later (a file path, a token) doesn't reach a
// webhook until it's declared here. The rules for what may be declared:
//   - people by display name only (the requester's or a Plex viewer's), never an email,
//     an account id or anything they could sign in with;
//   - no secrets, tokens, API keys or download links;
//   - nothing about what anyone listens to on Arrmada's audiobook server: admins see how
//     much and when, never what (no catalog event is about that listening, and none may be).
//     The Plex/Jellyfin stream events carry what's playing there, as their chat alerts do.

// DataField is one field under "data".
type DataField struct {
	Name string
	// Type is how it's written: "string", "integer", "boolean", "string[]" or "time"
	// (RFC 3339). A value the event doesn't have is null.
	Type string
	Doc  string
	// From is the payload key it's read from ("" = Name).
	From string
	// ZeroNull writes 0 as null (an id or year the event doesn't know).
	ZeroNull bool
	// Value computes the field instead of reading it (e.g. a quality parsed from a name).
	Value func(d map[string]any) any
}

// webhookEvent is one event's webhook declaration.
type webhookEvent struct {
	severity string // "" = info
	fields   []DataField
	sample   map[string]any
}

// read is the field's value from an event's payload, nil when it hasn't one.
func (f DataField) read(d map[string]any) any {
	if f.Value != nil {
		return f.Value(d)
	}
	key := f.From
	if key == "" {
		key = f.Name
	}
	v, ok := d[key]
	if !ok || v == nil {
		return nil
	}
	switch f.Type {
	case "integer":
		n := num(d, key)
		if n == 0 && f.ZeroNull {
			return nil
		}
		return n
	case "boolean":
		if b, ok := v.(bool); ok {
			return b
		}
		return nil
	case "string[]":
		list := strs(d, key)
		out := make([]string, 0, len(list))
		for _, s := range list {
			out = append(out, jsonText(s))
		}
		return out
	case "time":
		if n := num(d, key); n > 0 {
			return time.Unix(n, 0).UTC().Format(time.RFC3339)
		}
		return nil
	default:
		return jsonText(str(d, key))
	}
}

// webhookData is an event's "data": every declared field, null where the payload has none.
func webhookData(key string, d map[string]any) map[string]any {
	out := map[string]any{}
	spec, ok := webhookEvents[key]
	if !ok {
		return out
	}
	for _, f := range spec.fields {
		out[f.Name] = f.read(d)
	}
	return out
}

// WebhookSample is the sample payload "Send test" uses for an event (nil for an unknown one).
func WebhookSample(key string) map[string]any {
	spec, ok := webhookEvents[key]
	if !ok {
		return nil
	}
	out := make(map[string]any, len(spec.sample))
	for k, v := range spec.sample {
		out[k] = v
	}
	return out
}

// Field builders, to keep the table below readable.
func fStr(name, doc string) DataField  { return DataField{Name: name, Type: "string", Doc: doc} }
func fInt(name, doc string) DataField  { return DataField{Name: name, Type: "integer", Doc: doc} }
func fBool(name, doc string) DataField { return DataField{Name: name, Type: "boolean", Doc: doc} }
func fList(name, doc string) DataField { return DataField{Name: name, Type: "string[]", Doc: doc} }
func fID(name, from, doc string) DataField {
	return DataField{Name: name, From: from, Type: "integer", ZeroNull: true, Doc: doc}
}
func fFrom(name, from, typ, doc string) DataField {
	return DataField{Name: name, From: from, Type: typ, Doc: doc}
}

// qualityFields are a release's or file's quality, read from its name the way grabbing
// reads it. base: the source is a file path, of which only the file name is read.
func qualityFields(src string, base bool, what string) []DataField {
	parsed := func(d map[string]any) (parser.Release, bool) {
		name := str(d, src)
		if base {
			name = path.Base(strings.ReplaceAll(name, `\`, "/"))
		}
		if name == "" || name == "." || name == "/" {
			return parser.Release{}, false
		}
		return parser.Parse(name), true
	}
	text := func(get func(parser.Release) string) func(map[string]any) any {
		return func(d map[string]any) any {
			r, ok := parsed(d)
			if !ok || get(r) == "" {
				return nil
			}
			return get(r)
		}
	}
	return []DataField{
		{Name: "resolution", Type: "string", Doc: "2160p, 1080p, 720p, 576p or 480p, read from the " + what + "; null when it doesn't say.",
			Value: text(func(r parser.Release) string { return string(r.Resolution) })},
		{Name: "source", Type: "string", Doc: "Remux, BluRay, WEB-DL, WEBRip, HDTV, DVD or CAM, read from the " + what + ".",
			Value: text(func(r parser.Release) string { return string(r.Source) })},
		{Name: "codec", Type: "string", Doc: "x265, x264, AV1, VC-1 or XviD, read from the " + what + ".",
			Value: text(func(r parser.Release) string { return string(r.Codec) })},
		{Name: "hdr", Type: "string[]", Doc: "HDR formats named in the " + what + " (DV, HDR10, HDR10+, HLG); empty for SDR.",
			Value: func(d map[string]any) any {
				r, ok := parsed(d)
				if !ok {
					return nil
				}
				return append([]string{}, r.HDR...)
			}},
	}
}

// needsYouFields are the Needs-you and health events' fields (catalog_needsyou.go): one
// item, or several gathered into one message.
func needsYouFields() []DataField {
	return []DataField{
		fInt("count", "How many items this message is about. 1: title and detail describe it; more: names lists them."),
		fStr("title", "The item, in a sentence (null when count is more than 1)."),
		fStr("detail", "More about the item (null when count is more than 1)."),
		fList("names", "The items' short names, when count is more than 1."),
		fStr("level", "warning or error."),
	}
}

// streamFields are a Plex or Jellyfin stream's fields.
func streamFields() []DataField {
	return []DataField{
		fStr("server", "plex or jellyfin."),
		fStr("user", "The viewer's display name on Plex or Jellyfin."),
		fStr("title", "What's playing: a film's title, or \"Show · Episode\" for an episode."),
		fFrom("show_title", "grandparent_title", "string", "The show, for an episode."),
		fStr("media_type", "movie, episode or track."),
		DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "Release year."},
		fStr("player", "The player's name (e.g. Living Room TV)."),
		fStr("platform", "The player's platform (e.g. Roku, Chrome)."),
		fStr("decision", "direct_play, direct_stream or transcode."),
		fStr("location", "lan (inside the network) or wan (remote)."),
		fFrom("item_id", "rating_key", "string", "The server's id for the item (Plex's rating key)."),
	}
}

func needsYouSample(title, detail, level string) map[string]any {
	return map[string]any{"count": 1, "title": title, "detail": detail, "link": "/activity/needs-you", "level": level}
}

var webhookEvents = map[string]webhookEvent{
	"request.created": {
		fields: []DataField{
			fID("request_id", "id", "The request's id."),
			fStr("media_type", "movie, series or book."),
			fID("tmdb_id", "tmdb_id", "TMDB id (null for a book)."),
			fStr("title", "The title asked for."),
			DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "Release year."},
			fStr("seasons", "For a show: which seasons (e.g. \"S1–2\"); empty for the whole show."),
			fFrom("requested_by", "requested_by_name", "string", "The requester's display name."),
			fStr("note", "The requester's note, if they wrote one."),
			fBool("rerequest", "true when it was declined before and asked for again."),
			fStr("decline_reason", "Why it was declined before (re-requests)."),
		},
		sample: map[string]any{
			"id": 42, "media_type": "movie", "tmdb_id": 438631, "title": "Dune", "year": 2021,
			"requested_by_name": "Sam", "note": "The 4K one if you can", "rerequest": false, "decline_reason": "",
		},
	},
	"request.auto_approved": {
		fields: []DataField{
			fID("request_id", "id", "The request's id."),
			fStr("media_type", "movie, series or book."),
			fID("tmdb_id", "tmdb_id", "TMDB id (null for a book)."),
			fStr("title", "The title asked for."),
			DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "Release year."},
			fStr("requested_by", "The requester's display name."),
		},
		sample: map[string]any{"id": 43, "media_type": "series", "tmdb_id": 94997, "title": "House of the Dragon", "year": 2022, "requested_by": "Alex"},
	},
	"release.grabbed": {
		fields: append([]DataField{
			fFrom("release", "title", "string", "The release's name as the indexer listed it."),
			fStr("indexer", "The indexer it came from (\"manual\" for an uploaded file)."),
			fStr("network", "torrent or usenet."),
			fStr("media_type", "movie, series, book or music (null when it can't be told)."),
		}, qualityFields("title", false, "release name")...),
		sample: map[string]any{
			"title": "Dune.2021.2160p.UHD.BluRay.REMUX.DV.HDR10.HEVC.TrueHD.Atmos-GROUP", "indexer": "MyIndexer",
			"network": "torrent", "media_type": "movie",
		},
	},
	"movie.imported": {
		fields: append([]DataField{
			fID("movie_id", "id", "The movie's id in Arrmada."),
			fID("tmdb_id", "tmdb_id", "TMDB id."),
			fStr("title", "The movie's title."),
			DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "Release year."},
			fID("version_id", "version_id", "The extra version (e.g. a Director's Cut) the file belongs to; null for the main file."),
			fBool("upgrade", "true when it replaced a file of lower quality."),
			fFrom("file", "path", "string", "The imported file's full path inside Arrmada's container."),
		}, qualityFields("path", true, "file name")...),
		sample: map[string]any{
			"id": 12, "tmdb_id": 438631, "title": "Dune", "year": 2021, "version_id": 0, "upgrade": true,
			"path": "/movies/Dune (2021)/Dune (2021) [Remux-2160p DV HDR10 HEVC].mkv",
		},
	},
	"episodes.imported": {
		fields: append([]DataField{
			fID("series_id", "id", "The show's id in Arrmada."),
			fID("tmdb_id", "tmdb_id", "TMDB id."),
			fStr("title", "The show's title."),
			DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "The show's first-air year."},
			fInt("count", "How many episodes were imported."),
			fList("episodes", "The episodes imported, as S01E02."),
			fStr("release", "The download they came from."),
			DataField{Name: "size_bytes", Type: "integer", ZeroNull: true, Doc: "The download's size in bytes."},
		}, qualityFields("release", false, "release name")...),
		sample: map[string]any{
			"id": 7, "tmdb_id": 94997, "title": "House of the Dragon", "year": 2022, "count": 2,
			"episodes": []string{"S02E01", "S02E02"}, "release": "House.of.the.Dragon.S02.1080p.WEB-DL.DDP5.1.H.264-GROUP",
			"size_bytes": 8_421_000_000,
		},
	},
	"book.imported": {
		fields: []DataField{
			fID("book_id", "id", "The book's id in Arrmada."),
			fStr("title", "The book's title."),
			fStr("author", "The author."),
			DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "Publication year."},
			fStr("edition", "ebook or audiobook."),
			fStr("version", "For an extra audiobook version: its label (e.g. a narrator)."),
			fStr("format", "The file format (EPUB, M4B, MP3, …)."),
			DataField{Name: "size_bytes", Type: "integer", ZeroNull: true, Doc: "Size of the imported files in bytes."},
		},
		sample: map[string]any{
			"id": 31, "title": "Project Hail Mary", "author": "Andy Weir", "year": 2021, "edition": "audiobook",
			"format": "M4B", "size_bytes": 960_000_000,
		},
	},
	"music.imported": {
		fields: []DataField{
			fID("artist_id", "artist_id", "The artist's id in Arrmada."),
			fID("album_id", "album_id", "The album's id in Arrmada."),
			fStr("artist", "The artist."),
			fStr("album", "The album."),
			fFrom("tracks", "placed", "integer", "How many tracks were imported."),
			fStr("quality", "The tracks' quality (e.g. FLAC, MP3 320)."),
		},
		sample: map[string]any{"artist_id": 3, "album_id": 18, "artist": "Daft Punk", "album": "Discovery", "placed": 14, "quality": "FLAC"},
	},
	"plex.stream.started": {
		fields: streamFields(),
		sample: map[string]any{
			"server": "plex", "user": "Sam", "title": "Severance · Hello, Ms. Cobel", "grandparent_title": "Severance",
			"media_type": "episode", "year": 2025, "player": "Living Room TV", "platform": "Roku",
			"decision": "direct_play", "location": "lan", "rating_key": "51234",
		},
	},
	"plex.buffering": {
		severity: "warning",
		fields:   append(streamFields(), fStr("cause", "Why it's buffering, when the server says (e.g. transcoder too slow, network).")),
		sample: map[string]any{
			"server": "plex", "user": "Alex", "title": "Dune", "media_type": "movie", "year": 2021,
			"player": "iPhone", "platform": "iOS", "decision": "transcode", "location": "wan", "rating_key": "40001",
			"cause": "network",
		},
	},
	EventAuthorRelease: {
		fields: []DataField{
			fID("author_id", "author_id", "The author's id in Arrmada."),
			fStr("author", "The author."),
			fInt("count", "How many new books were added."),
			fList("titles", "The new books' titles."),
		},
		sample: map[string]any{"author_id": 5, "author": "Brandon Sanderson", "count": 2, "titles": []string{"Wind and Truth", "Isles of the Emberdark"}},
	},
	EventConvertFailed: {
		severity: "warning",
		fields: []DataField{
			fStr("title", "The file's title (with the version or episode)."),
			fFrom("item", "key", "string", "Convert's key for the file, e.g. movie:12 or episode:7:2:1."),
			fFrom("tries", "count", "integer", "How many times it failed."),
			fFrom("error", "note", "string", "The last failure."),
		},
		sample: map[string]any{"title": "Dune (2021)", "key": "movie:12", "count": 3, "note": "ffmpeg exited with status 1: Invalid data found when processing input"},
	},
	EventConvertStalled: {
		severity: "warning",
		fields:   needsYouFields(),
		sample:   needsYouSample("Convert is waiting for space on the library disk", "It needs 18 GB free and there's 6 GB.", "warning"),
	},
	EventConvertSummary: {
		fields: []DataField{
			fInt("files", "Files converted since the last summary."),
			fInt("saved_bytes", "Space freed, in bytes."),
			fInt("problems", "Files that ran into a problem."),
			fFrom("since", "since", "time", "When the counts started."),
		},
		sample: map[string]any{"files": 7, "saved_bytes": 335_000_000_000, "problems": 1, "since": 1_799_900_000},
	},
	EventIssueReported: {
		severity: "warning",
		fields: []DataField{
			fID("issue_id", "id", "The issue's id."),
			fStr("media_type", "movie or series."),
			fID("tmdb_id", "tmdb_id", "TMDB id."),
			fStr("title", "The title the problem is with."),
			DataField{Name: "year", Type: "integer", ZeroNull: true, Doc: "Release year."},
			DataField{Name: "season", Type: "integer", ZeroNull: true, Doc: "For an episode: its season."},
			DataField{Name: "episode", Type: "integer", ZeroNull: true, Doc: "For an episode: its number."},
			fStr("category", "video, audio, subtitles, wrong_file or other."),
			fStr("note", "What the reporter wrote."),
			fFrom("reported_by", "reported_by_name", "string", "The reporter's display name."),
		},
		sample: map[string]any{
			"id": 9, "media_type": "series", "tmdb_id": 95396, "title": "Severance", "year": 2022, "season": 2, "episode": 3,
			"category": "subtitles", "note": "Subtitles are a few seconds late", "reported_by_name": "Sam",
		},
	},
	EventImportHeld: {
		severity: "warning",
		fields:   needsYouFields(),
		sample:   needsYouSample("Dune.2021.2160p.WEB-DL is held for review", "The release's year doesn't match the movie's.", "warning"),
	},
	EventImportStuck: {
		severity: "warning",
		fields:   needsYouFields(),
		sample:   needsYouSample("Dune.2021.2160p.WEB-DL keeps failing to import", "No video file found in the download.", "error"),
	},
	EventDownloadFailed: {
		severity: "error",
		fields:   needsYouFields(),
		sample:   needsYouSample("Dune.2021.2160p.WEB-DL errored in the download client", "qBittorrent: missing files.", "error"),
	},
	EventDownloadStalled: {
		severity: "warning",
		fields:   needsYouFields(),
		sample: map[string]any{
			"count": 3, "names": []string{"Dune.2021.2160p.WEB-DL", "Severance.S02E03.1080p", "Arcane.S02.2160p"},
			"link": "/activity/needs-you#stalled", "level": "warning",
		},
	},
	EventHealthProblem: {
		severity: "warning",
		fields:   needsYouFields(),
		sample:   needsYouSample("qBittorrent can't be reached", "Connection refused at 192.168.1.20:8080.", "error"),
	},
	EventHealthResolved: {
		fields: needsYouFields(),
		sample: map[string]any{"count": 1, "title": "qBittorrent can't be reached", "link": "/settings/status#health"},
	},
	// Never which account: its name is what it signs in with.
	EventTwoFactorFailures: {
		severity: "warning",
		fields: []DataField{
			fInt("count", "Wrong two-factor codes for the account in the last hour, each typed after its right password."),
		},
		sample: map[string]any{"count": 5},
	},
}

// levelSeverity are the events whose severity is the item's own level ("warning" or
// "error") when its payload carries one: the problems. health.resolved carries the level
// of the problem that cleared, which is not how severe the good news is.
var levelSeverity = map[string]bool{
	EventImportHeld: true, EventImportStuck: true, EventDownloadFailed: true, EventDownloadStalled: true,
	EventHealthProblem: true, EventConvertStalled: true,
}

// WebhookFields lists an event's data fields, for the docs.
func WebhookFields(key string) []DataField {
	return append([]DataField(nil), webhookEvents[key].fields...)
}
