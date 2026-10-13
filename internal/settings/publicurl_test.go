package settings

import "testing"

func TestNormalizePublicURL(t *testing.T) {
	ok := map[string]string{
		"":                               "",
		"  https://Arrmada.Example.com ": "https://arrmada.example.com",
		"https://arrmada.example.com/":   "https://arrmada.example.com",
		"http://192.168.1.10:8484":       "http://192.168.1.10:8484",
		"HTTPS://media.example.org:443":  "https://media.example.org:443",
	}
	for in, want := range ok {
		got, err := NormalizePublicURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizePublicURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"arrmada.example.com", "ftp://x.example.com", "javascript:alert(1)", "https://", "https://user:pw@x.example.com",
		"https://x.example.com/arrmada", "https://x.example.com/?a=1", "https://x.example.com/#top", "https://x.example.com?",
		"//x.example.com",
	} {
		if got, err := NormalizePublicURL(in); err == nil {
			t.Errorf("NormalizePublicURL(%q) = %q, want an error", in, got)
		}
	}
}
