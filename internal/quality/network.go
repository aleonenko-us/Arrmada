package quality

import (
	"context"
	"strings"
)

// Which network wins between a usenet and a torrent release. By default the better release
// wins whichever network it's on; the download_network_preference setting can prefer one.
// A preference decides after the score and before size (KeyNetwork) — between releases the
// profile rates the same, never as a filter: a better torrent still beats a usenet release
// under "Prefer Usenet".

// KeyNetworkPreference is the settings key holding the preference.
const KeyNetworkPreference = "download_network_preference"

// The preference's values.
const (
	NetworkBest    = "best"    // the better release wins, whichever network (the default)
	NetworkUsenet  = "usenet"  // prefer the usenet release between equals
	NetworkTorrent = "torrent" // prefer the torrent between equals
)

// KeyNetwork: the preferred network decided it (download_network_preference).
const KeyNetwork RankKey = "network"

// NormalizeNetwork reads a stored preference: anything unrecognised is NetworkBest.
func NormalizeNetwork(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case NetworkUsenet:
		return NetworkUsenet
	case NetworkTorrent:
		return NetworkTorrent
	}
	return NetworkBest
}

// ValidNetwork reports whether v may be saved as the preference.
func ValidNetwork(v string) bool {
	return v == NetworkBest || v == NetworkUsenet || v == NetworkTorrent
}

// NetworkPreference is the preference in effect (NetworkBest unless set).
func (s *Service) NetworkPreference(ctx context.Context) string {
	if s == nil || s.settings == nil {
		return NetworkBest
	}
	return NormalizeNetwork(s.settings.Get(ctx, KeyNetworkPreference, NetworkBest))
}

// withNetwork is p with the preference in effect attached, so the ranking can apply it.
func (s *Service) withNetwork(ctx context.Context, p Profile) Profile {
	p.PreferNetwork = s.NetworkPreference(ctx)
	return p
}

// networkOrder applies the preference to a and b: decided when one is usenet and the other
// a torrent and a preference is set; aFirst says a is on the preferred network.
func (p Profile) networkOrder(a, b Candidate) (decided, aFirst bool) {
	if a.Usenet == b.Usenet {
		return false, false
	}
	switch p.PreferNetwork {
	case NetworkUsenet:
		return true, a.Usenet
	case NetworkTorrent:
		return true, !a.Usenet
	}
	return false, false
}

// networkLoss is why the release on the other network ranked second ("Chosen over the … —").
func (p Profile) networkLoss() string {
	if p.PreferNetwork == NetworkUsenet {
		return "you prefer Usenet"
	}
	return "you prefer torrents"
}

// networkWhy is the winner's line when the preference decided the pick.
func (p Profile) networkWhy() string {
	if p.PreferNetwork == NetworkUsenet {
		return "On Usenet, which you prefer"
	}
	return "A torrent, which you prefer"
}
