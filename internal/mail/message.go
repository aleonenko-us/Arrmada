package mail

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"
)

// writeMessage writes msg as a MIME message: a plain-text body, and when there is an
// attachment, multipart/mixed with the file base64-encoded as it streams from Open.
// Line endings are CRLF throughout (the SMTP DATA writer dot-stuffs).
func writeMessage(w io.Writer, from, to *netmail.Address, msg Message, now time.Time) error {
	bw := bufio.NewWriter(w)
	h := []string{
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("utf-8", oneLine(msg.Subject)),
		"Date: " + now.Format(time.RFC1123Z),
		"Message-ID: " + messageID(from.Address),
		"MIME-Version: 1.0",
	}
	for _, line := range h {
		if _, err := bw.WriteString(line + "\r\n"); err != nil {
			return err
		}
	}
	if msg.Attachment == nil {
		if _, err := bw.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n"); err != nil {
			return err
		}
		if err := writeText(bw, msg.Body); err != nil {
			return err
		}
		return bw.Flush()
	}

	mw := multipart.NewWriter(bw)
	if _, err := bw.WriteString("Content-Type: multipart/mixed; boundary=" + mw.Boundary() + "\r\n\r\n"); err != nil {
		return err
	}
	text, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	if err := writeText(text, msg.Body); err != nil {
		return err
	}
	a := msg.Attachment
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType(a)},
		"Content-Disposition":       {contentDisposition(a.Name)},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	rc, err := a.Open()
	if err != nil {
		return fmt.Errorf("open the attachment: %w", err)
	}
	enc := base64.NewEncoder(base64.StdEncoding, &lineWrapper{w: part})
	if _, err := io.Copy(enc, rc); err != nil {
		_ = rc.Close()
		return fmt.Errorf("read the attachment: %w", err)
	}
	_ = rc.Close()
	if err := enc.Close(); err != nil {
		return err
	}
	if _, err := io.WriteString(part, "\r\n"); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	return bw.Flush()
}

// writeText writes a body quoted-printable, with CRLF line ends.
func writeText(w io.Writer, body string) error {
	qp := quotedprintable.NewWriter(w)
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := io.WriteString(qp, body); err != nil {
		return err
	}
	if err := qp.Close(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

// contentType is the attachment's type with its name as an RFC 2047 encoded word, which
// older mail readers look for when they ignore Content-Disposition.
func contentType(a *Attachment) string {
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	if v := mime.FormatMediaType(ct, map[string]string{"name": mime.QEncoding.Encode("utf-8", safeName(a.Name))}); v != "" {
		return v
	}
	return "application/octet-stream"
}

// contentDisposition names the file per RFC 2231 (filename* in UTF-8) when it isn't plain
// ASCII; a name Go still can't express falls back to "file" plus its extension.
func contentDisposition(name string) string {
	name = safeName(name)
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return `attachment; filename="file` + filepath.Ext(name) + `"`
}

// safeName drops anything that could break a header out of a file name.
func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if strings.TrimSpace(name) == "" {
		return "file"
	}
	return name
}

// oneLine keeps a header value on one line, whatever the caller passed.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// messageID is a fresh, unique Message-ID at the sender's domain; spam filters (and
// Amazon's document service) treat mail without one as suspect.
func messageID(fromAddr string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	domain := "arrmada.local"
	if at := strings.LastIndexByte(fromAddr, '@'); at >= 0 && at < len(fromAddr)-1 {
		domain = fromAddr[at+1:]
	}
	return "<" + hex.EncodeToString(b[:]) + "@" + domain + ">"
}

// lineWrapper breaks base64 output into 76-character lines (RFC 2045).
type lineWrapper struct {
	w   io.Writer
	col int
}

const maxLine = 76

func (l *lineWrapper) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		room := maxLine - l.col
		chunk := p
		if len(chunk) > room {
			chunk = chunk[:room]
		}
		k, err := l.w.Write(chunk)
		n += k
		l.col += k
		if err != nil {
			return n, err
		}
		p = p[len(chunk):]
		if l.col == maxLine {
			if _, err := io.WriteString(l.w, "\r\n"); err != nil {
				return n, err
			}
			l.col = 0
		}
	}
	return n, nil
}
