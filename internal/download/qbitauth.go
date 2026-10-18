package download

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// The Compose bundle's qBittorrent was seeded to let anything on the private networks in
// without a password (its subnet whitelist: 10.0.0.0/8, 172.16.0.0/12 and 192.168.0.0/16,
// so every device on a home network too), with CSRF protection off. That includes
// FlareSolverr's Chromium, which opens tracker pages: a hostile one could add torrents or
// change settings. A new install's whitelist is the Docker network's own subnet and nothing
// else (docker/qbittorrent/init.sh), and it too only lasts until Arrmada first starts.
// SecureBundledQbit moves it to a password Arrmada makes and signs in with, like the
// all-in-one image's, without losing a torrent or a setting and without ever leaving
// Arrmada shut out. It does so even when its card was removed or pointed elsewhere, as
// long as the bundled container still answers and lets Arrmada in without a password.
//
// The order matters. While the whitelist is on, qBittorrent lets Arrmada in whatever login
// it sends, so a login can't be checked from Arrmada's own address: a sign-in from a
// whitelisted address always "works". So the check borrows qBittorrent's reverse-proxy
// support for a moment: with the Docker networks trusted as a proxy, a sign-in that carries
// X-Forwarded-For with an outside address is judged as coming from that address, where no
// whitelist applies. A deliberately wrong login made that way first proves the check is
// real (it must be refused); then the login itself must be accepted. Only after that does
// the whitelist go off. The reverse-proxy settings go back as they were straight after,
// and are kept in the state (KeyBundledQbitAuth) while they're borrowed, so a restart
// halfway puts them back too.

// KeyBundledQbitAuth is where SecureBundledQbit keeps how far it got (qbitAuthState, JSON):
// a restart part-way through carries on from there, and once it's done it isn't redone,
// so a whitelist the owner sets up later is theirs.
const KeyBundledQbitAuth = "download_bundled_qbit_auth"

// KeyBundledQbitCSRF records that SecureBundledQbit has switched the bundled qBittorrent's
// CSRF protection (and its localhost login) on once. Configs seeded before 2026-10-16 had
// both off, and installs whose switch ran before it covered them (or that kept a whitelist
// of their own) were left so. Once is enough: an owner who turns them off again keeps that.
const KeyBundledQbitCSRF = "download_bundled_qbit_csrf"

const (
	qbitAuthVerifying = "verifying" // reverse-proxy settings borrowed for the check
	qbitAuthSwitching = "switching" // login checked; the whitelist is going off
	qbitAuthSecured   = "secured"
)

type qbitAuthState struct {
	Phase string `json:"phase,omitempty"`
	// What qBittorrent's reverse-proxy settings were before the check borrowed them.
	ProxyEnabled bool   `json:"proxy_enabled,omitempty"`
	ProxyList    string `json:"proxy_list,omitempty"`
}

// SecureOutcome is what SecureBundledQbit did.
type SecureOutcome int

const (
	// SecureSkipped: nothing to secure: no card for it and it already asks Arrmada's
	// network for a password, or a client of the owner's signs in to it without one.
	SecureSkipped SecureOutcome = iota
	// SecureAlready: it already asks for a password, or nothing was left to do.
	SecureAlready
	// SecureOwnWhitelist: secured before, and the whitelist on now is one the owner set up.
	SecureOwnWhitelist
	// SecureNewPassword: it now asks for the password Arrmada made.
	SecureNewPassword
	// SecureKeptPassword: it now asks for the password the owner had set (and gave Arrmada).
	SecureKeptPassword
)

// ErrQbitUnreachable means the bundled qBittorrent didn't answer (it may still be
// starting): nothing was changed, and it's worth trying again shortly.
var ErrQbitUnreachable = errors.New("the bundled qBittorrent didn't answer")

// ErrQbitLoginRefused means the bundled qBittorrent asks for a password and the one Arrmada
// has for it doesn't work. SecureBundledQbit can't change anything then; the owner enters
// the right one on the client's card.
var ErrQbitLoginRefused = errors.New("the bundled qBittorrent refused Arrmada's login")

