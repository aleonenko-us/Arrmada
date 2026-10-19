package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Music naming (MUS-34): how an album's folder and its track files are named, from
// templates the user can set (Settings → Naming & metadata), on the naming engine books
// use (naming_engine.go). Three templates: the album folder ("/" between levels), a
// track's file, and a compilation's track file (a Various Artists album, where the file
// name needs the track's own artist). A track template may hold a "/" too, for a folder
// per disc inside the album folder.
//
// The defaults give "<Album Artist>/<Album> (<Year>)/<NN> - <Title>.<ext>", what Arrmada
// always did for an ordinary album, so a library that never touches the setting needs no
// rename. A multi-disc album's files start "1-04", "2-04" (the disc only when there's
// more than one), which Plex, Jellyfin and Navidrome all order correctly.

// The default music naming.
const (
	DefaultMusicFolder           = "{Album Artist}/{Album Title} ({Original Year})"
	DefaultMusicTrack            = "{Disc Prefix}{Track:00} - {Track Title}"
	DefaultMusicCompilationTrack = "{Disc Prefix}{Track:00} - {Track Artist} - {Track Title}"
)

// MusicNaming is the music templates.
type MusicNaming struct {
	Folder      string `json:"folder"`
	Track       string `json:"track"`
	Compilation string `json:"compilation"`
}

// DefaultMusicNaming is the built-in scheme.
var DefaultMusicNaming = MusicNaming{Folder: DefaultMusicFolder, Track: DefaultMusicTrack, Compilation: DefaultMusicCompilationTrack}

// MusicNamingPreset is a ready-made scheme the settings page offers.
type MusicNamingPreset struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Naming      MusicNaming `json:"naming"`
}

// MusicNamingPresets are the schemes offered as one click. The first is the default.
var MusicNamingPresets = []MusicNamingPreset{
	{
		ID: "default", Label: "Artist/Album (Year) (default)",
		Description: "One folder per album artist, one per album inside it; a disc prefix only on multi-disc albums. Reads well in Plex, Jellyfin and Navidrome.",
		Naming:      DefaultMusicNaming,
	},
	{
		ID: "lidarr", Label: "Lidarr-style",
		Description: "Lidarr's standard layout: the album's release year on the folder, and artist and album in every file name.",
		Naming: MusicNaming{
			Folder:      "{Album Artist}/{Album Title} ({Release Year})",
			Track:       "{Album Artist} - {Album Title} - {Disc Prefix}{Track:00} - {Track Title}",
			Compilation: "{Album Artist} - {Album Title} - {Disc Prefix}{Track:00} - {Track Artist} - {Track Title}",
		},
	},
	{
		ID: "disc-folders", Label: "Disc folders",
		Description: "A folder per disc inside the album folder (\"Disc 1\", \"Disc 2\"), the track number alone in the file name.",
		Naming: MusicNaming{
			Folder:      DefaultMusicFolder,
			Track:       "Disc {Disc}/{Track:00} - {Track Title}",
			Compilation: "Disc {Disc}/{Track:00} - {Track Artist} - {Track Title}",
		},
	},
}

// MusicNamingProvider supplies the music naming in effect, read fresh on every use so a
// settings change applies to the next import.
type MusicNamingProvider interface {
	MusicNaming() MusicNaming
}

// MusicName is everything an album's folder and a track's file names can be made of.
type MusicName struct {
	AlbumArtist     string // the library artist the album is filed under
	AlbumArtistSort string
	AlbumTitle      string
	ReleaseYear     int // the edition's year
	OriginalYear    int // the album's first release
	AlbumType       string
	Edition         string // the edition's disambiguation: "deluxe edition"
	Media           string // the disc's format: "CD", "Digital Media"
	AlbumMBID       string // the release group
	ReleaseMBID     string // the edition
	Compilation     bool   // a Various Artists album: the compilation template names its tracks

	Disc, DiscCount int
	DiscTitle       string
	Track           int
	TrackTitle      string
	TrackArtist     string // the track's credit; "" = the album artist

	// Measured facts of the file itself, never read from a release name: a rename must
	// not name a file from another file's release (#48).
	Quality    string // the measured tier: "FLAC-24", "MP3-V0"
	Codec      string
	BitDepth   int
	SampleRate int // Hz
}

