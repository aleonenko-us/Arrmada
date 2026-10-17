package download

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Against a real qBittorrent (ARRMADA_QBIT_IT=<its URL>, reachable without a login — a
// throwaway container whitelisting the test network), with Automatic torrent management
// on by default: an add with a save path lands there and stays manual, and a later
// category change doesn't move it. The bare add shows why: without autoTMM it's
// auto-managed and saves to the category's folder instead. Synthetic, never-started
// torrents only.
func TestQBittorrentAutoTMMLive(t *testing.T) {
	base := os.Getenv("ARRMADA_QBIT_IT")
	if base == "" {
		t.Skip("set ARRMADA_QBIT_IT to a throwaway qBittorrent to run")
	}
	ctx := context.Background()
	dc := Client{ID: 1, URL: base}
	q := NewQBittorrent()
	post := func(path string, form url.Values) {
		t.Helper()
		if err := q.postForm(ctx, dc, path, form); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	prefs, _ := json.Marshal(map[string]any{"auto_tmm_enabled": true})
	post("/api/v2/app/setPreferences", url.Values{"json": {string(prefs)}})
	post("/api/v2/torrents/createCategory", url.Values{"category": {"it-cat"}, "savePath": {"/tmp/it-category"}})
	post("/api/v2/torrents/createCategory", url.Values{"category": {"it-other"}, "savePath": {"/tmp/it-other"}})

	info := func(hash string) (savePath string, auto bool) {
		t.Helper()
		resp, err := q.getAuthed(ctx, dc, "/api/v2/torrents/info?hashes="+hash)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var items []struct {
			SavePath string `json:"save_path"`
			AutoTMM  bool   `json:"auto_tmm"`
		}
		b, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(b, &items); err != nil || len(items) != 1 {
			t.Fatalf("info %s: %s %v", hash, b, err)
		}
		return strings.TrimRight(items[0].SavePath, "/"), items[0].AutoTMM
	}
	wait := func(hash string) (string, bool) {
		t.Helper()
		var sp string
		var auto bool
		for i := 0; i < 40; i++ {
			resp, err := q.getAuthed(ctx, dc, "/api/v2/torrents/info?hashes="+hash)
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if strings.Contains(string(b), hash) {
					sp, auto = info(hash)
					return sp, auto
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("torrent %s never appeared", hash)
		return sp, auto
	}

	// Through Arrmada's Add: lands in the save path, manual.
	file, hash := synthTorrent("it-own")
	if err := q.Add(ctx, dc, AddRequest{File: file, Filename: "own.torrent", Category: "it-cat", SavePath: "/tmp/it-own", Paused: true}); err != nil {
		t.Fatal(err)
	}
	sp, auto := wait(hash)
	t.Logf("Add with savepath: save_path=%s auto_tmm=%v", sp, auto)
	if sp != "/tmp/it-own" || auto {
		t.Errorf("save_path %s auto_tmm %v, want /tmp/it-own and manual", sp, auto)
	}
	// A later category change doesn't move it.
	if err := q.SetCategory(ctx, dc, hash, "it-other"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if sp, auto := info(hash); sp != "/tmp/it-own" || auto {
		t.Errorf("after setCategory: save_path %s auto_tmm %v", sp, auto)
	}

	// The same add without autoTMM (what Arrmada sent before): auto-managed, savepath
	// ignored. Recorded as the reason for the fix; not asserted, since a qBittorrent with
	// the default mode on Manual behaves.
	file2, hash2 := synthTorrent("it-bare")
	resp, err := q.doAuthed(ctx, dc, func() (*http.Request, error) {
		var b strings.Builder
		bnd := "itbnd"
		fmt.Fprintf(&b, "--%s\r\nContent-Disposition: form-data; name=\"torrents\"; filename=\"b.torrent\"\r\nContent-Type: application/x-bittorrent\r\n\r\n%s\r\n", bnd, file2)
		for k, v := range map[string]string{"category": "it-cat", "savepath": "/tmp/it-own", "paused": "true"} {
			fmt.Fprintf(&b, "--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n", bnd, k, v)
		}
		fmt.Fprintf(&b, "--%s--\r\n", bnd)
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v2/torrents/add", strings.NewReader(b.String()))
		if err == nil {
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+bnd)
		}
		return r, err
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	sp, auto = wait(hash2)
	t.Logf("Add without autoTMM (before the fix): save_path=%s auto_tmm=%v", sp, auto)

	// No save path at all: the default mode applies — proof it's on Automatic here — and
	// SetCategory turns it manual first, so the files stay put rather than following the
	// new category's folder.
	file3, hash3 := synthTorrent("it-auto")
	if err := q.Add(ctx, dc, AddRequest{File: file3, Filename: "auto.torrent", Category: "it-cat", Paused: true}); err != nil {
		t.Fatal(err)
	}
	sp, auto = wait(hash3)
	t.Logf("Add without a save path: save_path=%s auto_tmm=%v", sp, auto)
	if sp != "/tmp/it-category" || !auto {
		t.Fatalf("default mode isn't Automatic here (save_path %s auto_tmm %v): the check proves nothing", sp, auto)
	}
	if err := q.SetCategory(ctx, dc, hash3, "it-other"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	sp, auto = info(hash3)
	t.Logf("auto-managed torrent after SetCategory: save_path=%s auto_tmm=%v", sp, auto)
	if sp != "/tmp/it-category" || auto {
		t.Errorf("SetCategory moved an auto-managed torrent: save_path %s auto_tmm %v", sp, auto)
	}
}

// synthTorrent is a minimal single-file .torrent (one zero piece) and its info hash.
func synthTorrent(name string) ([]byte, string) {
	piece := sha1.Sum(make([]byte, 16384))
	info := fmt.Sprintf("d6:lengthi16384e4:name%d:%s12:piece lengthi16384e6:pieces20:%se", len(name), name, piece[:])
	h := sha1.Sum([]byte(info))
	return []byte("d4:info" + info + "e"), hex.EncodeToString(h[:])
}