// BundledLoginKeeper keeps the login Arrmada makes for the bundled qBittorrent (a file in
// Arrmada's data folder, which admins can look up in Settings → Connections).
type BundledLoginKeeper interface {
	// Load is the saved login; ok is false when none has been made.
	Load() (username, password string, ok bool, err error)
	// Make generates a new password for username and saves the login before returning it,
	// so a password is never given to qBittorrent without being kept first.
	Make(username string) (password string, err error)
	// Reopened says whether update.sh (--rollback --with-db) or the troubleshooting steps
	// switched qBittorrent's login-free access for the Docker network back on, on purpose,
	// for Arrmada to secure again (a marker beside the login: qbittorrent-reopened).
	Reopened() Reopen
	// ClearReopened removes that marker once it has been acted on.
	ClearReopened() error
}

// Reopen is why qBittorrent's login-free access for the Docker network was switched back on.
type Reopen int

const (
	// ReopenNone: no marker. The Docker network's subnet is Arrmada's only before the
	// switch has been done.
	ReopenNone Reopen = iota
	// ReopenRollback: update.sh put it back for an older build (empty marker). Arrmada
	// takes it back, but never overwrites a password changed since.
	ReopenRollback
	// ReopenReset: the owner asked Arrmada to give qBittorrent its password again
	// (troubleshooting: the marker says "reset"), overwriting whatever it has.
	ReopenReset
)

// ErrQbitKeptLoginStale means the whitelist is one Arrmada set up, but qBittorrent no
// longer takes the login Arrmada kept (someone changed its password since). Nothing was
// changed: overwriting the password would shut out whoever changed it.
var ErrQbitKeptLoginStale = errors.New("the bundled qBittorrent's password isn't the one Arrmada kept")

// seedWhitelist is the subnet whitelist the Compose bundle's first qBittorrent config had.
var seedWhitelist = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// isArrmadaWhitelist says list is a whitelist Arrmada set up, so Arrmada's to replace with
// a password: the old seed's private ranges, or the Docker network's own subnet that
// docker/qbittorrent/init.sh (a new install) and reopen.sh (update.sh --rollback --with-db,
// troubleshooting) write. The Docker subnet counts only while ours may be: before the
// switch has ever been done (fresh), or when reopen.sh's marker says it was just put back
// on purpose. After the switch, the same subnet is an owner's choice (letting their other
// containers in, say) and theirs to keep. Any other list is the owner's.
func (s *Service) isArrmadaWhitelist(ctx context.Context, url, list string, fresh bool) bool {
	return sameSubnets(list, seedWhitelist) || (fresh && s.isDockerWhitelist(ctx, url, list))
}

// isDockerWhitelist says every subnet in list is exactly one of the networks Arrmada's own
// container is on, and one qBittorrent (url's host) is on too: the network they share.
func (s *Service) isDockerWhitelist(ctx context.Context, url, list string) bool {
	subs := splitSubnets(list)
	if len(subs) == 0 {
		return false
	}
	own := s.localNets()
	qbit := s.hostAddrs(ctx, url)
	for _, sub := range subs {
		_, n, err := net.ParseCIDR(sub)
		if err != nil || !hasNet(own, n) || !slices.ContainsFunc(qbit, n.Contains) {
			return false
		}
	}
	return true
}

