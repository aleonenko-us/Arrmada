// Package nntptest is a news server for the usenet integration tests: it serves the
// articles it was given, yEnc-encoded, and answers 430 (no such article) for everything
// else — a provider that has some posts and not others, with nothing leaving the machine.
// NZB builds an NZB of single-segment files pointing at them.
//
// Test code only: nothing outside a _test.go file imports it.
package nntptest

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Server is a running fake news server.
type Server struct {
	ln       net.Listener
	mu       sync.Mutex
	articles map[string]article // by message id, without <>
}

type article struct {
	name string
	data []byte
	// A part of a multi-part post (AddFile): its number of total, where it starts in the
	// whole file (0-based) and the whole file's size. part 0 is a single-part post.
	part, total int
	begin, size int
}

// Segment is one article of a file as an NZB lists it.
type Segment struct {
	ID    string // message id, without <>
	Bytes int
}

// File is one file of an NZB.
type File struct {
	Name     string
	Segments []Segment
}

// AddFile posts data as a multi-part yEnc file of partSize-byte articles, ids prefix.N, and
// returns it as an NZB lists it. Large files go this way: one article per file is a fake
// no real poster makes, and SABnzbd assembles parts exactly as it does from a provider.
func (s *Server) AddFile(prefix, name string, data []byte, partSize int) File {
	if partSize <= 0 {
		partSize = 750_000
	}
	total := (len(data) + partSize - 1) / partSize
	f := File{Name: name}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < total; i++ {
		begin := i * partSize
		end := min(begin+partSize, len(data))
		id := fmt.Sprintf("%s.%d", prefix, i+1)
		s.articles[id] = article{name: name, data: data[begin:end], part: i + 1, total: total, begin: begin, size: len(data)}
		f.Segments = append(f.Segments, Segment{ID: id, Bytes: end - begin + (end-begin)/50})
	}
	return f
}

// Start listens on addr ("0.0.0.0:0" for a container to reach it) until the test ends.
func Start(t testing.TB, addr string) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("fake NNTP: %v", err)
	}
	s := &Server{ln: ln, articles: map[string]article{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

// Port is the port it listens on.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Add makes an article available: msgID (without <>) carries the file name with data.
func (s *Server) Add(msgID, name string, data []byte) {
	s.mu.Lock()
	s.articles[msgID] = article{name: name, data: data}
	s.mu.Unlock()
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	say := func(line string) {
		w.WriteString(line + "\r\n")
		w.Flush()
	}
	say("200 arrmada test news server ready")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "QUIT":
			say("205 bye")
			return
		case "CAPABILITIES":
			say("101 capabilities\r\nVERSION 2\r\nREADER\r\n.")
		case "MODE":
			say("200 reader")
		case "AUTHINFO":
			say("281 ok")
		case "DATE":
			say("111 20261014000000")
		case "GROUP":
			say("211 0 0 0 " + strings.Join(fields[1:], " "))
		case "BODY", "ARTICLE", "HEAD", "STAT":
			id := ""
			if len(fields) > 1 {
				id = strings.Trim(fields[1], "<>")
			}
			s.mu.Lock()
			a, ok := s.articles[id]
			s.mu.Unlock()
			if !ok {
				say("430 No Such Article")
				continue
			}
			cmd := strings.ToUpper(fields[0])
			switch cmd {
			case "STAT":
				say("223 0 <" + id + ">")
			case "HEAD":
				say("221 0 <" + id + ">\r\nSubject: " + a.name + "\r\n.")
			default:
				code := "222"
				head := ""
				if cmd == "ARTICLE" {
					code, head = "220", "Subject: "+a.name+"\r\n\r\n"
				}
				w.WriteString(code + " 0 <" + id + ">\r\n" + head)
				w.Write(dotStuff(yencArticle(a)))
				w.WriteString(".\r\n")
				w.Flush()
			}
		default:
			say("500 unknown command")
		}
	}
}

