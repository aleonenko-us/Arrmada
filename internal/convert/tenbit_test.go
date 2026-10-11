package convert

import (
	"context"
	"strings"
	"testing"
)

// With "match the source", an 8-bit SDR file is encoded 8-bit; HDR and 10-bit sources stay
// 10-bit whatever the setting, and "always" keeps every re-encode 10-bit.
func TestTenBitChoiceInCPUArgs(t *testing.T) {
	sdr8 := film("h264", 1920, 1080, 12000)
	sdr10 := film("hevc", 1920, 1080, 30000)
	sdr10.TenBit = true
	hdr := film("hevc", 3840, 2160, 60000)
	hdr.HDR, hdr.TenBit = "HDR10", true
	hdrNoFlag := film("hevc", 3840, 2160, 60000) // HDR whose pix_fmt didn't read as 10-bit
	hdrNoFlag.HDR = "HDR10"

	pix := func(mi *MediaInfo, codec string, match bool) string {
		return argValue(videoArgs(cpuEncoder(codec), mi, Plan{VideoCodec: codec, Quality: 20, MatchSourceDepth: match}, false, 4, false), "-pix_fmt")
	}
	for _, c := range []struct {
		name  string
		mi    *MediaInfo
		codec string
		match bool
		want  string
	}{
		{"8-bit SDR, match source, HEVC", sdr8, "hevc", true, "yuv420p"},
		{"8-bit SDR, match source, AV1", sdr8, "av1", true, "yuv420p"},
		{"8-bit SDR, always", sdr8, "hevc", false, "yuv420p10le"},
		{"10-bit SDR, match source", sdr10, "hevc", true, "yuv420p10le"},
		{"HDR10, match source", hdr, "hevc", true, "yuv420p10le"},
		{"HDR10 without the 10-bit flag, match source", hdrNoFlag, "hevc", true, "yuv420p10le"},
		{"HDR10, AV1, match source", hdr, "av1", true, "yuv420p10le"},
	} {
		if got := pix(c.mi, c.codec, c.match); got != c.want {
			t.Errorf("%s: -pix_fmt %s, want %s", c.name, got, c.want)
		}
	}
	// Only one -pix_fmt in the command.
	if a := joined(videoArgs(cpuEncoder("hevc"), sdr8, Plan{VideoCodec: "hevc", MatchSourceDepth: true}, false, 4, false)); strings.Count(a, "-pix_fmt") != 1 {
		t.Errorf("args = %s", a)
	}
	// The plan view's note says so only when an 8-bit source is written as 10-bit.
	if w := eightToTenWarning(sdr8, Plan{VideoCodec: "hevc"}); !strings.Contains(w, "8-bit → 10-bit") {
		t.Errorf("always: note = %q", w)
	}
	for _, c := range []struct {
		mi   *MediaInfo
		plan Plan
	}{{sdr8, Plan{VideoCodec: "hevc", MatchSourceDepth: true}}, {hdr, Plan{VideoCodec: "hevc"}}, {sdr10, Plan{VideoCodec: "hevc"}}, {sdr8, Plan{}}} {
		if w := eightToTenWarning(c.mi, c.plan); w != "" {
			t.Errorf("no 8→10 here: %q", w)
		}
	}
}

// A new install matches the source; one that has already used Convert keeps 10-bit; either
// way the default is saved so it never changes under the owner, and it's part of the cache
// key and the plan.
func TestTenBitDefaultAndSetting(t *testing.T) {
	ctx := context.Background()
	fresh := newTestService(t)
	if got := fresh.prefs(ctx).tenBit; got != TenBitMatchSource {
		t.Fatalf("new install default = %q", got)
	}
	set(t, fresh, map[string]string{keyAuto: "true"}) // using it later doesn't flip the saved default
	if got := fresh.prefs(ctx).tenBit; got != TenBitMatchSource {
		t.Errorf("default changed after first use: %q", got)
	}
	if !fresh.prefs(ctx).basePlan().MatchSourceDepth {
		t.Error("match source didn't reach the plan")
	}

	existing := newTestService(t)
	set(t, existing, map[string]string{keyAuto: "true", keySweepStart: "01:00"})
	p := existing.prefs(ctx)
	if p.tenBit != TenBitAlways || p.basePlan().MatchSourceDepth {
		t.Fatalf("existing install = %q", p.tenBit)
	}
	before := p.cacheKey()
	got, err := existing.UpdateSettings(ctx, SettingsPatch{TenBit: ptr(TenBitMatchSource)})
	if err != nil || got.TenBit != TenBitMatchSource {
		t.Fatalf("update = %+v, %v", got.TenBit, err)
	}
	if existing.prefs(ctx).cacheKey() == before {
		t.Error("the 10-bit choice isn't part of the cache key")
	}
	if _, err := existing.UpdateSettings(ctx, SettingsPatch{TenBit: ptr("12-bit")}); err == nil {
		t.Error("an unknown choice was accepted")
	}
}

func ptr[T any](v T) *T { return &v }
