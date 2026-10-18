package insights

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/netsafe"
)

// The saved Plex token is the owner's plex.tv token: it opens every server on their
// account. It only ever goes to the address it was saved with. A Test or Save with a new
// address and no token typed is refused, so nobody who can edit the connection can point
// it at their own server and collect the token.
//
// One exception: a "Sign in with Plex" that stored a fresh token without settling on an
// address (several servers answered, or none did) leaves the token waiting for one. For
// a short while after, the address picked from the offered servers, or (when none
// answered) the one typed in, takes the token without retyping it, since retyping a
// token plex.tv handed over isn't something anyone can do.

// ErrTokenForNewAddress is the refusal for a changed address with the token left blank.
var ErrTokenForNewAddress = errors.New("Enter the token again for the new address, or sign in with Plex again — the saved token is only sent to the address it was saved for.")

const (
	// keyUnbound records a sign-in's token that's still waiting for its address.
	keyUnbound = "insights_plex_unbound"
	// unboundFor is how long that token waits.
	unboundFor = 30 * time.Minute
)

type unboundToken struct {
	At   int64    `json:"at"`             // unix seconds the sign-in finished
	URLs []string `json:"urls,omitempty"` // the servers offered; none = none answered
}

// markUnbound notes a sign-in that stored a token without an address (offered: the
// servers to pick from).
func (s *Service) markUnbound(ctx context.Context, offered []ServerChoice) {
	u := unboundToken{At: time.Now().Unix()}
	for _, c := range offered {
		u.URLs = append(u.URLs, c.URL)
	}
	b, _ := json.Marshal(u)
	if err := s.settings.Set(ctx, keyUnbound, string(b)); err != nil {
		s.log.Warn("insights: couldn't note the Plex sign-in awaiting an address", "err", err)
	}
}

// clearUnbound forgets a waiting sign-in: an address was saved, or a token typed.
func (s *Service) clearUnbound(ctx context.Context) {
	if s.settings.Get(ctx, keyUnbound, "") != "" {
		_ = s.settings.Set(ctx, keyUnbound, "")
	}
}

// savedTokenMayGo reports whether the saved token may be sent to url without being typed
// again: it's the saved address, there's no saved token, or a waiting sign-in allows it.
// An empty address (clearing the connection) sends it nowhere.
func (s *Service) savedTokenMayGo(ctx context.Context, url string) bool {
	if strings.TrimSpace(url) == "" || s.settings.Get(ctx, keyToken, "") == "" || netsafe.SameEndpoint(url, s.settings.Get(ctx, keyURL, "")) {
		return true
	}
	var u unboundToken
	if err := json.Unmarshal([]byte(s.settings.Get(ctx, keyUnbound, "")), &u); err != nil || u.At == 0 {
		return false
	}
	if time.Since(time.Unix(u.At, 0)) > unboundFor {
		return false
	}
	if len(u.URLs) == 0 {
		return true
	}
	return slices.ContainsFunc(u.URLs, func(o string) bool { return netsafe.SameEndpoint(o, url) })
}
