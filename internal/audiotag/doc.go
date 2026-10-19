// Package audiotag reads and writes Arrmada's "house" set of music tags, with
// real multi-value support, behind one small API: Read, Write and Properties
// (or a Tagger from New for an explicit backend).
//
// # Backends
//
// Two interchangeable backends sit behind that API, both pure Go to build (no
// cgo, no external tools), so they cross-compile to linux/amd64 and linux/arm64:
//
//   - Permissive (the default): MIT/Apache-2.0 libraries only. FLAC via
//     go-flac + flacvorbis, MP3 via bogem/id3v2, MP4/M4A via abema/go-mp4 with
//     the iTunes ilst items built here. No Ogg Vorbis/Opus.
//   - TagLib: TagLib 2 compiled to WebAssembly, run by wazero
//     (go.senan.xyz/taglib, LGPL-2.1). Adds Ogg Vorbis and Opus.
//
// DefaultBackend picks the one the package-level functions use. Note that it
// only picks behaviour: while taglib.go exists, the LGPL module is linked into
// any binary that imports this package. Shipping MIT/Apache-only means deleting
// taglib.go and the TagLib constant; nothing outside this package imports it.
//
// ffmpeg was evaluated and rejected as a writer: "-metadata KEY=a -metadata
// KEY=b" keeps only the last value in every format, and its MP4 muxer cannot
// write freeform iTunes atoms (see ffmpeg_experiment_test.go).
//
// # Tag names
//
// Tags is a map keyed by the upper-case Vorbis-comment names from MusicBrainz
// Picard's tag mapping table (also TagLib's property names), e.g. ARTISTS or
// MUSICBRAINZ_TRACKID (the recording id). mapping.go translates them per
// format: repeated Vorbis comments; ID3v2.4 frames with NUL-separated values,
// TXXX for MusicBrainz fields and a UFID frame for the recording id; iTunes
// atoms with one "data" child per value and "----:com.apple.iTunes:NAME"
// freeform atoms. Track and disc numbers are separate keys in Tags and become
// "n/total" (TRCK/TPOS, trkn/disk) where the format wants that. Both backends
// return identical, normalised Tags for the same file.
//
// # Write semantics
//
// Write makes the file's house tags exactly equal to the Tags it is given: a
// house key missing from Tags is removed. Tags outside the house set are kept.
// To change one field, Read, modify, Write. MP3s always come out with a single
// ID3v2.4 tag: ID3v2.3 is upgraded, and trailing ID3v1, Lyrics3 and APE tags
// are dropped so no stale copy can contradict it.
//
// The audio data is never modified; the proof tests compare decoded audio
// before and after. Only the metadata is rebuilt (in MP4 the chunk offset
// tables are shifted when the metadata changes size, which changes no sample).
//
// Every write is atomic: the new file is built in a temporary file in the same
// directory, synced, and renamed over the original. TagLib edits files in
// place, so its backend first copies the file to that temporary file and lets
// TagLib edit only the copy. A failed write leaves the original untouched and
// removes the temporary file. Because the rename replaces the directory entry
// rather than writing into the existing inode, a write also deliberately breaks
// any hardlink: a torrent client still seeding the original through another
// link keeps the old, byte-identical file, and only the path that was written
// sees the new tags. Callers should tag a staging copy, never a file that is
// still being seeded or served.
//
// # Backend differences worth knowing
//
// TagLib re-saves every tag it understands through its property map, so a
// TagLib write can also change how non-house tags are stored (e.g. an ffmpeg
// TXXX:comment gains a COMM copy), and it writes the MusicBrainz TXXX
// descriptions in upper case ("MUSICBRAINZ ALBUM ID") where Picard writes
// "MusicBrainz Album Id". Both backends read either spelling. The permissive
// backend writes Picard's exact names and leaves non-house frames untouched,
// but refuses (ErrUnsupported) the rare ID3 features its library can't rewrite
// safely: ID3v2.2, unsynchronisation, extended headers, compressed or
// encrypted frames.
package audiotag
