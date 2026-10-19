// Package audiotag reads and writes Arrmada's "house" set of music tags in FLAC,
// MP3 and MP4/M4A files, with real multi-value support.
//
// # Formats and libraries
//
// Every format is handled in pure Go, so the same code runs on linux/amd64 and
// linux/arm64 with no extra tools in the image:
//
//   - FLAC: Vorbis comments via github.com/go-flac/go-flac and
//     github.com/go-flac/flacvorbis (Apache-2.0). A multi-value field is written
//     as repeated comments with the same key (ARTISTS=Simon, ARTISTS=Garfunkel),
//     which is what Picard, Plex, Jellyfin and Navidrome all expect.
//   - MP3: ID3v2.4 via github.com/bogem/id3v2/v2 (MIT). Multi-value text and
//     TXXX frames hold their values separated by a NUL byte, as ID3v2.4 defines.
//     Tags are always written as v2.4, whatever version the file had.
//   - MP4/M4A: iTunes "ilst" atoms. github.com/abema/go-mp4 (MIT) walks and
//     rewrites the box tree; the ilst items themselves are built here. A
//     multi-value field is one atom holding several "data" children, and
//     MusicBrainz-style fields use freeform "----:com.apple.iTunes:NAME" atoms.
//
// ffmpeg was evaluated and rejected: "-metadata KEY=a -metadata KEY=b" keeps only
// the last value in all three formats, and its MP4 muxer cannot write freeform
// iTunes atoms at all (see ffmpeg_experiment_test.go).
//
// # Tag names
//
// Field names follow MusicBrainz Picard's tag mapping table, so files tagged here
// look the same as files tagged by Picard. Internally every field has a canonical
// key (the Vorbis comment name); id3.go and mp4.go translate those keys to frames
// and atoms. For example the MusicBrainz recording id is MUSICBRAINZ_TRACKID in
// FLAC, the UFID frame owned by "http://musicbrainz.org" in MP3, and
// "----:com.apple.iTunes:MusicBrainz Track Id" in MP4. ALBUMARTISTS is not in
// Picard's table; it follows the ARTISTS pattern in each format, which is what
// Navidrome and Jellyfin read.
//
// # Write semantics
//
// Write makes the file's house tags exactly equal to the Tags it is given: a
// field that is empty in Tags is removed from the file. Tags outside the house set
// (comments, encoder, lyrics, cover art, foreign TXXX frames or freeform atoms)
// are preserved. To change one field, Read, modify, Write.
//
// The audio data is never modified. Only the metadata section of the file is
// rebuilt; the audio bytes are copied over unchanged (in MP4 the chunk offset
// tables are shifted when the metadata changes size, which changes no sample).
//
// Every write is atomic: the new file is written to a temporary file in the same
// directory, synced, and renamed over the original. A failed write leaves the
// original untouched and removes the temporary file. Because the rename replaces
// the directory entry rather than writing into the existing inode, a write also
// deliberately breaks any hardlink: a torrent client still seeding the original
// through another link keeps the old, byte-identical file, and only the path that
// was written sees the new tags. Callers must not expect a tag write to show up
// through other hardlinks to the same file.
package audiotag
