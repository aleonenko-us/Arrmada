package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

func findingsByKey(fs []Finding) map[string]Finding {
	out := map[string]Finding{}
	for _, f := range fs {
		out[f.Key] = f
	}
	return out
}

// Each thing that keeps a usenet client from working is its own finding, linked to the
// client's card; a client that's fine, or can't be read (the client check says so), has none.
func TestUsenetClientsCheck(t *testing.T) {
	broken := download.UsenetFacts{
		ServersConfigured: 2, ServersWorking: 0,
		Servers: []download.UsenetServer{{Name: "Provider", Error: "502 Authentication failed"}, {Name: "Backup", Error: "timed out"}},
		Missing: []string{"arrmada-tv"},
		Categories: []download.UsenetCategory{
			{Name: "arrmada", Present: true, Folder: &download.PathCheck{Remote: "/films", Visible: false}},
			{Name: "arrmada-tv"},
		},
		CompleteFolder: download.PathCheck{Remote: "/downloads/complete", Local: "/downloads/complete", Visible: false},
		Language:       "nl", NotEnglish: true,
	}
	fine := download.UsenetFacts{ServersConfigured: 1, ServersWorking: 1, Language: "en",
		CompleteFolder: download.PathCheck{Remote: "/downloads/complete", Visible: true}}
	check := UsenetClientsCheck(func(context.Context) ([]UsenetClient, error) {
		return []UsenetClient{
			{ID: 3, Name: "SAB", Facts: broken},
			{ID: 4, Name: "SAB fine", Facts: fine},
			{ID: 5, Name: "SAB down", Err: errors.New("unreachable")},
		}, nil
	})
	got := findingsByKey(check.Run(context.Background()))
	want := map[string]struct{ level, says string }{
		"downloads.usenet.3.servers":          {LevelError, "None of SAB's news servers is working (Provider: 502 Authentication failed)"},
		"downloads.usenet.3.categories":       {LevelWarning, "missing Arrmada's arrmada-tv category"},
		"downloads.usenet.3.complete":         {LevelWarning, "can't see SAB's completed folder /downloads/complete"},
		"downloads.usenet.3.category.arrmada": {LevelWarning, "files arrmada downloads in (/films)"},
		"downloads.usenet.3.language":         {LevelWarning, `language is "nl"`},
	}
	if len(got) != len(want) {
		t.Errorf("findings = %+v", got)
	}
	for key, w := range want {
		f, ok := got[key]
		if !ok || f.Level != w.level || !strings.Contains(f.Message, w.says) {
			t.Errorf("%s = %+v, want %s saying %q", key, f, w.level, w.says)
			continue
		}
		if f.Fix == nil || f.Fix.Path != "/settings/connections#download_client-3" {
			t.Errorf("%s fix = %+v, want the client's card", key, f.Fix)
		}
	}

	none := UsenetClientsCheck(func(context.Context) ([]UsenetClient, error) {
		return []UsenetClient{{ID: 7, Name: "Empty", Facts: download.UsenetFacts{}}}, nil
	}).Run(context.Background())
	if len(none) != 1 || none[0].Level != LevelError || !strings.Contains(none[0].Message, "Empty has no news server") {
		t.Errorf("no servers = %+v", none)
	}
}

// The idle warning names the client, how long and how much is waiting, and points at the
// provider; the leftovers warning says what keeps being left behind.
func TestUsenetProgressAndLeftoversChecks(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	idle := UsenetProgressCheck(func(context.Context) []UsenetIdle {
		return []UsenetIdle{{ClientID: 2, ClientName: "SABnzbd", Since: now.Add(-31 * time.Minute), Jobs: 3}}
	}, func() time.Time { return now }).Run(context.Background())
	if len(idle) != 1 || idle[0].Level != LevelWarning || idle[0].Key != "downloads.usenet.2.progress" ||
		idle[0].Message != "SABnzbd hasn't downloaded anything for 31 minutes although 3 downloads are waiting — check your Usenet provider (its servers, login and account) in SABnzbd → Config → Servers." {
		t.Errorf("idle = %+v", idle)
	}
	left := UsenetLeftoversCheck(func(context.Context) []UsenetLeftovers {
		return []UsenetLeftovers{{ClientID: 2, ClientName: "SABnzbd", Count: 2, Name: "Film.2020", Detail: "left /x in place"}}
	}).Run(context.Background())
	if len(left) != 1 || !strings.Contains(left[0].Message, "imported 2 downloads from SABnzbd but had to leave their folders in place, most recently Film.2020 (left /x in place)") ||
		left[0].Fix == nil || !strings.HasSuffix(left[0].Fix.Path, "#download_client-2") {
		t.Errorf("leftovers = %+v", left)
	}
}
