package automation

import "context"

// AnyImported reports whether any grab has ever been imported into the library — the
// last tick on the Dashboard's Getting started card. Movie, series and book imports all
// mark their grab, so this covers each of them in one read.
func (c *Coordinator) AnyImported(ctx context.Context) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM grabs WHERE `+everImportedWhere+`)`).Scan(&n)
	return n == 1, err
}
