package quality

// DefaultFormats is the built-in custom-format catalog. These are the "Prefer/
// Avoid" attributes the Simple UI toggles and the Advanced UI scores.
func DefaultFormats() []CustomFormat {
	return []CustomFormat{
		{Name: "Dolby Vision", Conditions: []Condition{{Type: CondDynamicRange, Value: "DV"}}},
		{Name: "HDR10+", Conditions: []Condition{{Type: CondDynamicRange, Value: "HDR10+"}}},
		{Name: "HDR10", Conditions: []Condition{{Type: CondDynamicRange, Value: "HDR10"}}},
		{Name: "HLG", Conditions: []Condition{{Type: CondDynamicRange, Value: "HLG"}}},
		{Name: "SDR", Conditions: []Condition{{Type: CondDynamicRange, Value: "SDR"}}},
		{Name: "Atmos", Conditions: []Condition{{Type: CondAudio, Value: "Atmos"}}},
		{Name: "Lossless", Conditions: []Condition{{Type: CondLossless}}},
		{Name: "TrueHD", Conditions: []Condition{{Type: CondAudio, Value: "TrueHD"}}},
		{Name: "DTS-HD", Conditions: []Condition{{Type: CondAudio, Value: "DTS-HD"}}},
		{Name: "HEVC", Conditions: []Condition{{Type: CondCodec, Value: "x265"}}},
		{Name: "AV1", Conditions: []Condition{{Type: CondCodec, Value: "AV1"}}},
		{Name: "H.264", Conditions: []Condition{{Type: CondCodec, Value: "x264"}}},
		// Streaming services, for preferring one WEB source over another in Advanced.
		{Name: "AMZN", Conditions: []Condition{{Type: CondStreaming, Value: "AMZN"}}},
		{Name: "NF", Conditions: []Condition{{Type: CondStreaming, Value: "NF"}}},
		{Name: "ATVP", Conditions: []Condition{{Type: CondStreaming, Value: "ATVP"}}},
		{Name: "DSNP", Conditions: []Condition{{Type: CondStreaming, Value: "DSNP"}}},
		{Name: "HMAX", Conditions: []Condition{{Type: CondStreaming, Value: "HMAX|MAX"}}},
		{Name: "MA", Conditions: []Condition{{Type: CondStreaming, Value: "MA"}}},
		{Name: "PCOK", Conditions: []Condition{{Type: CondStreaming, Value: "PCOK"}}},
	}
}

// builtinFormatNames is DefaultFormats by name. A profile's own custom formats can't take
// one of these names (ValidateFormats), so a name here is always Arrmada's format.
var builtinFormatNames = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range DefaultFormats() {
		m[f.Name] = true
	}
	return m
}()

// isBuiltinFormat reports whether a format is one of Arrmada's (the target's formats and
// the "Other formats" list) rather than one the owner wrote.
func isBuiltinFormat(name string) bool { return builtinFormatNames[name] }

// DefaultGroupTiers are the release-group tiers a profile starts with until it edits its
// own: a few groups widely trusted for remuxes and WEB releases, as suggestions. They go
// stale and are a matter of taste, so they are short, shown in the editor and editable,
// and they only ever order releases — never refuse one. None of them is a low-quality
// group (DefaultLowQualityGroups).
func DefaultGroupTiers() GroupTiers {
	return GroupTiers{
		Tier1: []string{"FraMeSToR", "EPSiLON", "BLURANiUM", "FLUX", "NTb", "CtrlHD"},
		Tier2: []string{"CMRG", "TEPES", "playWEB", "NOSiViD", "SiC"},
	}
}

// fallbackProfile is a permissive, unlisted profile used only when no profile of the
// title's media type exists at all (a deleted profile resolves to the default through
// Effective instead). It keeps acquisition from stalling — it accepts any resolution
// and mildly prefers the common premium formats — but it still refuses cams and other
// pre-release copies, which nobody wants in their library by accident. It is never
// shown in the UI.
func fallbackProfile() Profile {
	return Profile{
		Name:             "Any quality",
		SmallBias:        0.15,
		FormatScores:     map[string]int{"Dolby Vision": 30, "HDR10": 25, "Atmos": 20},
		RejectPreRelease: true,
	}
}
