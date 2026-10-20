package connstatus

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// Class is the kind of failure a connection error falls into.
type Class string

const (
	ClassNone        Class = ""            // not recognised: the (redacted) text is shown as it is
	ClassTimeout     Class = "timeout"     // no answer in time
	ClassDNS         Class = "dns"         // the host name doesn't resolve
	ClassDockerName  Class = "docker_name" // an unresolved single-word host, usually a container name
	ClassRefused     Class = "refused"     // the host answered "nothing listens here"
	ClassLocalhost   Class = "localhost"   // refused, and the address was localhost
	ClassUnreachable Class = "unreachable" // no route to the host or network
	ClassReset       Class = "reset"       // the connection was cut off mid-way
	ClassTLS         Class = "tls"         // certificate problem
	ClassWrongScheme Class = "wrong_scheme"
	ClassBadURL      Class = "bad_url" // not a usable address at all
	ClassAuth        Class = "auth"    // 401, wrong key / password
	ClassForbidden   Class = "forbidden"
	ClassNotFound    Class = "not_found" // 404: reachable, but not that program or path
	ClassRateLimited Class = "rate_limited"
	ClassServerError Class = "server_error"
)

var (
	urlInTextRe = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s"'<>]+`)
	lookupRe    = regexp.MustCompile(`(?i)lookup ([^\s:]+)`)
	status4xxRe = map[string]*regexp.Regexp{
		"401": regexp.MustCompile(`\b401\b`),
		"403": regexp.MustCompile(`\b403\b`),
		"404": regexp.MustCompile(`\b404\b`),
		"429": regexp.MustCompile(`\b429\b`),
		"5xx": regexp.MustCompile(`\b(5\d\d)\b`),
	}
)

// Explain turns a raw connection error (a Go net error, "HTTP 401", a client's own
// wording) into a sentence a person can act on, and the class it fell into. What it can't
// classify comes back as ClassNone with the redacted original text, so nothing is lost.
// The text never carries a key, password or URL query: it's built from fixed wording plus
// the bare host name, and anything passed through goes via Redact first.
func Explain(err error) (Class, string) {
	if err == nil {
		return ClassNone, ""
	}
	msg := err.Error()
	if errors.Is(err, context.DeadlineExceeded) && !strings.Contains(strings.ToLower(msg), "timeout") {
		msg += " (timeout)"
	}
	return ExplainText(msg)
}

// ExplainText is Explain for an error already stored as text (a connection card's last error).
func ExplainText(msg string) (Class, string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ClassNone, ""
	}
	low := strings.ToLower(msg)
	host := hostIn(msg)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(low, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("server gave http response to https client"):
		return ClassWrongScheme, "That address uses https:// but the program answers with plain http. Change the address to start with http://."
	case has("first record does not look like a tls handshake"):
		return ClassWrongScheme, "That address uses https:// but the program doesn't speak TLS on that port. Change the address to start with http://."
	case has("x509", "certificate", "tls:", "tls handshake"):
		return ClassTLS, "The secure (https) connection failed because its certificate isn't trusted" + onHost(host) +
			". Use the address the certificate was made for, or plain http:// on a trusted home network."
	case has("unsupported protocol scheme", "invalid url", "missing protocol scheme", "no host in request", "invalid port", "first path segment in url"):
		return ClassBadURL, "That doesn't look like a web address. It needs to start with http:// or https:// and include the host and port, like http://192.168.1.10:8080."
	case has("no such host", "name or service not known", "server misbehaving", "temporary failure in name resolution", "no address associated"):
		if dockerName(host) {
			return ClassDockerName, "Couldn't find a machine called \"" + host + "\". A bare name like that only works when both containers share a Docker network. Otherwise use the machine's IP address (like 192.168.1.10) or its full host name."
		}
		return ClassDNS, "Couldn't find the host" + onHost(host) + ". Check the address for typos, or use the machine's IP address instead of its name."
	case has("connection refused", "actively refused"):
		if isLocalhost(host) {
			return ClassLocalhost, "Connection refused at " + host + ". Inside Docker, localhost means Arrmada's own container, not your computer. Use the machine's IP address or the other container's name."
		}
		return ClassRefused, "The host" + onHost(host) + " is there but nothing is listening on that port. Check the port number and that the program is running."
	case has("no route to host", "network is unreachable", "host is down"):
		return ClassUnreachable, "Can't reach" + hostOr(host, " that host") + " from here. Check the IP address, that the machine is on, and that Arrmada's network can see it."
	case has("connection reset", "broken pipe", "unexpected eof") || strings.HasSuffix(low, ": eof") || low == "eof":
		return ClassReset, "The connection was cut off before an answer. This often means http:// and https:// are mixed up, or a proxy is in the way. Check the address and port."
	case has("timeout", "timed out", "deadline exceeded", "did not answer", "didn't answer"):
		return ClassTimeout, "No answer" + onHost(host) + " in time. Check the address and port, that the program is running, and that a firewall isn't dropping the connection."
	case has("unauthorized", "invalid api key", "wrong api key", "api key was rejected", "rejected the api key", "rejected the key", "refused the api key", "incorrect api key", "login failed", "invalid credentials", "bad credentials", "wrong password", "invalid password", "invalid token", "invalid username"),
		status4xxRe["401"].MatchString(low):
		return ClassAuth, "It answered but didn't accept the login (401). Check the API key, or the username and password, and that it is copied in full without spaces."
	case has("forbidden"), status4xxRe["403"].MatchString(low):
		return ClassForbidden, "It answered but refused access (403). The key or user may lack permission, or the program is restricting which hosts or IP addresses may connect."
	case has("looking for beginning of value", "doctype html", "<html"):
		return ClassNotFound, "The address answered, but not like the program we expected. Check the port and any URL base or path at the end of the address."
	case status4xxRe["404"].MatchString(low):
		return ClassNotFound, "The address answered, but not as the program we expected (404). Check the port and any URL base or path at the end of the address."
	case status4xxRe["429"].MatchString(low), has("too many requests", "rate limit"):
		return ClassRateLimited, "It's asking us to slow down (too many requests). Wait a few minutes and test again."
	case status4xxRe["5xx"].MatchString(low), has("bad gateway", "service unavailable", "internal server error"):
		return ClassServerError, "The program answered with an error of its own (HTTP 5xx). It may still be starting up or have a problem. Check its logs, then test again."
	}
	return ClassNone, Redact(msg)
}

func onHost(h string) string {
	if h == "" {
		return ""
	}
	return " (" + h + ")"
}

func hostOr(h, fallback string) string {
	if h == "" {
		return fallback
	}
	return " " + h
}

// hostIn pulls the bare host (no port, user, path or query) out of an error's text.
func hostIn(msg string) string {
	if m := urlInTextRe.FindString(msg); m != "" {
		if u, err := url.Parse(strings.TrimRight(m, `:.,;)`)); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	if m := lookupRe.FindStringSubmatch(msg); m != nil {
		return strings.Trim(m[1], `."`)
	}
	return ""
}

func isLocalhost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.IsLoopback()
}

// dockerName: a single-label host (no dots, not an IP) such as "qbittorrent" or "sonarr".
func dockerName(h string) bool {
	if h == "" || strings.Contains(h, ".") || isLocalhost(h) {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return false
	}
	return true
}

// Friendly is Explain's text alone, for callers that just show the sentence.
func Friendly(err error) string {
	_, s := Explain(err)
	return s
}
