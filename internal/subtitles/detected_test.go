package subtitles

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/tristenlammi/arrmada/internal/store"
)

// DetectedAudio reads what detection heard from the probe cache alone: only confident
// detections on untagged tracks, only for a file whose recorded size is the cached one,
// and the same answer whether the paths are few (one IN query) or many (one pass).
func TestDetectedAudioFromTheCache(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Service{cache: &probeCache{db: st.DB()}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	s.cache.put(ctx, "/lib/a.mkv", 100, 1, &mediaInfo{ProbeVersion: probeVersion,
		Audio:    []AudioTrack{{Index: 0, Lang: "eng"}, {Index: 1, Lang: "und"}, {Index: 2}, {Index: 3, Lang: ""}},
		Detected: map[int]DetectedLang{0: {Lang: "fr", Prob: 0.99}, 1: {Lang: "de", Prob: 0.9}, 2: {Lang: "ja", Prob: 0.3}, 3: {Lang: "ger", Prob: 0.8}}})
	s.cache.put(ctx, "/lib/b.mkv", 200, 1, &mediaInfo{ProbeVersion: probeVersion,
		Audio: []AudioTrack{{Index: 0}}, Detected: map[int]DetectedLang{0: {Lang: "es", Prob: 0.9}}})
	s.cache.put(ctx, "/lib/c.mkv", 300, 1, &mediaInfo{ProbeVersion: probeVersion, Audio: []AudioTrack{{Index: 0, Lang: "eng"}}})

	want := map[string]map[int]string{"/lib/a.mkv": {1: "de", 3: "de"}}
	paths := map[string]int64{"/lib/a.mkv": 100, "/lib/b.mkv": 999, "/lib/c.mkv": 300}
	if got := s.DetectedAudio(ctx, paths); !reflect.DeepEqual(got, want) {
		t.Errorf("few paths: %v, want %v (tagged tracks, unsure answers and a resized file left out)", got, want)
	}
	// Past the batch size the cache is read in one pass; the answer is the same.
	many := map[string]int64{"/lib/a.mkv": 100, "/lib/b.mkv": 999}
	for i := 0; i < detectedQueryBatch+5; i++ {
		many[fmt.Sprintf("/lib/x%d.mkv", i)] = 1
	}
	if got := s.DetectedAudio(ctx, many); !reflect.DeepEqual(got, want) {
		t.Errorf("many paths: %v, want %v", got, want)
	}
	// An unknown size (0) doesn't gate.
	if got := s.DetectedAudio(ctx, map[string]int64{"/lib/b.mkv": 0}); got["/lib/b.mkv"][0] != "es" {
		t.Errorf("unknown size: %v", got)
	}
	var none *Service
	if got := none.DetectedAudio(ctx, paths); len(got) != 0 {
		t.Errorf("nil service: %v", got)
	}
}
