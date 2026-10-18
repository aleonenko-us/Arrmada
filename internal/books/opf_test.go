package books

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// calibreOPF2 is metadata.opf as Calibre 7 writes it beside a book: OPF 2, dc: and opf:
// prefixes on the metadata element, the calibre and uuid identifiers before the ISBN, two
// authors with file-as, Calibre's own contributor line, and the series as calibre: metas.
const calibreOPF2 = `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
    <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
        <dc:identifier opf:scheme="calibre" id="calibre_id">123</dc:identifier>
        <dc:identifier opf:scheme="uuid" id="uuid_id">3a1c3e1b-7d0e-4b8e-9d55-5b8f0e0f2c11</dc:identifier>
        <dc:title>Good Omens</dc:title>
        <dc:creator opf:file-as="Pratchett, Terry &amp; Gaiman, Neil" opf:role="aut">Terry Pratchett</dc:creator>
        <dc:creator opf:file-as="Pratchett, Terry &amp; Gaiman, Neil" opf:role="aut">Neil Gaiman</dc:creator>
        <dc:contributor opf:file-as="calibre" opf:role="bkp">calibre (7.6.0) [https://calibre-ebook.com]</dc:contributor>
        <dc:date>2006-11-28T05:00:00+00:00</dc:date>
        <dc:description>&lt;p&gt;The world will end on a Saturday. Next Saturday, in fact.&lt;/p&gt;</dc:description>
        <dc:publisher>William Morrow</dc:publisher>
        <dc:identifier opf:scheme="GOOGLE">dKQ6DwAAQBAJ</dc:identifier>
        <dc:identifier opf:scheme="ISBN">9780060853983</dc:identifier>
        <dc:identifier opf:scheme="AMAZON">0060853980</dc:identifier>
        <dc:language>eng</dc:language>
        <dc:subject>Fantasy</dc:subject>
        <meta name="calibre:author_link_map" content="{&quot;Terry Pratchett&quot;: &quot;&quot;}"/>
        <meta name="calibre:series" content="Discworld Adjacent"/>
        <meta name="calibre:series_index" content="1.5"/>
        <meta name="calibre:timestamp" content="2023-04-02T10:11:12.000000+00:00"/>
        <meta name="calibre:title_sort" content="Good Omens"/>
    </metadata>
    <guide>
        <reference type="cover" title="Cover" href="cover.jpg"/>
    </guide>
</package>
`

// calibreOPF3 is the EPUB 3 form (Calibre's OPF 3 output, and an EPUB's own package
// document): refined creators, belongs-to-collection, an "isbn:" identifier and no date.
const calibreOPF3 = `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uuid_id" prefix="calibre: https://calibre-ebook.com">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uuid_id">urn:uuid:0b9c1d2e-0000-4000-8000-000000000001</dc:identifier>
    <dc:identifier>calibre:77</dc:identifier>
    <dc:identifier>isbn:0-345-53981-8</dc:identifier>
    <dc:title id="t1">Golden Son</dc:title>
    <dc:creator id="create1">Pierce Brown</dc:creator>
    <meta refines="#create1" property="role" scheme="marc:relators">aut</meta>
    <meta refines="#create1" property="file-as">Brown, Pierce</meta>
    <dc:creator id="create2">Some Illustrator</dc:creator>
    <meta refines="#create2" property="role" scheme="marc:relators">ill</meta>
    <dc:language>en-US</dc:language>
    <dc:date>0101-01-01T00:00:00+00:00</dc:date>
    <meta property="belongs-to-collection" id="c01">Red Rising Saga</meta>
    <meta refines="#c01" property="collection-type">series</meta>
    <meta refines="#c01" property="group-position">2</meta>
    <meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>
  </metadata>
</package>
`

func TestParseCalibreOPF2(t *testing.T) {
	o, err := ParseOPF(strings.NewReader(calibreOPF2))
	if err != nil {
		t.Fatal(err)
	}
	want := OPF{Title: "Good Omens", Authors: []string{"Terry Pratchett", "Neil Gaiman"}, Series: "Discworld Adjacent",
		SeriesPosition: 1.5, ISBN: "9780060853983", Language: "en", Publisher: "William Morrow", Published: "2006-11-28", Year: 2006}
	if !reflect.DeepEqual(o, want) {
		t.Errorf("got  %+v\nwant %+v", o, want)
	}
	if o.Author() != "Terry Pratchett & Neil Gaiman" {
		t.Errorf("Author() = %q", o.Author())
	}
}