const (
	tokMAlbumArtist     = "Album Artist"
	tokMAlbumArtistSort = "Album Artist Sort"
	tokMFirstLetter     = "Album Artist First Letter"
	tokMAlbumTitle      = "Album Title"
	tokMReleaseYear     = "Release Year"
	tokMOriginalYear    = "Original Year"
	tokMAlbumType       = "Album Type"
	tokMEdition         = "Edition"
	tokMMedia           = "Media"
	tokMDisc            = "Disc"
	tokMDiscCount       = "Disc Count"
	tokMDiscTitle       = "Disc Title"
	tokMDiscPrefix      = "Disc Prefix"
	tokMTrack           = "Track"
	tokMTrackTitle      = "Track Title"
	tokMTrackArtist     = "Track Artist"
	tokMQuality         = "Quality"
	tokMCodec           = "Codec"
	tokMBitDepth        = "Bit Depth"
	tokMSampleRate      = "Sample Rate"
	tokMAlbumMBID       = "Album MBID"
	tokMReleaseMBID     = "Release MBID"
)

// MusicTokens lists the tokens in the order the settings page shows them.
var MusicTokens = []string{
	tokMAlbumArtist, tokMAlbumArtistSort, tokMFirstLetter, tokMAlbumTitle, tokMReleaseYear, tokMOriginalYear,
	tokMAlbumType, tokMEdition, tokMMedia, tokMDisc, tokMDiscCount, tokMDiscTitle, tokMDiscPrefix,
	tokMTrack, tokMTrackTitle, tokMTrackArtist, tokMQuality, tokMCodec, tokMBitDepth, tokMSampleRate,
	tokMAlbumMBID, tokMReleaseMBID,
}

// musicTokenSpellings: folded name → canonical. Lidarr's names are accepted too.
var musicTokenSpellings = map[string]string{
	"albumartist": tokMAlbumArtist, "artist": tokMAlbumArtist, "artistname": tokMAlbumArtist,
	"albumartistsort": tokMAlbumArtistSort, "artistsortname": tokMAlbumArtistSort, "artistsort": tokMAlbumArtistSort,
	"albumartistfirstletter": tokMFirstLetter, "artistfirstletter": tokMFirstLetter, "firstletter": tokMFirstLetter,
	"albumtitle": tokMAlbumTitle, "album": tokMAlbumTitle,
	"releaseyear": tokMReleaseYear, "originalyear": tokMOriginalYear, "year": tokMOriginalYear,
	"albumtype": tokMAlbumType, "edition": tokMEdition, "albumdisambiguation": tokMEdition,
	"media": tokMMedia, "mediumformat": tokMMedia,
	"disc": tokMDisc, "medium": tokMDisc, "discnumber": tokMDisc,
	"disccount": tokMDiscCount, "mediumcount": tokMDiscCount,
	"disctitle": tokMDiscTitle, "mediumname": tokMDiscTitle, "discsubtitle": tokMDiscTitle,
	"discprefix": tokMDiscPrefix,
	"track":      tokMTrack, "tracknumber": tokMTrack,
	"tracktitle": tokMTrackTitle, "title": tokMTrackTitle,
	"trackartist": tokMTrackArtist, "trackartistname": tokMTrackArtist,
	"quality": tokMQuality, "qualitytitle": tokMQuality, "codec": tokMCodec,
	"bitdepth": tokMBitDepth, "samplerate": tokMSampleRate,
	"albummbid": tokMAlbumMBID, "releasegroupmbid": tokMAlbumMBID, "releasembid": tokMReleaseMBID,
}

var musicVocab = tokenVocab{
	spellings: musicTokenSpellings,
	tokens:    MusicTokens,
	paddable:  map[string]bool{tokMDisc: true, tokMDiscCount: true, tokMTrack: true},
	padHelp:   "{Disc}, {Disc Count} and {Track}",
}

// trackOnlyTokens name one track or one file, so they can't be in the album's folder.
var trackOnlyTokens = map[string]bool{
	tokMDisc: true, tokMDiscTitle: true, tokMDiscPrefix: true, tokMTrack: true, tokMTrackTitle: true,
	tokMTrackArtist: true, tokMQuality: true, tokMCodec: true, tokMBitDepth: true, tokMSampleRate: true,
	tokMMedia: true,
}

