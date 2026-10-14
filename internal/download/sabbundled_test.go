package download

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseSABAPIKey(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, ini, want string
	}{
		{"misc", "__version__ = 19\n[misc]\nlanguage = en\napi_key = " + key + "\nnzb_key = ffff\n", key},
		{"quoted", "[misc]\napi_key = \"" + key + "\"\n", key},
		{"crlf", "[misc]\r\napi_key = " + key + "\r\n", key},
		// A server or category subsection named like the key isn't misc's key.
		{"subsection first", "[servers]\n[[x]]\napi_key = aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n[misc]\napi_key = " + key + "\n", key},
		{"other section", "[logging]\napi_key = " + key + "\n", ""},
		{"misc subsection", "[misc]\n[[x]]\napi_key = " + key + "\n", ""},
		{"missing", "[misc]\nlanguage = en\n", ""},
		{"empty", "[misc]\napi_key = \"\"\n", ""},
		{"not a key", "[misc]\napi_key = abc def ghi jkl mno pqr\n", ""},
	} {
		got, err := parseSABAPIKey(strings.NewReader(tc.ini))
		if got != tc.want || (tc.want == "") != (err != nil) {
			t.Errorf("%s: %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if _, err := ReadSABAPIKey(filepath.Join(t.TempDir(), "sabnzbd.ini")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file = %v, want os.ErrNotExist", err)
	}
}

// The bundled SABnzbd is registered with the key from its config, follows a key changed in
// SABnzbd, leaves a row its owner pointed elsewhere alone, stays deleted once deleted, and
// comes back on restore — all without touching the bundled qBittorrent.
func TestEnsureBundledSAB(t *testing.T) {
	ctx := context.Background()
	svc, flags := newTestService(t)
	const u = "http://arrmada-sabnzbd:8080"
	key1, key2 := strings.Repeat("a", 32), strings.Repeat("b", 32)

	if _, err := svc.EnsureBundledSAB(ctx, u, ""); err == nil {
		t.Error("registered with no key")
	}
	added, err := svc.EnsureBundledSAB(ctx, u, key1)
	if err != nil || !added {
		t.Fatalf("first = %v, %v", added, err)
	}
	c, ok, err := svc.BundledClient(ctx, KindSABnzbd)
	if err != nil || !ok || c.Password != key1 || c.URL != u || !c.Enabled || c.Name != "SABnzbd (bundled)" {
		t.Fatalf("bundled row = %+v, %v, %v", c, ok, err)
	}
	if _, ok, _ := svc.BundledClient(ctx, KindQbittorrent); ok {
		t.Error("a bundled qBittorrent appeared")
	}
	if !svc.HasTransport(ctx, TransportUsenet) {
		t.Error("usenet not available after the bundled SABnzbd was added")
	}
	if added, err := svc.EnsureBundledSAB(ctx, u, key1); err != nil || added {
		t.Errorf("again = %v, %v", added, err)
	}
	if added, err := svc.EnsureBundledSAB(ctx, u, key2); err != nil || added {
		t.Errorf("new key = %v, %v", added, err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Password != key2 {
		t.Error("the new key wasn't taken up")
	}

	// Pointed somewhere else by its owner: the config's key isn't that SABnzbd's.
	c.URL = "http://my-sab:8080"
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnsureBundledSAB(ctx, u, key1); err != nil {
		t.Fatal(err)
	}
	if c, _ = svc.Get(ctx, c.ID); c.Password != key2 {
		t.Error("the key of a SABnzbd at another address was overwritten")
	}

	// Deleted: stays deleted; the qBittorrent flag is untouched.
	if err := svc.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if flags.Get(ctx, KeyBundledRemovedSABnzbd, "") != "1" || flags.Get(ctx, KeyBundledRemoved, "") != "" {
		t.Errorf("flags after delete = %v", flags.m)
	}
	if added, err := svc.EnsureBundledSAB(ctx, u, key1); err != nil || added {
		t.Errorf("after delete = %v, %v", added, err)
	}
	if list, _ := svc.List(ctx); len(list) != 0 {
		t.Errorf("re-added after delete: %+v", list)
	}

	if err := svc.RestoreBundledSAB(ctx, u, key1); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ = svc.BundledClient(ctx, KindSABnzbd); !ok || c.Password != key1 {
		t.Errorf("after restore = %+v, %v", c, ok)
	}
}

// A SABnzbd someone added by hand at the bundled address becomes the bundled one, with the
// config's key.
func TestEnsureBundledSABAdoptsRow(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	const u = "http://arrmada-sabnzbd:8080"
	key := strings.Repeat("c", 32)
	own := mustCreate(t, svc, Client{Name: "Mine", Kind: KindSABnzbd, URL: u, Password: "old-key-0123456789", Enabled: true})
	if added, err := svc.EnsureBundledSAB(ctx, u, key); err != nil || added {
		t.Fatalf("= %v, %v", added, err)
	}
	c, _ := svc.Get(ctx, own.ID)
	if !c.Bundled || c.Password != key || c.Name != "Mine" {
		t.Errorf("adopted row = %+v", c)
	}
}

// sabFolders is a fake SABnzbd's folder settings and folder browser over this machine's
// filesystem, so "SABnzbd sees the folder" is real.
type sabFolders struct {
	mu                   sync.Mutex
	complete, incomplete string
	blind                bool // the browser sees nothing (a folder not mounted into SABnzbd)
	sets                 int
}

func (s *sabFolders) wire(f *sabFake) {
	f.setAnswer("fullstatus", func(url.Values) (int, string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"status": map[string]any{"version": "5.1.3", "completedir": s.complete, "downloaddir": s.incomplete}})
		return 200, string(b)
	})
	f.setAnswer("browse", func(form url.Values) (int, string) {
		s.mu.Lock()
		blind := s.blind
		s.mu.Unlock()
		dir := form.Get("name")
		paths := []map[string]any{{"current_path": dir}}
		if ents, err := os.ReadDir(dir); err == nil && !blind && form.Get("show_files") == "1" {
			for _, e := range ents {
				paths = append(paths, map[string]any{"name": e.Name(), "path": filepath.ToSlash(filepath.Join(dir, e.Name())), "dir": e.IsDir()})
			}
		}
		b, _ := json.Marshal(map[string]any{"paths": paths})
		return 200, string(b)
	})
	f.setAnswer("set_config", func(form url.Values) (int, string) {
		if form.Get("section") == "categories" {
			f.mu.Lock()
			f.cats = append(f.cats, form.Get("name"))
			f.mu.Unlock()
			return 200, `{"config":{"categories":[]}}`
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sets++
		v := form.Get("value")
		if strings.Contains(v, "refuse") {
			return 200, `{"config":{}}` // SABnzbd keeps the old folder, without an error
		}
		switch form.Get("keyword") {
		case "complete_dir":
			s.complete = v
		case "download_dir":
			s.incomplete = v
		}
		return 200, `{"config":{}}`
	})
}

func TestSABnzbdUseDownloadsFolder(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "5.1.3")
	folders := &sabFolders{complete: "/config/Downloads/complete", incomplete: "/config/Downloads/incomplete"}
	folders.wire(f)
	sab := newSAB()
	root := filepath.ToSlash(t.TempDir())

	if got, err := sab.UseDownloadsFolder(ctx, dc, "relative/dir"); err != nil || got != "" {
		t.Errorf("relative folder = %q, %v", got, err)
	}

	// Not mounted into SABnzbd: nothing changes.
	folders.blind = true
	if _, err := sab.UseDownloadsFolder(ctx, dc, root); !errors.Is(err, ErrSABCantSee) {
		t.Errorf("blind = %v, want ErrSABCantSee", err)
	}
	if folders.sets != 0 || folders.complete != "/config/Downloads/complete" {
		t.Errorf("folders changed though SABnzbd can't see them: %+v", folders)
	}
	if ents, _ := os.ReadDir(filepath.Join(root, "usenet")); len(ents) != 0 {
		t.Errorf("the marker was left behind: %v", ents)
	}

	folders.blind = false
	got, err := sab.UseDownloadsFolder(ctx, dc, root)
	if err != nil || got != root+"/usenet" {
		t.Fatalf("= %q, %v", got, err)
	}
	if folders.complete != root+"/usenet/complete" || folders.incomplete != root+"/usenet/incomplete" {
		t.Errorf("folders = %+v", folders)
	}
	if ents, _ := os.ReadDir(filepath.Join(root, "usenet")); len(ents) != 0 {
		t.Errorf("the marker was left behind: %v", ents)
	}
	// Already there: nothing to do.
	sets := folders.sets
	if got, err := sab.UseDownloadsFolder(ctx, dc, root+"/"); err != nil || got != "" || folders.sets != sets {
		t.Errorf("again = %q, %v (%d sets)", got, err, folders.sets-sets)
	}

	// SABnzbd keeping its old folder is an error, not a success.
	refuse := filepath.ToSlash(filepath.Join(t.TempDir(), "refuse"))
	if _, err := sab.UseDownloadsFolder(ctx, dc, refuse); err == nil || errors.Is(err, ErrSABCantSee) {
		t.Errorf("refused = %v", err)
	}
}

