package httpapi

import (
	"net/http"
	"strings"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// Who may call a route is decided here, on the server, for every route — not by which
// pages the UI happens to show. It used to be one helper (protected) that let any
// signed-in account through, so a family member's requester account on the LAN could
// run live tracker searches, read the transfer list and walk the host's folders just by
// calling the API the staff pages use.
//
// Now a route can't be registered without saying who it's for: the router's HandleFunc
// only takes a guard, and the only ways to make one are below. A plain handler doesn't
// compile. Anything not deliberately opened to everyone signed in is staff-only, and
// testdata/routes.golden lists every route with its audience so a change shows up in
// review.
//
// A request made with a personal API key has one more gate: the route must be marked for
// keys (apikey_routes.go; "key" in the golden table), and a read-only key only reaches
// GET. Being marked for keys never widens who may call a route: the scope check below
// still applies, with the key's (capped) role.

// scope is the least privileged audience a route admits.
type scope int

const (
	scopePublic    scope = iota // no session needed: health, status, sign-in, the SPA
	scopeUser                   // any signed-in account, read-only included
	scopeRequester              // requester and up: create or withdraw requests
	scopeStaff                  // manager and admin
	scopeAdmin                  // admin only
)

func (s scope) String() string {
	switch s {
	case scopePublic:
		return "public"
	case scopeUser:
		return "user"
	case scopeRequester:
		return "requester"
	case scopeStaff:
		return "staff"
	case scopeAdmin:
		return "admin"
	}
	return "unknown"
}

// role is the minimum account role a non-public scope needs.
func (s scope) role() auth.Role {
	switch s {
	case scopeUser:
		return auth.RoleReadonly
	case scopeRequester:
		return auth.RoleRequester
	case scopeStaff:
		return auth.RoleManager
	}
	return auth.RoleAdmin
}

// guard is a handler with its audience attached. Make one with public, signedIn or
// requireRole.
type guard struct {
	scope scope
	// external marks a route reachable from outside the LAN for non-staff (requesters,
	// read-only accounts and anyone not signed in). Without it, such a request from
	// outside gets 403 before the handler runs. Staff are never limited this way.
	external bool
	// apiKey marks a route a personal API key may call (apikey_routes.go decides; only
	// HandleFunc sets it). A request made with a key gets 403 from every other route.
	apiKey bool
	// upload marks a route that takes a file as multipart/form-data (csrf.go); every other
	// state-changing route takes JSON or nothing.
	upload bool
	h      http.HandlerFunc
}

// ext marks the route as reachable from outside the LAN.
func (g guard) ext() guard {
	g.external = true
	return g
}

// uploads marks the route as taking a multipart file upload.
func (g guard) uploads() guard {
	g.upload = true
	return g
}

// public opens a route to anyone, signed in or not.
func (a *api) public(h http.HandlerFunc) guard { return guard{scope: scopePublic, h: h} }

// signedIn opens a route to every signed-in account, read-only included. Only the
// requester-facing surface (Discover, Calendar, My Books, the user's own account and
// audiobooks) belongs here.
func (a *api) signedIn(h http.HandlerFunc) guard { return guard{scope: scopeUser, h: h} }

// requireRole admits accounts at or above min.
func (a *api) requireRole(min auth.Role, h http.HandlerFunc) guard {
	s := scopeAdmin
	switch min {
	case auth.RoleReadonly:
		s = scopeUser
	case auth.RoleRequester:
		s = scopeRequester
	case auth.RoleManager:
		s = scopeStaff
	}
	return guard{scope: s, h: h}
}

// routeSpec is one registered route, as the golden table records it.
type routeSpec struct {
	Method   string // "" for a method-less pattern (the SPA)
	Pattern  string
	Scope    scope
	External bool
	APIKey   bool // a personal API key may call it
}

// router is the only thing in this package that registers on the ServeMux.
type router struct {
	a     *api
	mux   *http.ServeMux
	specs []routeSpec
	// sentinel (tests only) swaps every handler for a stub that answers 299 after the
	// scope check, so the route walk can tell "let through" from anything a real
	// handler might answer.
	sentinel bool
}

const sentinelStatus = 299

func newRouter(a *api) *router {
	return &router{a: a, mux: http.NewServeMux()}
}

// HandleFunc registers pattern ("METHOD /path") behind g's scope check.
func (rt *router) HandleFunc(pattern string, g guard) {
	method, path := "", pattern
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		method, path = pattern[:i], strings.TrimSpace(pattern[i+1:])
	}
	g.apiKey = apiKeyRoutes[pattern]
	spec := routeSpec{Method: method, Pattern: path, Scope: g.scope, External: g.external, APIKey: g.apiKey}
	rt.specs = append(rt.specs, spec)
	rt.mux.HandleFunc(pattern, rt.sameOrigin(g, rt.keyed(g, rt.wrap(g))))
}

// keyed puts the personal-API-key rules in front of h: a request made with a key reaches
// only routes marked for keys, and a read-only key only their GET (and HEAD) side. A
// request without a key passes straight through.
func (rt *router) keyed(g guard, h http.HandlerFunc) http.HandlerFunc {
	a := rt.a
	return func(w http.ResponseWriter, r *http.Request) {
		if k, ok := keyUseFrom(r); ok {
			if !g.apiKey {
				a.writeError(w, http.StatusForbidden, "API keys can't be used for this; sign in to Arrmada instead")
				return
			}
			if k.Scope != auth.KeyScopeFull && r.Method != http.MethodGet && r.Method != http.MethodHead {
				a.writeError(w, http.StatusForbidden, "this API key is read-only")
				return
			}
		}
		h(w, r)
	}
}

func (rt *router) wrap(g guard) http.HandlerFunc {
	h := rt.scoped(g)
	if g.external {
		return h
	}
	a := rt.a
	// Off-LAN reach comes from this same table: externalGate has stamped whether the
	// request is from outside and not staff, and a route not marked ext() refuses it
	// before the scope check, as the old prefix-list gate did.
	return func(w http.ResponseWriter, r *http.Request) {
		if isExternalRequest(r) {
			a.writeError(w, http.StatusForbidden, "not available outside your network")
			return
		}
		h(w, r)
	}
}

// scoped wraps g's handler in its scope check.
func (rt *router) scoped(g guard) http.HandlerFunc {
	h := g.h
	if rt.sentinel {
		h = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Route", r.Pattern)
			w.WriteHeader(sentinelStatus)
		}
	}
	if g.scope == scopePublic {
		return h
	}
	min := g.scope.role()
	a := rt.a
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFrom(r)
		if !ok || u == nil {
			a.writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// Sessions and API keys of disabled accounts already fail validation; checking
		// again here costs nothing.
		if u.Disabled || !u.Role.AtLeast(min) {
			a.writeError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		h(w, r)
	}
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}

// spa serves the web UI for every path no API route claimed — except under /api/,
// where an unknown path is a JSON 404 rather than the app's index page answering 200.
func (a *api) spa(ui http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == "/api" || strings.HasPrefix(p, "/api/") {
			a.writeError(w, http.StatusNotFound, "not found")
			return
		}
		ui.ServeHTTP(w, r)
	}
}