// hostAddrs is the addresses url's host resolves to (qBittorrent's on the Docker network).
func (s *Service) hostAddrs(ctx context.Context, rawURL string) []net.IP {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	if s.lookupHost != nil {
		return s.lookupHost(u.Hostname())
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return []net.IP{ip}
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(lctx, "ip", u.Hostname())
	if err != nil {
		return nil
	}
	return ips
}

// localNets is the networks Arrmada's container is on, from its interfaces' addresses.
func (s *Service) localNets() []*net.IPNet {
	if s.ownNets != nil {
		return s.ownNets()
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		out = append(out, &net.IPNet{IP: ipn.IP.Mask(ipn.Mask), Mask: ipn.Mask})
	}
	return out
}

func hasNet(nets []*net.IPNet, n *net.IPNet) bool {
	ones, bits := n.Mask.Size()
	for _, o := range nets {
		if oo, ob := o.Mask.Size(); oo == ones && ob == bits && o.IP.Equal(n.IP) {
			return true
		}
	}
	return false
}

// cardlessQbit is the client SecureBundledQbit works with when no card points at url: the
// login Arrmada keeps, or none. Name is empty when there's nothing to do. That's so while
// any qBittorrent card of the owner's is switched on: it may well reach this same
// qBittorrent under another address (a trailing slash, localhost for 127.0.0.1, the
// container's name or IP, the server's LAN address and published port), and a password it
// doesn't have would shut it out (and get its address banned). The health line says why
// it was left (BundledQbitExposure). It's also so when qBittorrent already asks Arrmada's
// network for a password, which is found out without signing in, so a qBittorrent with a
// password of the owner's never counts a failed login against Arrmada.
func (s *Service) cardlessQbit(ctx context.Context, api *qbitAuthAPI, url string, keeper BundledLoginKeeper) (Client, error) {
	clients, err := s.repo.List(ctx)
	if err != nil {
		return Client{}, err
	}
	for _, c := range clients {
		if c.Kind == KindQbittorrent && c.Enabled {
			s.log.Info("bundled qBittorrent: it has no card of its own, and a qBittorrent client of yours is switched on (it may reach this one under another address), so Arrmada leaves its login settings alone", "name", c.Name)
			s.holdQbit(fmt.Sprintf("your download client “%s” may sign in to it without a password, and would be shut out", c.Name))
			return Client{}, nil
		}
	}
	if _, open, err := api.anonymousPrefs(ctx); err != nil || !open {
		return Client{}, err
	}
	user, pass, _, err := keeper.Load()
	if err != nil {
		return Client{}, fmt.Errorf("the saved qBittorrent login: %w", err)
	}
	s.log.Info("bundled qBittorrent: it has no card in Arrmada but still lets the Docker network in without a password; giving it one")
	return Client{Name: bundledName(KindQbittorrent), Kind: KindQbittorrent, URL: url, Username: user, Password: pass}, nil
}

// QbitExposure is who gets into the bundled qBittorrent without a password, as far as
// Arrmada can see from its own network.
type QbitExposure struct {
	// Open: its subnet whitelist lets Arrmada's own network in with no login at all.
	Open bool
	// Broad lists the whitelisted subnets that reach beyond that network, each covering a
	// /16 or more (every home network in 192.168.0.0/16, say). Empty when there are none.
	Broad []string
	// OldSeed: the whitelist is exactly the one the first Compose config had.
	OldSeed bool
	// Held says why Arrmada's last start left it as it is on purpose ("" when it didn't).
	Held string
}

// holdQbit records why SecureBundledQbit left an open qBittorrent alone on purpose ("" to
// clear it), for BundledQbitExposure.
func (s *Service) holdQbit(why string) { s.qbitHeld.Store(&why) }

func (s *Service) qbitHeldReason() string {
	if p := s.qbitHeld.Load(); p != nil {
		return *p
	}
	return ""
}

// BundledQbitExposure asks the qBittorrent at url, without signing in, whether it lets
// Arrmada in anyway (its subnet whitelist) and, when it does, which subnets that covers.
// Nothing is changed and no login is tried, so it can run as often as a health check does.
func (s *Service) BundledQbitExposure(ctx context.Context, url string) (QbitExposure, error) {
	p, open, err := newQbitAuthAPI(url).anonymousPrefs(ctx)
	if err != nil || !open {
		return QbitExposure{}, err
	}
	e := QbitExposure{Open: true, Held: s.qbitHeldReason()}
	if !p.WhitelistEnabled {
		return e, nil // in some other way (it only bypasses for localhost): nothing to name
	}
	e.OldSeed = sameSubnets(p.Whitelist, seedWhitelist)
	own := s.localNets()
	for _, sub := range splitSubnets(p.Whitelist) {
		_, n, err := net.ParseCIDR(sub)
		if err != nil || hasNet(own, n) {
			continue
		}
		if ones, bits := n.Mask.Size(); (bits == 32 && ones <= 16) || (bits == 128 && ones <= 48) {
			e.Broad = append(e.Broad, sub)
		}
	}
	return e, nil
}

// SecureBundledQbit gives the Compose bundle's qBittorrent (the container at url) a
// password and switches off its subnet whitelist, then turns CSRF protection on and the
// localhost bypass off. A login the owner gave the bundled client is kept when qBittorrent
// accepts it; otherwise Arrmada makes one (keeper) and the client signs in with it. Every
// step can be repeated, and a failure leaves the whitelist on, so Arrmada still gets in.
//
// The bundled card switched off is secured all the same (and given the login, ready for
// when it's back on). With no card at url (removed, or pointed at another qBittorrent) the
// container is still there and still open, so it's secured with the login Arrmada keeps,
// but only while it lets Arrmada in without a password, and only while no qBittorrent card
// is switched on at all (cardlessQbit): one may reach it under another address.
// Once done it isn't redone, unless the whitelist is Arrmada's again (the old seed's, or
// the Docker network's put back by reopen.sh: isArrmadaWhitelist) and qBittorrent still
// takes the login Arrmada kept: a password changed since is never overwritten
// (ErrQbitKeptLoginStale), and the health line says why it was left.
func (s *Service) SecureBundledQbit(ctx context.Context, url string, keeper BundledLoginKeeper) (SecureOutcome, error) {
	if s.flags == nil {
		return SecureSkipped, errors.New("no settings to keep the progress in")
	}
	s.holdQbit("")
	reopened := keeper.Reopened()
	out, err := s.secureBundledQbit(ctx, url, keeper, reopened)
	if err == nil && reopened != ReopenNone {
		if cerr := keeper.ClearReopened(); cerr != nil {
			s.log.Warn("bundled qBittorrent: couldn't remove the reopened marker", "err", cerr)
		}
	}
	return out, err
}

func (s *Service) secureBundledQbit(ctx context.Context, url string, keeper BundledLoginKeeper, reopened Reopen) (SecureOutcome, error) {
	c, ok, err := s.repo.BundledOf(ctx, KindQbittorrent)
	if err != nil {
		return SecureSkipped, err
	}
	api := newQbitAuthAPI(url)
	card := ok && c.URL == url
	if !card {
		if c, err = s.cardlessQbit(ctx, api, url, keeper); err != nil || c.Name == "" {
			return SecureSkipped, err
		}
	}
	st := s.qbitAuthState(ctx)

	// The client's own login: let in by the whitelist while it's on, judged when it's off.
	sess, res, err := api.login(ctx, c.Username, c.Password, "")
	if err != nil {
		return SecureSkipped, err
	}
	if res != loginAccepted {
		return SecureSkipped, fmt.Errorf("%w (%s)", ErrQbitLoginRefused, res)
	}
	p, err := sess.prefs(ctx)
	if err != nil {
		return SecureSkipped, err
	}

	// Once, whatever else happens below: CSRF protection and the localhost login on, which
	// configs seeded before 2026-10-16 had off (KeyBundledQbitCSRF).
	if s.flags.Get(ctx, KeyBundledQbitCSRF, "") == "" {
		if err := sess.setPrefs(ctx, map[string]any{"bypass_local_auth": false, "web_ui_csrf_protection_enabled": true}); err != nil {
			return SecureSkipped, err
		}
		if err := s.flags.Set(ctx, KeyBundledQbitCSRF, "1"); err != nil {
			return SecureSkipped, err
		}
		s.log.Info("bundled qBittorrent: switched its CSRF protection and localhost login on (once; switch them off again in qBittorrent if you need to)", "name", c.Name, "has_card", card)
	}

	// A check cut short last time may have left the reverse-proxy settings borrowed. The
	// whitelist was still on then, so the login above got in: put them back.
	if st.Phase == qbitAuthVerifying {
		if err := sess.setPrefs(ctx, proxyPrefs(st.ProxyEnabled, st.ProxyList)); err != nil {
			return SecureSkipped, err
		}
		p.ProxyEnabled, p.ProxyList = st.ProxyEnabled, st.ProxyList
		st = qbitAuthState{}
		if err := s.saveQbitAuthState(ctx, st); err != nil {
			return SecureSkipped, err
		}
	}

	// Reverse-proxy trust of exactly the seed's subnets is the check's, left behind when the
	// progress record that would have put it back was lost (a database rolled back
	// mid-check): nobody sets that up on purpose. Switch it off.
	if st.Phase != qbitAuthSecured && p.ProxyEnabled && sameSubnets(p.ProxyList, seedWhitelist) {
		if err := sess.setPrefs(ctx, proxyPrefs(false, "")); err != nil {
			return SecureSkipped, err
		}
		p.ProxyEnabled, p.ProxyList = false, ""
		s.log.Info("bundled qBittorrent: switched off reverse-proxy trust an earlier password check left behind", "name", c.Name)
	}

	switch {
	case st.Phase == qbitAuthSwitching && !p.WhitelistEnabled:
		// Cut short after the whitelist went off. The client's login was checked before
		// that, and it got in just now with the whitelist off: finish.
		if err := sess.setPrefs(ctx, securedPrefs()); err != nil {
			return SecureSkipped, err
		}
		if err := s.saveQbitAuthState(ctx, qbitAuthState{Phase: qbitAuthSecured}); err != nil {
			return SecureSkipped, err
		}
		s.log.Info("bundled qBittorrent: finished switching it to its password", "name", c.Name)
		return SecureNewPassword, nil
	case !p.WhitelistEnabled:
		// Already asks for a password (the owner set that up, or this ran before). The
		// first time, CSRF protection and the localhost login are switched on too: the
		// seed had them off, and someone who only removed the whitelist left them so.
		if st.Phase != qbitAuthSecured {
			if err := sess.setPrefs(ctx, map[string]any{"bypass_local_auth": false, "web_ui_csrf_protection_enabled": true}); err != nil {
				return SecureSkipped, err
			}
			return SecureAlready, s.saveQbitAuthState(ctx, qbitAuthState{Phase: qbitAuthSecured})
		}
		return SecureAlready, nil
	case !s.isArrmadaWhitelist(ctx, url, p.Whitelist, st.Phase != qbitAuthSecured || reopened != ReopenNone):
		// A whitelist the owner set up (changed from the seed's, or turned on again with
		// subnets of their own): theirs to keep. Only one Arrmada seeded is its to replace.
		if st.Phase != qbitAuthSecured {
			s.log.Info("bundled qBittorrent: its subnet whitelist isn't the one it was installed with, so it's left as you set it", "name", c.Name)
			return SecureOwnWhitelist, s.saveQbitAuthState(ctx, qbitAuthState{Phase: qbitAuthSecured})
		}
		return SecureOwnWhitelist, nil
	}

	// From here the whitelist is on, so every call on sess gets in.
	saved, savedPass, haveSaved, err := keeper.Load()
	if err != nil {
		return SecureSkipped, fmt.Errorf("the saved qBittorrent login: %w", err)
	}

	// The owner's own login, when the client has one and Arrmada hasn't made one before.
	var verified *qbitAuthSession
	outcome := SecureNewPassword
	user, pass := c.Username, c.Password
	if !haveSaved && user != "" && pass != "" {
		vs, ok, err := s.checkQbitLogin(ctx, api, sess, p, user, pass)
		if err != nil {
			return SecureSkipped, err
		}
		if ok {
			verified, outcome = vs, SecureKeptPassword
		} else {
			s.log.Warn("bundled qBittorrent: the login on its card doesn't work; Arrmada is giving it a password of its own", "name", c.Name)
		}
	}

	// The login Arrmada kept: when qBittorrent still takes it, nothing needs changing. When
	// it doesn't after the switch was done (or after update.sh's reopen), someone changed
	// the password since: overwriting it would shut them out, so everything is left as it
	// is. Only the owner's own reset (troubleshooting) overwrites it.
	if verified == nil && haveSaved {
		vs, ok, err := s.checkQbitLogin(ctx, api, sess, p, saved, savedPass)
		if err != nil {
			return SecureSkipped, err
		}
		switch {
		case ok:
			verified, user, pass = vs, saved, savedPass
			if card {
				if err := s.adoptBundledLogin(ctx, c, user, pass); err != nil {
					return SecureSkipped, err
				}
			}
		case reopened != ReopenReset && (st.Phase == qbitAuthSecured || reopened == ReopenRollback):
			s.holdQbit("its password isn't the one Arrmada kept (was it changed in qBittorrent?), and Arrmada won't overwrite it")
			return SecureSkipped, ErrQbitKeptLoginStale
		}
	}

	if verified == nil {
		user, pass = saved, savedPass
		if !haveSaved {
			user = p.Username
			if user == "" {
				user = "admin"
			}
			if pass, err = keeper.Make(user); err != nil {
				return SecureSkipped, fmt.Errorf("couldn't save a new qBittorrent login: %w", err)
			}
		}
		if err := sess.setPrefs(ctx, map[string]any{"web_ui_username": user, "web_ui_password": pass}); err != nil {
			return SecureSkipped, err
		}
		vs, ok, err := s.checkQbitLogin(ctx, api, sess, p, user, pass)
		if err != nil {
			return SecureSkipped, err
		}
		if !ok {
			return SecureSkipped, errors.New("qBittorrent didn't take the password Arrmada gave it; it still lets Arrmada in without one")
		}
		verified = vs
		// The client signs in with it from now on. The whitelist still lets any login in,
		// so this can come before the switch, and must: once the whitelist is off, the
		// client needs this login. With no card there's no client to give it to: the login
		// stays in the keeper, for Show its login and for a card restored later.
		if card {
			if err := s.adoptBundledLogin(ctx, c, user, pass); err != nil {
				return SecureSkipped, err
			}
		}
	}

	if err := s.saveQbitAuthState(ctx, qbitAuthState{Phase: qbitAuthSwitching}); err != nil {
		return SecureSkipped, err
	}
	if err := verified.setPrefs(ctx, securedPrefs()); err != nil {
		return SecureSkipped, err
	}
	// A plain sign-in, as the client makes them, now the whitelist is off. If it fails
	// (it shouldn't: the login was just checked), the checked session puts the whitelist
	// back so Arrmada isn't shut out.
	if _, res, err := api.login(ctx, user, pass, ""); err != nil || res != loginAccepted {
		if rerr := verified.setPrefs(context.WithoutCancel(ctx), map[string]any{"bypass_auth_subnet_whitelist_enabled": true}); rerr != nil {
			return SecureSkipped, fmt.Errorf("qBittorrent refused the checked login after the whitelist went off, and putting the whitelist back failed: %v", rerr)
		}
		_ = s.saveQbitAuthState(ctx, qbitAuthState{})
		if err == nil {
			err = fmt.Errorf("%s", res)
		}
		return SecureSkipped, fmt.Errorf("qBittorrent refused the checked login after the whitelist went off, so the whitelist is back on: %w", err)
	}
	if err := s.saveQbitAuthState(ctx, qbitAuthState{Phase: qbitAuthSecured}); err != nil {
		return SecureSkipped, err
	}
	return outcome, nil
}

// securedPrefs is the end state: no login-free way in, from the Docker networks or from
// inside qBittorrent's container, and CSRF protection on. The whitelist's subnets are left
// as they were (only switched off), so turning it back on in qBittorrent.conf is a way back.
func securedPrefs() map[string]any {
	return map[string]any{
		"bypass_auth_subnet_whitelist_enabled": false,
		"bypass_local_auth":                    false,
		"web_ui_csrf_protection_enabled":       true,
	}
}

func proxyPrefs(enabled bool, list string) map[string]any {
	return map[string]any{"web_ui_reverse_proxy_enabled": enabled, "web_ui_reverse_proxies_list": list}
}

// checkQbitLogin reports whether qBittorrent accepts user/pass, while its whitelist is on
// (sess gets in through it), and returns a session signed in with them. See the top of
// this file for how; the reverse-proxy settings are always put back before it returns, or
// left recorded in the state for the next start when that fails.
func (s *Service) checkQbitLogin(ctx context.Context, api *qbitAuthAPI, sess *qbitAuthSession, p qbitAuthPrefs, user, pass string) (*qbitAuthSession, bool, error) {
	outside, err := outsideAddress(p.Whitelist)
	if err != nil {
		return nil, false, err
	}
	// Kept first, so a restart half-way puts qBittorrent's own settings back.
	if err := s.saveQbitAuthState(ctx, qbitAuthState{Phase: qbitAuthVerifying, ProxyEnabled: p.ProxyEnabled, ProxyList: p.ProxyList}); err != nil {
		return nil, false, err
	}
	vs, ok, err := s.checkQbitLoginBorrowed(ctx, api, sess, p, user, pass, outside)
	restore := context.WithoutCancel(ctx)
	if rerr := sess.setPrefs(restore, proxyPrefs(p.ProxyEnabled, p.ProxyList)); rerr != nil {
		return nil, false, fmt.Errorf("couldn't put qBittorrent's reverse-proxy settings back (tried again at the next start): %w", rerr)
	}
	if serr := s.saveQbitAuthState(restore, qbitAuthState{}); serr != nil {
		return nil, false, serr
	}
	return vs, ok, err
}

func (s *Service) checkQbitLoginBorrowed(ctx context.Context, api *qbitAuthAPI, sess *qbitAuthSession, p qbitAuthPrefs, user, pass, outside string) (*qbitAuthSession, bool, error) {
	trusted := strings.Join(splitSubnets(p.Whitelist), ";")
	if err := sess.setPrefs(ctx, proxyPrefs(true, trusted)); err != nil {
		return nil, false, err
	}
	// A login that can't be right must be refused, or the check proves nothing.
	wrong, err := randomToken()
	if err != nil {
		return nil, false, err
	}
	_, res, err := api.login(ctx, "arrmada-check", wrong, outside)
	if err != nil {
		return nil, false, err
	}
	if res == loginAccepted {
		return nil, false, errors.New("couldn't check the qBittorrent login: qBittorrent let a wrong one in, so the whitelist stays on")
	}
	// The login itself from another outside address: the wrong one above counts as a failed
	// attempt against its address, and a bad run of luck must never add up to a ban there.
	real, err := outsideAddress(p.Whitelist)
	for i := 0; err == nil && real == outside && i < 8; i++ {
		real, err = outsideAddress(p.Whitelist)
	}
	if err != nil {
		return nil, false, err
	}
	vs, res, err := api.login(ctx, user, pass, real)
	if err != nil {
		return nil, false, err
	}
	return vs, res == loginAccepted, nil
}

// adoptBundledLogin gives the bundled client the login Arrmada made, and records it as the
// one last given (KeyBundledLogin), so the next start's EnsureBundledLogin leaves it be.
func (s *Service) adoptBundledLogin(ctx context.Context, c Client, user, pass string) error {
	// Read afresh: the owner may have edited the card (its name, priority…) meanwhile.
	c, err := s.repo.Get(ctx, c.ID)
	if err != nil {
		return err
	}
	if c.Username != user || c.Password != pass {
		c.Username, c.Password = user, pass
		// Arrmada's own change, not the owner's edit (update, not Update).
		if _, err := s.update(ctx, c); err != nil {
			return err
		}
	}
	h := sha256.Sum256([]byte(user + "\x00" + pass))
	return s.setFlag(ctx, KeyBundledLogin, hex.EncodeToString(h[:]))
}

func (s *Service) qbitAuthState(ctx context.Context) qbitAuthState {
	var st qbitAuthState
	if raw := s.flags.Get(ctx, KeyBundledQbitAuth, ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &st) // unreadable: start from the beginning
	}
	return st
}

