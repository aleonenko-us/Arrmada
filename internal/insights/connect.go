package insights

import (
	"context"
	"errors"
	"sync"

	"github.com/tristenlammi/arrmada/internal/plex"
	"github.com/tristenlammi/arrmada/internal/safego"
)

// configured: a URL and a token are both saved.
func (s *Service) configured(ctx context.Context) bool {
	return s.settings.Get(ctx, keyURL, "") != "" && s.settings.Get(ctx, keyToken, "") != ""
}

// enableIfUnset turns monitoring on the first time Plex is fully connected. Once anyone
// has switched it on or off, that choice stands: a later sign-in never overrides it.
func (s *Service) enableIfUnset(ctx context.Context) {
	if s.settings.Get(ctx, keyEnabled, "") != "" || !s.configured(ctx) {
		return
	}
	if err := s.settings.SetBool(ctx, keyEnabled, true); err != nil {
		s.log.Warn("insights: could not switch monitoring on", "err", err)
	}
}

// refreshIdentity stores the saved server's machine id and name, or clears them when it
// doesn't answer (a stale id would gate Plex sign-in against the wrong server).
func (s *Service) refreshIdentity(ctx context.Context) {
	id, name, err := s.probe(ctx, s.settings.Get(ctx, keyURL, ""), s.settings.Get(ctx, keyToken, ""))
	if err != nil {
		id, name = "", ""
	}
	s.saveIdentity(ctx, id, name)
}

func (s *Service) saveIdentity(ctx context.Context, machineID, name string) {
	if err := s.settings.Set(ctx, KeyMachineID, machineID); err != nil {
		s.log.Warn("insights: could not save the Plex server id", "err", err)
	}
	_ = s.settings.Set(ctx, keyServerName, name)
}

// probe asks a server who it is, within probeTimeout: its machine id, and its name
// (best-effort — an unnamed server still counts as answering).
func (s *Service) probe(ctx context.Context, url, token string) (machineID, name string, err error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c := plex.New(url, token)
	id, err := c.Identity(ctx)
	if err != nil {
		return "", "", err
	}
	name, _ = c.ServerName(ctx)
	return id.MachineIdentifier, name, nil
}

// ServerChoice is one owned server that answered during sign-in, for the UI's picker when
// there is more than one.
type ServerChoice struct {
	Name      string `json:"name"`
	MachineID string `json:"machine_id"`
	URL       string `json:"url"`
}

// PlexAuthResult is a sign-in poll's answer. Authorized with ServerName set: connected.
// Authorized with Choices: several servers answered and the person picks one. Authorized
// with neither: the token is saved but no server could be reached, so the URL is theirs
// to type.
type PlexAuthResult struct {
	Authorized bool           `json:"authorized"`
	ServerName string         `json:"server_name,omitempty"`
	Choices    []ServerChoice `json:"choices,omitempty"`
}

// PollPlexAuth checks whether the user has authorized the sign-in. On success it reads
// the account's own servers from plex.tv, stores the token, and makes sure the saved URL
// is one of those servers: the stored one if it still is, else the owned server plex.tv
// lists that answers (with the machine id plex.tv gave for it). A friend's server shared
// with the account is never picked. Monitoring is switched on unless someone already chose.
//
// The token is the owner's plex.tv token, so it's only ever sent to an address that has
// first said, without it, that it's one of the account's own servers. When plex.tv can't
// list them nothing can be checked: the sign-in fails and nothing changes. A saved URL
// that isn't one of them is cleared rather than left to be polled with the new token.
func (s *Service) PollPlexAuth(ctx context.Context, pinID int) (PlexAuthResult, error) {
	cid := s.clientID(ctx)
	token, err := plex.CheckPIN(ctx, cid, pinID)
	if err != nil {
		return PlexAuthResult{}, err
	}
	if token == "" {
		return PlexAuthResult{}, nil // still pending
	}
	cands, err := plex.DiscoverServers(ctx, cid, token)
	if err != nil {
		s.log.Warn("insights: could not list your Plex servers after sign-in", "err", err)
		return PlexAuthResult{}, errors.New("plex.tv didn't list your servers, so nothing was changed — try signing in again")
	}
	if err := s.settings.Set(ctx, keyToken, token); err != nil {
		return PlexAuthResult{}, err
	}
	res := PlexAuthResult{Authorized: true}

	// The saved URL stays when it says it's a server this account owns, and then answers
	// to the token as that same server.
	if url := s.settings.Get(ctx, keyURL, ""); url != "" {
		if id, name, ok := s.ownedProbe(ctx, url, token, cands); ok {
			s.saveIdentity(ctx, id, name)
			s.enableIfUnset(ctx)
			s.clearUnbound(ctx)
			res.ServerName = name
			return res, nil
		}
		// Not one of theirs (or not answering): the new token is never polled against it.
		_ = s.settings.Set(ctx, keyURL, "")
		s.saveIdentity(ctx, "", "")
	}
	answering := s.probeCandidates(ctx, cands, token)
	switch len(answering) {
	case 0:
		s.log.Warn("insights: signed in with Plex, but none of your servers answered", "servers", len(cands))
		s.markUnbound(ctx, nil, cands) // the address is theirs to type
	case 1:
		ch := answering[0]
		if err := s.settings.Set(ctx, keyURL, ch.URL); err != nil {
			return res, err
		}
		s.saveIdentity(ctx, ch.MachineID, ch.Name)
		s.enableIfUnset(ctx)
		s.clearUnbound(ctx)
		res.ServerName = ch.Name
	default:
		res.Choices = answering
		s.markUnbound(ctx, answering, cands) // one of these is picked next
	}
	return res, nil
}

// probeCandidates tries every address of every server at once (a dead one costs the probe
// timeout, not the timeout times how many there are) and keeps, per server, the best-ranked
// address that answered as that server. Servers come back in the order plex.tv listed them.
func (s *Service) probeCandidates(ctx context.Context, cands []plex.ServerCandidate, token string) []ServerChoice {
	type hit struct {
		id, name string
		ok       bool
	}
	hits := make([][]hit, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		hits[i] = make([]hit, len(c.Conns))
		for j, conn := range c.Conns {
			wg.Add(1)
			safego.Go(s.log, "plex probe", func() {
				defer wg.Done()
				id, name, ok := s.ownedProbe(ctx, conn.URL(), token, []plex.ServerCandidate{c})
				hits[i][j] = hit{id, name, ok}
			})
		}
	}
	wg.Wait()
	var answering []ServerChoice
	for i, c := range cands {
		for j, conn := range c.Conns { // already ranked: local http first
			h := hits[i][j]
			if !h.ok {
				continue
			}
			name := h.name
			if name == "" {
				name = c.Name
			}
			answering = append(answering, ServerChoice{Name: name, MachineID: h.id, URL: conn.URL()})
			break
		}
	}
	return answering
}

// ownedProbe checks url without the token first: only a server that says it's one of
// cands is then asked with it, and must answer as that same server.
func (s *Service) ownedProbe(ctx context.Context, url, token string, cands []plex.ServerCandidate) (machineID, name string, ok bool) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	pub, err := plex.PublicIdentity(pctx, url)
	cancel()
	if err != nil || !ownsMachine(cands, pub.MachineIdentifier) {
		return "", "", false
	}
	id, name, err := s.probe(ctx, url, token)
	if err != nil || id != pub.MachineIdentifier {
		return "", "", false
	}
	return id, name, true
}

func ownsMachine(cands []plex.ServerCandidate, machineID string) bool {
	for _, c := range cands {
		if c.MachineID == machineID {
			return true
		}
	}
	return false
}
