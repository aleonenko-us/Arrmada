package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A sign-in ticket is the "password was right, now the code" step between the two halves
// of a two-factor sign-in. It is NOT a session and opens nothing: nothing that checks
// sessions or API keys reads it, it lives only in the sign-in page's memory (never a
// cookie), and the only things it can be traded for are a session (with a right code) or,
// for an admin who must set two-factor up first, that setup.
//
// It is signed with a key that exists only in this process, lasts five minutes, works
// once, names its purpose, and stops working the moment the account's password changes
// (its signature covers a fingerprint of the stored hash) or the account is disabled. A
// restart forgets the key, so a sign-in caught halfway just starts again.

// TicketTTL is how long the code step may take.
const TicketTTL = 5 * time.Minute

// TicketPurpose says what a ticket may be traded for.
type TicketPurpose string

const (
	// TicketVerify: the account has two-factor on; a right code makes the session.
	TicketVerify TicketPurpose = "verify"
	// TicketEnrol: an admin who must use two-factor (the admin policy) and hasn't set it
	// up; setting it up makes the session.
	TicketEnrol TicketPurpose = "enrol"
)

// ErrTicket is any ticket that can't be used: forged, expired, spent, for another
// purpose, or for an account whose password changed or that was disabled since.
var ErrTicket = errors.New("that sign-in took too long — enter your password again")

// tickets holds the signing key and the nonces of spent tickets.
type tickets struct {
	key  []byte
	mu   sync.Mutex
	used map[string]time.Time // nonce → when the ticket would have expired
}

func newTickets() *tickets {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("auth: no randomness for the sign-in ticket key: " + err.Error())
	}
	return &tickets{key: key, used: map[string]time.Time{}}
}

// IssueLoginTicket makes a ticket for an account whose password was just checked.
func (s *Service) IssueLoginTicket(ctx context.Context, userID int64, purpose TicketPurpose) (string, error) {
	fp, err := s.passwordFingerprint(ctx, userID)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	exp := s.now().Add(TicketTTL).Unix()
	payload := strings.Join([]string{"1", string(purpose), strconv.FormatInt(userID, 10), strconv.FormatInt(exp, 10),
		base64.RawURLEncoding.EncodeToString(nonce)}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(s.tickets.sign(payload, fp)), nil
}

// CheckLoginTicket returns the account a ticket was made for, if it is still usable for
// purpose. It doesn't spend it: SpendLoginTicket does, once the step it guards succeeds,
// so a mistyped code can be tried again with the same ticket (the code attempts
// themselves are rate-limited by the caller).
func (s *Service) CheckLoginTicket(ctx context.Context, raw string, purpose TicketPurpose) (*User, error) {
	payload, mac, ok := splitTicket(raw)
	if !ok {
		return nil, ErrTicket
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 5 || parts[0] != "1" || parts[1] != string(purpose) {
		return nil, ErrTicket
	}
	userID, err1 := strconv.ParseInt(parts[2], 10, 64)
	exp, err2 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || s.now().Unix() >= exp {
		return nil, ErrTicket
	}
	fp, err := s.passwordFingerprint(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrTicket
	}
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(mac, s.tickets.sign(payload, fp)) {
		return nil, ErrTicket
	}
	if s.tickets.spent(parts[4]) {
		return nil, ErrTicket
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil || u.Disabled {
		return nil, ErrTicket
	}
	return u, nil
}

// SpendLoginTicket marks a ticket used, so it can't make a second session. Reports false
// when it was already spent (two requests racing with one ticket: only one wins).
func (s *Service) SpendLoginTicket(raw string) bool {
	payload, _, ok := splitTicket(raw)
	if !ok {
		return false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 5 {
		return false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return false
	}
	return s.tickets.spend(parts[4], time.Unix(exp, 0), s.now())
}

func splitTicket(raw string) (payload string, mac []byte, ok bool) {
	p, m, found := strings.Cut(strings.TrimSpace(raw), ".")
	if !found || len(raw) > 512 {
		return "", nil, false
	}
	pb, err1 := base64.RawURLEncoding.DecodeString(p)
	mb, err2 := base64.RawURLEncoding.DecodeString(m)
	if err1 != nil || err2 != nil {
		return "", nil, false
	}
	return string(pb), mb, true
}

func (t *tickets) sign(payload, fingerprint string) []byte {
	m := hmac.New(sha256.New, t.key)
	m.Write([]byte("arrmada-signin-ticket\x00"))
	m.Write([]byte(payload))
	m.Write([]byte{0})
	m.Write([]byte(fingerprint))
	return m.Sum(nil)
}

func (t *tickets) spent(nonce string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.used[nonce]
	return ok
}

func (t *tickets) spend(nonce string, exp, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.used[nonce]; ok {
		return false
	}
	// Forget spent tickets once they'd have expired anyway; the map stays small.
	for k, e := range t.used {
		if now.After(e) {
			delete(t.used, k)
		}
	}
	t.used[nonce] = exp
	return true
}

// passwordFingerprint is a short digest of the account's stored password hash: a ticket
// signed over it dies when the password changes.
func (s *Service) passwordFingerprint(ctx context.Context, userID int64) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:16]), nil
}
