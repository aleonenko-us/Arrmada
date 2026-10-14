package download

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Only one download's key may reach a client. "all", lists in either client's syntax and
// half-formed usenet keys are refused, so a remove can never act on the whole client.
func TestValidKey(t *testing.T) {
	v1 := strings.Repeat("ab12", 10)
	v2 := strings.Repeat("cd34", 16)
	for key, want := range map[string]bool{
		v1:                               true,
		v2:                               true,
		strings.ToUpper(v1):              true, // torrent hashes: either case, as ValidHash always took
		"sab-sabnzbd_nzo_p86tgx":         true,
		"sab-x":                          true,
		"sab-" + strings.Repeat("a", 80): true,

		"":                                     false,
		"all":                                  false,
		"ALL":                                  false,
		"sab-":                                 false, // the prefix alone
		"sab":                                  false,
		"sab-all,sab-b":                        false,
		"sab-a,b":                              false,
		"sab-a|b":                              false,
		"sab-a b":                              false,
		"sab-a-b":                              false,
		"sab-a/b":                              false,
		"sab-a%2Cb":                            false,
		"sab-" + strings.Repeat("a", 81):       false,
		"SAB-sabnzbd_nzo_p86tgx":               false, // usenet keys are stored lowercase
		"sab-SABnzbd_nzo_p86tgx":               false,
		"sabnzbd_nzo_p86tgx":                   false, // a raw job id isn't a key
		v1 + "|" + v1:                          false,
		v1 + "," + v1:                          false,
		v1[:39]:                                false,
		v1 + "0":                               false,
		strings.Repeat("g", 40):                false,
		" sab-abc":                             false,
		"sab-abc\n":                            false,
		"sab-ab\x00c":                          false,
		"sab-été":                              false,
		strings.Repeat("a", 39) + "%":          false,
		"sab-" + v1:                            true, // hex after the prefix is still a usenet key
		"magnet:?xt=urn:btih:" + v1:            false,
		"sab-sabnzbd_nzo_p86tgx,sab-sabnzbd_2": false,
	} {
		if got := ValidKey(key); got != want {
			t.Errorf("ValidKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// Whatever the input, a key ValidKey accepts names one download of one transport and
// carries none of the clients' list or wildcard syntax.
func FuzzValidKey(f *testing.F) {
	for _, s := range []string{"all", "sab-", "sab-abc", strings.Repeat("ab", 20), "sab-a,b", "a|b", "SAB-X"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if !ValidKey(key) {
			if KeyTransport(key) != "" {
				t.Fatalf("KeyTransport(%q) = %q for an invalid key", key, KeyTransport(key))
			}
			return
		}
		if strings.EqualFold(key, "all") || strings.ContainsAny(key, ",| \t\r\n/%") {
			t.Fatalf("ValidKey accepted %q", key)
		}
		switch KeyTransport(key) {
		case TransportTorrent:
			if len(key) != 40 && len(key) != 64 {
				t.Fatalf("torrent key %q has length %d", key, len(key))
			}
		case TransportUsenet:
			if !strings.HasPrefix(key, "sab-") || len(key) == len("sab-") || key != strings.ToLower(key) {
				t.Fatalf("usenet key %q", key)
			}
		default:
			t.Fatalf("valid key %q has no transport", key)
		}
	})
}

func TestKeyTransport(t *testing.T) {
	hash := strings.Repeat("0a", 20)
	for key, want := range map[string]Transport{
		hash:                     TransportTorrent,
		strings.ToUpper(hash):    TransportTorrent,
		"sab-sabnzbd_nzo_abc":    TransportUsenet,
		"all":                    "",
		"":                       "",
		"sab-":                   "",
		"sab-SABnzbd_nzo_abc":    "",
		"not-a-key-of-any-kind!": "",
	} {
		if got := KeyTransport(key); got != want {
			t.Errorf("KeyTransport(%q) = %q, want %q", key, got, want)
		}
	}
	if KindQbittorrent.Transport() != TransportTorrent || KindSABnzbd.Transport() != TransportUsenet || Kind("nzbget").Transport() != "" {
		t.Error("Kind.Transport maps the kinds wrongly")
	}
}

// A job id becomes a lowercase key and back; an id that can't make a key gives none.
func TestUsenetKey(t *testing.T) {
	if got := UsenetKey("SABnzbd_nzo_P86tgx"); got != "sab-sabnzbd_nzo_p86tgx" {
		t.Errorf("UsenetKey = %q", got)
	}
	for _, bad := range []string{"", "  ", "a,b", "all|x", "a b", strings.Repeat("x", 81)} {
		if got := UsenetKey(bad); got != "" {
			t.Errorf("UsenetKey(%q) = %q, want none", bad, got)
		}
	}
	if id, ok := UsenetID("sab-sabnzbd_nzo_abc"); !ok || id != "sabnzbd_nzo_abc" {
		t.Errorf("UsenetID = %q, %v", id, ok)
	}
	if _, ok := UsenetID(strings.Repeat("ab", 20)); ok {
		t.Error("a torrent hash read as a usenet id")
	}
}

// Remove refuses anything that isn't one download's key before it looks at any client,
// usenet syntax included.
func TestRemoveRefusesInvalidKey(t *testing.T) {
	s := &Service{} // no repo: reaching the client list would panic
	for _, k := range []string{"all", "sab-", "sab-a,sab-b", "SAB-abc", "sab-a|b"} {
		if err := s.Remove(context.Background(), k, true); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Remove(%q) err = %v, want ErrInvalidHash", k, err)
		}
	}
}

// A usenet job being verified, repaired or extracted reads as unpacking; the same words
// from a torrent client keep their old meaning.
func TestPhaseUnpacking(t *testing.T) {
	for _, raw := range []string{"QuickCheck", "Verifying", "Repairing", "Extracting", "Moving", "Running"} {
		if got := (Item{Transport: TransportUsenet, RawState: raw, State: "downloading"}).Phase(); got != PhaseUnpacking {
			t.Errorf("usenet %q: Phase = %q, want unpacking", raw, got)
		}
		if got := (Item{Transport: TransportTorrent, RawState: raw, State: "downloading"}).Phase(); got == PhaseUnpacking {
			t.Errorf("torrent %q read as unpacking", raw)
		}
	}
	if got := (Item{State: PhaseUnpacking}).Phase(); got != PhaseUnpacking {
		t.Errorf("State unpacking: Phase = %q", got)
	}
	if got := (Item{Transport: TransportTorrent, RawState: "moving", State: "downloading"}).Phase(); got != "moving" {
		t.Errorf("qBittorrent moving: Phase = %q", got)
	}
}
