package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"hash"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords (RFC 6238 over RFC 4226's HOTP): the six-digit codes an
// authenticator app (Aegis, 1Password, Google Authenticator…) shows, a new one every 30
// seconds. Every app reads the same otpauth:// link, so these are the defaults all of
// them assume: SHA-1, six digits, a 30-second step.

const (
	totpStep   = 30 * time.Second
	totpDigits = 6
	// totpSkew is how many steps either side of now a code is accepted for: a phone clock
	// a little off, or a code typed as it turned over.
	totpSkew = 1
	// totpSecretBytes is the shared secret's length: 160 bits, RFC 4226's recommendation.
	totpSecretBytes = 20
	// totpIssuer names Arrmada in the authenticator app.
	totpIssuer = "Arrmada"
)

// b32 is the secret's text form: RFC 4648 base32 without padding, as authenticator apps
// expect in the otpauth link and when typed by hand.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret draws a fresh shared secret and returns it in base32.
func newTOTPSecret() (string, error) {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// decodeTOTPSecret reads a base32 secret, ignoring case, spaces and padding (people copy
// it by hand in groups).
func decodeTOTPSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '=' {
			return -1
		}
		return r
	}, s))
	return b32.DecodeString(s)
}

// hotp is RFC 4226's HMAC-based one-time password for counter, as digits decimal digits,
// with the hash RFC 6238 lets a token choose (SHA-1 for every authenticator app).
func hotp(newHash func() hash.Hash, key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(newHash, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	// Dynamic truncation: the low nibble of the last byte picks four bytes.
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	code := bin % mod
	out := make([]byte, digits)
	for i := digits - 1; i >= 0; i-- {
		out[i] = byte('0' + code%10)
		code /= 10
	}
	return string(out)
}

// totpStepAt is the 30-second step t falls in (RFC 6238's T, with T0 = 0).
func totpStepAt(t time.Time) int64 {
	return t.Unix() / int64(totpStep/time.Second)
}

// totpCode is the six-digit code for one step.
func totpCode(key []byte, step int64) string {
	return hotp(sha1.New, key, uint64(step), totpDigits)
}

// matchTOTP looks for code among the steps around now (±totpSkew) newer than lastStep,
// and returns the step it matched. A step at or before lastStep has had a code accepted
// already, so its code can't be used again. Every candidate is compared, in constant
// time, so the answer's timing doesn't say which step was close.
func matchTOTP(key []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	cur := totpStepAt(now)
	var found int64
	ok := false
	for step := cur - totpSkew; step <= cur+totpSkew; step++ {
		match := subtle.ConstantTimeCompare([]byte(totpCode(key, step)), []byte(code)) == 1
		if match && step > lastStep && !ok {
			found, ok = step, true
		}
	}
	return found, ok
}

// otpauthURI is the link an authenticator app reads from the QR code (Google's Key URI
// format): issuer "Arrmada", the account named by its username.
func otpauthURI(username, secret string) string {
	label := url.PathEscape(totpIssuer + ":" + username)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// normalizeOTP strips what people type around a code: spaces and dashes.
func normalizeOTP(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

// isDigits reports whether s is all ASCII digits (and not empty).
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