// discTokens say which disc a file is on.
var discTokens = map[string]bool{tokMDisc: true, tokMDiscPrefix: true}

// ValidateMusicFolder / ValidateMusicTrack check a template for its place before it's
// saved, and say what's wrong in a sentence.
func ValidateMusicFolder(s string) error {
	nodes, err := parseTemplate(s, musicVocab)
	if err != nil {
		return err
	}
	var bad string
	walkTokens(nodes, true, func(n bnNode, _ bool) {
		if bad == "" && trackOnlyTokens[n.tok] {
			bad = "{" + n.tok + "} names a track or a file, so it can't be in the album folder — use it in the track file name"
		}
	})
	if bad != "" {
		return errors.New(bad)
	}
	// Every album needs a folder of its own: two albums sharing one would have their
	// tracks listed, renamed and replaced as each other's.
	if !lastSegmentHas(nodes, tokMAlbumTitle) {
		return errors.New("the last folder must have {Album Title} in it (outside any < >), so every album gets a folder of its own")
	}
	return nil
}

func ValidateMusicTrack(s string) error {
	nodes, err := parseTemplate(s, musicVocab)
	if err != nil {
		return err
	}
	if !lastSegmentHas(nodes, tokMTrack) {
		return errors.New("the file name needs {Track} (outside any < >, after the last /), or tracks of one album would get the same name")
	}
	return nil
}

