package automation

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Adding an Audio language rule to a profile with a library behind it makes every file
// without the language upgradeable at once. That wave goes through the same per-sweep
// budget as any other upgrade: one sweep grabs at most the budget, the next picks up the
// rest, and nothing is grabbed twice.
func TestLanguageRuleUpgradesHonourTheSweepBudget(t *testing.T) {
	h := newStallHarness(t)
	if _, err := h.c.db.Exec(`UPDATE quality_profiles SET upgrades_enabled = 1, ideal = '{"language":{"de":"want"}}'`); err != nil {
		t.Fatal(err)
	}
	var offers []string
	for i := 0; i < 6; i++ {
		title := "Film " + string(rune('A'+i))
		id := h.addMovie(t, 3000+i, title, 2001)
		dotted := strings.ReplaceAll(title, " ", ".")
		// The name says English, so the file is known to lack German.
		have := dotted + ".2001.ENGLISH.1080p.WEB-DL.x264-GRP"
		if _, err := h.c.db.Exec(`UPDATE movies SET has_file = 1, movie_file_path = ?, source_release = ?, extra_json = '{"original_language":"en"}' WHERE id = ?`,
			fmt.Sprintf("/lib/%s/%s.mkv", title, have), have, id); err != nil {
			t.Fatal(err)
		}
		// The same release with German audio beside the original: only the language is new.
		offers = append(offers, dotted+".2001.German.DL.1080p.WEB-DL.x264-GRP")
	}
	h.ix.offer(offers...)
	h.c.SetUpgradeBudget(func(context.Context) int { return 4 })
	adds := func() int {
		n := 0
		for _, c := range h.qbit.callLog() {
			if strings.HasPrefix(c, "add ") {
				n++
			}
		}
		return n
	}

	h.c.UpgradeMovies(h.ctx)
	if n := adds(); n != 4 {
		t.Fatalf("one sweep grabbed %d language upgrades, want the budget of 4", n)
	}
	h.c.UpgradeMovies(h.ctx)
	if n := adds(); n != 6 {
		t.Errorf("after the second sweep %d grabbed in all, want 6 (the two left, none twice)", n)
	}
}

// Adding a German Must (or Prefer) to a profile whose files nobody has read — no analysis,
// names that state no language — re-downloads nothing: those files aren't judged on a
// guess, so a release that says German is no upgrade over them. No wave, whatever budget.
func TestLanguageRuleOnUnreadFilesTriggersNoUpgrades(t *testing.T) {
	for _, rule := range []string{`{"language":{"de":"must"}}`, `{"language":{"de":"want","original":"must"}}`} {
		t.Run(rule, func(t *testing.T) {
			h := newStallHarness(t)
			if _, err := h.c.db.Exec(`UPDATE quality_profiles SET upgrades_enabled = 1, ideal = ?`, rule); err != nil {
				t.Fatal(err)
			}
			var offers []string
			for i := 0; i < 5; i++ {
				title := "Film " + string(rune('A'+i))
				id := h.addMovie(t, 4000+i, title, 2001)
				dotted := strings.ReplaceAll(title, " ", ".")
				have := dotted + ".2001.1080p.WEB-DL.x264-GRP"
				if _, err := h.c.db.Exec(`UPDATE movies SET has_file = 1, movie_file_path = ?, source_release = ?, extra_json = '{"original_language":"en"}' WHERE id = ?`,
					fmt.Sprintf("/lib/%s/%s.mkv", title, have), have, id); err != nil {
					t.Fatal(err)
				}
				offers = append(offers, dotted+".2001.German.DL.1080p.WEB-DL.x264-GRP")
			}
			h.ix.offer(offers...)
			h.c.SetUpgradeBudget(func(context.Context) int { return 0 }) // no limit: nothing may be grabbed at all
			h.c.UpgradeMovies(h.ctx)
			for _, c := range h.qbit.callLog() {
				if strings.HasPrefix(c, "add ") {
					t.Fatalf("grabbed an upgrade for a file whose audio nobody read: %s", c)
				}
			}
		})
	}
}
