package quality

import (
	"strings"
	"testing"
)

func usenetCand(name string, gb float64) Candidate {
	c := NewCandidate(name, gb, 0)
	c.Usenet = true
	return c
}

func preferring(net string) Profile { return Profile{PreferNetwork: net} }

// The network preference across both rankings: "best" never adds a step, a preference
// decides between releases that score the same (before size), and never overrides a
// better release or a dead torrent. Seeders never decide between the two networks.
func TestNetworkPreferenceTable(t *testing.T) {
	const (
		u1080  = "Film.2021.1080p.WEB-DL.x264-NZBGRP"
		t1080  = "Film.2021.1080p.WEB-DL.x264-TORGRP"
		t2160  = "Film.2021.2160p.WEB-DL.x264-TORGRP"
		u2160  = "Film.2021.2160p.WEB-DL.x264-NZBGRP"
		tDead  = "Film.2021.2160p.BluRay.x264-DEADGRP"
		equal  = "an equal match"
		vSmall = "smaller file"
	)
	cases := []struct {
		name     string
		pref     string
		a, b     Candidate
		winner   string
		key      map[string]RankKey // by ranking mode; "" = same for both (keyAll)
		keyAll   RankKey
		lostText string
		stable   bool // a tie: the input order decides, so each order wins for its first
	}{
		{name: "best, equal: a tie, not seeders", pref: NetworkBest,
			a: usenetCand(u1080, 8), b: NewCandidate(t1080, 8, 500), keyAll: KeyTie, lostText: equal, stable: true},
		{name: "unset, equal: a tie", pref: "",
			a: usenetCand(u1080, 8), b: NewCandidate(t1080, 8, 500), keyAll: KeyTie, lostText: equal, stable: true},
		{name: "prefer usenet, equal", pref: NetworkUsenet,
			a: usenetCand(u1080, 8), b: NewCandidate(t1080, 8, 500), winner: u1080, keyAll: KeyNetwork, lostText: "you prefer Usenet"},
		{name: "prefer torrent, equal", pref: NetworkTorrent,
			a: usenetCand(u1080, 8), b: NewCandidate(t1080, 8, 5), winner: t1080, keyAll: KeyNetwork, lostText: "you prefer torrents"},
		{name: "best, torrent larger: size decides", pref: NetworkBest,
			a: usenetCand(u1080, 8), b: NewCandidate(t1080, 12, 5), winner: t1080,
			key: map[string]RankKey{RankingV1: KeyBitrate, RankingV2: KeySize}, lostText: vSmall},
		{name: "prefer usenet beats size", pref: NetworkUsenet,
			a: usenetCand(u1080, 8), b: NewCandidate(t1080, 12, 5), winner: u1080, keyAll: KeyNetwork, lostText: "you prefer Usenet"},
		{name: "prefer torrent beats size", pref: NetworkTorrent,
			a: usenetCand(u1080, 12), b: NewCandidate(t1080, 8, 5), winner: t1080, keyAll: KeyNetwork, lostText: "you prefer torrents"},
		{name: "best, near-equal sizes: no seeders band across networks", pref: NetworkBest,
			a: usenetCand(u1080, 10), b: NewCandidate(t1080, 9.5, 900), winner: u1080,
			key: map[string]RankKey{RankingV1: KeyBitrate, RankingV2: KeySize}, lostText: vSmall},
		{name: "prefer usenet never beats a better torrent", pref: NetworkUsenet,
			a: usenetCand(u1080, 8), b: NewCandidate(t2160, 8, 5), winner: t2160, keyAll: KeyResolution, lostText: "lower resolution"},
		{name: "prefer torrent never beats a better usenet release", pref: NetworkTorrent,
			a: usenetCand(u2160, 8), b: NewCandidate(t1080, 8, 5), winner: u2160, keyAll: KeyResolution, lostText: "lower resolution"},
		{name: "a dead torrent loses to usenet whatever the preference", pref: NetworkTorrent,
			a: usenetCand(u1080, 8), b: NewCandidate(tDead, 30, 0), winner: u1080, keyAll: KeyHealth, lostText: "it has no seeders"},
	}
	forEachRanking(t, func(t *testing.T, mode string) {
		for _, tc := range cases {
			p := preferring(tc.pref)
			for _, in := range [][]Candidate{{tc.a, tc.b}, {tc.b, tc.a}} {
				d := decideWith(NewDefaultEngine(), mode, p, in...)
				want := tc.winner
				if tc.stable {
					want = in[0].Name
				}
				if winnerName(d) != want || len(d.Eligible) != 2 {
					t.Fatalf("%s: winner %q (eligible %d), want %q", tc.name, winnerName(d), len(d.Eligible), want)
				}
				key := tc.keyAll
				if k, ok := tc.key[mode]; ok {
					key = k
				}
				if got := d.Eligible[1].LostOn; got != key {
					t.Errorf("%s: lost on %q, want %q", tc.name, got, key)
				}
				if got := d.Eligible[1].LostOnText; got != tc.lostText {
					t.Errorf("%s: lost-on text %q, want %q", tc.name, got, tc.lostText)
				}
				if !strings.Contains(d.ChosenOver, tc.lostText) {
					t.Errorf("%s: ChosenOver = %q, want it to say %q", tc.name, d.ChosenOver, tc.lostText)
				}
				if strings.Contains(d.ChosenOver, "fewer seeders") {
					t.Errorf("%s: seeders cited between a usenet and a torrent release: %q", tc.name, d.ChosenOver)
				}
				if key == KeyNetwork {
					if d.DecidedBy != KeyNetwork || !containsStr(d.Why, p.networkWhy()) {
						t.Errorf("%s: decided by %q, why %v", tc.name, d.DecidedBy, d.Why)
					}
				}
			}
		}
	})
}

