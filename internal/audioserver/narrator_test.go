package audioserver

import (
	"context"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// The narrator comes from the first tag that says: narrator, composer, album_artist
// (unless it's the artist or the book's author), performer.
func TestParseProbeNarratorTags(t *testing.T) {
	probe := func(tags string) []byte {
		return []byte(`{"format":{"duration":"60.0","bit_rate":"64000","tags":{` + tags + `}},
			"streams":[{"codec_name":"mp3"}],"chapters":[]}`)
	}
	for _, c := range []struct {
		name, tags, author, want string
	}{
		{"narrator tag", `"artist":"Matt Dinniman","narrator":"Jeff Hays","composer":"Someone Else"`, "Matt Dinniman", "Jeff Hays"},
		{"composer", `"artist":"Matt Dinniman","COMPOSER":"Jeff Hays"`, "Matt Dinniman", "Jeff Hays"},
		{"album_artist naming the reader", `"artist":"Matt Dinniman","album_artist":"Jeff Hays"`, "Matt Dinniman", "Jeff Hays"},
		{"album_artist equal to the artist", `"artist":"Matt Dinniman","album_artist":"matt dinniman"`, "Matt Dinniman", ""},
		{"album_artist equal to the book's author", `"artist":"Soundbooth Theater","album_artist":"Matt Dinniman"`, "matt dinniman", ""},
		{"performer last", `"artist":"Matt Dinniman","album_artist":"Matt Dinniman","performer":"Jeff Hays"`, "Matt Dinniman", "Jeff Hays"},
		{"nothing says", `"artist":"Matt Dinniman"`, "Matt Dinniman", ""},
	} {
		var af AudioFile
		if !parseProbe(probe(c.tags), &af, c.author) {
			t.Fatalf("%s: parse failed", c.name)
		}
		if af.Narrator != c.want {
			t.Errorf("%s: narrator %q, want %q", c.name, af.Narrator, c.want)
		}
	}
}

func (h *harness) rememberNarrated(narrator string) {
	h.t.Helper()
	for _, n := range []string{"Part 1.mp3", "Part 2.mp3", "Part 10.mp3"} {
		h.rememberAs(filepath.Join(h.audioDir(), n), 600, narrator, metaVersion)
	}
}

// A tagged narrator shows in the item, in list entries, on the narrators route and in
// the filter data, the way Audiobookshelf lists them.
func TestNarratorInItemJSONAndNarratorsRoute(t *testing.T) {
	h := newHarness(t)
	h.rememberNarrated("Jeff Hays & Travis Baldree")
	h.signIn()
	key := itemKeyFor(h.book.ID, 0)

	meta := obj1(t, obj1(t, h.json("GET", "/api/items/"+key, nil)["media"])["metadata"])
	if names := list1(t, meta["narrators"]); len(names) != 2 || names[0] != "Jeff Hays" || names[1] != "Travis Baldree" {
		t.Fatalf("item narrators = %v", meta["narrators"])
	}
	if meta["narratorName"] != "Jeff Hays, Travis Baldree" {
		t.Fatalf("item narratorName = %v", meta["narratorName"])
	}
	items := h.json("GET", "/api/libraries/"+libraryID+"/items?limit=10&page=0", nil)
	listMeta := obj1(t, obj1(t, obj1(t, list1(t, items["results"])[0])["media"])["metadata"])
	if listMeta["narratorName"] != "Jeff Hays, Travis Baldree" {
		t.Fatalf("list narratorName = %v", listMeta["narratorName"])
	}

	nar := list1(t, h.json("GET", "/api/libraries/"+libraryID+"/narrators", nil)["narrators"])
	if len(nar) != 2 {
		t.Fatalf("narrators route = %v", nar)
	}
	first := obj1(t, nar[0])
	if first["name"] != "Jeff Hays" || first["numBooks"].(float64) != 1 || first["id"] != narratorID("Jeff Hays") {
		t.Fatalf("narrators[0] = %v", first)
	}
	fd := h.json("GET", "/api/libraries/"+libraryID+"/filterdata", nil)
	if names := list1(t, fd["narrators"]); len(names) != 2 || names[1] != "Travis Baldree" {
		t.Fatalf("filterdata narrators = %v", fd["narrators"])
	}
	search := h.json("GET", "/api/libraries/"+libraryID+"/search?q=baldree", nil)
	if hits := list1(t, search["narrators"]); len(hits) != 1 || obj1(t, hits[0])["name"] != "Travis Baldree" {
		t.Fatalf("search narrators = %v", search["narrators"])
	}
	// The web player's book sheet says who reads it.
	d, err := h.srv.Detail(context.Background(), h.readerID(), key)
	if err != nil || d.Narrator != "Jeff Hays & Travis Baldree" {
		t.Fatalf("Detail narrator = %q, %v", d.Narrator, err)
	}
}

// A narrator typed on the Books page wins over the files' tags, and each audiobook
// version has its own.
func TestManualNarratorWins(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rememberNarrated("Wrong Tag")
	if err := h.srv.books.SetNarrator(ctx, h.book.ID, "  Jeff   Hays "); err != nil {
		t.Fatal(err)
	}
	// A full-cast version in its own folder, read by a cast its tags don't name.
	v, err := h.srv.books.AddAudioVersion(ctx, h.book.ID, "Full cast", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	vdir := h.audioDir() + " (Full cast)"
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		t.Fatal(err)
	}
	vfile := filepath.Join(vdir, "01.mp3")
	if err := os.WriteFile(vfile, []byte("full cast audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.books.SetAudioVersionFile(ctx, v.ID, vdir, "MP3", 15, 1); err != nil {
		t.Fatal(err)
	}
	h.rememberAs(vfile, 900, "Soundbooth Theater", metaVersion)
	h.srv.Invalidate()
	h.signIn()

	narratorName := func(key string) any {
		t.Helper()
		return obj1(t, obj1(t, h.json("GET", "/api/items/"+key, nil)["media"])["metadata"])["narratorName"]
	}
	if got := narratorName(itemKeyFor(h.book.ID, 0)); got != "Jeff Hays" {
		t.Fatalf("standard audiobook narrator %v, want the typed one", got)
	}
	vkey := itemKeyFor(h.book.ID, v.ID)
	if got := narratorName(vkey); got != "Soundbooth Theater" {
		t.Fatalf("version narrator %v, want its own tag", got)
	}
	if _, err := h.srv.books.SetAudioVersionNarrator(ctx, h.book.ID, v.ID, "GraphicAudio cast"); err != nil {
		t.Fatal(err)
	}
	if got := narratorName(vkey); got != "GraphicAudio cast" {
		t.Fatalf("version narrator %v after typing one", got)
	}
	// Clearing the typed one hands it back to the tags.
	if err := h.srv.books.SetNarrator(ctx, h.book.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := narratorName(itemKeyFor(h.book.ID, 0)); got != "Wrong Tag" {
		t.Fatalf("cleared narrator %v, want the tag again", got)
	}
}

// "narrators.<id>" keeps the books a narrator reads, in the id form the narrators route
// gives and base64'd plainly.
func TestNarratorFilter(t *testing.T) {
	h := newHarness(t)
	h.rememberNarrated("Jeff Hays")
	h.signIn()
	count := func(filter string) int {
		t.Helper()
		return len(list1(t, h.json("GET", "/api/libraries/"+libraryID+"/items?limit=10&page=0&filter="+url.QueryEscape(filter), nil)["results"]))
	}
	if n := count("narrators." + narratorID("Jeff Hays")); n != 1 {
		t.Fatalf("filter by the route's id: %d books", n)
	}
	if n := count("narrators." + base64.StdEncoding.EncodeToString([]byte("jeff hays"))); n != 1 {
		t.Fatalf("filter by plain base64: %d books", n)
	}
	if n := count("narrators." + narratorID("Someone Else")); n != 0 {
		t.Fatalf("another narrator's filter: %d books", n)
	}
}

// Files probed before narrators were read are read once more by the warm-up, and only
// once: a file ffprobe still can't read isn't tried every half hour.
func TestWarmRereadsStaleRowsOnce(t *testing.T) {
	h := newHarness(t)
	for _, n := range []string{"Part 1.mp3", "Part 2.mp3", "Part 10.mp3"} {
		h.rememberAs(filepath.Join(h.audioDir(), n), 600, "", 1)
	}
	ctx := context.Background()
	if k := h.srv.probe.knownUnder(ctx, h.audioDir()); k.Stale != 3 {
		t.Fatalf("stale = %d, want 3", k.Stale)
	}
	var d diskCounter
	d.install(h.srv.probe)
	if n := h.srv.Warm(ctx); n != 1 {
		t.Fatalf("warm read %d items, want the stale one", n)
	}
	if k := h.srv.probe.knownUnder(ctx, h.audioDir()); k.Stale != 0 || k.Files != 3 || k.Seconds != 1800 {
		t.Fatalf("after warm: %+v (lengths must survive a failed re-read)", k)
	}
	before := d.calls()
	h.srv.Warm(ctx)
	if d.calls() != before {
		t.Fatal("the re-read was tried again")
	}
}
