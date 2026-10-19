package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tristenlammi/arrmada/internal/audiotag"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/extract"
	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/music"
)

// The music import's identify step (MUS-31). The matcher (internal/music, matcher*.go)
// decides which edition of which album a download's files are and which file is which
// track; this file gathers what it needs (the files' tags and lengths, the grab, the
// candidate editions), acts on its decision (switch the edition, place, or hold for
// Reviews), and hands the placing to placeAlbumTracks, which decides how each file lands.
//
// The handoff is music.MatchResult: AlbumID and EditionID say where, Switch says the
// edition becomes the album's target first, Assignments say which file is which release
// track, Unplaced are the files left in the download (and why), and Report is the per-field
// breakdown a review stores.

// Reasons the matcher holds a music download for review, beside the shared ones
// (mismatch, unmatched, import_failed, no_media).
const (
	ReasonLowConfidence       = music.MatchReasonLowConfidence
	ReasonCloseEditions       = music.MatchReasonCloseEditions
	ReasonPartial             = music.MatchReasonPartial
	ReasonTagConflict         = music.MatchReasonTagConflict
	ReasonFingerprintConflict = music.MatchReasonFingerprintConflict
)

// MusicFingerprinter identifies audio files by sound (AcoustID, MUS-32): the matcher's
// second pass when the tags and names aren't enough.
type MusicFingerprinter interface {
	// Enabled reports whether it can run (an AcoustID key is set).
	Enabled() bool
	// Recordings returns the MusicBrainz recording ids AcoustID names for a file.
	Recordings(ctx context.Context, path string) ([]string, error)
}

// SetMusicFingerprinter wires the AcoustID second pass (nil: off).
func (c *Coordinator) SetMusicFingerprinter(f MusicFingerprinter) { c.fingerprinter = f }

// readMatchFile reads one audio file's tags and length (ms) for the matcher. A file the tag
// reader can't open (a container it doesn't know, a broken file) has no tags; its name and
// folder still say something.
func (c *Coordinator) readMatchFile(path string) (tags music.FileTags, ms int, err error) {
	if c.musicTagsFn != nil {
		return c.musicTagsFn(path)
	}
	tags, props, err := readAudioFacts(path)
	if props != nil {
		ms = int(props.DurationSec * 1000)
	}
	return tags, ms, err
}

// readAudioFacts reads one audio file's tags and what its headers say about the audio
// (length, codec, sample rate, bit depth, channels, average bitrate), in-process: no
// ffprobe, so the library scan can afford it for every new file. props is nil when the
// headers couldn't be read.
func readAudioFacts(path string) (tags music.FileTags, props *mediainfo.AudioInfo, err error) {
	// A tag library meeting a file broken in a way nobody planned for must cost that
	// file its tags, not the import loop.
	defer func() {
		if p := recover(); p != nil {
			tags, props, err = music.FileTags{}, nil, fmt.Errorf("reading tags: %v", p)
		}
	}()
	t, rerr := audiotag.Read(path)
	if rerr != nil {
		return music.FileTags{}, nil, rerr
	}
	f := audiotag.FieldsOf(t)
	tags = music.FileTags{
		Title: f.Title, Artist: f.Artist, AlbumArtist: f.AlbumArtist, Album: f.Album,
		Track: f.TrackNumber, TrackTotal: f.TrackTotal, Disc: f.DiscNumber, DiscTotal: f.DiscTotal,
		Date: f.Date, RecordingMBID: f.MusicBrainzRecordingID, ReleaseTrackMBID: f.MusicBrainzReleaseTrackID,
		ReleaseMBID: f.MusicBrainzReleaseID, ReleaseGroupMBID: f.MusicBrainzReleaseGroupID,
		ISRC: f.ISRC, Barcode: f.Barcode, Label: f.Label, CatalogNumber: f.CatalogNumber,
		Country: f.ReleaseCountry, Media: f.Media, Compilation: f.Compilation,
	}
	if len(f.MusicBrainzAlbumArtistIDs) > 0 {
		tags.AlbumArtistMBID = f.MusicBrainzAlbumArtistIDs[0]
	}
	if f.Date == "" {
		tags.Date = f.OriginalDate
	}
	if p, perr := audiotag.Properties(path); perr == nil {
		props = &mediainfo.AudioInfo{
			Codec: p.Codec, BitrateKbps: p.Bitrate, BitsPerSample: p.BitDepth, SampleRateHz: p.SampleRate,
			Channels: p.Channels, DurationSec: p.Duration.Seconds(),
		}
	}
	return tags, props, nil
}