// Two torrents still tie-break on seeders exactly as before; the preference has no say
// between releases on the same network.
func TestNetworkPreferenceLeavesSameNetworkAlone(t *testing.T) {
	forEachRanking(t, func(t *testing.T, mode string) {
		few := NewCandidate("Movie.2020.1080p.WEB-DL.x264-FEW", 10.0, 2)
		many := NewCandidate("Movie.2020.1080p.WEB-DL.x264-MANY", 9.5, 150)
		for _, pref := range []string{NetworkBest, NetworkUsenet, NetworkTorrent} {
			d := decideWith(NewDefaultEngine(), mode, preferring(pref), few, many)
			if winnerName(d) != many.Name || d.Eligible[1].LostOn != KeyHealth {
				t.Errorf("%s: winner %q lost on %q", pref, winnerName(d), d.Eligible[1].LostOn)
			}
		}
		a, b := usenetCand("Movie.2020.1080p.WEB-DL.x264-NZBA", 8), usenetCand("Movie.2020.1080p.WEB-DL.x264-NZBB", 8)
		d := decideWith(NewDefaultEngine(), mode, preferring(NetworkUsenet), a, b)
		if winnerName(d) != a.Name || d.Eligible[1].LostOn != KeyTie {
			t.Errorf("two usenet releases: winner %q lost on %q", winnerName(d), d.Eligible[1].LostOn)
		}
	})
}

// A usenet release has no seeders to count: the profile's minimum doesn't apply to it,
// and it is never "dead".
func TestMinSeedersIgnoredForUsenet(t *testing.T) {
	p := Profile{MinSeeders: 5}
	e := NewDefaultEngine()
	if ev := e.Evaluate(p, usenetCand("Film.2021.1080p.WEB-DL.x264-NZB", 8)); !ev.Eligible {
		t.Fatalf("usenet release rejected: %s", ev.RejectReason)
	}
	if ev := e.Evaluate(p, NewCandidate("Film.2021.1080p.WEB-DL.x264-TOR", 8, 0)); ev.Eligible || ev.RejectCode != RejectSeeders {
		t.Fatalf("0-seeder torrent: %+v", ev)
	}
	forEachRanking(t, func(t *testing.T, mode string) {
		// Ranked against a seeded torrent that scores lower, the usenet release still wins:
		// 0 seeders isn't dead on usenet.
		d := decideWith(NewDefaultEngine(), mode, Profile{},
			NewCandidate("Film.2021.1080p.WEB-DL.x264-TOR", 8, 300), usenetCand("Film.2021.2160p.WEB-DL.x264-NZB", 8))
		if winnerName(d) != "Film.2021.2160p.WEB-DL.x264-NZB" {
			t.Errorf("winner %q", winnerName(d))
		}
	})
}

func TestNormalizeNetwork(t *testing.T) {
	for in, want := range map[string]string{"": NetworkBest, "best": NetworkBest, "USENET": NetworkUsenet, " torrent ": NetworkTorrent, "nzb": NetworkBest} {
		if got := NormalizeNetwork(in); got != want {
			t.Errorf("NormalizeNetwork(%q) = %q, want %q", in, got, want)
		}
	}
	for _, v := range []string{NetworkBest, NetworkUsenet, NetworkTorrent} {
		if !ValidNetwork(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{"", "Usenet", "both"} {
		if ValidNetwork(v) {
			t.Errorf("%q should not be valid", v)
		}
	}
}

// The service reads the setting on every decision, so a change applies to the next search.
func TestServiceAppliesNetworkPreference(t *testing.T) {
	s, ctx := testService(t)
	u := usenetCand("Film.2021.1080p.WEB-DL.x264-NZBGRP", 8)
	tor := NewCandidate("Film.2021.1080p.WEB-DL.x264-TORGRP", 8, 50)
	if d := s.Decide(ctx, "", []Candidate{tor, u}); winnerName(d) != tor.Name || d.DecidedBy == KeyNetwork {
		t.Fatalf("default: winner %q decided by %q", winnerName(d), d.DecidedBy)
	}
	if err := s.settings.Set(ctx, KeyNetworkPreference, NetworkUsenet); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Resolve(ctx, ""); p.PreferNetwork != NetworkUsenet {
		t.Fatalf("resolved preference %q", p.PreferNetwork)
	}
	d := s.Decide(ctx, "", []Candidate{tor, u})
	if winnerName(d) != u.Name || d.DecidedBy != KeyNetwork || !strings.HasSuffix(d.ChosenOver, "— you prefer Usenet") {
		t.Fatalf("prefer usenet: winner %q decided by %q, %q", winnerName(d), d.DecidedBy, d.ChosenOver)
	}
	if d.Shadow == nil || d.Shadow.Winner != u.Name || d.Shadow.DecidedBy != KeyNetwork {
		t.Errorf("v2 shadow: %+v", d.Shadow)
	}
}