// yencArticle is an article's yEnc body with CRLF lines: single-part, or one part of a
// multi-part file (=ypart, with the part's own CRC).
func yencArticle(a article) []byte {
	if a.part == 0 {
		return yencEncode(a.name, a.data)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "=ybegin part=%d total=%d line=128 size=%d name=%s\r\n", a.part, a.total, a.size, a.name)
	fmt.Fprintf(&b, "=ypart begin=%d end=%d\r\n", a.begin+1, a.begin+len(a.data))
	yencLines(&b, a.data)
	fmt.Fprintf(&b, "=yend size=%d part=%d pcrc32=%08x\r\n", len(a.data), a.part, crc32.ChecksumIEEE(a.data))
	return b.Bytes()
}

// yencEncode is a single-part yEnc body with CRLF lines.
func yencEncode(name string, data []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "=ybegin line=128 size=%d name=%s\r\n", len(data), name)
	yencLines(&b, data)
	fmt.Fprintf(&b, "=yend size=%d crc32=%08x\r\n", len(data), crc32.ChecksumIEEE(data))
	return b.Bytes()
}

// yencLines writes data yEnc-encoded in lines of 128.
func yencLines(b *bytes.Buffer, data []byte) {
	col := 0
	for _, c := range data {
		o := byte(int(c) + 42)
		// The critical bytes, plus a dot at a line's start and every space or tab (so none
		// sits at a line's end, where a decoder may strip it). Escaping more is always allowed.
		if o == 0 || o == '\n' || o == '\r' || o == '=' || o == ' ' || o == '\t' || (col == 0 && o == '.') {
			b.WriteByte('=')
			o += 64
			col++
		}
		b.WriteByte(o)
		col++
		if col >= 128 {
			b.WriteString("\r\n")
			col = 0
		}
	}
	if col > 0 {
		b.WriteString("\r\n")
	}
}

// dotStuff doubles a leading "." on every line, as NNTP requires.
func dotStuff(body []byte) []byte {
	lines := bytes.SplitAfter(body, []byte("\r\n"))
	var out bytes.Buffer
	for _, l := range lines {
		if len(l) > 0 && l[0] == '.' {
			out.WriteByte('.')
		}
		out.Write(l)
	}
	return out.Bytes()
}

// NZB is an NZB of single-segment files: name → message id, with each file's segment
// size and an optional password. Files are listed in name order.
func NZB(password string, files map[string]string, sizes map[string]int) []byte {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">` + "\n")
	b.WriteString(`<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + "\n<head>\n")
	if password != "" {
		b.WriteString(`<meta type="password">`)
		_ = xml.EscapeText(&b, []byte(password))
		b.WriteString("</meta>\n")
	}
	b.WriteString("</head>\n")
	for _, name := range names {
		fmt.Fprintf(&b, `<file poster="tester@example.invalid" date="1700000000" subject="Arrmada test [1/1] - &quot;%s&quot; yEnc (1/1)">`+"\n", name)
		b.WriteString("<groups><group>alt.binaries.test</group></groups>\n")
		fmt.Fprintf(&b, "<segments><segment bytes=\"%d\" number=\"1\">%s</segment></segments>\n</file>\n", sizes[name], files[name])
	}
	b.WriteString("</nzb>\n")
	return []byte(b.String())
}

// NZBFiles is an NZB of the given files, each with its segments (AddFile, or segments of
// articles the server doesn't have, for a job that can't complete).
func NZBFiles(files ...File) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + "\n<head></head>\n")
	for _, f := range files {
		n := len(f.Segments)
		fmt.Fprintf(&b, `<file poster="tester@example.invalid" date="1700000000" subject="Arrmada test [1/1] - &quot;%s&quot; yEnc (1/%d)">`+"\n", f.Name, n)
		b.WriteString("<groups><group>alt.binaries.test</group></groups>\n<segments>\n")
		for i, s := range f.Segments {
			fmt.Fprintf(&b, "<segment bytes=\"%d\" number=\"%d\">%s</segment>\n", s.Bytes, i+1, s.ID)
		}
		b.WriteString("</segments>\n</file>\n")
	}
	b.WriteString("</nzb>\n")
	return []byte(b.String())
}
