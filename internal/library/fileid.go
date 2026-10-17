package library

import (
	"path/filepath"
	"strconv"
)

// The ids listening apps know an audiobook's files by (Audiobookshelf's "ino"). The
// audiobook server stores each one the first time it lists the file and keeps it through
// renames (audio_file_meta.ino); these work out the id a file has always been given, from
// its path within the item, for a file with none stored yet. They live here because the
// book rename has to fix a file's id before it moves the file, with the old name.

// AudioFileID is the id a file is given from its path within its item ("Part 1.mp3",
// "CD1/01.mp3") — a number, the shape Audiobookshelf clients handle safely, within 53 bits
// so it survives JSON numbers too.
func AudioFileID(rel string) string {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(rel); i++ {
		h ^= uint64(rel[i])
		h *= 1099511628211
	}
	return strconv.FormatUint(h>>11, 10)
}

// EbookFileID is the id an item's ebook file is given from its name; the "ebook/" prefix
// keeps it apart from every audio file's.
func EbookFileID(name string) string { return AudioFileID("ebook/" + name) }

// RelPath is p relative to base with "/" separators, or p's name when it isn't under base.
func RelPath(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.Base(p)
}

// ItemBase is the folder an audiobook item's file ids are relative to: the item's own
// folder, or the folder of a single-file item.
func ItemBase(itemPath string, isDir bool) string {
	if isDir {
		return itemPath
	}
	return filepath.Dir(itemPath)
}
