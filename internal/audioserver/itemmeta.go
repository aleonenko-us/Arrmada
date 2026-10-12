package audioserver

import "github.com/tristenlammi/arrmada/internal/books"

// Small facts about an Item that several replies share.

// updatedAt is when the item last changed as apps see it: added, or its book's details
// edited (title, cover, series, files). Apps that put ?ts=updatedAt on cover URLs fetch
// the new cover when it moves.
func (it Item) updatedAt() int64 { return max(it.AddedAt, it.Book.MetaUpdatedAt) }

// recordedFile is the file record the import left for an item.
func (it Item) recordedFile() *books.BookFile {
	if it.Version != nil {
		return it.Version.File
	}
	return it.Book.Audiobook
}
