package settings

import (
	"reflect"
	"testing"
)

func TestTitleLanguages(t *testing.T) {
	if got := ParseTitleLanguages(""); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
	if got := ParseTitleLanguages("de, fre,German,xx-nonsense,pt-br"); !reflect.DeepEqual(got, []string{"de", "fr", "pt-br"}) {
		t.Errorf("parsed = %v", got)
	}
	// TMDB files translations by the base language: Brazilian Portuguese reads "pt".
	if got := TitleLanguageBases("de,pt-br,pt"); !reflect.DeepEqual(got, []string{"de", "pt"}) {
		t.Errorf("bases = %v", got)
	}
}
