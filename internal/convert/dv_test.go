package convert

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// "Keep" converts a Dolby Vision file with its layer kept — always HEVC — and leaves alone
// any it can't keep it for, rather than converting it without.
func TestKeepDolbyVisionPlan(t *testing.T) {
	keep := prefs{dolbyVision: DVKeep, doviTool: true, allowAV1: true, imageSubs: ImageSubsKeep}
	plan, n := keep.planFor(dvP8(60000), "", "", nil)
	if !n.Video || !n.Worth || !plan.KeepDV || plan.VideoCodec != "hevc" {
		t.Fatalf("P8: %+v keep %v codec %q, want a worthwhile HEVC re-encode keeping DV (AV1 allowed or not)", n, plan.KeepDV, plan.VideoCodec)
	}
	p7 := dvP8(60000)
	p7.DVProfile = 7
	if plan, n := keep.planFor(p7, "", "", nil); !n.Video || !plan.KeepDV {
		t.Errorf("P7: %+v keep %v, want it converted to 8.1", n, plan.KeepDV)
	}
	unknown := dvP8(60000)
	unknown.DVProfile = 0 // no configuration record: the RPU says at conversion time
	if plan, _ := keep.planFor(unknown, "", "", nil); !plan.KeepDV {
		t.Error("an unknown profile is read from the RPU at conversion time, not refused up front")
	}

	for name, c := range map[string]struct {
		p   prefs
		mi  func() *MediaInfo
		why string
	}{
		"no dovi_tool": {prefs{dolbyVision: DVKeep, imageSubs: ImageSubsKeep}, func() *MediaInfo { return dvP8(60000) }, "needs dovi_tool"},
		"profile 8.4":  {keep, func() *MediaInfo { mi := dvP8(60000); mi.DVBase = "HLG"; return mi }, "8.4 (HLG)"},
		"profile 5":    {keep, func() *MediaInfo { mi := dvP8(60000); mi.DVProfile, mi.DVBase = 5, "SDR"; return mi }, "profile 5"},
		"profile 9":    {keep, func() *MediaInfo { mi := dvP8(60000); mi.DVProfile = 9; return mi }, "only profiles 7 and 8"},
	} {
		plan, n := c.p.planFor(c.mi(), "", "", nil)
		if n.Video || plan.KeepDV || plan.VideoCodec != "" || !strings.Contains(n.Why, c.why) {
			t.Errorf("%s: %+v (keep %v, codec %q), want left alone saying %q", name, n, plan.KeepDV, plan.VideoCodec, c.why)
		}
	}

	// Other files are untouched by the choice, and AV1 stays possible for them.
	if plan, _ := keep.planFor(film("h264", 1920, 1080, 30000, aud("ac3", "eng", 6)), "", "", nil); plan.KeepDV || plan.VideoCodec != "av1" {
		t.Errorf("a plain film: keep %v codec %q", plan.KeepDV, plan.VideoCodec)
	}
	if keep.cacheKey() == (prefs{dolbyVision: DVDrop, doviTool: true, allowAV1: true, imageSubs: ImageSubsKeep}).cacheKey() {
		t.Error("the cached views must be rebuilt when the choice changes")
	}
}

// The keep pipeline holds the stream twice in scratch, like HDR10+; the library disk only
// ever takes the finished file.
func TestKeepDolbyVisionScratchDoubles(t *testing.T) {
	mi := dvP8(60000)
	plan := Plan{VideoCodec: "hevc"}
	keep := plan
	keep.KeepDV = true
	if got, want := scratchNeeded(mi, keep, false), scratchNeeded(mi, plan, true); got != want || got <= scratchNeeded(mi, plan, false) {
		t.Fatalf("keep-DV needs %d, want twice the cap (%d)", got, want)
	}
	if candScratchFor(*mi, keep, mi.SizeBytes, false) != scratchNeeded(mi, keep, false) {
		t.Error("Up next's scratch figure must count the keep pipeline's two copies")
	}
}

// The plan says what keeping costs: nothing for profile 8, the enhancement layer for 7.
func TestKeepDolbyVisionWarnings(t *testing.T) {
	mi := dvP8(60000)
	drop := strings.Join(planWarnings(mi, Plan{VideoCodec: "hevc"}), "|")
	if !strings.Contains(drop, "Dolby Vision layer dropped") {
		t.Fatalf("drop: %q", drop)
	}
	if w := strings.Join(planWarnings(mi, Plan{VideoCodec: "hevc", KeepDV: true}), "|"); strings.Contains(w, "Dolby Vision") {
		t.Errorf("keep P8: %q, want no Dolby Vision warning", w)
	}
	mi.DVProfile = 7
	if w := strings.Join(planWarnings(mi, Plan{VideoCodec: "hevc", KeepDV: true}), "|"); !strings.Contains(w, "enhancement layer is dropped") {
		t.Errorf("keep P7: %q, want the enhancement-layer warning", w)
	}
	e := effectsOf(mi, Plan{VideoCodec: "hevc", KeepDV: true}, Needs{Video: true}, mi.SizeBytes, 0, 0)
	if e.dv {
		t.Error("the Switch-on summary must not count a kept layer as lost")
	}
}

