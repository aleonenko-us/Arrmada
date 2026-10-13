package books

import (
	"context"
	"testing"
)

// slugCatalogue is a refreshCatalogue that also knows slugs, recording what it was asked.
type slugCatalogue struct {
	refreshCatalogue
	slugAsked []string
}

func (c *slugCatalogue) BookSlug(_ context.Context, key string) (string, error) {
	c.slugAsked = append(c.slugAsked, key)
	return "dune", nil
}

// COPY-15: only a Hardcover book key is looked up, and not once background work has
// used most of the day's Hardcover budget.
func TestCatalogueSlug(t *testing.T) {
	cat := &slugCatalogue{refreshCatalogue: refreshCatalogue{budget: 100}}
	s, ctx := refreshService(t, &cat.refreshCatalogue)
	s.meta = cat // the service must see the slug-capable provider

	if got, _ := s.CatalogueSlug(ctx, "hc:1245"); got != "dune" {
		t.Errorf("hardcover book = %q, want dune", got)
	}
	for _, key := range []string{"/works/OL1W", "gb:abc", "hc:a:7", ""} {
		if got, _ := s.CatalogueSlug(ctx, key); got != "" {
			t.Errorf("%q = %q, want no slug", key, got)
		}
	}
	cat.used = 71
	if got, _ := s.CatalogueSlug(ctx, "hc:1245"); got != "" {
		t.Errorf("over the budget gate = %q, want no lookup", got)
	}
	if len(cat.slugAsked) != 1 {
		t.Errorf("asked %v, want only the first Hardcover book", cat.slugAsked)
	}
}