// readMatchFiles reads every file for the matcher. A cue sheet's virtual track (MUS-35)
// has the sheet's tags and the length between its INDEX points.
func (c *Coordinator) readMatchFiles(ctx context.Context, files []music.AudioFile) []music.MatchFile {
	out := make([]music.MatchFile, 0, len(files))
	cs := cueSetFrom(ctx)
	for _, f := range files {
		mf := music.MatchFile{Path: f.Path, SizeBytes: f.SizeBytes, FolderDisc: f.Disc}
		if part, ok := cs.part(f.Path); ok {
			mf.Tags, mf.DurationMs = part.tags, part.ms
			out = append(out, mf)
			continue
		}
		if tags, ms, err := c.readMatchFile(f.Path); err == nil {
			mf.Tags, mf.DurationMs = tags, ms
		}
		out = append(out, mf)
	}
	return out
}

// musicMatch is what one download's match knows besides its files.
type musicMatch struct {
	grabbed int64   // the album it was grabbed for (0: nobody grabbed it)
	named   []int64 // albums its name points at
	folder  string  // the group's folder (or the download's) name
}

// candidateAlbums are the albums worth weighing: the grabbed one, the ones its tags name
// (a release MBID most files share, a release-group MBID), and the ones its name or tags'
// artist and album fold-match.
func (c *Coordinator) candidateAlbums(ctx context.Context, files []music.MatchFile, m musicMatch) []int64 {
	ids := []int64{m.grabbed}
	counts := map[string]int{}
	groups := map[string]bool{}
	for _, f := range files {
		if id := strings.ToLower(f.Tags.ReleaseMBID); id != "" {
			counts[id]++
		}
		if g := strings.ToLower(f.Tags.ReleaseGroupMBID); g != "" {
			groups[g] = true
		}
	}
	for rel, n := range counts {
		if float64(n) >= 0.75*float64(len(files)) {
			if id, err := c.music.AlbumOfRelease(ctx, rel); err == nil && id > 0 {
				ids = append(ids, id)
			}
		}
	}
	for g := range groups {
		if id, err := c.music.AlbumByMBID(ctx, g); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	ids = append(ids, m.named...)
	// The tags' own artist and album, like a release name.
	var artist, album string
	for _, f := range files {
		if artist == "" {
			artist = f.Tags.AlbumArtist
			if artist == "" {
				artist = f.Tags.Artist
			}
		}
		if album == "" {
			album = f.Tags.Album
		}
	}
	if artist != "" && album != "" {
		if al, _, ok := c.albumForRelease(ctx, artist+" - "+album); ok {
			ids = append(ids, al.ID)
		}
	}
	return ids
}

// matchGroup runs the matcher on one group of files, with the AcoustID second pass when
// the first one wasn't sure.
func (c *Coordinator) matchGroup(ctx context.Context, files []music.MatchFile, m musicMatch) (music.MatchResult, error) {
	ids := c.candidateAlbums(ctx, files, m)
	cands, err := c.music.MatchCandidates(ctx, ids, len(files))
	if err != nil {
		return music.MatchResult{}, err
	}
	in := music.MatchInput{
		Files: files, FolderName: m.folder, Candidates: cands, GrabbedAlbumID: m.grabbed,
		Settings: c.music.MatchSettings(), Policy: c.music.EditionPolicy(),
	}
	if m.grabbed > 0 {
		if w, err := c.music.AlbumWanted(ctx, m.grabbed); err == nil {
			in.Wanted = w
		}
	}
	res := music.Match(in)
	if needsFingerprint(res, files) {
		switch fp := c.fingerprinter; {
		case fp == nil || !fp.Enabled():
			// Reviews says how to turn it on.
			res.Report.Fingerprint = "off"
		default:
			if again, ok := c.fingerprintPass(ctx, in, ids); ok {
				res = again
				res.Report.Fingerprint = "used"
			}
		}
	}
	return res, nil
}

// needsFingerprint is MUS-32's trigger (Lidarr's, plus the no-tags case): the best fit is
// over 0.15, tracks are missing or extra, one file fits its track worse than 0.40, or the
// files carry no MBIDs and fewer than half have a title tag.
func needsFingerprint(res music.MatchResult, files []music.MatchFile) bool {
	best := res.Best
	if best == nil && len(res.Ranked) > 0 {
		best = res.Ranked[0]
	}
	if best == nil {
		return true
	}
	if best.Distance > 0.15 || len(best.Missing) > 0 || len(best.Extra) > 0 {
		return true
	}
	for _, p := range best.Pairs {
		if p.Distance > 0.40 {
			return true
		}
	}
	mbids, titled := 0, 0
	for _, f := range files {
		if f.Tags.RecordingMBID != "" || f.Tags.ReleaseMBID != "" {
			mbids++
		}
		if f.Tags.Title != "" {
			titled++
		}
	}
	return mbids == 0 && titled*2 < len(files)
}

// fingerprintMax bounds the files one match fingerprints (each decodes up to two minutes
// of audio at idle priority).
const fingerprintMax = 60

// fingerprintPass fingerprints the files, lets what AcoustID names count as the files'
// recording ids (where the tags have none), adds the library albums those recordings are
// on to the candidates, and decides again. ok is false when nothing was learned.
func (c *Coordinator) fingerprintPass(ctx context.Context, in music.MatchInput, ids []int64) (music.MatchResult, bool) {
	files := append([]music.MatchFile(nil), in.Files...)
	learned := 0
	var recs []string
	cs := cueSetFrom(ctx)
	for i := range files {
		if i >= fingerprintMax {
			break
		}
		if _, virtual := cs.part(files[i].Path); virtual {
			continue // a cue sheet's track: not a file until the image is split
		}
		got, err := c.fingerprinter.Recordings(ctx, files[i].Path)
		if err != nil {
			c.log.Info("music: couldn't fingerprint a file", "file", filepath.Base(files[i].Path), "err", err)
			continue
		}
		if len(got) == 0 {
			continue
		}
		learned++
		files[i].Fingerprint = got
		recs = append(recs, got...)
		// Kept apart from the tags: the matcher weighs what AcoustID heard on its own, so
		// a fingerprint never reads as a tag naming another album's recording.
	}
	if learned == 0 {
		return music.MatchResult{}, false
	}
	if more, err := c.music.AlbumsWithRecordings(ctx, recs); err == nil && len(more) > 0 {
		all := append(append([]int64(nil), ids...), more...)
		if cands, err := c.music.MatchCandidates(ctx, all, len(files)); err == nil {
			in.Candidates = cands
		}
	}
	in.Files = files
	c.log.Info("music: identified files by sound", "files", learned)
	return music.Match(in), true
}

// importMusicDownload imports one finished album download through the matcher: each group
// of files (an album folder, disc folders merged) is matched; when every group is
// confident its files are placed (switching the album to the edition they are, unpinned),
// and otherwise the whole download is held for review with the matcher's explanation and
// nothing is touched.
func (c *Coordinator) importMusicDownload(ctx context.Context, it download.Item) {
	if fi, err := os.Stat(it.ContentPath); err == nil && fi.IsDir() {
		// Scene music releases routinely ship inside a RAR set.
		if n, xerr := extract.ExtractTree(it.ContentPath); xerr != nil {
			c.log.Warn("music import: archive extraction failed", "path", it.ContentPath, "err", xerr)
		} else if n > 0 {
			c.log.Info("music import: extracted archives before import", "count", n, "path", it.ContentPath)
		}
	}
	gid, idx, grabbed := c.grabbedMediaForHash(ctx, it.Hash, it.Name, "music")
	if grabbed {
		if _, err := c.music.AlbumRecord(ctx, gid); err != nil {
			gid, grabbed = 0, false // the album it was grabbed for is gone: judge it as ungrabbed
		}
	}
	named, _, nameOK := c.albumForRelease(ctx, it.Name)
	files := findAudioFiles(it.ContentPath)
	if len(files) == 0 {
		// No audio at all: a still-archived release, or content the container can't
		// read. Books had no branch here and silently retried every 30 seconds forever.
		expected := music.Album{}
		switch {
		case grabbed:
			expected, _ = c.music.AlbumRecord(ctx, gid)
		case nameOK:
			expected = named
		}
		n := c.noteUnmatched(it.Hash)
		switch {
		case n == 1:
			c.log.Warn("music import: download holds no audio files", "album", expected.Title, "release", it.Name, "path", it.ContentPath)
		case n >= unmatchedReviewAfter && expected.ID > 0:
			c.addReview(ctx, Review{
				Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "music", ReasonCode: ReasonNoMedia,
				ExpectedID: expected.ID, ExpectedTitle: expected.Title, SizeBytes: it.SizeBytes, Indexer: idx,
				Reason: "Downloaded but holds no audio files — still archived, or unreadable",
			})
		case n >= unmatchedReviewAfter:
			c.addReview(ctx, Review{
				Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "music", ReasonCode: ReasonUnmatched,
				ParsedTitle: music.ParseRelease(it.Name).Album, SizeBytes: it.SizeBytes, Indexer: idx,
				Reason: "Matches no album in your library, and holds no audio files",
			})
		}
		return
	}
	// A disc image with a cue sheet stands in for its tracks (MUS-35); it is split only
	// once the matcher is sure and placement takes its tracks.
	files, cues, err := c.expandCueSheets(ctx, it.ContentPath, files)
	if err != nil {
		var problem *cueProblem
		if !errors.As(err, &problem) {
			// The image couldn't be read just now (a probe timed out, shutting down): the
			// next sweep tries again.
			c.log.Warn("music import: couldn't read a disc image just now — will retry", "release", it.Name, "err", err)
			return
		}
		expected := gid
		if expected == 0 && nameOK {
			expected = named.ID
		}
		c.musicCueReview(ctx, it, idx, expected, err.Error())
		return
	}
	ctx = withCueSet(ctx, cues)
	m := musicMatch{grabbed: gid}
	if nameOK {
		m.named = []int64{named.ID}
	}
	groups := music.GroupFiles(c.readMatchFiles(ctx, files))
	results := make([]music.MatchResult, 0, len(groups))
	for _, g := range groups {
		m.folder = filepath.Base(g.Dir)
		if len(groups) == 1 {
			m.folder = it.Name
		}
		res, err := c.matchGroup(ctx, g.Files, m)
		if err != nil {
			c.log.Warn("music import: couldn't gather what to match against — will retry", "release", it.Name, "err", err)
			return
		}
		results = append(results, res)
	}
	if grabbed && !weighed(results, gid) {
		// The album it was grabbed for has no listing to match against yet (MusicBrainz
		// unreachable, or no release listed): wait for one rather than judge the download
		// against other albums, and only hold it for a person after a while.
		switch n := c.noteUnmatched(it.Hash); {
		case n == 1:
			c.log.Info("music import: the album this was grabbed for has no track listing yet — waiting", "release", it.Name)
			return
		case n < unmatchedReviewAfter:
			return
		}
	}
	for _, res := range results {
		if res.Decision != music.MatchReview {
			continue
		}
		c.holdMusicMatch(ctx, it, idx, gid, res)
		return
	}

	var total albumImport
	total.found = true
	var placedAlbum music.Album
	var placedArtist music.Artist
	for _, res := range results {
		res.ContentRoot = it.ContentPath
		al, art, r, err := c.applyMatch(ctx, res, it.Name, it.Hash)
		if err != nil {
			c.log.Warn("music import: couldn't apply the match — will retry", "release", it.Name, "err", err)
			total.failed++
			continue
		}
		total.add(r)
		placedAlbum, placedArtist = al, art
	}
	switch {
	case total.taken > 0:
		c.musicTakenReview(ctx, it, total, placedAlbum.ID, placedAlbum.Title)
	case total.splitFailed > 0:
		// The disc image couldn't be split exactly (MUS-35): nothing from it was placed.
		c.musicSplitReview(ctx, it, total, placedAlbum.ID, placedAlbum.Title)
	case total.tagFailed > 0:
		// House tags couldn't be written into a staged copy, or it didn't check out
		// (MUS-33): the download is untouched; a person looks.
		c.musicTagReview(ctx, it, total, placedAlbum.ID, placedAlbum.Title)
	case total.refused > 0 || total.failed > 0:
		// A better copy couldn't replace what's held (the recycle bin can't take the old
		// file), or a track couldn't be placed. Nothing was lost; the download stays and is
		// tried again later (tracks already placed are then kept).
		c.musicImportRetry(ctx, it, total, placedAlbum.ID, placedAlbum.Title)
	case total.placed > 0 || total.kept > 0:
		c.settleMusicImport(ctx, it, "music", placedAlbum.ID, placedArtist.ID, total)
		for _, res := range results {
			if al, err := c.music.AlbumRecord(ctx, res.AlbumID); err == nil {
				if art, err := c.music.GetArtist(ctx, al.ArtistID); err == nil {
					c.afterAlbumImport(ctx, art, al)
				}
			}
		}
	default:
		c.log.Warn("music import: matched but placed nothing — will retry next sweep", "album", placedAlbum.Title, "release", it.Name)
	}
}

// weighed reports whether any match weighed an edition of album id.
func weighed(results []music.MatchResult, id int64) bool {
	for _, res := range results {
		for _, sc := range res.Ranked {
			if sc.Cand.AlbumID == id {
				return true
			}
		}
	}
	return false
}

// applyMatch carries out one confident match: switches the album to the matched edition
// when the matcher says so, then places the assigned files.
func (c *Coordinator) applyMatch(ctx context.Context, res music.MatchResult, release, hash string) (music.Album, music.Artist, albumImport, error) {
	al, err := c.music.AlbumRecord(ctx, res.AlbumID)
	if err != nil {
		return music.Album{}, music.Artist{}, albumImport{}, err
	}
	art, err := c.music.GetArtist(ctx, al.ArtistID)
	if err != nil {
		return al, music.Artist{}, albumImport{}, err
	}
	before := al
	if res.Switch {
		if _, err := c.music.SwitchEdition(ctx, al.ID, res.EditionID, "to match the download"); err != nil {
			return al, art, albumImport{}, fmt.Errorf("switch the edition: %w", err)
		}
		if al, err = c.music.AlbumRecord(ctx, al.ID); err != nil {
			return al, art, albumImport{}, err
		}
	}
	c.log.Info("music: matched a download", "artist", art.Name, "album", al.Title, "edition", res.EditionID,
		"distance", bestDistance(res), "decision", res.Decision, "files", len(res.Assignments), "left", len(res.Unplaced))
	tracks, err := c.music.Tracks(ctx, al.ID)
	if err != nil {
		return al, art, albumImport{}, err
	}
	_, unmatched := assignmentsOnto(tracks, res)
	if len(unmatched) > 0 {
		names := make([]string, 0, len(unmatched))
		for _, u := range unmatched {
			names = append(names, filepath.Base(u.Path))
		}
		// Reported rather than force-placed: guessing at these is how an album ends up
		// labelled one track out, which is worse than leaving them for a person.
		c.log.Warn("music import: some files were left in the download", "album", al.Title, "files", strings.Join(names, ", "))
	}
	quality := music.DetectQuality(release)
	r := c.placeMatch(ctx, art, al, tracks, res, quality, release)
	r.found, r.unmatched = true, len(unmatched)
	if c.switchBack(ctx, before, r) {
		if back, err := c.music.AlbumRecord(ctx, al.ID); err == nil {
			al = back
		}
	}
	c.reportPlaced(ctx, art, al, r, len(tracks), quality, release)
	return al, art, r, nil
}

// switchBack puts an album back on the edition (and pin) it had before a match, when the
// match switched it and then nothing was placed or kept: no space, the music folder
// missing, a tag that didn't check out. The switch only stands with files to show for it.
// It reports whether it switched back.
func (c *Coordinator) switchBack(ctx context.Context, before music.Album, r albumImport) bool {
	if r.placed > 0 || r.kept > 0 || before.TargetEditionID == 0 {
		return false
	}
	now, err := c.music.AlbumRecord(ctx, before.ID)
	if err != nil || (now.TargetEditionID == before.TargetEditionID && now.EditionPinned == before.EditionPinned) {
		return false
	}
	if _, err := c.music.RestoreEdition(ctx, before.ID, before.TargetEditionID, before.EditionPinned); err != nil {
		c.log.Warn("music import: couldn't put the album back on its edition after placing nothing",
			"album", before.Title, "edition", before.TargetEditionID, "err", err)
		return false
	}
	c.log.Info("music import: nothing placed, so the album stays on its edition", "album", before.Title)
	return true
}

// assignmentsOnto turns a match's assignments into placeAlbumTracks' map over the album's
// target tracks. An assignment onto a track that isn't on the target (the edition moved
// meanwhile) is left in the download with the unplaced files.
func assignmentsOnto(tracks []music.Track, res music.MatchResult) (map[int64]music.AudioFile, []music.AudioFile) {
	onTarget := make(map[int64]bool, len(tracks))
	for _, t := range tracks {
		onTarget[t.ID] = true
	}
	matched := map[int64]music.AudioFile{}
	var left []music.AudioFile
	for _, a := range res.Assignments {
		f := music.AudioFile{Path: a.File.Path, SizeBytes: a.File.SizeBytes, Disc: a.File.FolderDisc}
		if !onTarget[a.TrackID] {
			left = append(left, f)
			continue
		}
		matched[a.TrackID] = f
	}
	for _, u := range res.Unplaced {
		left = append(left, music.AudioFile{Path: u.File.Path, SizeBytes: u.File.SizeBytes, Disc: u.File.FolderDisc})
	}
	return matched, left
}

// reportPlaced writes the import's line in the artist's history and tells open pages.
func (c *Coordinator) reportPlaced(ctx context.Context, a music.Artist, al music.Album, res albumImport, tracks int, quality music.Quality, release string) {
	if res.placed > 0 {
		c.log.Info("music: imported tracks", "artist", a.Name, "album", al.Title,
			"placed", res.placed, "kept", res.kept, "of", tracks, "quality", quality)
		c.music.AddEvent(ctx, a.ID, "imported",
			fmt.Sprintf("Imported %d/%d track(s) of %q (%s)", res.placed, tracks, al.Title, quality))
		c.bus.Publish("music.imported", map[string]any{
			"artist_id": a.ID, "album_id": al.ID, "placed": res.placed, "artist": a.Name, "album": al.Title, "quality": quality,
		})
	} else if res.kept > 0 && res.refused == 0 {
		c.music.AddEvent(ctx, a.ID, "imported",
			fmt.Sprintf("Kept the copies already held of %q: %s is no better", al.Title, release))
	}
}

func bestDistance(res music.MatchResult) float64 {
	if res.Best == nil {
		return 1
	}
	return res.Best.Distance
}

// holdMusicMatch puts a download the matcher wasn't sure of in Reviews, with its
// explanation; nothing in the download is touched. A download nobody grabbed that matches
// nothing waits a while first (an album added to the library later can still claim it),
// as before.
func (c *Coordinator) holdMusicMatch(ctx context.Context, it download.Item, idx string, grabbed int64, res music.MatchResult) {
	expected := grabbed
	if expected == 0 && res.Best != nil && res.Reason != music.MatchReasonUnmatched {
		expected = res.Best.Cand.AlbumID
	}
	if res.Reason == music.MatchReasonUnmatched && grabbed == 0 {
		switch n := c.noteUnmatched(it.Hash); {
		case n == 1:
			c.log.Info("music import: no matching album in the library", "release", it.Name, "why", res.Summary)
			return
		case n < unmatchedReviewAfter:
			return
		}
		c.log.Warn("music import: download still matches no album — sending to review", "release", it.Name)
	}
	title := ""
	if expected > 0 {
		if al, err := c.music.AlbumRecord(ctx, expected); err == nil {
			title = al.Title
		}
	}
	raw, err := json.Marshal(res.Report)
	if err != nil {
		raw = nil
	}
	c.log.Info("music: download held for review", "release", it.Name, "reason", res.Reason,
		"distance", bestDistance(res), "why", res.Summary)
	c.addReview(ctx, Review{
		Hash: it.Hash, Name: it.Name, ContentPath: it.ContentPath, MediaType: "music", ReasonCode: res.Reason,
		ExpectedID: expected, ExpectedTitle: title, ParsedTitle: music.ParseRelease(it.Name).Album,
		Reason: res.Summary, SizeBytes: it.SizeBytes, Indexer: idx, Match: raw,
	})
}

// musicEditionKey carries the edition a person chose in Reviews to the import.
type musicEditionKey struct{}

// WithMusicEdition asks a review's music import to place the files on one edition of the
// album (a release id), switching the album to it.
func WithMusicEdition(ctx context.Context, releaseID int64) context.Context {
	if releaseID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, musicEditionKey{}, releaseID)
}

func musicEditionFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(musicEditionKey{}).(int64)
	return id
}

// matchInto matches files to one album a person (or the library's folder layout) already
// chose: the best of its editions takes them (or the edition chosen with WithMusicEdition),
// file by file, and the album switches to that edition when it isn't pinned. targetOnly
// keeps to the album's target edition with no MusicBrainz call (the library scan, which
// can't afford one per edition per album). It returns the album's target tracks after any
// switch, what goes where, and the files left over.
func (c *Coordinator) matchInto(ctx context.Context, al music.Album, files []music.AudioFile, folder string, targetOnly bool) ([]music.Track, map[int64]music.AudioFile, []music.AudioFile, error) {
	var cands []music.MatchCandidate
	var err error
	if targetOnly {
		cands, err = c.music.TargetCandidates(ctx, al.ID)
	} else {
		cands, err = c.music.MatchCandidates(ctx, []int64{al.ID}, len(files))
	}
	if err != nil {
		return nil, nil, nil, err
	}
	// The edition a person chose is weighed whatever the candidate list held: it may not
	// be among the closest, or not otherwise acceptable (a bootleg they know is right).
	if fe := musicEditionFrom(ctx); fe != 0 && !targetOnly {
		found := false
		for _, cd := range cands {
			found = found || cd.Edition.ID == fe
		}
		if !found {
			cd, err := c.music.EditionCandidate(ctx, al.ID, fe)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("the edition chosen for %q: %w", al.Title, err)
			}
			cands = append(cands, cd)
		}
	}
	if len(cands) == 0 {
		return nil, nil, nil, nil
	}
	in := music.MatchInput{
		Files: c.readMatchFiles(ctx, files), FolderName: folder, Candidates: cands, Forced: true,
		ForceEditionID: musicEditionFrom(ctx), Settings: c.music.MatchSettings(), Policy: c.music.EditionPolicy(),
	}
	res := music.Match(in)
	// Nothing placed, nothing switched: a review that can't import keeps the album's edition.
	if res.Switch && !targetOnly && res.Decision != music.MatchReview && len(res.Assignments) > 0 {
		var err error
		if in.ForceEditionID != 0 && in.ForceEditionID == res.EditionID {
			// Chosen by a person: a pin stays a pin, now on their choice.
			_, err = c.music.SetEdition(ctx, al.ID, res.EditionID, al.EditionPinned)
		} else {
			_, err = c.music.SwitchEdition(ctx, al.ID, res.EditionID, "to match the files")
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("switch the edition: %w", err)
		}
	}
	tracks, err := c.music.Tracks(ctx, al.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if res.Switch && targetOnly {
		res.Assignments = nil // the scan never moves an album to another edition
	}
	matched, left := assignmentsOnto(tracks, res)
	return tracks, matched, left, nil
}