// TuneBundledSAB only ever acts on a switched-on bundled SABnzbd: categories, then folders.
func TestTuneBundledSAB(t *testing.T) {
	ctx := context.Background()
	f, dc := newSABFake(t, "5.1.3")
	folders := &sabFolders{complete: "/config/Downloads/complete", incomplete: "/config/Downloads/incomplete"}
	folders.wire(f)
	svc, _ := newTestService(t)
	root := filepath.ToSlash(t.TempDir())

	if _, err := svc.TuneBundledSAB(ctx, root); !errors.Is(err, ErrBundledInactive) {
		t.Errorf("no bundled SABnzbd = %v", err)
	}
	// Someone's own SABnzbd is never tuned.
	mine := mustCreate(t, svc, Client{Name: "Mine", Kind: KindSABnzbd, URL: dc.URL, Password: sabTestKey, Enabled: true})
	if _, err := svc.TuneBundledSAB(ctx, root); !errors.Is(err, ErrBundledInactive) || folders.sets != 0 {
		t.Errorf("own SABnzbd = %v, %d sets", err, folders.sets)
	}
	if err := svc.Delete(ctx, mine.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.EnsureBundledSAB(ctx, dc.URL, sabTestKey); err != nil {
		t.Fatal(err)
	}
	f.setCats("*", "arrmada", "arrmada-tv")
	got, err := svc.TuneBundledSAB(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Categories, ",") != "arrmada-books,arrmada-music" || got.Folders != root+"/usenet" {
		t.Errorf("tuned = %+v", got)
	}

	c, _, _ := svc.BundledClient(ctx, KindSABnzbd)
	c.Enabled = false
	if _, err := svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TuneBundledSAB(ctx, root); !errors.Is(err, ErrBundledInactive) {
		t.Errorf("switched off = %v", err)
	}
}
