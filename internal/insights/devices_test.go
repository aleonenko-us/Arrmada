package insights

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/plex"
)

// A Roku that had 10-bit HEVC transcoded but plays 8-bit HEVC, a Shield that plays
// everything, and a phone seen only remotely: the panel's ticks come from what each did.
func TestDeviceCodecSupport(t *testing.T) {
	s := newDataTestService(t)
	ctx := context.Background()
	now := time.Now()
	play := func(product, player, src, stream, location string, depth int, profile string, ago time.Duration) {
		t.Helper()
		if _, err := s.repo.insertSession(ctx, sessionRecord{
			SessionKey: "k", UserID: "1", RatingKey: "r", MediaType: "movie", Title: "t",
			Product: product, Platform: product, Player: player, Location: location, Decision: "direct_play",
			StartedAt: now.Add(-ago - time.Hour).Unix(), StoppedAt: now.Add(-ago).Unix(),
			VideoSrc: src, VideoStream: stream, VideoSrcBitDepth: depth, VideoSrcProfile: profile,
		}); err != nil {
			t.Fatal(err)
		}
	}
	play("Plex for Roku", "Bedroom Roku", "HEVC 4K", "H264 1080p", "lan", 10, "main 10", time.Hour)
	play("Plex for Roku", "Bedroom Roku", "HEVC 1080p", "", "lan", 8, "main", 2*time.Hour)
	play("Plex for Android (TV)", "Shield", "HEVC 4K", "", "lan", 10, "main 10", 3*time.Hour)
	play("Plex for Android (TV)", "Shield", "AV1 1080p", "", "lan", 0, "", 4*time.Hour)
	play("Plex for Android (TV)", "Shield", "H264 1080p", "", "lan", 0, "", 5*time.Hour)
	// Remote and fitted to the connection: not evidence either way.
	play("Plex for iOS", "Phone", "AV1 4K", "H264 720p", "wan", 0, "", 6*time.Hour)
	// Older history: HEVC without a recorded depth still says HEVC plays, 10-bit unseen.
	play("Plex Web", "Chrome", "HEVC 1080p", "H264 1080p", "lan", 0, "", 7*time.Hour)
	// Outside the window, and music: ignored.
	play("Plex for Roku", "Old Roku", "HEVC 4K", "H264 1080p", "lan", 10, "", 100*24*time.Hour)
	if _, err := s.repo.insertSession(ctx, sessionRecord{SessionKey: "m", UserID: "1", MediaType: "track", Product: "Plexamp", Player: "Car",
		StartedAt: now.Unix() - 60, StoppedAt: now.Unix(), VideoSrc: "—"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.DeviceCodecSupport(ctx, now.Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DeviceSupport{}
	for _, d := range got {
		by[d.Player] = d
	}
	if len(got) != 4 {
		t.Fatalf("devices = %+v", got)
	}
	if got[0].Player != "Bedroom Roku" {
		t.Errorf("most recently seen first: %q", got[0].Player)
	}
	check := func(player, format string, c CodecUse, want string) {
		t.Helper()
		if c.State != want {
			t.Errorf("%s %s = %+v, want %s", player, format, c, want)
		}
	}
	roku := by["Bedroom Roku"]
	check("Roku", "hevc", roku.HEVC, CodecPlays)
	check("Roku", "hevc10", roku.HEVC10, CodecTranscodes)
	check("Roku", "av1", roku.AV1, CodecUnseen)
	if roku.HEVC10.Transcoded != 1 || roku.Plays != 2 {
		t.Errorf("roku = %+v", roku)
	}
	shield := by["Shield"]
	check("Shield", "hevc", shield.HEVC, CodecPlays)
	check("Shield", "hevc10", shield.HEVC10, CodecPlays)
	check("Shield", "av1", shield.AV1, CodecPlays)
	check("Phone", "av1", by["Phone"].AV1, CodecUnseen)
	chrome := by["Chrome"]
	check("Chrome", "hevc", chrome.HEVC, CodecTranscodes)
	check("Chrome", "hevc10", chrome.HEVC10, CodecUnseen)
}

// The poller records the source's bit depth with the play.
func TestRecordKeepsSourceBitDepth(t *testing.T) {
	ls := &liveSession{sess: plex.Session{SessionKey: "1", SrcVideoCodec: "hevc", SrcResolution: "4k", SrcVideoBitDepth: 10, SrcVideoProfile: "main 10"},
		started: time.Now().Add(-time.Hour)}
	rec := ls.record(time.Now())
	if rec.VideoSrcBitDepth != 10 || rec.VideoSrcProfile != "main 10" || rec.VideoSrc != "HEVC 4K" {
		t.Errorf("record = %+v", rec)
	}
}