func (s *Service) saveQbitAuthState(ctx context.Context, st qbitAuthState) error {
	raw, _ := json.Marshal(st)
	return s.flags.Set(ctx, KeyBundledQbitAuth, string(raw))
}

// splitSubnets reads qBittorrent's whitelist (one subnet a line; commas in the config file).
func splitSubnets(list string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == '\n' || r == ',' || r == ';' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func sameSubnets(list string, want []string) bool {
	got := splitSubnets(list)
	if len(got) != len(want) {
		return false
	}
	w := append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(w)
	for i := range got {
		if got[i] != w[i] {
			return false
		}
	}
	return true
}

// outsideAddress is an address in the documentation ranges (never a real client) that the
// whitelist doesn't cover, picked at random so qBittorrent's failed-login count for it never
// adds up to a ban.
func outsideAddress(whitelist string) (string, error) {
	var nets []*net.IPNet
	for _, s := range splitSubnets(whitelist) {
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, n)
		} else if ip := net.ParseIP(s); ip != nil {
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(8*len(ip), 8*len(ip))})
		}
	}
	for _, prefix := range []string{"192.0.2.", "198.51.100.", "203.0.113."} {
		n, err := rand.Int(rand.Reader, big.NewInt(254))
		if err != nil {
			return "", err
		}
		ip := net.ParseIP(prefix + fmt.Sprint(n.Int64()+1))
		covered := false
		for _, w := range nets {
			if w.Contains(ip) {
				covered = true
				break
			}
		}
		if !covered {
			return ip.String(), nil
		}
	}
	return "", errors.New("couldn't check the qBittorrent login: its whitelist covers every address the check could use, so the whitelist stays on")
}

