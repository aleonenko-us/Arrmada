package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/notify"
)

// authorReleaseLimit is how many followed authors one check-author-releases run looks
// at: about one catalogue request each, so at 4 runs a day the job stays well inside the
// Hardcover budget while still reaching 280 followed authors a week.
const authorReleaseLimit = 10

// authorReleaseJob is the check-author-releases task: the books service finds and adds a
// followed author's new books; this tells the admin and searches the ones that are out.
type authorReleaseJob struct {
	check          func(ctx context.Context, limit int, defaultProfile string) (books.AuthorCheck, error)
	defaultProfile func(ctx context.Context) string
	// emit is notify.Service.EmitOnce: an admin alert, queued once per author per check.
	emit   func(ctx context.Context, key, dedupe string, data map[string]any) (int, error)
	search func(ctx context.Context, bookID int64) (automation.SearchOutcome, error)
	log    *slog.Logger
	now    func() time.Time
}

func (j authorReleaseJob) run(ctx context.Context) error {
	res, err := j.check(ctx, authorReleaseLimit, j.defaultProfile(ctx))
	msg := fmt.Sprintf("check-author-releases: checked %d authors (%d first looks), added %d books", res.Checked, res.Seeded, res.Added())
	if res.Skipped != "" {
		msg += ", skipped: " + res.Skipped
	}
	j.log.Info(msg)
	outage := false
	for _, rel := range res.Releases {
		if len(rel.Books) == 0 {
			continue
		}
		titles := make([]string, len(rel.Books))
		for i, b := range rel.Books {
			titles[i] = b.Title
		}
		// One alert per author per check. The first new book's id and the day tell two
		// checks apart; a retry of this one (a crash after the queue) is the same alert.
		dedupe := fmt.Sprintf("author_release:%d:%d:%s", rel.AuthorID, rel.Books[0].ID, j.now().Format("2006-01-02"))
		if _, eerr := j.emit(ctx, notify.EventAuthorRelease, dedupe, map[string]any{
			"author_id": rel.AuthorID, "author": rel.Author, "count": len(rel.Books), "titles": titles,
		}); eerr != nil && ctx.Err() == nil {
			j.log.Warn("check-author-releases: could not queue the alert", "author_id", rel.AuthorID, "err", eerr)
		}
		if !outage {
			outage = j.searchReleased(ctx, rel.Books)
		}
	}
	return err
}

// searchReleased searches the new books that are out now; one not out yet is left to the
// missing-books sweep, which searches it from its release day. It reports an indexer
// outage, after which nothing more is searched this run: no miss is recorded, so the
// sweep picks the rest up once the indexers answer.
func (j authorReleaseJob) searchReleased(ctx context.Context, list []books.Book) (outage bool) {
	sctx := automation.WithSearchTrigger(ctx, automation.TriggerAdd)
	for _, b := range list {
		if ctx.Err() != nil {
			return false
		}
		if !b.Monitored || !books.Released(b.ReleaseDate, j.now()) {
			continue
		}
		_, err := j.search(sctx, b.ID)
		switch {
		case err == nil, errors.Is(err, automation.ErrAlreadySearching):
		case indexer.IsOutage(err):
			j.log.Warn("check-author-releases: no indexer could answer — leaving the new books to the scheduled sweep", "err", err)
			return true
		default:
			j.log.Warn("check-author-releases: search failed", "book_id", b.ID, "err", err)
		}
	}
	return false
}
