package books

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/langs"
	"github.com/tristenlammi/arrmada/internal/metadata"
)

// A Calibre library is "Author/Title (123)/Title - Author.epub" with a metadata.opf and a
// cover.jpg beside each book. The OPF says exactly which book it is — title, authors,
// series, ISBN — so a library scan pointed at a Calibre folder reads it instead of
// guessing from folder names. Scans only ever read these files.

// OPFName is the file Calibre writes beside each book.
const OPFName = "metadata.opf"

// CalibreCover is the cover Calibre writes beside each book.
const CalibreCover = "cover.jpg"

// maxOPFBytes bounds an OPF read: Calibre's are a few KB, a long description included.
const maxOPFBytes = 1 << 20

// OPF is what a metadata.opf says about its book. Empty fields weren't there.
type OPF struct {
	Title string
	// Authors in order, as displayed ("Pierce Brown"); creators with a role other than
	// author (an editor, an illustrator, Calibre itself) are left out.
	Authors        []string
	Series         string
	SeriesPosition float64
	// ISBN is normalised to its digits (10 or 13), and only kept when its checksum holds.
	ISBN string
	// Language is the first dc:language as a canonical code ("en"), "" if unknown.
	Language  string
	Publisher string
	// Published is the publication day, YYYY-MM-DD, and Year its year; zero when the OPF
	// had none or Calibre's "unknown" placeholder (year 101).
	Published string
	Year      int
}

// Author is the authors joined the way the library stores several ("A & B").
func (o OPF) Author() string { return strings.Join(o.Authors, " & ") }

// ReadOPF reads dir's metadata.opf. A folder without one answers os.ErrNotExist.
// Anything but a regular file (a folder, a symlink, a FIFO that would hang the open)
// counts as no OPF; it's checked before the file is opened.
func ReadOPF(dir string) (OPF, error) {
	p := filepath.Join(dir, OPFName)
	st, err := os.Lstat(p)
	if err != nil {
		return OPF{}, err
	}
	if !st.Mode().IsRegular() {
		return OPF{}, os.ErrNotExist
	}
	f, err := os.Open(p)
	if err != nil {
		return OPF{}, err
	}
	defer f.Close()
	return ParseOPF(f)
}

// errOPFTooLarge: no Calibre OPF is a megabyte.
var errOPFTooLarge = errors.New("metadata.opf is too large")