// lastSegmentHas reports whether a top-level tok comes after the last "/".
func lastSegmentHas(nodes []bnNode, tok string) bool {
	ok := false
	for _, n := range nodes {
		switch {
		case n.group:
			for _, alt := range n.alts {
				if hasSlash(alt) {
					ok = false
				}
			}
		case n.tok == tok:
			ok = true
		case n.tok == "" && strings.ContainsAny(n.lit, `/\`):
			ok = false
		}
	}
	return ok
}

// topHasAny reports whether any of toks is in the template outside every optional part.
func topHasAny(tpl string, toks map[string]bool) bool {
	nodes, err := parseTemplate(tpl, musicVocab)
	if err != nil {
		return false
	}
	found := false
	walkTokens(nodes, true, func(n bnNode, top bool) {
		if top && toks[n.tok] {
			found = true
		}
	})
	return found
}

// musicNamingOrDefault fills a blank or broken template with its default, so a scheme
// saved some other way can never stop an import.
func musicNamingOrDefault(n MusicNaming) MusicNaming {
	if ValidateMusicFolder(n.Folder) != nil {
		n.Folder = DefaultMusicFolder
	}
	if ValidateMusicTrack(n.Track) != nil {
		n.Track = DefaultMusicTrack
	}
	if ValidateMusicTrack(n.Compilation) != nil {
		n.Compilation = DefaultMusicCompilationTrack
	}
	return n
}

// musicRender fills the music tokens for one album, disc and track.
type musicRender struct{ n MusicName }

func (r musicRender) value(n bnNode, top bool) string {
	m := r.n
	pad := func(v, def int) string {
		if v <= 0 {
			return ""
		}
		return fmt.Sprintf("%0*d", max(n.pad, def), v)
	}
	switch n.tok {
	case tokMAlbumArtist:
		if v := cleanValue(m.AlbumArtist); v != "" || !top {
			return v
		}
		return "Unknown Artist"
	case tokMAlbumArtistSort:
		if v := cleanValue(m.AlbumArtistSort); v != "" {
			return v
		}
		return cleanValue(m.AlbumArtist)
	case tokMFirstLetter:
		return firstLetter(m)
	case tokMAlbumTitle:
		if v := cleanValue(m.AlbumTitle); v != "" || !top {
			return v
		}
		return "Unknown Album"
	case tokMReleaseYear:
		return pad(m.ReleaseYear, 1)
	case tokMOriginalYear:
		return pad(m.OriginalYear, 1)
	case tokMAlbumType:
		return cleanValue(m.AlbumType)
	case tokMEdition:
		return cleanValue(m.Edition)
	case tokMMedia:
		return cleanValue(m.Media)
	case tokMDisc:
		return pad(m.Disc, 1)
	case tokMDiscCount:
		return pad(m.DiscCount, 1)
	case tokMDiscTitle:
		return cleanValue(m.DiscTitle)
	case tokMDiscPrefix:
		// "1-" only when the edition has more than one disc: a single disc's files are
		// "04 - Title", not "1-04 - Title".
		if m.DiscCount > 1 && m.Disc > 0 {
			return strconv.Itoa(m.Disc) + "-"
		}
		return ""
	case tokMTrack:
		return pad(m.Track, 2)
	case tokMTrackTitle:
		return cleanValue(m.TrackTitle)
	case tokMTrackArtist:
		if v := cleanValue(m.TrackArtist); v != "" {
			return v
		}
		return cleanValue(m.AlbumArtist)
	case tokMQuality:
		return cleanValue(m.Quality)
	case tokMCodec:
		return cleanValue(m.Codec)
	case tokMBitDepth:
		return pad(m.BitDepth, 1)
	case tokMSampleRate:
		if m.SampleRate <= 0 {
			return ""
		}
		return strconv.FormatFloat(float64(m.SampleRate)/1000, 'f', -1, 64)
	case tokMAlbumMBID:
		return cleanValue(m.AlbumMBID)
	case tokMReleaseMBID:
		return cleanValue(m.ReleaseMBID)
	}
	return ""
}

// firstLetter is the album artist's sort name's first letter, upper case: "Beatles, The"
// → "B". A name that starts with anything else (a digit, a symbol) files under "#".
func firstLetter(m MusicName) string {
	name := cleanValue(m.AlbumArtistSort)
	if name == "" {
		name = cleanValue(m.AlbumArtist)
	}
	for _, r := range name {
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r))
		}
		return "#"
	}
	return ""
}

// withDisc keeps two discs' tracks apart: a track template that says nothing about the
// disc (nor does the folder) would give disc 1's track 4 and disc 2's track 4 one name, so
// on a multi-disc album the disc goes in front ("2-04 - Title").
func withDisc(nm MusicNaming, tpl string, n MusicName) string {
	if n.DiscCount <= 1 || topHasAny(tpl, discTokens) || topHasAny(nm.Folder, discTokens) {
		return tpl
	}
	return "{Disc}-" + tpl
}

// MusicTrackRel renders a track's path relative to the music folder with naming nm: the
// album folder, any folders the track template adds, and the file name with ext.
func MusicTrackRel(nm MusicNaming, n MusicName, ext string) string {
	nm = musicNamingOrDefault(nm)
	folder := musicSegments(nm.Folder, n)
	if len(folder) == 0 {
		folder = []string{"Unknown Artist", "Unknown Album"}
	}
	tpl := nm.Track
	if n.Compilation {
		tpl = nm.Compilation
	}
	segs := musicSegments(withDisc(nm, tpl, n), n)
	if len(segs) == 0 {
		segs = []string{fmt.Sprintf("%02d", max(n.Track, 0))}
	}
	last := len(segs) - 1
	segs[last] = truncateName(segs[last], maxNameBytes-len(ext)) + ext
	return filepath.Join(append(folder, segs...)...)
}

// MusicAlbumFolderRel is the album's folder relative to the music folder.
func MusicAlbumFolderRel(nm MusicNaming, n MusicName) string {
	nm = musicNamingOrDefault(nm)
	folder := musicSegments(nm.Folder, n)
	if len(folder) == 0 {
		folder = []string{"Unknown Artist", "Unknown Album"}
	}
	return filepath.Join(folder...)
}

func musicSegments(tpl string, n MusicName) []string {
	nodes, err := parseTemplate(tpl, musicVocab)
	if err != nil {
		return nil
	}
	segs := renderTemplate(nodes, musicRender{n: n})
	for i := range segs {
		segs[i] = truncateName(segs[i], maxNameBytes)
	}
	return segs
}

// SetMusicNaming installs the music naming provider.
func (im *Importer) SetMusicNaming(np MusicNamingProvider) { im.musicNaming = np }

// MusicNamingNow is the music naming in effect, with a blank or broken template replaced
// by its default.
func (im *Importer) MusicNamingNow() MusicNaming {
	n := DefaultMusicNaming
	if im.musicNaming != nil {
		n = im.musicNaming.MusicNaming()
	}
	return musicNamingOrDefault(n)
}

// MusicTrackTarget is the library path for one track under the music folder, with the
// naming in effect. It is the one name builder for music imports, retags and renames.
func (im *Importer) MusicTrackTarget(n MusicName, ext string) string {
	return filepath.Join(im.MusicDir(), MusicTrackRel(im.MusicNamingNow(), n, ext))
}

// MusicAlbumFolder is the album's folder under the music folder, with the naming in effect.
func (im *Importer) MusicAlbumFolder(n MusicName) string {
	return filepath.Join(im.MusicDir(), MusicAlbumFolderRel(im.MusicNamingNow(), n))
}

// MusicNamingSample is one rendered example for the settings preview.
type MusicNamingSample struct {
	Label string   `json:"label"`
	Paths []string `json:"paths"` // relative to the music folder, "/" between levels
}

type sampleAlbum struct {
	label  string
	album  MusicName
	tracks []MusicName // Disc, Track, TrackTitle, TrackArtist only
	ext    string
}

var musicSamples = []sampleAlbum{
	{"An album", MusicName{AlbumArtist: "Radiohead", AlbumArtistSort: "Radiohead", AlbumTitle: "OK Computer", ReleaseYear: 1997, OriginalYear: 1997, AlbumType: "Album", DiscCount: 1, Quality: "FLAC", Codec: "flac", BitDepth: 16, SampleRate: 44100},
		[]MusicName{{Disc: 1, Track: 1, TrackTitle: "Airbag"}, {Disc: 1, Track: 2, TrackTitle: "Paranoid Android"}}, ".flac"},
	{"A two-disc album", MusicName{AlbumArtist: "The Beatles", AlbumArtistSort: "Beatles, The", AlbumTitle: "The Beatles", ReleaseYear: 2009, OriginalYear: 1968, AlbumType: "Album", Edition: "2009 remaster", DiscCount: 2, Quality: "FLAC-24", Codec: "flac", BitDepth: 24, SampleRate: 44100},
		[]MusicName{{Disc: 1, Track: 1, TrackTitle: "Back in the U.S.S.R."}, {Disc: 2, Track: 1, TrackTitle: "Birthday"}}, ".flac"},
	{"A compilation", MusicName{AlbumArtist: "Various Artists", AlbumTitle: "Pulp Fiction: Music From the Motion Picture", ReleaseYear: 1994, OriginalYear: 1994, AlbumType: "Compilation", DiscCount: 1, Compilation: true, Quality: "MP3-V0", Codec: "mp3", SampleRate: 44100},
		[]MusicName{{Disc: 1, Track: 2, TrackTitle: "Misirlou", TrackArtist: "Dick Dale & His Del-Tones"}, {Disc: 1, Track: 6, TrackTitle: "Son of a Preacher Man", TrackArtist: "Dusty Springfield"}}, ".mp3"},
}

// PreviewMusicNaming renders the sample albums with nm, for the settings page. Templates
// are checked first; the first problem comes back as an error naming the field.
func PreviewMusicNaming(nm MusicNaming) ([]MusicNamingSample, *BookNamingError) {
	if err := ValidateMusicFolder(nm.Folder); err != nil {
		return nil, &BookNamingError{Field: "folder", Message: err.Error()}
	}
	if err := ValidateMusicTrack(nm.Track); err != nil {
		return nil, &BookNamingError{Field: "track", Message: err.Error()}
	}
	if err := ValidateMusicTrack(nm.Compilation); err != nil {
		return nil, &BookNamingError{Field: "compilation", Message: err.Error()}
	}
	out := make([]MusicNamingSample, 0, len(musicSamples))
	for _, s := range musicSamples {
		var paths []string
		for _, t := range s.tracks {
			n := s.album
			n.Disc, n.Track, n.TrackTitle, n.TrackArtist = t.Disc, t.Track, t.TrackTitle, t.TrackArtist
			paths = append(paths, filepath.ToSlash(MusicTrackRel(nm, n, s.ext)))
		}
		out = append(out, MusicNamingSample{Label: s.label, Paths: paths})
	}
	return out, nil
}
