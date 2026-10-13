package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/insights"
)

// A person's page is staff information (their devices and addresses): a requester gets
// 403, an unknown id 404, staff the profile — with no audiobook data in it.
func TestInsightsPersonRoute(t *testing.T) {
	s := insightsRouteServer(t)
	if _, err := s.st.DB().ExecContext(context.Background(),
		`INSERT INTO stream_sessions (user_id,user_name,rating_key,media_type,title,ip_address,started_at,stopped_at)
		 VALUES ('7','Sam','5','movie','Dune','203.0.113.9',1760000000,1760003600)`); err != nil {
		t.Fatal(err)
	}
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	if rec := s.do("GET", "/api/v1/insights/users/7", kid); rec.Code != http.StatusForbidden {
		t.Fatalf("requester: %d, want 403", rec.Code)
	}
	_, boss := s.user(t, "boss@example.com", auth.RoleManager)
	if rec := s.do("GET", "/api/v1/insights/users/404", boss); rec.Code != http.StatusNotFound {
		t.Errorf("unknown person: %d, want 404", rec.Code)
	}
	rec := s.do("GET", "/api/v1/insights/users/7?window=0", boss)
	if rec.Code != http.StatusOK {
		t.Fatalf("manager: %d %s", rec.Code, rec.Body)
	}
	var p insights.PersonProfile
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.TotalPlays != 1 || p.Username != "Sam" || len(p.IPs) != 1 || p.IPs[0].IP != "203.0.113.9" {
		t.Errorf("profile = %+v", p)
	}
	body := strings.ToLower(rec.Body.String())
	for _, banned := range []string{"audiobook", "listen", "book_id"} {
		if strings.Contains(body, banned) {
			t.Errorf("the People page mentions %q: %s", banned, rec.Body)
		}
	}
}
