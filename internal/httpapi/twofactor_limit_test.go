package httpapi

import (
	"testing"
	"time"
)

func newTestCodeLimiter() (*codeLimiter, *time.Time) {
	now := time.Unix(1_800_000_000, 0)
	c := newCodeLimiter()
	c.now = func() time.Time { return now }
	return c, &now
}

// One attempt per account at a time; another account isn't held up.
func TestCodeLimiterOneAtATime(t *testing.T) {
	c, _ := newTestCodeLimiter()
	if ok, _ := c.begin(1); !ok {
		t.Fatal("first attempt refused")
	}
	if ok, retry := c.begin(1); ok || retry != codeBusyRetry {
		t.Errorf("second attempt while the first is checked: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := c.begin(2); !ok {
		t.Error("another account was held up")
	}
	c.end(1, codeNeutral)
	c.end(2, codeNeutral)
	if ok, _ := c.begin(1); !ok {
		t.Error("refused after the first attempt ended")
	}
	c.end(1, codeNeutral)
	if len(c.accts) != 0 {
		t.Errorf("neutral attempts left a record: %v", c.accts)
	}
}

// Five free wrong codes, then a pause that doubles; ten in fifteen minutes block. The
// failure is counted while the attempt is in flight, before its outcome is known.
func TestCodeLimiterPauseAndBlock(t *testing.T) {
	c, now := newTestCodeLimiter()
	for i := range codeFree {
		if ok, _ := c.begin(1); !ok {
			t.Fatalf("wrong code %d refused", i+1)
		}
		if n := c.end(1, codeWrong); n != i+1 {
			t.Fatalf("count after %d = %d", i+1, n)
		}
	}
	if ok, retry := c.begin(1); ok || retry != codeDelayBase {
		t.Fatalf("after five: ok=%v retry=%v, want a %v pause", ok, retry, codeDelayBase)
	}
	*now = now.Add(codeDelayBase)
	if ok, _ := c.begin(1); !ok {
		t.Fatal("refused after the pause")
	}
	c.end(1, codeWrong)
	if ok, retry := c.begin(1); ok || retry != 2*codeDelayBase {
		t.Errorf("after six: ok=%v retry=%v, want %v", ok, retry, 2*codeDelayBase)
	}
	// Reserved before checking: an attempt in flight already counts toward the pause.
	*now = now.Add(time.Hour + time.Minute) // all aged out
	for range codeFree - 1 {
		c.begin(1)
		c.end(1, codeWrong)
	}
	if ok, _ := c.begin(1); !ok {
		t.Fatal("fifth refused")
	}
	if n := len(c.accts[1].fails); n != codeFree {
		t.Errorf("in flight, the record holds %d, want %d (reserved)", n, codeFree)
	}
	c.end(1, codeWrong)

	// The block: ten in fifteen minutes, until the oldest of them is fifteen minutes old.
	c2, now2 := newTestCodeLimiter()
	a := &codeAccount{}
	for i := 0; i < codeMax; i++ {
		a.fails = append(a.fails, now2.Add(-14*time.Minute+time.Duration(i)*time.Minute))
	}
	c2.accts[1] = a
	if ok, retry := c2.begin(1); ok || retry != time.Minute {
		t.Errorf("ten in fifteen minutes: ok=%v retry=%v, want blocked for 1m", ok, retry)
	}
}

// A right code gives back its own reservation and forgives only the older half of the
// record: one success doesn't wipe it. The rest ages out after an hour.
func TestCodeLimiterSuccessKeepsStrikes(t *testing.T) {
	c, now := newTestCodeLimiter()
	for range 4 {
		c.begin(1)
		c.end(1, codeWrong)
	}
	c.begin(1)
	if n := c.end(1, codeRight); n != 2 {
		t.Fatalf("after a right code the record holds %d, want 2 (half of 4)", n)
	}
	c.begin(1)
	if n := c.end(1, codeRight); n != 1 {
		t.Fatalf("after a second right code: %d, want 1", n)
	}
	c.begin(1)
	if n := c.end(1, codeRight); n != 1 {
		t.Fatalf("one strike survives one success: %d", n)
	}
	// Three more wrong codes make five: the pause applies despite the successes between.
	for range 4 {
		c.begin(1)
		c.end(1, codeWrong)
	}
	if ok, _ := c.begin(1); ok {
		t.Error("no pause after five wrong codes with right ones in between")
	}
	*now = now.Add(codeMemory)
	if ok, _ := c.begin(1); !ok {
		t.Error("still limited an hour later")
	}
	if n := c.end(1, codeNeutral); n != 0 {
		t.Errorf("record an hour later = %d, want 0", n)
	}
}
