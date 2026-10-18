package httpapi

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/tristenlammi/arrmada/internal/netutil"
)

// Cross-site request forgery. The session cookie is SameSite=Lax, which stops other
// websites, but not other sites on the same "site": another port on the same host
// (qBittorrent, SABnzbd, Jellyfin at the same address) or a sibling subdomain counts as
// same-site to the browser and gets the cookie sent along. A plain HTML form there, posting
// text/plain shaped like JSON, could create an admin through /api/v1/users; a body-less
// POST could start any action. The checks below run on every state-changing request
// (POST, PUT, PATCH, DELETE) that isn't made with a personal API key:
//
//   - Sec-Fetch-Site, which every current browser sends and no page can set, must say
//     same-origin (or none: typed by the person, a bookmark). It's the browser's own
//     verdict, so it doesn't care which proxy or hostname is in front of us.
//   - A browser too old to send it still sends Origin on a POST: it must name the host
//     the request reached, as the browser addressed it (Host, or X-Forwarded-Host from
//     a local proxy that rewrites Host).
//   - With neither header the request is let through. Scripts and other non-browser
//     clients send neither, and a browser that sends neither isn't a realistic way in.
//   - A request with a body must say it's JSON (multipart only on the upload routes). A
//     cross-site form can't send that type without the browser asking first (CORS),
//     which this server never answers, so it backs up the header checks.
//
// Sign-in and first-run setup are covered too, so nobody can sign a visitor into an
// account of theirs (login CSRF). Personal API keys come in a header, which a form can't
// set; the OPDS catalogue (Basic auth, reading only) and the audiobook server (its own
// port and bearer tokens) never see a cookie-authenticated write.

// sameOrigin puts the checks in front of h.
func (rt *router) sameOrigin(g guard, h http.HandlerFunc) http.HandlerFunc {
	a := rt.a
	return func(w http.ResponseWriter, r *http.Request) {
		if changesState(r.Method) {
			if _, byKey := keyUseFrom(r); !byKey {
				if ok, why := fromThisSite(r); !ok {
					if a.deps.Log != nil {
						a.deps.Log.Warn("refused a cross-site request", "route", r.Pattern, "why", why)
					}
					a.writeError(w, http.StatusForbidden, "this request came from another site, so it was refused")
					return
				}
				if !bodyTypeAllowed(r, g.upload) {
					a.writeError(w, http.StatusUnsupportedMediaType, "send this request as JSON (Content-Type: application/json)")
					return
				}
			}
		}
		h(w, r)
	}
}

func changesState(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	return true
}

// fromThisSite reports whether a browser request came from Arrmada's own pages, and if
// not, why (for the log: header values only, never cookies or the body).
func fromThisSite(r *http.Request) (bool, string) {
	switch site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site {
	case "same-origin", "none":
		return true, ""
	case "":
	default:
		return false, "Sec-Fetch-Site: " + site
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true, ""
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false, "Origin: unreadable"
	}
	if sameHost(u, r.Host) {
		return true, ""
	}
	if netutil.FromLocalProxy(r) {
		if fh := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); fh != "" && sameHost(u, fh) {
			return true, ""
		}
	}
	return false, "Origin " + u.Host + " isn't " + r.Host
}

// sameHost reports whether origin names host ("name" or "name:port"), a port the
// origin's scheme implies counting as no port.
func sameHost(origin *url.URL, host string) bool {
	return strings.EqualFold(hostKey(origin.Scheme, origin.Host), hostKey(origin.Scheme, host))
}

// hostKey is host without the scheme's default port, so https://a and a:443 compare equal.
func hostKey(scheme, host string) string {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return strings.Trim(host, "[]")
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return h
	}
	return net.JoinHostPort(h, port)
}

// bodyTypeAllowed reports whether a state-changing request's body is of a type the route
// takes: none at all, JSON, or (an upload route) multipart.
func bodyTypeAllowed(r *http.Request, upload bool) bool {
	if r.ContentLength == 0 {
		return true
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mt == "application/json" || (upload && mt == "multipart/form-data")
}
