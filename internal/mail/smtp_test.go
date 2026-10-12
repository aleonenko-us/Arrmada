package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/mail/mailtest"
)

func startFake(t *testing.T, o mailtest.Options) *mailtest.Server {
	t.Helper()
	s, err := mailtest.Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func cfgFor(s *mailtest.Server, security, user string) Config {
	return Config{Host: s.Host, Port: s.Port, Security: security, Username: user, From: "Arrmada <books@example.com>"}
}

// A message with an attachment arrives as multipart/mixed: the text part, then the file
// base64-encoded in 76-column lines, named per RFC 2231 so a non-ASCII title survives.
func TestSendAttachmentOverSTARTTLS(t *testing.T) {
	s := startFake(t, mailtest.Options{User: "owner", Pass: "app-password", StartTLS: true})
	payload := bytes.Repeat([]byte("Les Misérables, every chapter. "), 400) // ~12 KB: several base64 lines
	opened := false
	msg := Message{
		To: "reader_42@kindle.com", Subject: "Les Misérables", Body: "Sent from Arrmada.\nEnjoy.",
		Attachment: &Attachment{Name: "Les Misérables - Victor Hugo.epub", ContentType: "application/epub+zip", Size: int64(len(payload)),
			Open: func() (io.ReadCloser, error) { opened = true; return io.NopCloser(bytes.NewReader(payload)), nil }},
	}
	if err := send(context.Background(), cfgFor(s, SecurityStartTLS, "owner"), "app-password", msg, s.Roots); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !opened {
		t.Fatal("the attachment was never opened")
	}
	got := s.Messages()
	if len(got) != 1 {
		t.Fatalf("server got %d messages, want 1", len(got))
	}
	m := got[0]
	if !m.TLS || m.User != "owner" || m.From != "books@example.com" || len(m.To) != 1 || m.To[0] != "reader_42@kindle.com" {
		t.Fatalf("envelope = %+v", m)
	}

	parsed, err := netmail.ReadMessage(strings.NewReader(m.Data))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	if subj, _ := dec.DecodeHeader(parsed.Header.Get("Subject")); subj != "Les Misérables" {
		t.Errorf("subject = %q", subj)
	}
	if parsed.Header.Get("Message-ID") == "" || parsed.Header.Get("Date") == "" {
		t.Error("Message-ID and Date are required headers")
	}
	mt, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type = %q (%v)", parsed.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(parsed.Body, params["boundary"])
	text, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if ct := text.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("first part = %q, want the text body", ct)
	}
	body, _ := io.ReadAll(text) // multipart decodes quoted-printable itself
	if !strings.Contains(string(body), "Sent from Arrmada.") {
		t.Errorf("body = %q", body)
	}
	file, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if file.FileName() != "Les Misérables - Victor Hugo.epub" {
		t.Errorf("file name = %q (Content-Disposition %q)", file.FileName(), file.Header.Get("Content-Disposition"))
	}
	if cd := file.Header.Get("Content-Disposition"); !strings.Contains(cd, "filename*=utf-8''") {
		t.Errorf("a non-ASCII name must be RFC 2231 encoded: %q", cd)
	}
	if ct := file.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/epub+zip") || !strings.Contains(ct, "=?utf-8?") {
		t.Errorf("attachment content type = %q, want epub with an RFC 2047 name", ct)
	}
	if file.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("attachment encoding = %q", file.Header.Get("Content-Transfer-Encoding"))
	}
	raw, _ := io.ReadAll(file)
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\r\n"), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line of %d chars; RFC 2045 says 76 at most", len(line))
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(string(raw)))
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("attachment didn't survive the trip (%v, %d bytes, want %d)", err, len(decoded), len(payload))
	}
}

// Implicit TLS (port 465) and a plain-text message with no attachment.
func TestSendImplicitTLSPlainText(t *testing.T) {
	s := startFake(t, mailtest.Options{User: "owner", Pass: "pw", Implicit: true})
	if err := send(context.Background(), cfgFor(s, SecurityTLS, "owner"), "pw", Message{To: "me@example.com", Subject: "Arrmada test email", Body: "It works."}, s.Roots); err != nil {
		t.Fatalf("send: %v", err)
	}
	m := s.Messages()
	if len(m) != 1 || !m[0].TLS || !strings.Contains(m[0].Data, "Content-Type: text/plain") {
		t.Fatalf("messages = %+v", m)
	}
}