func randomToken() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// qbitAuthAPI is the slice of qBittorrent's WebUI API the switch needs, with sessions of
// its own (the client's cached ones are left alone). Every request carries a Referer from
// the same address, which is what qBittorrent's CSRF protection wants.
type qbitAuthAPI struct{ base string }

func newQbitAuthAPI(u string) *qbitAuthAPI { return &qbitAuthAPI{base: strings.TrimRight(u, "/")} }

type qbitAuthSession struct {
	api    *qbitAuthAPI
	client *http.Client
}

type loginResult int

const (
	loginAccepted loginResult = iota
	loginRejected             // wrong username or password
	loginRefused              // 403: banned for too many failures, or a host/CSRF check
	loginOther
)

func (r loginResult) String() string {
	switch r {
	case loginAccepted:
		return "accepted"
	case loginRejected:
		return "wrong username or password"
	case loginRefused:
		return "refused (too many failed logins, or a host/CSRF check)"
	}
	return "unexpected answer"
}

// login signs in. forwardedFor, when set, is sent as X-Forwarded-For.
func (a *qbitAuthAPI) login(ctx context.Context, user, pass, forwardedFor string) (*qbitAuthSession, loginResult, error) {
	jar, _ := cookiejar.New(nil)
	sess := &qbitAuthSession{api: a, client: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	form := url.Values{"username": {user}, "password": {pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, loginOther, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", a.base)
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	resp, err := sess.client.Do(req)
	if err != nil {
		return nil, loginOther, fmt.Errorf("%w: %v", ErrQbitUnreachable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode == http.StatusUnauthorized, strings.TrimSpace(string(body)) == "Fails.":
		// 401 from qBittorrent 5; 200 "Fails." from 4.x.
		return nil, loginRejected, nil
	case resp.StatusCode == http.StatusForbidden:
		return nil, loginRefused, nil
	case resp.StatusCode/100 == 2:
		return sess, loginAccepted, nil
	}
	return nil, loginOther, fmt.Errorf("qbittorrent: login HTTP %d", resp.StatusCode)
}

// qbitAuthPrefs is what the switch reads from qBittorrent's preferences.
type qbitAuthPrefs struct {
	WhitelistEnabled bool   `json:"bypass_auth_subnet_whitelist_enabled"`
	Whitelist        string `json:"bypass_auth_subnet_whitelist"`
	Username         string `json:"web_ui_username"`
	ProxyEnabled     bool   `json:"web_ui_reverse_proxy_enabled"`
	ProxyList        string `json:"web_ui_reverse_proxies_list"`
}

func (s *qbitAuthSession) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.api.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Referer", s.api.base)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrQbitUnreachable, err)
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("qbittorrent: %s HTTP %d", path, resp.StatusCode)
	}
	return resp, nil
}

// anonymousPrefs reads qBittorrent's preferences with no session at all. open is false
// when it asks for a login (401/403): its whitelist doesn't cover Arrmada's address. An
// unauthenticated API request isn't a failed login, so this never adds up to a ban.
func (a *qbitAuthAPI) anonymousPrefs(ctx context.Context) (qbitAuthPrefs, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+"/api/v2/app/preferences", nil)
	if err != nil {
		return qbitAuthPrefs{}, false, err
	}
	req.Header.Set("Referer", a.base)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return qbitAuthPrefs{}, false, fmt.Errorf("%w: %v", ErrQbitUnreachable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return qbitAuthPrefs{}, false, nil
	case resp.StatusCode/100 != 2:
		return qbitAuthPrefs{}, false, fmt.Errorf("qbittorrent: preferences HTTP %d", resp.StatusCode)
	}
	var p qbitAuthPrefs
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p); err != nil {
		return qbitAuthPrefs{}, false, fmt.Errorf("qbittorrent: preferences: %w", err)
	}
	return p, true, nil
}

func (s *qbitAuthSession) prefs(ctx context.Context) (qbitAuthPrefs, error) {
	resp, err := s.do(ctx, http.MethodGet, "/api/v2/app/preferences", nil, "")
	if err != nil {
		return qbitAuthPrefs{}, err
	}
	defer resp.Body.Close()
	var p qbitAuthPrefs
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p); err != nil {
		return qbitAuthPrefs{}, fmt.Errorf("qbittorrent: preferences: %w", err)
	}
	return p, nil
}

func (s *qbitAuthSession) setPrefs(ctx context.Context, prefs map[string]any) error {
	raw, _ := json.Marshal(prefs)
	form := url.Values{"json": {string(raw)}}
	resp, err := s.do(ctx, http.MethodPost, "/api/v2/app/setPreferences", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
