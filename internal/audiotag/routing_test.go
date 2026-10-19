package audiotag

import "testing"

// The owner's decision (2026-10-19): the permissive libraries for everything they cover,
// TagLib only for Ogg Vorbis and Opus.
func TestDefaultRoutesOnlyOggToTagLib(t *testing.T) {
	if DefaultBackend != Auto {
		t.Fatalf("DefaultBackend = %s, want auto", DefaultBackend)
	}
	tg := New(DefaultBackend)
	for f, want := range map[format]Backend{
		formatFLAC: Permissive, formatMP3: Permissive, formatMP4: Permissive, formatOgg: TagLib,
	} {
		if got := tg.backendFor(f); got != want {
			t.Errorf("format %d goes to %s, want %s", f, got, want)
		}
	}
	if !tg.supports(formatOgg) || New(Permissive).supports(formatOgg) {
		t.Error("only a Tagger that can reach TagLib takes Ogg")
	}
}
