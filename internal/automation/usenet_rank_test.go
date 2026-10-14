package automation

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
)

// fakeNewznab is a Newznab indexer: releases with a size and no seeders, as usenet has none.
type fakeNewznab struct {
	mu       sync.Mutex
	releases []string
}

func (f *fakeNewznab) offer(titles ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = titles
}

func (f *fakeNewznab) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>`)
		for i, title := range f.releases {
			fmt.Fprintf(&b, `<item><title>%s</title><link>http://nzb.invalid/getnzb/%d.nzb?r=KEY</link><size>%d</size></item>`,
				html.EscapeString(title), i, int64(4<<30))
		}
		b.WriteString(`</channel></rss>`)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, b.String())
	})
}

// usenetRankHarness is the movie stall harness with a usenet client (SABnzbd at the given
// download-client priority), a Newznab indexer at the given indexer priority whose NZB
// fetch is stubbed, and the app's one settings service so a test can set the preference.
type usenetRankHarness struct {
	*stallHarness
	nzb *fakeNewznab
	sab *fakeUsenet
	set *settings.Service
}

func newUsenetRankHarness(t *testing.T, sabPriority, nzbIndexerPriority int) *usenetRankHarness {
	t.Helper()
	h := newStallHarness(t)
	set := settings.NewService(h.c.db)
	h.c.quality = quality.NewServiceWith(h.c.db, set)
	sab := &fakeUsenet{}
	h.c.downloads.SetDownloader(download.KindSABnzbd, sab)
	if _, err := h.c.downloads.Create(h.ctx, download.Client{Name: "sab", Kind: download.KindSABnzbd, URL: "http://sab.invalid", Enabled: true, Priority: sabPriority}); err != nil {
		t.Fatal(err)
	}
	h.c.indexers.SetUsenetAvailable(func() bool { return h.c.downloads.HasTransport(context.Background(), download.TransportUsenet) })
	nzb := &fakeNewznab{}
	srv := httptest.NewServer(nzb.handler())
	t.Cleanup(srv.Close)
	if _, err := h.c.indexers.Create(h.ctx, indexer.Indexer{Name: "NZB", Kind: indexer.KindNewznab, URL: srv.URL, Priority: nzbIndexerPriority, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.c.fetchRelease = func(_ context.Context, name, _ string) (indexer.FetchResult, error) {
		if name != "NZB" {
			return indexer.FetchResult{}, errors.New("unexpected fetch from " + name)
		}
		return indexer.FetchResult{File: []byte(testNZB), Filename: "release.nzb"}, nil
	}
	return &usenetRankHarness{stallHarness: h, nzb: nzb, sab: sab, set: set}
}

func (h *usenetRankHarness) prefer(t *testing.T, network string) {
	t.Helper()
	if err := h.set.Set(h.ctx, quality.KeyNetworkPreference, network); err != nil {
		t.Fatal(err)
	}
}

func rowOf(t *testing.T, list ReleaseList, title string) RankedRelease {
	t.Helper()
	for _, r := range list.Releases {
		if r.Title == title {
			return r
		}
	}
	t.Fatalf("no row %q in %+v", title, list.Releases)
	return RankedRelease{}
}

func recommendedOf(list ReleaseList) string {
	for _, r := range list.Releases {
		if r.Recommended {
			return r.Title
		}
	}
	return ""
}

// MOV-11's counterpart: with a usenet client, a better usenet release is listed eligible,
// recommended, and is what the search grabs — through the usenet client, as an NZB file.
func TestRankReleasesRecommendsUsenetWithAClient(t *testing.T) {
	h := newUsenetRankHarness(t, 1, 10)
	mid := h.addMovie(t, 1, "Arrival", 2016)
	const top = "Arrival.2016.2160p.BluRay.REMUX.x265-NZB" // outscores the torrent, usenet only
	h.nzb.offer(top)
	h.ix.offer(arrivalRelease)

	list, err := h.c.RankReleases(h.ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	if got := recommendedOf(list); got != top {
		t.Fatalf("recommended %q, want the usenet release; rows %+v", got, list.Releases)
	}
	row := rowOf(t, list, top)
	if !row.Eligible || row.RejectReason != "" || row.Transport != string(indexer.TransportUsenet) || row.Seeders != 0 {
		t.Errorf("usenet row = %+v", row)
	}
	if tor := rowOf(t, list, arrivalRelease); strings.Contains(tor.LostOn, "seeders") {
		t.Errorf("the torrent row cites seeders against a usenet release: %+v", tor)
	}

	if _, err := h.c.SearchMovie(h.ctx, mid); err != nil {
		t.Fatal(err)
	}
	g := grabRowOf(t, h.c, top)
	if g.transport != "usenet" || g.seed || download.KeyTransport(g.hash) != download.TransportUsenet {
		t.Fatalf("grab = %+v", g)
	}
	h.sab.mu.Lock()
	files := len(h.sab.files)
	h.sab.mu.Unlock()
	if files != 1 {
		t.Fatalf("NZBs sent %d, want 1", files)
	}
	for _, call := range h.qbit.callLog() {
		if strings.HasPrefix(call, "add ") {
			t.Fatalf("the torrent client was sent %q", call)
		}
	}
}

// Equal releases on both networks: the preference decides and says so; with "best" the
// tie-breaks do (indexer priority, then the download-client order) and nothing cites
// seeders between the two.
func TestInteractiveNetworkStepAndTieBreaks(t *testing.T) {
	const (
		tor = "Arrival.2016.1080p.WEB-DL.x264-TORGRP"
		nzb = "Arrival.2016.1080p.WEB-DL.x264-NZBGRP"
	)
	cases := []struct {
		name               string
		sabPrio, nzbIxPrio int
		pref               string
		winner, lostKey    string
		lostText           string
	}{
		{"prefer usenet", 50, 10, quality.NetworkUsenet, nzb, string(quality.KeyNetwork), "you prefer Usenet"},
		{"prefer torrent", 1, 1, quality.NetworkTorrent, tor, string(quality.KeyNetwork), "you prefer torrents"},
		{"best: indexer priority", 50, 5, quality.NetworkBest, nzb, string(quality.KeyTie), "an equal match"},
		{"best: usenet client first", 1, 10, quality.NetworkBest, nzb, string(quality.KeyTie), "an equal match"},
		{"best: torrent client first", 50, 10, quality.NetworkBest, tor, string(quality.KeyTie), "an equal match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newUsenetRankHarness(t, tc.sabPrio, tc.nzbIxPrio) // the torrent indexer is priority 10
			h.prefer(t, tc.pref)
			mid := h.addMovie(t, 1, "Arrival", 2016)
			h.nzb.offer(nzb)
			h.ix.offer(tor)
			list, err := h.c.RankReleases(h.ctx, mid)
			if err != nil {
				t.Fatal(err)
			}
			if got := recommendedOf(list); got != tc.winner {
				t.Fatalf("recommended %q, want %q; rows %+v", got, tc.winner, list.Releases)
			}
			loser := tor
			if tc.winner == tor {
				loser = nzb
			}
			row := rowOf(t, list, loser)
			if row.LostOnKey != tc.lostKey || row.LostOn != tc.lostText {
				t.Errorf("loser lost on (%q, %q), want (%q, %q)", row.LostOnKey, row.LostOn, tc.lostKey, tc.lostText)
			}
			if strings.Contains(list.ChosenOver, "seeders") {
				t.Errorf("ChosenOver cites seeders: %q", list.ChosenOver)
			}
		})
	}
}

// One copy per title: the same release on both networks is kept once, chosen by the dead
// torrent rule, the preference, indexer priority and client order — never by the usenet
// copy's 0 seeders. Without a usenet client it's the torrent, as before.
func TestBestByTitleOneCopyPerNetwork(t *testing.T) {
	tor := indexer.Release{Title: "X", Indexer: "Tor", Transport: indexer.TransportTorrent, Seeders: 40}
	dead := indexer.Release{Title: "X", Indexer: "Tor", Transport: indexer.TransportTorrent}
	nzb := indexer.Release{Title: "X", Indexer: "NZB", Transport: indexer.TransportUsenet}
	nzbLow := indexer.Release{Title: "X", Indexer: "NZB2", Transport: indexer.TransportUsenet}
	prio := map[string]int{"Tor": 10, "NZB": 10, "NZB2": 30}
	both := func(prefer string, first indexer.Transport) netPolicy {
		return netPolicy{usenet: true, torrent: true, prefer: prefer, priority: prio, first: first}
	}
	cases := []struct {
		name string
		p    netPolicy
		in   []indexer.Release
		want indexer.Release
	}{
		{"no usenet client", netPolicy{torrent: true}, []indexer.Release{nzb, tor}, tor},
		{"best, torrent client first", both(quality.NetworkBest, indexer.TransportTorrent), []indexer.Release{nzb, tor}, tor},
		{"best, usenet client first", both(quality.NetworkBest, indexer.TransportUsenet), []indexer.Release{tor, nzb}, nzb},
		{"best, usenet indexer higher", netPolicy{usenet: true, torrent: true, priority: map[string]int{"Tor": 20, "NZB": 5}, first: indexer.TransportTorrent}, []indexer.Release{tor, nzb}, nzb},
		{"prefer usenet", both(quality.NetworkUsenet, indexer.TransportTorrent), []indexer.Release{tor, nzb}, nzb},
		{"prefer torrent", both(quality.NetworkTorrent, indexer.TransportUsenet), []indexer.Release{nzb, tor}, tor},
		{"prefer torrent, but nobody seeds it", both(quality.NetworkTorrent, indexer.TransportTorrent), []indexer.Release{dead, nzb}, nzb},
		{"the usenet copy from the higher-priority indexer", both(quality.NetworkUsenet, ""), []indexer.Release{nzbLow, nzb}, nzb},
		{"best, no client order known: first listed", both(quality.NetworkBest, ""), []indexer.Release{nzb, tor}, nzb},
	}
	for _, tc := range cases {
		got := tc.p.bestByTitle(tc.in)
		if len(got) != 1 || got[0].Indexer != tc.want.Indexer || got[0].Transport != tc.want.Transport || got[0].Seeders != tc.want.Seeders {
			t.Errorf("%s: kept %+v, want %+v", tc.name, got, tc.want)
		}
	}
	// With a usenet client the list comes back in tie-break order.
	p := both(quality.NetworkBest, indexer.TransportTorrent)
	in := []indexer.Release{
		{Title: "A", Indexer: "NZB2", Transport: indexer.TransportUsenet},
		{Title: "B", Indexer: "NZB", Transport: indexer.TransportUsenet},
		{Title: "C", Indexer: "Tor", Transport: indexer.TransportTorrent, Seeders: 1},
	}
	if got := p.bestByTitle(in); got[0].Title != "C" || got[1].Title != "B" || got[2].Title != "A" {
		t.Errorf("order = %+v", got)
	}
}

// Book and album picks: seeders only between two torrents; across networks the profile's
// points, then a live release over a dead torrent, then the preference and the tie-breaks.
func TestBeatsAcrossNetworks(t *testing.T) {
	tor := func(seeders int) indexer.Release {
		return indexer.Release{Indexer: "Tor", Transport: indexer.TransportTorrent, Seeders: seeders}
	}
	nzb := indexer.Release{Indexer: "NZB", Transport: indexer.TransportUsenet}
	score := func(points int, r indexer.Release) int { return points*1_000_000 + r.Seeders }
	best := netPolicy{usenet: true, torrent: true, priority: map[string]int{"Tor": 10, "NZB": 10}, first: indexer.TransportUsenet}
	cases := []struct {
		name    string
		p       netPolicy
		a       indexer.Release
		ap      int
		b       indexer.Release
		bp      int
		aBeatsB bool
	}{
		{"torrents compare seeders", best, tor(50), 5, tor(3), 5, true},
		{"points beat seeders", best, nzb, 6, tor(900), 5, true},
		{"seeders don't beat usenet; client order does", best, nzb, 5, tor(900), 5, true},
		{"…and the other way round", best, tor(900), 5, nzb, 5, false},
		{"a dead torrent loses", netPolicy{usenet: true, torrent: true, prefer: quality.NetworkTorrent}, nzb, 5, tor(0), 5, true},
		{"prefer torrent", netPolicy{usenet: true, torrent: true, prefer: quality.NetworkTorrent}, tor(1), 5, nzb, 5, true},
		{"prefer usenet", netPolicy{usenet: true, torrent: true, prefer: quality.NetworkUsenet}, nzb, 5, tor(500), 5, true},
		{"prefer usenet never beats more points", netPolicy{usenet: true, torrent: true, prefer: quality.NetworkUsenet}, nzb, 5, tor(1), 6, false},
		{"indexer priority", netPolicy{usenet: true, torrent: true, priority: map[string]int{"Tor": 5, "NZB": 10}}, tor(2), 5, nzb, 5, true},
	}
	for _, tc := range cases {
		if got := tc.p.beats(tc.a, score(tc.ap, tc.a), tc.b, score(tc.bp, tc.b)); got != tc.aBeatsB {
			t.Errorf("%s: beats = %v", tc.name, got)
		}
	}
}

// What grabbable drops, and why: a usenet release is "not_torrent" only while no usenet
// client exists; with only a usenet client a torrent is "not_usenet"; with both, nothing.
func TestGrabbableFollowsTheClients(t *testing.T) {
	rels := []indexer.Release{
		{Title: "T", Transport: indexer.TransportTorrent},
		{Title: "U", Transport: indexer.TransportUsenet},
	}
	cases := []struct {
		name string
		p    netPolicy
		kept string
		code map[string]string
	}{
		{"no clients at all", netPolicy{}, "T", map[string]string{"U": DropNotTorrent}},
		{"torrent client only", netPolicy{torrent: true}, "T", map[string]string{"U": DropNotTorrent}},
		{"usenet client only", netPolicy{usenet: true}, "U", map[string]string{"T": DropNotUsenet}},
		{"both", netPolicy{usenet: true, torrent: true}, "TU", map[string]string{}},
	}
	for _, tc := range cases {
		ctx, notes := withSearchNotes(context.Background())
		kept := tc.p.usable(ctx, rels)
		var titles string
		for _, r := range kept {
			titles += r.Title
		}
		if titles != tc.kept {
			t.Errorf("%s: kept %q, want %q", tc.name, titles, tc.kept)
		}
		notes.mu.Lock()
		got := map[string]string{}
		for title, code := range notes.class {
			got[title] = code
		}
		notes.mu.Unlock()
		if fmt.Sprint(got) != fmt.Sprint(tc.code) {
			t.Errorf("%s: drop codes %v, want %v", tc.name, got, tc.code)
		}
	}
	// The interactive row says why it can't be grabbed.
	if rr := withIndexerFacts(RankedRelease{Eligible: true}, rels[0], netPolicy{usenet: true}); rr.Eligible || rr.RejectReason != torrentReject {
		t.Errorf("torrent row with only a usenet client = %+v", rr)
	}
	if rr := withIndexerFacts(RankedRelease{Eligible: true}, rels[1], netPolicy{usenet: true, torrent: true}); !rr.Eligible || rr.RejectReason != "" {
		t.Errorf("usenet row with a usenet client = %+v", rr)
	}
}
