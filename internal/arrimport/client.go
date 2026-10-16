// Package arrimport reads a Radarr or Sonarr's root folders, so someone moving to Arrmada
// with several of them (Movies and Kids movies, TV and Anime) can set up the same folders
// here. It only reads: nothing in Radarr or Sonarr changes, and the API key is used for
// this one call and never stored.
package arrimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// The two apps, by their name.
const (
	Radarr = "radarr"
	Sonarr = "sonarr"
)

// Root is one of the app's root folders and how many titles live in it.
type Root struct {
	Path      string `json:"path"`
	Titles    int    `json:"titles"`
	FreeBytes int64  `json:"free_bytes"`
}

// ErrBadKey is a Radarr/Sonarr that refused the API key.
var ErrBadKey = errors.New("the API key was refused — copy it from Settings → General in Radarr or Sonarr")

// Client reads one Radarr or Sonarr over its v3 API.
type Client struct {
	app, base, key string
	http           *http.Client
}

// New builds a client for app (Radarr or Sonarr) at baseURL (http://host:7878).
func New(app, baseURL, key string) (*Client, error) {
	app = strings.ToLower(strings.TrimSpace(app))
	if app != Radarr && app != Sonarr {
		return nil, fmt.Errorf("choose Radarr or Sonarr")
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("the address should start with http:// or https:// (for example http://192.168.1.10:7878)")
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("the API key is required")
	}
	return &Client{app: app, base: base, key: strings.TrimSpace(key), http: &http.Client{
		Timeout: 60 * time.Second,
		// Never follow a redirect: it would carry the X-Api-Key header to wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Media is which Arrmada library the app's folders belong to ("movie" or "series").
func (c *Client) Media() string {
	if c.app == Sonarr {
		return "series"
	}
	return "movie"
}

func (c *Client) get(ctx context.Context, p string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+p, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrBadKey
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("%s answered with a redirect, which Arrmada doesn't follow (it would send the API key along) — use the app's own address", c.base)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s answered 404 — check the address is the app's own (for example http://host:7878), with its URL base if it has one", c.base)
	case resp.StatusCode >= 300:
		return fmt.Errorf("%s answered HTTP %d", c.base, resp.StatusCode)
	}
	// A big library's title list is large but bounded; the decoder reads only the fields
	// below.
	return json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(out)
}

// Roots lists the app's root folders, each with how many titles it holds, in path order.
func (c *Client) Roots(ctx context.Context) ([]Root, error) {
	var folders []struct {
		Path      string `json:"path"`
		FreeSpace int64  `json:"freeSpace"`
	}
	if err := c.get(ctx, "/api/v3/rootfolder", &folders); err != nil {
		return nil, err
	}
	list := "/api/v3/movie"
	if c.app == Sonarr {
		list = "/api/v3/series"
	}
	var titles []struct {
		RootFolderPath string `json:"rootFolderPath"`
		Path           string `json:"path"`
	}
	if err := c.get(ctx, list, &titles); err != nil {
		return nil, err
	}
	byRoot := map[string]*Root{}
	var out []*Root
	for _, f := range folders {
		r := &Root{Path: cleanArr(f.Path), FreeBytes: f.FreeSpace}
		if r.Path == "" || byRoot[r.Path] != nil {
			continue
		}
		byRoot[r.Path] = r
		out = append(out, r)
	}
	for _, t := range titles {
		root := cleanArr(t.RootFolderPath)
		if root == "" {
			// Older Radarr leaves rootFolderPath empty: the title's own folder's parent is it.
			root = cleanArr(parentOf(t.Path))
		}
		if r, ok := byRoot[root]; ok {
			r.Titles++
		}
	}
	res := make([]Root, 0, len(out))
	for _, r := range out {
		res = append(res, *r)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Path < res[j].Path })
	return res, nil
}

// cleanArr tidies a path as Radarr/Sonarr report it (often with a trailing slash; a
// Windows install uses backslashes). Paths stay in the app's own spelling.
func cleanArr(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.Contains(p, `\`) && !strings.HasPrefix(p, "/") {
		return strings.TrimRight(p, `\`)
	}
	return path.Clean(p)
}

func parentOf(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), `/\`)
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return ""
}

// BaseName is the last segment of an app path, either spelling.
func BaseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
