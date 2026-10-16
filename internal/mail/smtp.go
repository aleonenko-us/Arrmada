// Package mail is Arrmada's one way to send email: a plain SMTP client over the owner's
// own mail account (an app password from Gmail, Fastmail, iCloud…). Alerts go through
// Apprise, which can't carry a per-person attachment cleanly; Send to Kindle needs exactly
// that, so this sends a message with at most one attached file.
//
// The settings live in the app (Settings → Book server → Send to Kindle email): host, username and
// password in the credentials store, port, security and From address beside them. Nothing
// here logs; callers log the outcome, never the host, the login or the recipient.
package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Security is how the connection to the mail server is protected.
const (
	SecurityStartTLS = "starttls" // plain connect, then upgrade (port 587)
	SecurityTLS      = "tls"      // TLS from the first byte (port 465)
	SecurityNone     = "none"     // no encryption: a relay on the local network only
)

// ValidSecurity reports whether s is one of the three security modes.
func ValidSecurity(s string) bool {
	return s == SecurityStartTLS || s == SecurityTLS || s == SecurityNone
}

// DefaultPort is the usual port for a security mode.
func DefaultPort(security string) int {
	switch security {
	case SecurityTLS:
		return 465
	case SecurityNone:
		return 25
	}
	return 587
}

// Config is where mail goes out from. The password is not part of it: it is read from the
// credentials store at send time and passed to Send on its own, so a Config can be logged
// or compared without carrying a secret.
type Config struct {
	Host     string
	Port     int
	Security string // starttls | tls | none
	Username string // "" = the server takes mail without signing in
	From     string // "Arrmada <books@example.com>" or a bare address
}

// ErrNotConfigured is a send with no server or From address saved.
var ErrNotConfigured = errors.New("outgoing email isn't set up")

// Ready reports whether there is enough to try a send: a server and a From address.
func (c Config) Ready() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

// Validate checks a Config before it's saved or used, in words the settings card shows.
func (c Config) Validate() error {
	if !c.Ready() {
		return ErrNotConfigured
	}
	if err := ValidHost(c.Host); err != nil {
		return err
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("the port must be between 1 and 65535")
	}
	if !ValidSecurity(c.Security) {
		return errors.New("security must be STARTTLS, TLS or None")
	}
	if _, err := ParseAddress(c.From); err != nil {
		return fmt.Errorf("the From address isn't valid: %w", err)
	}
	return nil
}

// ValidHost accepts a bare server name or IP ("smtp.gmail.com"), and refuses the things
// people paste by mistake: a URL, a port, spaces.
func ValidHost(h string) error {
	switch {
	case h == "":
		return errors.New("the server is required")
	case len(h) > 253:
		return errors.New("the server name is too long")
	case strings.Contains(h, "://"):
		return errors.New("enter just the server name (smtp.example.com), not a URL")
	case strings.ContainsAny(h, " \t\r\n/@"):
		return errors.New("the server name can't contain spaces, '/' or '@'")
	case strings.Count(h, ":") == 1:
		return errors.New("enter the port in its own field, not after the server name")
	}
	return nil
}

// ParseAddress reads one email address, with or without a display name. Header-breaking
// characters are refused outright: an address ends up in a header.
func ParseAddress(s string) (*netmail.Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("an address is required")
	}
	if strings.ContainsAny(s, "\r\n") {
		return nil, errors.New("an address can't span lines")
	}
	a, err := netmail.ParseAddress(s)
	if err != nil {
		return nil, errors.New("that doesn't look like an email address")
	}
	at := strings.LastIndexByte(a.Address, '@')
	if at <= 0 || !strings.Contains(a.Address[at+1:], ".") {
		return nil, errors.New("that doesn't look like an email address")
	}
	return a, nil
}

// Message is one email: plain text, with at most one attached file.
type Message struct {
	To      string
	Subject string
	Body    string
	// Attachment is streamed from Open while the message is written, never read into
	// memory whole: a 50 MB ebook is base64-encoded on the way out.
	Attachment *Attachment
}

// Attachment is a file to send with a message.
type Attachment struct {
	Name        string // the file name the recipient sees
	ContentType string // "application/epub+zip"; "" = application/octet-stream
	Open        func() (io.ReadCloser, error)
	Size        int64 // informational; the stream decides what's sent
}

