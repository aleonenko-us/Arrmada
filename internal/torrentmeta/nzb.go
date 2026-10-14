package torrentmeta

import (
	"bytes"
	"encoding/xml"
	"errors"
	"path"
	"strings"
)

// An uploaded .nzb is previewed like a .torrent: its name, its files and roughly how big
// they are. An NZB lists each file as articles, with each article's encoded size, so the
// sizes are the posted bytes — a few percent over the files themselves. Nothing in it is
// fetched or trusted beyond being shown.

// LooksLikeNZB reports whether data is an NZB rather than a .torrent (a bencoded
// dictionary, which starts with "d").
func LooksLikeNZB(data []byte) bool {
	t := bytes.TrimSpace(data)
	if len(t) == 0 || t[0] != '<' {
		return false
	}
	head := t
	if len(head) > 4096 {
		head = head[:4096]
	}
	return bytes.Contains(bytes.ToLower(head), []byte("<nzb"))
}

type nzbDoc struct {
	Head struct {
		Meta []struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"head"`
	Files []struct {
		Subject  string `xml:"subject,attr"`
		Segments []struct {
			Bytes int64 `xml:"bytes,attr"`
		} `xml:"segments>segment"`
	} `xml:"file"`
}

// ParseNZB reads an NZB's name (its <meta type="name">, else filename without .nzb), its
// files (named from each post's subject) and their sizes.
func ParseNZB(data []byte, filename string) (*Info, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	var doc nzbDoc
	if err := d.Decode(&doc); err != nil {
		return nil, errors.New("not a valid .nzb: it couldn't be read as XML")
	}
	if len(doc.Files) == 0 {
		return nil, errors.New("not a valid .nzb: it lists no files")
	}
	info := &Info{}
	for _, m := range doc.Head.Meta {
		if strings.EqualFold(m.Type, "name") && strings.TrimSpace(m.Value) != "" {
			info.Name = strings.TrimSpace(m.Value)
		}
	}
	if info.Name == "" {
		info.Name = strings.TrimSuffix(path.Base(strings.ReplaceAll(filename, `\`, "/")), ".nzb")
	}
	for _, f := range doc.Files {
		var size int64
		for _, s := range f.Segments {
			if s.Bytes > 0 {
				size += s.Bytes
			}
		}
		info.SizeBytes += size
		info.Files = append(info.Files, File{Path: subjectFileName(f.Subject), SizeBytes: size})
	}
	return info, nil
}

// subjectFileName is the file name a post's subject quotes ("… "film.part01.rar" yEnc
// (1/50)"), else the subject itself.
func subjectFileName(subject string) string {
	if i := strings.Index(subject, `"`); i >= 0 {
		if j := strings.Index(subject[i+1:], `"`); j > 0 {
			return subject[i+1 : i+1+j]
		}
	}
	return strings.TrimSpace(subject)
}
