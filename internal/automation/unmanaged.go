package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/tristenlammi/arrmada/internal/download"
)

// Torrents Arrmada didn't grab — added by hand in the client, or left over from another
// tool — that finished and sit outside its categories. Needs you lists them and offers
// "Import into…": the same per-kind import a review's "Choose a different title" runs,
// into a library item the user picks.

// ErrAlreadyImported: the torrent's hash is already recorded as imported.
var ErrAlreadyImported = errors.New("that download has already been imported")

// GrabbedHashes is every info hash a grab ever recorded, lowercased — whatever became of
// the grab. A torrent outside it is one Arrmada never grabbed. Unreadable is an error:
// callers mustn't treat Arrmada's own downloads as strangers.
func (c *Coordinator) GrabbedHashes(ctx context.Context) (map[string]bool, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT DISTINCT lower(info_hash) FROM grabs WHERE info_hash != ''`)
	if err != nil {
		return nil, fmt.Errorf("grabs unreadable: %w", err)
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("grabs unreadable: %w", err)
		}
		known[h] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("grabs unreadable: %w", err)
	}
	return known, nil
}

// ImportUnmanaged imports a finished torrent Arrmada didn't grab into library item
// targetID of kind (movie | series | book | music). contentPath is where its files are,
// already vetted by the caller to lie inside the download or library folders. Its hash is
// then recorded as imported, so the queue shows it in the library and the import sweeps
// leave it alone. No grab is created: seed cleanup still never touches a torrent it
// didn't grab.
func (c *Coordinator) ImportUnmanaged(ctx context.Context, it download.Item, contentPath, kind string, targetID int64) error {
	switch kind {
	case "movie", "series", "book", "music":
	default:
		return fmt.Errorf("%w: %q isn't a kind of library item", ErrWrongTargetKind, kind)
	}
	if targetID <= 0 {
		return needsTargetError{kindLabel(kind)}
	}
	if c.hashAlreadyImported(ctx, it.Hash) {
		return ErrAlreadyImported
	}
	r := Review{Hash: it.Hash, Name: it.Name, ContentPath: contentPath, MediaType: kind, SizeBytes: it.SizeBytes}
	if err := c.importContentInto(ctx, r, targetID, "Needs you"); err != nil {
		return err
	}
	c.recordImportedHash(ctx, r.Hash, r.Name, r.SizeBytes)
	c.log.Info("imported a download Arrmada didn't grab", "kind", kind, "id", targetID, "release", it.Name)
	return nil
}
