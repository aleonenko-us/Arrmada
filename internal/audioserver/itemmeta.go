package audioserver

import (
	"context"
	"encoding/base64"
	"net/url"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/books"
)

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

// manualNarrator is the narrator typed in Arrmada for this item: the version's own, or
// the book's for the standard audiobook. Typed wins over the files' tags.
func (it Item) manualNarrator() string {
	if it.Version != nil {
		return it.Version.Narrator
	}
	return it.Book.AudiobookNarrator
}

// narratorOf is who reads an item: typed in Arrmada, otherwise what its first probed
// file's tags say. From the database alone.
func (s *Server) narratorOf(ctx context.Context, it Item) string {
	if n := it.manualNarrator(); n != "" {
		return n
	}
	return s.probe.knownUnder(ctx, it.Path).Narrator
}

// filesNarrator is narratorOf for an item whose files are already listed.
func filesNarrator(it Item, files []AudioFile) string {
	if n := it.manualNarrator(); n != "" {
		return n
	}
	for _, f := range files {
		if f.Narrator != "" {
			return f.Narrator
		}
	}
	return ""
}

// splitNarrators turns "A, B" / "A & B" into names, as Audiobookshelf lists them.
func splitNarrators(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, " & ", "\x00")
	s = strings.ReplaceAll(s, ", ", "\x00")
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, "\x00") {
		if p = strings.TrimSpace(p); p != "" && !seen[strings.ToLower(p)] {
			seen[strings.ToLower(p)] = true
			out = append(out, p)
		}
	}
	return out
}

// narratorLookup answers narratorOf for many items in one reply, reading each item once.
func (s *Server) narratorLookup(ctx context.Context) func(Item) []string {
	memo := map[string][]string{}
	return func(it Item) []string {
		if n, ok := memo[it.Key]; ok {
			return n
		}
		n := splitNarrators(s.narratorOf(ctx, it))
		memo[it.Key] = n
		return n
	}
}

// narratorID is Audiobookshelf's id for a narrator: the name, base64'd and URL-encoded.
// A narrator filter ("narrators.<id>") carries the same.
func narratorID(name string) string {
	return url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(name)))
}

// narratorCount is one narrator and how many items they read.
type narratorCount struct {
	name  string
	books int
}

// countNarrators lists the narrators of items, A to Z, with how many each reads.
func countNarrators(items []Item, narr func(Item) []string) []narratorCount {
	if narr == nil {
		return nil
	}
	byKey := map[string]*narratorCount{}
	for _, it := range items {
		for _, n := range narr(it) {
			k := strings.ToLower(n)
			if byKey[k] == nil {
				byKey[k] = &narratorCount{name: n}
			}
			byKey[k].books++
		}
	}
	out := make([]narratorCount, 0, len(byKey))
	for _, c := range byKey {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].name) < strings.ToLower(out[j].name) })
	return out
}
