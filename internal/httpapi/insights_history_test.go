package httpapi

import (
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/insights"
	"github.com/tristenlammi/arrmada/internal/settings"
)

func insightsRouteServer(t *testing.T) *routeServer {
	t.Helper()
	return newRouteServer(t, func(d *Deps) {
		d.Insights = insights.NewService(d.Store.DB(), settings.NewService(d.Store.DB()), nil, nil, d.Log)
	})
}

// The History export is staff-only, takes History's filters, quotes a title with a comma,
// and defuses a value a spreadsheet would run as a formula.
func TestHistoryCSV(t *testing.T) {
	s := insightsRouteServer(t)
	db := s.st.DB()
	for _, q := range []string{
		`INSERT INTO stream_sessions (user_id,user_name,rating_key,media_type,title,grandparent_title,parent_index,media_index,player,platform,ip_address,decision,started_at,stopped_at,view_offset_ms,duration_ms)
		 VALUES ('7','Sam','8812','episode','Hello, World','Severance',1,2,'Living Room','tvOS','203.0.113.9','transcode',1760000000,1760001800,1800000,3600000)`,
		`INSERT INTO stream_sessions (user_id,user_name,rating_key,media_type,title,player,decision,started_at,stopped_at)
		 VALUES ('7','Sam','5','movie','=HYPERLINK("x")','TV','direct_play',1750000000,1750003600)`,
		`INSERT INTO stream_sessions (user_id,user_name,rating_key,media_type,title,decision,started_at,stopped_at)
		 VALUES ('8','Kim','6','movie','Not Sam''s','direct_play',1760000000,1760003600)`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}

	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	if rec := s.do("GET", "/api/v1/insights/history.csv", kid); rec.Code != http.StatusForbidden {
		t.Fatalf("requester: %d, want 403", rec.Code)
	}
	_, boss := s.user(t, "boss@example.com", auth.RoleManager)
	rec := s.do("GET", "/api/v1/insights/history.csv?user=7", boss)
	if rec.Code != http.StatusOK {
		t.Fatalf("manager: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("disposition %q", cd)
	}
	if !strings.Contains(rec.Body.String(), `"Hello, World"`) {
		t.Errorf("a title with a comma isn't quoted:\n%s", rec.Body)
	}
	recs, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || strings.Join(recs[0], ",") != "when,user,title,show,season_episode,player,platform,ip,decision,watched_secs,progress_pct,sessions,server" {
		t.Fatalf("csv = %q", recs)
	}
	ep := recs[1]
	if ep[2] != "Hello, World" || ep[3] != "Severance" || ep[4] != "S01E02" || ep[7] != "203.0.113.9" || ep[9] != "1800" || ep[10] != "50" || ep[11] != "1" || ep[12] != "plex" {
		t.Errorf("episode row = %q", ep)
	}
	if recs[2][2] != `'=HYPERLINK("x")` {
		t.Errorf("formula title = %q, want it defused", recs[2][2])
	}

	// The date filter narrows it the same way as History.
	rec = s.do("GET", "/api/v1/insights/history.csv?from=1755000000", boss)
	recs, _ = csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if len(recs) != 3 { // header + Sam's episode + Kim's film
		t.Errorf("from filter: %d records", len(recs))
	}
}

// History reads its new filters from the query string.
func TestHistoryFilterParams(t *testing.T) {
	r, _ := http.NewRequest("GET", "/api/v1/insights/history?user=7&from=100&to=200&group=1&type=movie&decision=transcode&q=dune", nil)
	f := historyFilter(r)
	if f.UserID != "7" || f.From != 100 || f.To != 200 || !f.Group || f.Type != "movie" || f.Decision != "transcode" || f.Query != "dune" {
		t.Errorf("filter = %+v", f)
	}
	r, _ = http.NewRequest("GET", "/api/v1/insights/history?from=-5&to=x&group=yes", nil)
	if f := historyFilter(r); f.From != 0 || f.To != 0 || f.Group {
		t.Errorf("bad values = %+v", f)
	}
}
