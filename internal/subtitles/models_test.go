package subtitles

import (
	"strings"
	"testing"
)

// The model cards on Settings → Subtitles show these labels. large-v3-turbo transcribes
// speech in any language — it only can't translate into English, which takes large-v3 —
// so calling it the English model sent people to download 3 GB they didn't need.
func TestModelLabelsSayWhatEachCanDo(t *testing.T) {
	labels := map[string]string{}
	for _, a := range whisperAssets {
		labels[a.name] = a.label
	}
	turbo, large := labels[modelTurbo], labels[modelLarge]
	if turbo == "" || large == "" {
		t.Fatalf("labels = %v, want both models offered", labels)
	}
	if strings.Contains(turbo, "English") {
		t.Errorf("turbo label %q reads as English-only", turbo)
	}
	if !strings.Contains(turbo, "any language") || !strings.Contains(turbo, "can't translate") {
		t.Errorf("turbo label %q should say it transcribes any language and can't translate", turbo)
	}
	if !strings.Contains(large, "translate") || !strings.Contains(large, "English") {
		t.Errorf("large-v3 label %q should say it's the one that translates to English", large)
	}
}