// ParseOPF reads an OPF package document, OPF 2 (Calibre's metadata.opf) or EPUB 3.
//
// The decoder is hardened: at most a megabyte is read, a DOCTYPE that declares entities
// is refused (encoding/xml never fetches external ones, but nothing here needs any), and
// only UTF-8 is accepted. Elements are matched by local name, so whatever prefix or
// namespace URI Calibre or an older tool used for dc: and opf: is fine.
func ParseOPF(r io.Reader) (OPF, error) {
	lr := &io.LimitedReader{R: r, N: maxOPFBytes + 1}
	d := xml.NewDecoder(lr)
	d.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return in, nil
		}
		return nil, fmt.Errorf("metadata.opf in %s isn't supported", charset)
	}

	var (
		o       OPF
		inMeta  bool
		creator []opfCreator
		// EPUB 3 refinements: <meta refines="#id" property="role">aut</meta>, and series
		// as belongs-to-collection with collection-type and group-position.
		refines     = map[string]map[string]string{}
		collections []opfCollection
	)
	for {
		if lr.N <= 0 {
			return OPF{}, errOPFTooLarge
		}
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if lr.N <= 0 {
				return OPF{}, errOPFTooLarge
			}
			return OPF{}, fmt.Errorf("read metadata.opf: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(t)), "ENTITY") {
				return OPF{}, errors.New("metadata.opf declares entities")
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if name == "metadata" {
				inMeta = true
				continue
			}
			if !inMeta {
				continue
			}
			switch name {
			case "title", "creator", "identifier", "language", "publisher", "date", "meta":
			default:
				continue
			}
			var text string
			if name != "meta" || attr(t, "property") != "" {
				// A meta with content="" carries no text; property metas do (EPUB 3).
				var s string
				if err := d.DecodeElement(&s, &t); err != nil {
					if lr.N <= 0 {
						return OPF{}, errOPFTooLarge
					}
					return OPF{}, fmt.Errorf("read metadata.opf: %w", err)
				}
				text = strings.TrimSpace(s)
			}
			switch name {
			case "title":
				if o.Title == "" {
					o.Title = text
				}
			case "creator":
				creator = append(creator, opfCreator{id: attr(t, "id"), name: text, role: attr(t, "role")})
			case "identifier":
				if isbn := opfISBN(attr(t, "scheme"), text); isbn != "" && o.ISBN == "" {
					o.ISBN = isbn
				}
			case "language":
				if o.Language == "" {
					o.Language = langs.Canonical(text)
				}
			case "publisher":
				if o.Publisher == "" {
					o.Publisher = text
				}
			case "date":
				// Calibre writes one date; an EPUB may have several, with the publication
				// one marked (opf:event="publication") or unmarked.
				if ev := attr(t, "event"); (ev == "" || strings.EqualFold(ev, "publication")) && o.Published == "" {
					o.Published, o.Year = opfDate(text)
				}
			case "meta":
				if prop := attr(t, "property"); prop != "" {
					if ref := strings.TrimPrefix(attr(t, "refines"), "#"); ref != "" {
						if refines[ref] == nil {
							refines[ref] = map[string]string{}
						}
						refines[ref][strings.ToLower(prop)] = text
					} else if strings.EqualFold(prop, "belongs-to-collection") {
						collections = append(collections, opfCollection{id: attr(t, "id"), name: text})
					}
					continue
				}
				// Calibre's own: <meta name="calibre:series" content="…"/>.
				switch strings.ToLower(attr(t, "name")) {
				case "calibre:series":
					o.Series = strings.TrimSpace(attr(t, "content"))
				case "calibre:series_index":
					if v, err := strconv.ParseFloat(strings.TrimSpace(attr(t, "content")), 64); err == nil && v > 0 {
						o.SeriesPosition = v
					}
				}
			}
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "metadata") {
				inMeta = false
			}
		}
	}

	for _, c := range creator {
		role := c.role
		if role == "" && c.id != "" {
			role = refines[c.id]["role"]
		}
		if c.name == "" || (role != "" && !strings.EqualFold(role, "aut")) {
			continue
		}
		o.Authors = append(o.Authors, c.name)
	}
	if o.Series == "" {
		for _, c := range collections {
			ref := refines[c.id]
			if t := ref["collection-type"]; t != "" && !strings.EqualFold(t, "series") {
				continue
			}
			o.Series = c.name
			if v, err := strconv.ParseFloat(strings.TrimSpace(ref["group-position"]), 64); err == nil && v > 0 {
				o.SeriesPosition = v
			}
			break
		}
	}
	if o.Series == "" {
		o.SeriesPosition = 0
	}
	return o, nil
}

type opfCreator struct{ id, name, role string }

type opfCollection struct{ id, name string }

// attr is an attribute by local name ("opf:role" and "role" alike).
func attr(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if strings.EqualFold(a.Name.Local, local) {
			return a.Value
		}
	}
	return ""
}

// opfISBN reads an ISBN identifier: opf:scheme="ISBN" with the number, or the
// "urn:isbn:" / "isbn:" forms EPUB 3 and newer Calibre write. Other identifiers (calibre,
// uuid, amazon, google) are not ISBNs even when they're digits.
func opfISBN(scheme, text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.EqualFold(scheme, "isbn"):
	case strings.HasPrefix(lower, "urn:isbn:"), strings.HasPrefix(lower, "isbn:"):
		lower = lower[strings.Index(lower, "isbn:")+len("isbn:"):]
	default:
		return ""
	}
	return metadata.NormalizeISBN(strings.TrimSpace(lower))
}

// opfDate reads "2014-01-07T05:00:00+00:00", "2014-01-07", "2014-01" or "2014". Calibre
// writes "0101-01-01T…" for a book with no date, which is no date at all.
func opfDate(s string) (day string, year int) {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return "", 0
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y < 1000 {
		return "", 0
	}
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		if _, err := strconv.Atoi(s[5:7]); err == nil {
			if _, err := strconv.Atoi(s[8:10]); err == nil {
				return s[:10], y
			}
		}
	}
	return "", y
}
