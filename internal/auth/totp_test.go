package auth

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 4226 Appendix D: HOTP with the ASCII secret "12345678901234567890", counters 0–9.
func TestHOTPRFC4226Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := hotp(sha1.New, key, uint64(i), 6); got != w {
			t.Errorf("counter %d: %s, want %s", i, got, w)
		}
	}
}

// RFC 6238 Appendix B: TOTP, eight digits, a 30-second step from T0 = 0, each hash with
// its own seed. Authenticator apps only ever use SHA-1 and six digits, but the algorithm
// is the RFC's.
func TestTOTPRFC6238Vectors(t *testing.T) {
	seeds := map[string]struct {
		h   func() hash.Hash
		key string
	}{
		"SHA1":   {sha1.New, "12345678901234567890"},
		"SHA256": {sha256.New, "12345678901234567890123456789012"},
		"SHA512": {sha512.New, "1234567890123456789012345678901234567890123456789012345678901234"},
	}
	vectors := []struct {
		unix int64
		want map[string]string
	}{
		{59, map[string]string{"SHA1": "94287082", "SHA256": "46119246", "SHA512": "90693936"}},
		{1111111109, map[string]string{"SHA1": "07081804", "SHA256": "68084774", "SHA512": "25091201"}},
		{1111111111, map[string]string{"SHA1": "14050471", "SHA256": "67062674", "SHA512": "99943326"}},
		{1234567890, map[string]string{"SHA1": "89005924", "SHA256": "91819424", "SHA512": "93441116"}},
		{2000000000, map[string]string{"SHA1": "69279037", "SHA256": "90698825", "SHA512": "38618901"}},
		{20000000000, map[string]string{"SHA1": "65353130", "SHA256": "77737706", "SHA512": "47863826"}},
	}
	for _, v := range vectors {
		step := totpStepAt(time.Unix(v.unix, 0))
		for name, seed := range seeds {
			if got := hotp(seed.h, []byte(seed.key), uint64(step), 8); got != v.want[name] {
				t.Errorf("T=%d %s: %s, want %s", v.unix, name, got, v.want[name])
			}
		}
		// The six-digit code the app shows is the same value's last six digits.
		if got, want := totpCode([]byte(seeds["SHA1"].key), step), v.want["SHA1"][2:]; got != want {
			t.Errorf("T=%d six digits: %s, want %s", v.unix, got, want)
		}
	}
}

// The otpauth link names Arrmada and the account, and carries the secret apps decode back
// to the same key.
func TestOtpauthURI(t *testing.T) {
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 || strings.ContainsAny(secret, "=") {
		t.Fatalf("secret %q: want 32 base32 characters, no padding", secret)
	}
	u, err := url.Parse(otpauthURI("mum@example.com", secret))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Arrmada:mum@example.com" {
		t.Errorf("link = %s", u)
	}
	q := u.Query()
	if q.Get("issuer") != "Arrmada" || q.Get("secret") != secret || q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Errorf("query = %v", q)
	}
	key, err := decodeTOTPSecret(strings.ToLower(secret[:4] + " " + secret[4:]))
	if err != nil || len(key) != totpSecretBytes {
		t.Errorf("typed secret decodes to %d bytes (%v)", len(key), err)
	}
}

// matchTOTP takes the codes for the step before, now and the step after, never further,
// and never a step at or before the last one used.
func TestMatchTOTPWindow(t *testing.T) {
	key := []byte("12345678901234567890")
	now := time.Unix(1_700_000_010, 0) // the start of a step
	cur := totpStepAt(now)
	for off := int64(-3); off <= 3; off++ {
		_, ok := matchTOTP(key, totpCode(key, cur+off), now, 0)
		if want := off >= -1 && off <= 1; ok != want {
			t.Errorf("step %+d: accepted %v, want %v", off, ok, want)
		}
	}
	if _, ok := matchTOTP(key, totpCode(key, cur), now, cur); ok {
		t.Error("the step already used was accepted again")
	}
	if step, ok := matchTOTP(key, totpCode(key, cur+1), now, cur); !ok || step != cur+1 {
		t.Errorf("the next step: %d %v", step, ok)
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := matchTOTP(key, bad, now, 0); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