// dialTimeout bounds connecting when ctx has no sooner deadline.
const dialTimeout = 30 * time.Second

// Send delivers msg through the server in cfg, signing in with cfg.Username and password
// when a username is set. ctx bounds the whole exchange: connecting, the TLS handshake and
// every read and write. An error from the server carries its reply ("535 5.7.8
// Username and Password not accepted"), so the settings card can show what it said.
func Send(ctx context.Context, cfg Config, password string, msg Message) error {
	return send(ctx, cfg, password, msg, nil)
}

// send is Send with the certificate roots to trust (nil = the system's); tests pass the
// fake server's self-signed one.
func send(ctx context.Context, cfg Config, password string, msg Message, roots *x509.CertPool) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	from, err := ParseAddress(cfg.From)
	if err != nil {
		return err
	}
	to, err := ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("recipient: %w", err)
	}

	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return fmt.Errorf("couldn't reach the mail server: %w", ctxErr(ctx, err))
	}
	defer conn.Close()
	// Every read and write honours ctx: its deadline up front, and a cancel unblocks
	// whatever is waiting by expiring the connection.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	tlsCfg := &tls.Config{ServerName: cfg.Host, RootCAs: roots, MinVersion: tls.VersionTLS12}
	if cfg.Security == SecurityTLS {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("TLS with the mail server failed: %w", ctxErr(ctx, err))
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("the mail server didn't greet: %w", ctxErr(ctx, err))
	}
	defer c.Close()
	if err := c.Hello("arrmada.local"); err != nil {
		return fmt.Errorf("the mail server refused the greeting: %w", ctxErr(ctx, err))
	}
	if cfg.Security == SecurityStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server doesn't offer STARTTLS on this port; try TLS (usually port 465)")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS with the mail server failed: %w", ctxErr(ctx, err))
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("the mail server doesn't accept a sign-in on this connection")
		}
		// net/smtp's PLAIN refuses to send the password over an unencrypted connection to
		// anything but localhost, which is exactly the guard wanted for Security "none".
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, password, cfg.Host)); err != nil {
			return fmt.Errorf("sign-in refused: %w", ctxErr(ctx, err))
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("sender refused: %w", ctxErr(ctx, err))
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("recipient refused: %w", ctxErr(ctx, err))
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("the mail server wouldn't take the message: %w", ctxErr(ctx, err))
	}
	if err := writeMessage(w, from, to, msg, time.Now()); err != nil {
		_ = w.Close()
		return fmt.Errorf("writing the message: %w", ctxErr(ctx, err))
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the mail server rejected the message: %w", ctxErr(ctx, err))
	}
	// The message is accepted once DATA is answered; a QUIT that fails changes nothing.
	_ = c.Quit()
	return nil
}

// ctxErr prefers the context's reason (a timeout, a shutdown) over the i/o error it caused,
// and otherwise cleans the error of addresses (see clean).
func ctxErr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return clean(err)
}

// clean keeps what went wrong and drops where: Go's network, TLS and file errors carry the
// server's name or address ("dial tcp 10.0.0.5:587: connect: connection refused") or a
// file's path, and Send's errors reach browsers — the settings card, and a requester's
// inbox after a failed Send to Kindle. The mail server is a credential there, never shown
// in full. A reply from the server itself is kept whole: it's what the person needs.
func clean(err error) error {
	var reply *textproto.Error
	if err == nil || errors.As(err, &reply) {
		return err
	}
	var dns *net.DNSError
	var host x509.HostnameError
	var authority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var op *net.OpError
	var path *fs.PathError
	switch {
	case errors.As(err, &dns):
		return errors.New("the mail server's name couldn't be looked up")
	case errors.As(err, &host):
		return errors.New("the mail server's certificate is for a different name")
	case errors.As(err, &authority):
		return errors.New("the mail server's certificate isn't from a trusted authority")
	case errors.As(err, &invalid):
		return errors.New("the mail server's certificate is invalid or expired")
	case errors.As(err, &op):
		if op.Timeout() {
			return errors.New("the mail server stopped answering")
		}
		return errors.New(op.Err.Error()) // "connect: connection refused", no address
	case errors.As(err, &path):
		return path.Err
	}
	return err
}

// ReplyCode is the SMTP status code inside err (535, 550…), or 0 when the error isn't a
// server reply. Callers log this rather than the reply text, which can echo addresses.
func ReplyCode(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}