func TestParseCalibreOPF3(t *testing.T) {
	o, err := ParseOPF(strings.NewReader(calibreOPF3))
	if err != nil {
		t.Fatal(err)
	}
	// The ISBN-10 comes back as its digits; the illustrator isn't an author; Calibre's
	// "no date" year 101 is no date.
	if o.Title != "Golden Son" || !reflect.DeepEqual(o.Authors, []string{"Pierce Brown"}) {
		t.Errorf("title/authors = %q %v", o.Title, o.Authors)
	}
	if o.Series != "Red Rising Saga" || o.SeriesPosition != 2 {
		t.Errorf("series = %q #%v", o.Series, o.SeriesPosition)
	}
	if o.ISBN != "0345539818" {
		t.Errorf("ISBN = %q, want 0345539818", o.ISBN)
	}
	if o.Published != "" || o.Year != 0 {
		t.Errorf("date = %q / %d, want none for Calibre's placeholder", o.Published, o.Year)
	}
	if o.Language != "en" {
		t.Errorf("language = %q", o.Language)
	}
}

func TestParseOPFEdges(t *testing.T) {
	t.Run("an author with no role counts", func(t *testing.T) {
		o, err := ParseOPF(strings.NewReader(`<package><metadata><dc:title>X</dc:title><dc:creator>Ann Leckie</dc:creator><dc:date>1999</dc:date></metadata></package>`))
		if err != nil || !reflect.DeepEqual(o.Authors, []string{"Ann Leckie"}) || o.Year != 1999 || o.Published != "" {
			t.Errorf("%+v %v", o, err)
		}
	})
	t.Run("digits that aren't an ISBN scheme aren't an ISBN", func(t *testing.T) {
		o, _ := ParseOPF(strings.NewReader(`<package><metadata><dc:identifier opf:scheme="calibre">9780060853983</dc:identifier><dc:identifier opf:scheme="AMAZON">0060853980</dc:identifier></metadata></package>`))
		if o.ISBN != "" {
			t.Errorf("ISBN = %q, want none", o.ISBN)
		}
	})
	t.Run("a bad ISBN is dropped", func(t *testing.T) {
		o, _ := ParseOPF(strings.NewReader(`<package><metadata><dc:identifier opf:scheme="ISBN">9780060853984</dc:identifier></metadata></package>`))
		if o.ISBN != "" {
			t.Errorf("ISBN = %q, want a failed checksum dropped", o.ISBN)
		}
	})
	t.Run("a series index without a series is nothing", func(t *testing.T) {
		o, _ := ParseOPF(strings.NewReader(`<package><metadata><meta name="calibre:series_index" content="3"/></metadata></package>`))
		if o.SeriesPosition != 0 {
			t.Errorf("position = %v", o.SeriesPosition)
		}
	})
	t.Run("a title outside metadata is ignored", func(t *testing.T) {
		o, _ := ParseOPF(strings.NewReader(`<package><guide><title>Not it</title></guide><metadata><dc:title>It</dc:title></metadata></package>`))
		if o.Title != "It" {
			t.Errorf("title = %q", o.Title)
		}
	})
}

// The decoder refuses what a hostile OPF could try: entity declarations (billion laughs,
// external files), something huge, an unsupported charset, broken XML.
func TestParseOPFHardened(t *testing.T) {
	for name, doc := range map[string]string{
		"external entity": `<?xml version="1.0"?><!DOCTYPE package [<!ENTITY x SYSTEM "file:///etc/passwd">]><package><metadata><dc:title>&x;</dc:title></metadata></package>`,
		"entity bomb":     `<?xml version="1.0"?><!DOCTYPE package [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;">]><package><metadata><dc:title>&b;</dc:title></metadata></package>`,
		"undeclared":      `<package><metadata><dc:title>&nbsp;</dc:title></metadata></package>`,
		"charset":         `<?xml version="1.0" encoding="ISO-8859-1"?><package><metadata><dc:title>X</dc:title></metadata></package>`,
		"broken":          `<package><metadata><dc:title>X</metadata>`,
	} {
		if o, err := ParseOPF(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: parsed as %+v, want an error", name, o)
		}
	}
	huge := `<package><metadata><dc:description>` + strings.Repeat("x", maxOPFBytes) + `</dc:description><dc:title>X</dc:title></metadata></package>`
	if _, err := ParseOPF(strings.NewReader(huge)); !errors.Is(err, errOPFTooLarge) {
		t.Errorf("huge OPF: err = %v, want errOPFTooLarge", err)
	}
}

func TestReadOPF(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadOPF(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no OPF: err = %v, want os.ErrNotExist", err)
	}
	if err := os.WriteFile(filepath.Join(dir, OPFName), []byte(calibreOPF2), 0o444); err != nil {
		t.Fatal(err)
	}
	o, err := ReadOPF(dir)
	if err != nil || o.Title != "Good Omens" {
		t.Errorf("ReadOPF = %+v, %v", o, err)
	}
	// A folder named metadata.opf isn't one.
	d2 := t.TempDir()
	_ = os.Mkdir(filepath.Join(d2, OPFName), 0o755)
	if _, err := ReadOPF(d2); err == nil {
		t.Error("a folder named metadata.opf parsed")
	}
}
