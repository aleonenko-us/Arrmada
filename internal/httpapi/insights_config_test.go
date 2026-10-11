package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/insights"
)

// PLEX-17: Settings → Plex saves the monitoring switch and the poll interval on their own,
// without the server address. A missing url keeps the saved connection; the answer never
// carries the token.
func TestUpdatePlexConfigWithoutURLKeepsTheConnection(t *testing.T) {
	pms := (&fakePMS{}).server(t)
	a := plexScanAPI(t, pms.URL)
	ctx := t.Context()

	put := func(body string) insights.Config {
		t.Helper()
		rec := httptest.NewRecorder()
		a.handleUpdateInsightsConfig(rec, httptest.NewRequest("PUT", "/api/v1/insights/plex", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("PUT %s = %d %s", body, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "plex-secret") {
			t.Fatalf("the answer carries the token: %s", rec.Body)
		}
		var c insights.Config
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}

	c := put(`{"enabled":false}`)
	if c.URL != pms.URL || !c.TokenSet || c.Enabled || !c.EnabledSet {
		t.Fatalf("after switching monitoring off: %+v", c)
	}
	c = put(`{"poll_seconds":12}`)
	if c.URL != pms.URL || c.PollSeconds != 12 || c.Enabled {
		t.Fatalf("after changing the interval: %+v", c)
	}
	if got := a.deps.Insights.Config(ctx); got.URL != pms.URL || !got.TokenSet {
		t.Fatalf("stored connection changed: %+v", got)
	}

	// The connection editor still sends the address, and an empty one still clears it.
	c = put(`{"url":""}`)
	if c.URL != "" || c.Enabled {
		t.Fatalf("an explicit empty url: %+v", c)
	}
}
