package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// A Hardcover book's page lives at hardcover.app/books/<slug>, and the book key only
// carries the numeric id. The slug has its own one-field query on purpose: the field
// name hasn't been checked against the live schema, and inside the shared book fields a
// wrong guess would break every Hardcover search, browse and book load. Here it can only
// cost the direct link — the book page falls back to a Hardcover search.
const hcSlugQuery = `query($id: Int!) {
  books(where: {id: {_eq: $id}}, limit: 1) { slug }
}`

// BookSlug returns Hardcover's page name for a book key ("hc:123" → "dune"), or "" when
// Hardcover has none, isn't configured, or refused the query before. Answers (including
// "none") are cached for a day like a book, so reopening a book doesn't spend budget.
// When the API refuses the query the error comes back once and "" is remembered for the
// same day, so a schema mismatch costs one request per book, not one per page view.
func (h *Hardcover) BookSlug(ctx context.Context, key string) (string, error) {
	id, ok := hcBookID(key)
	if !ok {
		return "", fmt.Errorf("hardcover: not a hardcover book key: %q", key)
	}
	if !h.Available() || h.noSlug.Load() {
		return "", nil
	}
	ck := "slug:v1:" + key
	slug, err := cached(ctx, h.cache, ck, hcTTLBook, func(ctx context.Context) (string, error) {
		return h.slugLive(ctx, id)
	})
	var refused *hcAPIError
	if errors.As(err, &refused) {
		h.cache.put(ck, "", hcTTLBook)
	}
	return slug, err
}

func (h *Hardcover) slugLive(ctx context.Context, id int) (string, error) {
	var data struct {
		Books []struct {
			// Raw, so an unexpected shape (null, a number) reads as "no slug" rather
			// than failing the decode.
			Slug json.RawMessage `json:"slug"`
		} `json:"books"`
	}
	err := h.query(ctx, hcSlugQuery, map[string]any{"id": id}, &data)
	var refused *hcAPIError
	if errors.As(err, &refused) {
		// Say so in the API's own words the first time — this is the line to look for
		// after a deploy — and if it's the field itself that's unknown, stop asking.
		if h.slugRefused.CompareAndSwap(false, true) {
			slog.Warn("hardcover: the book-slug query was refused — Hardcover badges link to a search instead", "err", err)
		}
		if strings.Contains(strings.ToLower(refused.msg), "slug") {
			h.noSlug.Store(true)
		}
		return "", err
	}
	if err != nil {
		return "", err
	}
	if len(data.Books) == 0 {
		return "", nil
	}
	var slug string
	if json.Unmarshal(data.Books[0].Slug, &slug) != nil {
		return "", nil
	}
	return cleanSlug(slug), nil
}

// cleanSlug keeps a slug that looks like one (letters, digits, dashes, underscores) and
// drops anything else, so a surprise value can't produce a strange link.
func cleanSlug(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 200 {
		return ""
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return ""
		}
	}
	return s
}
