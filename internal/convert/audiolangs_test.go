package convert

import (
	"context"
	"reflect"
	"testing"

	langtab "github.com/tristenlammi/arrmada/internal/langs"
)

// A file's audio languages: tags in any spelling fold to base codes, commentary is left
// out, and an untagged track takes what the AI heard on it or stays unidentified.
func TestAudioLangsAndBadges(t *testing.T) {
	mi := &MediaInfo{VideoCodec: "h264", Audio: []AudioStream{
		{AudIndex: 0, Lang: "ger"}, {AudIndex: 1, Lang: "eng"}, {AudIndex: 2, Lang: "eng", Commentary: true},
		{AudIndex: 3, Lang: "und"}, {AudIndex: 4, Lang: "deu"}, {AudIndex: 5, Lang: "fre", Commentary: true},
	}}
	codes, untagged := audioLangs(mi, nil)
	if !reflect.DeepEqual(codes, []string{"de", "en"}) || !untagged {
		t.Errorf("untold: %v untagged=%v", codes, untagged)
	}
	codes, untagged = audioLangs(mi, map[int]string{3: "ja"})
	if !reflect.DeepEqual(codes, []string{"de", "en", "ja"}) || untagged {
		t.Errorf("heard: %v untagged=%v", codes, untagged)
	}
	f := FactsHeard(mi, map[int]string{3: "ja"})
	if !reflect.DeepEqual(f.AudioLangs, []string{"de", "en", "ja"}) || f.AudioUntagged {
		t.Errorf("facts: %+v", f)
	}
	badges := langtab.OrderAudio(AudioBadges(mi, map[int]string{3: "ja"}), "en")
	want := []string{"en", "de", "ja"}
	if len(badges) != 3 || !badges[2].Detected {
		t.Fatalf("badges %+v", badges)
	}
	for i, b := range badges {
		if b.Code != want[i] {
			t.Errorf("badge %d = %s, want %s", i, b.Code, want[i])
		}
	}
	if b := langtab.OrderAudio(AudioBadges(mi, nil), "en"); b[len(b)-1].Code != "" {
		t.Errorf("an unheard untagged track badges as unidentified: %+v", b)
	}
}

// AudioFor reads one title's files from the index alone, asks the AI's detections only for
// the files with an untagged track, and leaves out what isn't analysed by this version.
func TestAudioForReadsTheIndex(t *testing.T) {
	s := newTestService(t)
	ctx := t.Context()
	rows := []indexRow{
		{Path: "/tv/Show/S01E01.mkv", MediaType: "episode", SeriesID: 7, Season: 1, Episode: 1, SizeBytes: 100, Codec: "h264",
			Info: &MediaInfo{VideoCodec: "h264", Audio: []AudioStream{{AudIndex: 0, Lang: "jpn"}, {AudIndex: 1, Lang: "eng"}}}},
		{Path: "/tv/Show/S01E02.mkv", MediaType: "episode", SeriesID: 7, Season: 1, Episode: 2, SizeBytes: 200, Codec: "h264",
			Info: &MediaInfo{VideoCodec: "h264", Audio: []AudioStream{{AudIndex: 0}}}},
		{Path: "/tv/Other/S01E01.mkv", MediaType: "episode", SeriesID: 8, SizeBytes: 300, Codec: "h264",
			Info: &MediaInfo{VideoCodec: "h264", Audio: []AudioStream{{AudIndex: 0, Lang: "fre"}}}},
		{Path: "/tv/Show/S01E03.mkv", MediaType: "episode", SeriesID: 7, Season: 1, Episode: 3, SizeBytes: 400}, // probe failed
	}
	for _, r := range rows {
		if err := s.index.upsert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	var asked map[string]int64
	s.SetDetectedAudio(func(_ context.Context, paths map[string]int64) map[string]map[int]string {
		asked = paths
		return map[string]map[int]string{"/tv/Show/S01E02.mkv": {0: "ja"}}
	})
	got, err := s.AudioFor(ctx, "episode", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("files %v, want the two analysed episodes of show 7", got)
	}
	if !reflect.DeepEqual(asked, map[string]int64{"/tv/Show/S01E02.mkv": 200}) {
		t.Errorf("asked the AI about %v, want only the untagged file", asked)
	}
	e2 := got["/tv/Show/S01E02.mkv"]
	if len(e2.Badges) != 1 || e2.Badges[0].Code != "ja" || !e2.Badges[0].Detected || e2.SizeBytes != 200 {
		t.Errorf("heard episode: %+v", e2)
	}
	if !reflect.DeepEqual(e2.Facts.AudioLangs, []string{"ja"}) {
		t.Errorf("heard facts: %+v", e2.Facts)
	}
	if e1 := got["/tv/Show/S01E01.mkv"]; len(e1.Badges) != 2 || e1.Badges[0].Code != "ja" {
		t.Errorf("tagged episode: %+v", e1)
	}
	// A movie is read by its id.
	if err := s.index.upsert(ctx, indexRow{Path: "/m/a.mkv", MediaType: "movie", MovieID: 3, SizeBytes: 10, Codec: "hevc",
		Info: &MediaInfo{VideoCodec: "hevc", Audio: []AudioStream{{AudIndex: 0, Lang: "ger"}}}}); err != nil {
		t.Fatal(err)
	}
	m, err := s.AudioFor(ctx, "movie", 3)
	if err != nil || len(m) != 1 || m["/m/a.mkv"].Badges[0].Name != "German" {
		t.Errorf("movie: %+v %v", m, err)
	}

	// FactsForPath and FactsByPath read the AI's answer for untagged tracks too.
	f, ok := s.FactsForPath(ctx, "/tv/Show/S01E02.mkv", 200)
	if !ok || !reflect.DeepEqual(f.AudioLangs, []string{"ja"}) || f.AudioUntagged {
		t.Errorf("FactsForPath: %v %+v", ok, f)
	}
	ix, err := s.FactsByPath(ctx, "episode")
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := ix.Lookup("/tv/Show/S01E02.mkv", 200); !ok || !reflect.DeepEqual(f.AudioLangs, []string{"ja"}) {
		t.Errorf("FactsByPath: %v %+v", ok, f)
	}
}