// matchGrabbedFolder is the discography import's match for one album folder: judged as if
// grabbed for that album (the folder's name chose it, and the pack was grabbed for its
// artist). A doubtful folder places nothing; its files stay in the download.
func (c *Coordinator) matchGrabbedFolder(ctx context.Context, al music.Album, files []music.AudioFile, folder string) ([]music.Track, map[int64]music.AudioFile, []music.AudioFile, error) {
	mfiles := c.readMatchFiles(ctx, files)
	cands, err := c.music.MatchCandidates(ctx, []int64{al.ID}, len(files))
	if err != nil || len(cands) == 0 {
		return nil, nil, files, err
	}
	in := music.MatchInput{
		Files: mfiles, FolderName: folder, Candidates: cands, GrabbedAlbumID: al.ID,
		Settings: c.music.MatchSettings(), Policy: c.music.EditionPolicy(),
	}
	if w, err := c.music.AlbumWanted(ctx, al.ID); err == nil {
		in.Wanted = w
	}
	res := music.Match(in)
	if res.Decision == music.MatchReview {
		c.log.Info("music import: a discography folder wasn't a confident match — left in the download",
			"album", al.Title, "folder", folder, "reason", res.Reason, "why", res.Summary)
		return nil, nil, files, nil
	}
	if res.Switch {
		if _, err := c.music.SwitchEdition(ctx, al.ID, res.EditionID, "to match the download"); err != nil {
			return nil, nil, files, fmt.Errorf("switch the edition: %w", err)
		}
	}
	tracks, err := c.music.Tracks(ctx, al.ID)
	if err != nil {
		return nil, nil, files, err
	}
	matched, left := assignmentsOnto(tracks, res)
	return tracks, matched, left, nil
}

// importLeftovers counts files a review's import left in the download (carried in the
// context under importLeftoversKey), so ImportReview closes the grab as 'dismissed' and
// seed cleanup keeps them.
type importLeftovers struct{ files int }

type importLeftoversKey struct{}