// A wrong password comes back with the server's own reply, so the settings card can show
// it; nothing is delivered.
func TestSendWrongPasswordReturnsServerReply(t *testing.T) {
	s := startFake(t, mailtest.Options{User: "owner", Pass: "right", StartTLS: true})
	err := send(context.Background(), cfgFor(s, SecurityStartTLS, "owner"), "wrong", Message{To: "me@example.com", Subject: "x", Body: "x"}, s.Roots)
	if err == nil {
		t.Fatal("a wrong password must fail")
	}
	if !strings.Contains(err.Error(), "535") || !strings.Contains(err.Error(), "Authentication credentials invalid") {
		t.Errorf("error = %q, want the server's 535 reply", err)
	}
	if ReplyCode(err) != 535 {
		t.Errorf("ReplyCode = %d, want 535", ReplyCode(err))
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Error("the error must never echo the password")
	}
	if n := len(s.Messages()); n != 0 {
		t.Errorf("%d messages delivered after a refused sign-in", n)
	}
}

// STARTTLS that the server doesn't offer is an error, not a silent plain-text send; and
// a self-signed certificate nobody trusts is refused.
func TestSendRefusesDowngradeAndUntrustedCert(t *testing.T) {
	plain := startFake(t, mailtest.Options{User: "owner", Pass: "pw"})
	if err := send(context.Background(), cfgFor(plain, SecurityStartTLS, "owner"), "pw", Message{To: "me@example.com", Subject: "x"}, plain.Roots); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("no STARTTLS offered: err = %v", err)
	}
	tlsSrv := startFake(t, mailtest.Options{User: "owner", Pass: "pw", StartTLS: true})
	if err := Send(context.Background(), cfgFor(tlsSrv, SecurityStartTLS, "owner"), "pw", Message{To: "me@example.com", Subject: "x"}); err == nil {
		t.Error("an untrusted certificate must be refused")
	}
	if n := len(plain.Messages()) + len(tlsSrv.Messages()); n != 0 {
		t.Errorf("%d messages delivered", n)
	}
}

// With no username the server is used without signing in (a LAN relay); a refused
// recipient is reported with its reply.
func TestSendWithoutLoginAndRefusedRecipient(t *testing.T) {
	s := startFake(t, mailtest.Options{RejectRcpt: "nobody@example.com"})
	if err := send(context.Background(), cfgFor(s, SecurityNone, ""), "", Message{To: "me@example.com", Subject: "x", Body: "y"}, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	err := send(context.Background(), cfgFor(s, SecurityNone, ""), "", Message{To: "nobody@example.com", Subject: "x"}, nil)
	if ReplyCode(err) != 550 {
		t.Errorf("refused recipient: err = %v", err)
	}
}

// A cancelled or expired context ends the exchange instead of hanging on the server.
func TestSendHonoursContext(t *testing.T) {
	s := startFake(t, mailtest.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := send(ctx, cfgFor(s, SecurityNone, ""), "", Message{To: "me@example.com", Subject: "x"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled send: err = %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if err := send(ctx, cfgFor(s, SecurityNone, ""), "", Message{To: "me@example.com", Subject: "x"}, nil); err == nil {
		t.Error("an expired context must fail")
	}
}

func TestValidate(t *testing.T) {
	ok := Config{Host: "smtp.example.com", Port: 587, Security: SecurityStartTLS, From: "books@example.com"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, c := range map[string]Config{
		"no host":       {Port: 587, Security: SecurityStartTLS, From: "a@b.co"},
		"url host":      {Host: "smtps://smtp.example.com", Port: 465, Security: SecurityTLS, From: "a@b.co"},
		"port in host":  {Host: "smtp.example.com:587", Port: 587, Security: SecurityStartTLS, From: "a@b.co"},
		"bad port":      {Host: "smtp.example.com", Port: 0, Security: SecurityStartTLS, From: "a@b.co"},
		"bad security":  {Host: "smtp.example.com", Port: 587, Security: "ssl", From: "a@b.co"},
		"bad from":      {Host: "smtp.example.com", Port: 587, Security: SecurityStartTLS, From: "not an address"},
		"header inject": {Host: "smtp.example.com", Port: 587, Security: SecurityStartTLS, From: "a@b.co\r\nBcc: x@y.co"},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A subject can't smuggle a header in.
func TestSubjectStaysOneHeader(t *testing.T) {
	var buf bytes.Buffer
	from, _ := ParseAddress("a@example.com")
	to, _ := ParseAddress("b@example.com")
	if err := writeMessage(&buf, from, to, Message{Subject: "Hi\r\nBcc: evil@example.com", Body: "x"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m, err := netmail.ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if m.Header.Get("Bcc") != "" {
		t.Fatal("a newline in the subject became a header")
	}
}
