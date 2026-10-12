package books

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The ebook file behind an edition, shared by everything that hands one out: the shelf's
// download, and the audiobook server for apps that read as well as listen.

// EbookFile resolves the stored ebook path to the file to send. An ebook edition is
// one file, but should it ever be a folder, the best-known format inside wins.
func EbookFile(stored string) (string, error) {
	st, err := os.Stat(stored)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return stored, nil
	}
	entries, err := os.ReadDir(stored)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() && EbookContentType(strings.ToLower(filepath.Ext(e.Name()))) != "" {
			found = append(found, e.Name())
		}
	}
	if len(found) == 0 {
		return "", os.ErrNotExist
	}
	sort.SliceStable(found, func(i, j int) bool {
		return ebookRank(found[i]) < ebookRank(found[j])
	})
	return filepath.Join(stored, found[0]), nil
}

// ebookRank orders formats for the pick above: the ones most readers open first.
func ebookRank(name string) int {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".epub":
		return 0
	case ".azw3", ".kepub":
		return 1
	case ".mobi", ".azw":
		return 2
	case ".pdf":
		return 3
	}
	return 4
}

// EbookContentType maps an ebook extension (lower case, with the dot) to its media type
// ("" = not an ebook).
func EbookContentType(ext string) string {
	switch ext {
	case ".epub", ".kepub":
		return "application/epub+zip"
	case ".mobi":
		return "application/x-mobipocket-ebook"
	case ".azw", ".azw3":
		return "application/vnd.amazon.mobi8-ebook"
	case ".pdf":
		return "application/pdf"
	}
	return ""
}
