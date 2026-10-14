package download

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"net"
	"strings"
	"sync"
	"testing"
)

// A news server for the SABnzbd integration test: it serves the articles it was given,
// yEnc-encoded, and answers 430 (no such article) for everything else — a provider that
// has some posts and not others, with nothing leaving the machine.

type fakeNNTP struct {
	ln       net.Listener
	mu       sync.Mutex
	articles map[string]fakeArticle // by message id, without <>
}

type fakeArticle struct {
	name string
	data []byte
}

func startFakeNNTP(t *testing.T, addr string) *fakeNNTP {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("fake NNTP: %v", err)
	}
	f := &fakeNNTP{ln: ln, articles: map[string]fakeArticle{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeNNTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeNNTP) add(msgID, name string, data []byte) {
	f.mu.Lock()
	f.articles[msgID] = fakeArticle{name: name, data: data}
	f.mu.Unlock()
}

func (f *fakeNNTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	say := func(s string) {
		w.WriteString(s + "\r\n")
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
			f.mu.Lock()
			a, ok := f.articles[id]
			f.mu.Unlock()
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
				w.Write(dotStuff(yencEncode(a.name, a.data)))
				w.WriteString(".\r\n")
				w.Flush()
			}
		default:
			say("500 unknown command")
		}
	}
}

// yencEncode is a single-part yEnc body with CRLF lines.
func yencEncode(name string, data []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "=ybegin line=128 size=%d name=%s\r\n", len(data), name)
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
	fmt.Fprintf(&b, "=yend size=%d crc32=%08x\r\n", len(data), crc32.ChecksumIEEE(data))
	return b.Bytes()
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

// testNZB is an NZB of single-segment files: name → message id, with an optional password.
func testNZB(password string, files map[string]string, sizes map[string]int) []byte {
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
	for name, id := range files {
		fmt.Fprintf(&b, `<file poster="tester@example.invalid" date="1700000000" subject="Arrmada test [1/1] - &quot;%s&quot; yEnc (1/1)">`+"\n", name)
		b.WriteString("<groups><group>alt.binaries.test</group></groups>\n")
		fmt.Fprintf(&b, "<segments><segment bytes=\"%d\" number=\"1\">%s</segment></segments>\n</file>\n", sizes[name], id)
	}
	b.WriteString("</nzb>\n")
	return []byte(b.String())
}
