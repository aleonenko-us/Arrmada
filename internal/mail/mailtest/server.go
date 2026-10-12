// Package mailtest is a fake SMTP server for tests: it listens on 127.0.0.1, follows a
// scripted dialog (EHLO, optional STARTTLS or implicit TLS, AUTH PLAIN, MAIL, RCPT, DATA)
// and keeps every message it accepts. Tests use it so nothing ever talks to a real mail
// server.
package mailtest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options shape the fake server's dialog.
type Options struct {
	// User and Pass are the only credentials AUTH PLAIN accepts. With User empty the
	// server doesn't offer AUTH at all.
	User, Pass string
	// StartTLS offers STARTTLS; Implicit speaks TLS from the first byte (port 465 style).
	StartTLS, Implicit bool
	// RejectRcpt answers RCPT TO for this address with a 550.
	RejectRcpt string
}

// Received is one message the server accepted.
type Received struct {
	From string
	To   []string
	Data string // the DATA section, CRLF line ends, dot-unstuffed
	User string // who signed in ("" = nobody)
	TLS  bool   // whether the connection was encrypted when DATA arrived
}

// Server is a running fake.
type Server struct {
	Host string // always 127.0.0.1
	Port int
	// Roots trusts the server's self-signed certificate.
	Roots *x509.CertPool

	opts Options
	ln   net.Listener
	cert tls.Certificate

	mu   sync.Mutex
	msgs []Received
	wg   sync.WaitGroup
}

// Start listens on a free local port. Close it when done.
func Start(opts Options) (*Server, error) {
	cert, roots, err := selfSigned()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Roots: roots, opts: opts, ln: ln, cert: cert}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Close stops listening and waits for open dialogs to end.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

// Messages is a copy of everything accepted so far.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.msgs...)
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.dialog(c)
		}()
	}
}

func (s *Server) tlsConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12}
}

// dialog runs one SMTP conversation.
func (s *Server) dialog(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	secure := false
	if s.opts.Implicit {
		conn = tls.Server(conn, s.tlsConfig())
		secure = true
	}
	r := textproto.NewReader(bufio.NewReader(conn))
	w := bufio.NewWriter(conn)
	say := func(lines ...string) {
		for _, l := range lines {
			_, _ = w.WriteString(l + "\r\n")
		}
		_ = w.Flush()
	}
	say("220 mailtest ESMTP ready")
	var cur Received
	user := ""
	for {
		line, err := r.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			ext := []string{"250-mailtest", "250-8BITMIME"}
			if s.opts.StartTLS && !secure {
				ext = append(ext, "250-STARTTLS")
			}
			if s.opts.User != "" {
				ext = append(ext, "250-AUTH PLAIN")
			}
			ext = append(ext, "250 SIZE 104857600")
			say(ext...)
		case "STARTTLS":
			if !s.opts.StartTLS || secure {
				say("502 5.5.1 not offered")
				continue
			}
			say("220 2.0.0 go ahead")
			tc := tls.Server(conn, s.tlsConfig())
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, secure = tc, true
			r = textproto.NewReader(bufio.NewReader(conn))
			w = bufio.NewWriter(conn)
		case "AUTH":
			mech, resp, _ := strings.Cut(arg, " ")
			if !strings.EqualFold(mech, "PLAIN") || s.opts.User == "" {
				say("504 5.5.4 mechanism not supported")
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(resp)
			parts := strings.Split(string(raw), "\x00")
			if err != nil || len(parts) != 3 || parts[1] != s.opts.User || parts[2] != s.opts.Pass {
				say("535 5.7.8 Authentication credentials invalid")
				continue
			}
			user = parts[1]
			say("235 2.7.0 Authentication successful")
		case "MAIL":
			if s.opts.User != "" && user == "" {
				say("530 5.7.0 Authentication required")
				continue
			}
			cur = Received{From: addrOf(arg), User: user}
			say("250 2.1.0 ok")
		case "RCPT":
			to := addrOf(arg)
			if s.opts.RejectRcpt != "" && strings.EqualFold(to, s.opts.RejectRcpt) {
				say("550 5.1.1 mailbox unavailable")
				continue
			}
			cur.To = append(cur.To, to)
			say("250 2.1.5 ok")
		case "DATA":
			if len(cur.To) == 0 {
				say("503 5.5.1 need RCPT first")
				continue
			}
			say("354 go ahead")
			lines, err := r.ReadDotLines()
			if err != nil {
				return
			}
			cur.Data = strings.Join(lines, "\r\n")
			cur.TLS = secure
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			cur = Received{}
			say("250 2.0.0 queued as " + strconv.Itoa(len(s.Messages())))
		case "RSET":
			cur = Received{}
			say("250 ok")
		case "NOOP":
			say("250 ok")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 5.5.2 unknown command")
		}
	}
}

// addrOf pulls the address out of "FROM:<a@b>" / "TO:<a@b> SIZE=…".
func addrOf(arg string) string {
	if i := strings.IndexByte(arg, '<'); i >= 0 {
		if j := strings.IndexByte(arg[i:], '>'); j > 0 {
			return arg[i+1 : i+j]
		}
	}
	return arg
}

// selfSigned makes a throwaway certificate for 127.0.0.1 and localhost.
func selfSigned() (tls.Certificate, *x509.CertPool, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mailtest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool, nil
}