// Keeping Dolby Vision is only ever the x265 raw-stream pipeline with dovi_tool.
func TestKeepDolbyVisionNeedsTheTool(t *testing.T) {
	s := newTestService(t)
	mi := dvP8(60000)
	cpu := cpuEncoder("hevc")
	plan := Plan{VideoCodec: "hevc", KeepDV: true}
	if s.canPreserveHDR(mi, plan, cpu, false) {
		t.Fatal("without dovi_tool the layer can't be kept")
	}
	s.doviTool, s.hdr10plusTool = "/usr/local/bin/dovi_tool", "/usr/local/bin/hdr10plus_tool"
	if !s.canPreserveHDR(mi, plan, cpu, false) {
		t.Fatal("with dovi_tool on the CPU it can")
	}
	if s.canPreserveHDR(mi, Plan{VideoCodec: "av1", KeepDV: true}, cpuEncoder("av1"), false) {
		t.Error("never AV1")
	}
	if s.canPreserveHDR(mi, plan, Encoder{Codec: "hevc", Kind: "vaapi", Hardware: true}, false) {
		t.Error("never a hardware encoder")
	}
	keep := DVKeep
	if got, err := s.UpdateSettings(context.Background(), SettingsPatch{DolbyVision: &keep}); err != nil || got.DolbyVision != DVKeep || !got.DoviTool {
		t.Fatalf("with dovi_tool, keep saves: %+v %v", got.DolbyVision, err)
	}
}

func TestParseRPUSummary(t *testing.T) {
	out := "Parsing RPU file...\n\nSummary:\n  Frames: 120\n  Profile: 8\n  DM version: 2 (CM v4.0)\n  Scene/shot count: 1\n"
	sum, err := parseRPUSummary(out)
	if err != nil || sum.frames != 120 || sum.profile != 8 || !strings.HasPrefix(sum.text, "Summary:\nFrames: 120\nProfile: 8") {
		t.Fatalf("%+v %v", sum, err)
	}
	if p7, _ := parseRPUSummary("Summary:\n  Frames: 3\n  Profile: 7 (FEL)\n"); p7.profile != 7 {
		t.Errorf("profile 7: %+v", p7)
	}
	if _, err := parseRPUSummary("error: no RPU found"); err == nil {
		t.Error("a summary without figures must be an error")
	}
}

// Frames compared one by one: first, last, three between — never past the end.
func TestDolbyVisionSampleFrames(t *testing.T) {
	if got := sampleFrames(480); len(got) != 5 || got[0] != 0 || got[4] != 479 || got[2] != 240 {
		t.Errorf("480: %v", got)
	}
	if got := sampleFrames(2); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("2: %v", got)
	}
	if got := sampleFrames(0); got != nil {
		t.Errorf("0: %v", got)
	}
}

// Two RPUs of one frame compare equal however their extension blocks are ordered, and not
// at all when a field differs; the CRC (which covers the exact bytes) is left out.
func TestNormaliseRPU(t *testing.T) {
	parse := func(s string) any {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		return normaliseRPU(v)
	}
	a := parse(`{"dovi_profile":8,"vdr_dm_data":{"cmv29_metadata":{"ext_metadata_blocks":[{"Level5":{"x":0}},{"Level6":{"y":1000}}]}},"rpu_data_crc32":1}`)
	b := parse(`{"dovi_profile":8,"vdr_dm_data":{"cmv29_metadata":{"ext_metadata_blocks":[{"Level6":{"y":1000}},{"Level5":{"x":0}}]}},"rpu_data_crc32":2}`)
	c := parse(`{"dovi_profile":8,"vdr_dm_data":{"cmv29_metadata":{"ext_metadata_blocks":[{"Level6":{"y":400}},{"Level5":{"x":0}}]}},"rpu_data_crc32":2}`)
	if !reflect.DeepEqual(a, b) {
		t.Error("reordered blocks and another CRC must compare equal")
	}
	if reflect.DeepEqual(a, c) {
		t.Error("a changed L6 must not compare equal")
	}
}

// One RPU per encoded frame, or nothing is injected.
func TestDolbyVisionAlignment(t *testing.T) {
	s := newTestService(t)
	frames := int64(480)
	old := streamFramesFn
	streamFramesFn = func(*Service, context.Context, string, string) (int64, error) { return frames, nil }
	t.Cleanup(func() { streamFramesFn = old })
	dv := &dvRun{frames: 480}
	if err := s.dvAligned(context.Background(), "enc.hevc", dv); err != nil {
		t.Fatalf("aligned: %v", err)
	}
	frames = 478
	err := s.dvAligned(context.Background(), "enc.hevc", dv)
	if !errors.Is(err, errDVMismatch) || !strings.Contains(err.Error(), "480 frames of Dolby Vision metadata, 478 encoded frames") {
		t.Fatalf("mismatch: %v", err)
	}
}

// The gate refuses a file whose container doesn't say profile 8.1 before reading anything.
func TestDolbyVisionGateContainer(t *testing.T) {
	s := newTestService(t)
	job := &Job{ID: 1, Title: "DV"}
	dv := &dvRun{frames: 480}
	for name, out := range map[string]*MediaInfo{
		"no DV":        {HDR: "HDR10"},
		"profile 7":    {HDR: "Dolby Vision", DVProfile: 7, DVCompat: 6},
		"8.4":          {HDR: "Dolby Vision", DVProfile: 8, DVCompat: 4},
		"no record":    {HDR: "Dolby Vision"},
		"nothing read": nil,
	} {
		kind, reason := s.checkDolbyVision(context.Background(), job, "/scratch/convert-1.mkv", out, dv)
		if kind != SkipHDRUnsupported || !strings.Contains(reason, "not profile 8.1") {
			t.Errorf("%s: %q %q", name, kind, reason)
		}
	}
	if kind, _ := s.checkDolbyVision(context.Background(), job, "x", &MediaInfo{HDR: "Dolby Vision", DVProfile: 8, DVCompat: 1}, nil); kind != SkipHDRUnsupported {
		t.Error("no RPU read from the original: fail closed")
	}
}
