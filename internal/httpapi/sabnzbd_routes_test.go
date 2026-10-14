package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/download"
)

// A usenet client's status carries its card's facts, and "Add missing categories" creates
// only what's missing — on that click, by a manager, and never for a torrent client.
func TestUsenetClientStatusAndCategories(t *testing.T) {
	const key = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	var mu sync.Mutex
	cats := []string{"*", "arrmada"}
	var sets []string
	sab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("apikey") != key && r.PostForm.Get("mode") != "version" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "API Key Incorrect")
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.PostForm.Get("mode") {
		case "version":
			_, _ = io.WriteString(w, `{"version":"5.1.3"}`)
		case "get_cats":
			b, _ := json.Marshal(map[string]any{"categories": cats})
			_, _ = w.Write(b)
		case "fullstatus":
			_, _ = io.WriteString(w, `{"status":{"version":"5.1.3","apikey":"`+key+`","completedir":"/downloads/complete","servers":[{"servername":"news","servererror":""}]}}`)
		case "get_config":
			if r.PostForm.Get("section") == "categories" {
				_, _ = io.WriteString(w, `{"config":{"categories":[]}}`)
			} else {
				_, _ = io.WriteString(w, `{"config":{"misc":{"language":"en"}}}`)
			}
		case "set_config":
			sets = append(sets, r.PostForm.Get("name"))
			cats = append(cats, r.PostForm.Get("name"))
			_, _ = io.WriteString(w, `{"config":{}}`)
		case "queue":
			_, _ = io.WriteString(w, `{"queue":{"slots":[]}}`)
		case "history":
			_, _ = io.WriteString(w, `{"history":{"slots":[]}}`)
		default:
			_, _ = io.WriteString(w, `{"status":true}`)
		}
	}))
	defer sab.Close()

	s, dl := newClientServer(t)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	ctx := context.Background()
	c, err := dl.Create(ctx, download.Client{Name: "SAB", Kind: download.KindSABnzbd, URL: sab.URL, Password: key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	qb, err := dl.Create(ctx, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: "http://qb.invalid", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	rec := s.do("GET", fmt.Sprintf("/api/v1/downloadclients/%d/status", c.ID), mgr)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), key) {
		t.Fatalf("status: HTTP %d %s", rec.Code, rec.Body)
	}
	var st struct {
		ListenPort int                  `json:"listen_port"`
		Usenet     *download.UsenetFacts `json:"usenet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.Usenet == nil {
		t.Fatalf("status body %s: %v", rec.Body, err)
	}
	if strings.Join(st.Usenet.Missing, ",") != "arrmada-tv,arrmada-books,arrmada-music" || st.Usenet.ServersWorking != 1 {
		t.Errorf("facts = %+v", st.Usenet)
	}
	mu.Lock()
	if len(sets) != 0 {
		t.Errorf("reading the status changed SABnzbd: %v", sets)
	}
	mu.Unlock()

	path := fmt.Sprintf("/api/v1/downloadclients/%d/categories", c.ID)
	if rec := s.do("POST", path, kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d", rec.Code)
	}
	rec = s.do("POST", path, mgr)
	if rec.Code != http.StatusOK {
		t.Fatalf("add categories: HTTP %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Created []string             `json:"created"`
		Usenet  download.UsenetFacts `json:"usenet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Created, ",") != "arrmada-tv,arrmada-books,arrmada-music" || len(out.Usenet.Missing) != 0 {
		t.Errorf("add categories = %s", rec.Body)
	}

	// A torrent client has neither; a missing client is 404.
	if rec := s.do("POST", fmt.Sprintf("/api/v1/downloadclients/%d/categories", qb.ID), mgr); rec.Code != http.StatusBadRequest {
		t.Errorf("torrent client categories: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("GET", fmt.Sprintf("/api/v1/downloadclients/%d/status", qb.ID), mgr); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "usenet") {
		t.Errorf("torrent client status: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("POST", "/api/v1/downloadclients/9999/categories", mgr); rec.Code != http.StatusNotFound {
		t.Errorf("missing client: HTTP %d", rec.Code)
	}
}
